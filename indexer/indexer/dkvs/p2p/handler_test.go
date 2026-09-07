package p2p

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func xorPathMetaRootForTest(root *chainhash.Hash, record *wire.DKVSRecord) {
	effective := dkvs.RecordHash(record)
	h := sha256.New()
	_, _ = h.Write([]byte("dkvs-path-leaf-v1"))
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], uint64(len(record.Key)))
	_, _ = h.Write(scratch[:])
	_, _ = h.Write([]byte(record.Key))
	_, _ = h.Write(effective[:])
	leaf := h.Sum(nil)
	for i := range root {
		root[i] ^= leaf[i]
	}
}

type handlerTestStore struct {
	records             map[string]*wire.DKVSRecord
	subscriptions       []dkvs.Subscription
	putCount            int
	syncRecords         []*wire.DKVSRecord
	syncRoot            chainhash.Hash
	pathSnapshot        *dkvs.PathSnapshot
	appliedPathSnapshot *dkvs.PathSnapshot
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

func (s *handlerTestStore) GetDKVSRecordByHashForRelay(hash chainhash.Hash) (*wire.DKVSRecord, error) {
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

func (s *handlerTestStore) SyncFilteredDKVSRecords([]byte, uint32, []dkvs.Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	return s.syncRecords, nil, true, s.syncRoot, nil
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
		LocalServices: wire.SFNodeMiner, RemoteServices: wire.SFNodeMiner,
		MirrorAuthority: true,
		Sign:            func([]byte) ([]byte, error) { return []byte{1, 2, 3}, nil },
		Send:            func(msg wire.Message) { sent = msg },
	}
	handler.OnSyncRequest(&wire.MsgDKVSSyncRequest{SessionID: 7, Limit: 10})
	response, ok := sent.(*wire.MsgDKVSSyncResponse)
	if !ok || len(response.Records) != 1 || len(response.SourceSignature) == 0 || !response.Done {
		t.Fatalf("response=%#v", sent)
	}
}

func TestHandlerRejectsPathSnapshotFromUnclassifiedValidator(t *testing.T) {
	store := &handlerTestStore{}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	var sent wire.Message
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		ValidatorID:    "remote-miner",
		RemoteServices: wire.SFNodeMiner,
		Send:           func(msg wire.Message) { sent = msg },
	}
	if handler.TrustedSource {
		t.Fatal("test requires an unclassified remote miner")
	}
	handler.QueuePathSync("/name/8888.btc")
	if sent != nil {
		t.Fatalf("unclassified validator received destructive path request: %#v", sent)
	}
}

func TestHandlerRoutesUntrustedPathHintToAuthorizedSource(t *testing.T) {
	store := &handlerTestStore{}
	var peer PeerState
	var node NodeState
	requested := ""
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		ValidatorID:       "ordinary-peer",
		RequestPathRepair: func(path string) { requested = path },
	}
	const accountID = "148cbe135aea8ee9b72f18ca6ddf0efc052e54b6d723cc473a0cc6011766d776"
	record := &wire.DKVSRecord{Version: dkvs.Version,
		Key: "/personal/" + accountID + "/data/item", Seq: 2}
	if !handler.queuePathRepair(record, dkvs.ErrPathDiverged) {
		t.Fatal("path divergence was not recognized as a repair hint")
	}
	if requested != "/personal/"+accountID+"/data" {
		t.Fatalf("authorized repair path=%q", requested)
	}
}

func TestHandlerRebroadcastsRecordsAfterPathSnapshotRepair(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := &wire.DKVSRecord{Version: dkvs.Version, Key: "/name/8888.btc", Seq: 1}
	meta := &dkvs.PathMeta{Version: 3, Path: record.Key, Generation: 1, ViewHeight: 1}
	xorPathMetaRootForTest(&meta.StateRoot, record)
	snapshot := &dkvs.PathSnapshot{
		Path: record.Key, PathMeta: meta, Records: []*wire.DKVSRecord{record}, ServerTimeMS: 1,
	}
	store := &handlerTestStore{pathSnapshot: snapshot}
	var peer PeerState
	var node NodeState
	var relayed []*wire.MsgDKVSNotify
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		Net:           chaincfg.TestNetParams.Net,
		ValidatorID:   hex.EncodeToString(priv.PubKey().SerializeCompressed()),
		TrustedSource: true,
		Broadcast:     func(msg *wire.MsgDKVSNotify) { relayed = append(relayed, msg) },
	}
	filters := []wire.DKVSSyncFilter{{Type: pathSyncFilterType, Target: snapshot.Path}}
	start, err := peer.StartPathSync(snapshot.Path, time.Now())
	if err != nil || start.Request == nil {
		t.Fatalf("start path sync err=%v request=%#v", err, start.Request)
	}
	wireRecords, err := pathSnapshotWireRecords(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	response := &wire.MsgDKVSSyncResponse{
		SessionID: start.Request.SessionID, Records: wireRecords,
		Done: true, CheckpointRoot: meta.StateRoot,
	}
	payloadHash := chainhash.HashB(SyncAuthPayload(handler.Net, nil, filters, response))
	response.SourceSignature = ecdsa.Sign(priv, payloadHash).Serialize()
	handler.OnSyncResponse(response)
	if store.appliedPathSnapshot == nil || len(relayed) != 1 {
		t.Fatalf("snapshot=%#v relayed=%d", store.appliedPathSnapshot, len(relayed))
	}
	got, err := RecordFromNotify(relayed[0])
	if err != nil || dkvs.RecordHash(got) != dkvs.RecordHash(record) {
		t.Fatalf("relayed record=%#v err=%v", got, err)
	}
}

func TestHandlerQueuesDistinctPathRepairsWithoutReplacingActiveSession(t *testing.T) {
	store := &handlerTestStore{}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	var sent []wire.Message
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		ValidatorID: "remote-miner", TrustedSource: true,
		Send: func(msg wire.Message) { sent = append(sent, msg) },
	}
	handler.QueuePathSync("/personal/account/a")
	handler.QueuePathSync("/mail/account/share")
	if len(sent) != 1 {
		t.Fatalf("sent requests=%d", len(sent))
	}
	first, ok := sent[0].(*wire.MsgDKVSSyncRequest)
	if !ok || len(first.Filters) != 1 || first.Filters[0].Target != "/personal/account/a" {
		t.Fatalf("first request=%#v", sent[0])
	}
	peer.syncMtx.Lock()
	peer.resetSyncLocked()
	peer.syncMtx.Unlock()
	handler.queueNextPathSync()
	if len(sent) != 2 {
		t.Fatalf("queued request not started: sent=%d", len(sent))
	}
	second, ok := sent[1].(*wire.MsgDKVSSyncRequest)
	if !ok || len(second.Filters) != 1 || second.Filters[0].Target != "/mail/account/share" ||
		second.SessionID == first.SessionID {
		t.Fatalf("second request=%#v", sent[1])
	}
}

func TestPathSyncTimeoutAdvancesQueuedPathAndRequeuesTimedOutPath(t *testing.T) {
	store := &handlerTestStore{}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	var sent []wire.Message
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		ValidatorID: "remote-miner", TrustedSource: true,
		Send: func(msg wire.Message) { sent = append(sent, msg) },
	}
	handler.QueuePathSync("/personal/account/a")
	handler.QueuePathSync("/mail/account/share")
	first := sent[0].(*wire.MsgDKVSSyncRequest)
	if !peer.TimeoutPathSyncRequest(first.SessionID, first.Cursor) {
		t.Fatal("exact outstanding path request did not time out")
	}
	handler.queueNextPathSync()
	if len(sent) != 2 {
		t.Fatalf("queued path did not advance: sent=%d", len(sent))
	}
	second := sent[1].(*wire.MsgDKVSSyncRequest)
	if second.Filters[0].Target != "/mail/account/share" {
		t.Fatalf("second path=%s", second.Filters[0].Target)
	}
	peer.syncMtx.Lock()
	defer peer.syncMtx.Unlock()
	if len(peer.pendingPathSync) != 1 || peer.pendingPathSync[0] != "/personal/account/a" {
		t.Fatalf("timed-out path queue=%v", peer.pendingPathSync)
	}
}

func TestFailedPathSyncAdvancesNextQueuedPath(t *testing.T) {
	store := &handlerTestStore{}
	var peer PeerState
	var node NodeState
	node.SetReady(true)
	var sent []wire.Message
	handler := Handler{
		Store: store, Peer: &peer, Node: &node,
		ValidatorID: "remote-miner", TrustedSource: true,
		Send: func(msg wire.Message) { sent = append(sent, msg) },
	}
	handler.QueuePathSync("/personal/account/a")
	handler.QueuePathSync("/mail/account/share")
	first := sent[0].(*wire.MsgDKVSSyncRequest)
	// An unauthenticated response is terminal for the current source/path and
	// must not block the independently queued path.
	handler.OnSyncResponse(&wire.MsgDKVSSyncResponse{
		SessionID: first.SessionID, Done: true,
	})
	if len(sent) != 2 {
		t.Fatalf("next path not started after terminal failure: sent=%d", len(sent))
	}
	second := sent[1].(*wire.MsgDKVSSyncRequest)
	if second.Filters[0].Target != "/mail/account/share" {
		t.Fatalf("second path=%s", second.Filters[0].Target)
	}
}
