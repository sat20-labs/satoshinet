package p2p

import (
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type handlerTestStore struct {
	records       map[string]*wire.DKVSRecord
	subscriptions []dkvs.Subscription
	putCount      int
	syncRecords   []*wire.DKVSRecord
	syncRoot      chainhash.Hash
}

func (s *handlerTestStore) PutRemoteDKVSRecord(record *wire.DKVSRecord) (bool, error) {
	if record == nil {
		return false, errors.New("nil record")
	}
	if s.records == nil {
		s.records = make(map[string]*wire.DKVSRecord)
	}
	s.records[record.Key] = record
	s.putCount++
	return true, nil
}

func (s *handlerTestStore) GetDKVSRecordForRelay(key string) (*wire.DKVSRecord, error) {
	record := s.records[key]
	if record == nil {
		return nil, dkvs.ErrRecordNotFound
	}
	return record, nil
}

func (s *handlerTestStore) GetDKVSRecordByHash(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	for _, record := range s.records {
		if dkvs.RecordHash(record) == hash {
			return record, nil
		}
	}
	return nil, dkvs.ErrRecordNotFound
}

func (s *handlerTestStore) ApplyDKVSMirror([]dkvs.Subscription, []*wire.DKVSRecord, chainhash.Hash) (int, error) {
	return 0, nil
}

func (s *handlerTestStore) ApplyDKVSRecordSet(records []*wire.DKVSRecord) (int, error) {
	return len(records), nil
}

func (s *handlerTestStore) SyncFilteredDKVSRecords([]byte, uint32, []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return s.syncRecords, nil, true, s.syncRoot, nil
}

func (s *handlerTestStore) GetDKVSCheckpoint() (*dkvs.Checkpoint, error) {
	return &dkvs.Checkpoint{}, nil
}

func (s *handlerTestStore) ListDKVSSubscriptions() []dkvs.Subscription {
	return append([]dkvs.Subscription{}, s.subscriptions...)
}

func (s *handlerTestStore) IsDKVSSubscribed(key string) bool {
	for _, sub := range s.subscriptions {
		if dkvs.SubscriptionMatchesKey(sub, key) {
			return true
		}
	}
	return false
}

func TestHandlerInlineNotifyStoresAndRelaysWithoutGet(t *testing.T) {
	store := &handlerTestStore{subscriptions: []dkvs.Subscription{{Type: dkvs.SubscriptionKey, Target: "/tmp/a"}}}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	var sent []wire.Message
	var relayed *wire.MsgDKVSNotify
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		RemoteServices: wire.SFNodeMiner, TrustedSource: true,
		Send:      func(msg wire.Message) { sent = append(sent, msg) },
		Broadcast: func(msg *wire.MsgDKVSNotify) { relayed = msg },
	}
	record := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 1}
	handler.OnNotify(NotifyForRecord(record))
	if len(sent) != 0 {
		t.Fatalf("unexpected get requests=%d", len(sent))
	}
	relayedRecord, err := RecordFromNotify(relayed)
	if store.putCount != 1 || err != nil || dkvs.RecordHash(relayedRecord) != dkvs.RecordHash(record) {
		t.Fatalf("putCount=%d relayed=%#v", store.putCount, relayed)
	}
	handler.OnNotify(NotifyForRecord(record))
	if store.putCount != 1 {
		t.Fatalf("duplicate notify putCount=%d", store.putCount)
	}
}

func TestHandlerOrdinaryNodeRejectsUntrustedNotify(t *testing.T) {
	store := &handlerTestStore{subscriptions: []dkvs.Subscription{{Type: dkvs.SubscriptionKey, Target: "/tmp/a"}}}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	sent := 0
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		RemoteServices: wire.SFNodeMiner,
		Send:           func(wire.Message) { sent++ },
	}
	handler.OnNotify(NotifyForRecord(&wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 1}))
	if sent != 0 || store.putCount != 0 || handler.ShouldRequestSync() {
		t.Fatal("ordinary node accepted an untrusted DKVS source")
	}
	handler.TrustedSource = true
	if !handler.ShouldRequestSync() {
		t.Fatal("ordinary node rejected a trusted DKVS mirror source")
	}
	handler.OnNotify(NotifyForRecord(&wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 1}))
	if store.putCount != 1 {
		t.Fatal("ordinary node rejected subscribed notify from trusted source")
	}
}

func TestHandlerRejectsMalformedAndMismatchedNotify(t *testing.T) {
	store := &handlerTestStore{}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	penalties := 0
	handler := Handler{
		Store: store, Peer: &peer, Node: &node, LocalServices: wire.SFNodeMiner,
		Penalize: func(_, _ uint32, _ string) { penalties++ },
	}
	handler.OnNotify(&wire.MsgDKVSNotify{EventType: dkvs.EventRecordUpdate, Data: []byte("not-a-record")})
	tombstone := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 2, Flags: dkvs.FlagTombstone}
	data, err := wire.SerializeDKVSRecord(tombstone)
	if err != nil {
		t.Fatal(err)
	}
	handler.OnNotify(&wire.MsgDKVSNotify{EventType: dkvs.EventRecordUpdate, Data: data})
	if penalties != 2 || store.putCount != 0 {
		t.Fatalf("penalties=%d putCount=%d", penalties, store.putCount)
	}
}

func TestHandlerInlineDeleteRelaysSignedCommand(t *testing.T) {
	store := &handlerTestStore{}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	var relayed *wire.MsgDKVSNotify
	handler := Handler{
		Store: store, Peer: &peer, Node: &node, LocalServices: wire.SFNodeMiner,
		Broadcast: func(msg *wire.MsgDKVSNotify) { relayed = msg },
	}
	deleted := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 2, Flags: dkvs.FlagTombstone}
	handler.OnNotify(NotifyForRecord(deleted))
	record, err := RecordFromNotify(relayed)
	if err != nil || !dkvs.IsTombstone(record.Flags) || store.putCount != 1 {
		t.Fatalf("record=%#v putCount=%d err=%v", record, store.putCount, err)
	}
}

func TestHandlerServesSignedSyncResponse(t *testing.T) {
	record := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 1}
	store := &handlerTestStore{syncRecords: []*wire.DKVSRecord{record}, syncRoot: chainhash.DoubleHashH([]byte("root"))}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	var sent wire.Message
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		Net: chaincfg.TestNetParams.Net, ValidatorID: "remote",
		RemoteServices: wire.SFNodeMiner, MirrorAuthority: true,
		Sign: func([]byte) ([]byte, error) { return []byte{1, 2, 3}, nil },
		Send: func(msg wire.Message) { sent = msg },
	}
	handler.OnSyncRequest(&wire.MsgDKVSSyncRequest{SessionID: 7, Limit: 10})
	response, ok := sent.(*wire.MsgDKVSSyncResponse)
	if !ok || len(response.Records) != 1 || len(response.SourceSignature) == 0 || !response.Done {
		t.Fatalf("response=%#v", sent)
	}
}
