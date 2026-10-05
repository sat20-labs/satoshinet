package p2p

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// Reuse the Handler fixture, supplying just the current-state admission hooks.
type nodeCurrentTestStore struct {
	*handlerTestStore
	accept     func(*wire.DKVSRecord)
	applyError error
}

func (s *nodeCurrentTestStore) AcceptDKVSCurrentRecord(r *wire.DKVSRecord) (bool, error) {
	if s.accept != nil {
		s.accept(r)
	}
	return s.handlerTestStore.AcceptDKVSCurrentRecord(r)
}
func (s *nodeCurrentTestStore) DKVSNetworkSyncBaseline(path string) (dkvs.ActiveMeta, error) {
	return dkvs.ActiveMeta{Scope: dkvs.ActiveScope{Prefix: path, Network: true}}, nil
}
func (s *nodeCurrentTestStore) ApplyDKVSPathSnapshotFrom(p *dkvs.PathSnapshot, _ dkvs.ActiveMeta) (int, error) {
	if s.applyError != nil {
		return 0, s.applyError
	}
	return s.ApplyDKVSPathSnapshot(p)
}
func (s *nodeCurrentTestStore) DKVSNetworkPaths() ([]string, error) { return nil, nil }

func TestNodeSyncRetainsWorkAcrossDisconnectAndFailedInstall(t *testing.T) {
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := &nodeCurrentTestStore{handlerTestStore: &handlerTestStore{}}
	node := &NodeState{}
	peerA, peerB := &PeerState{}, &PeerState{}
	defer peerA.Close()
	defer peerB.Close()
	var request *wire.MsgDKVSSyncRequest
	h := Handler{Store: store, Node: node, Peer: peerA, TrustedSource: true,
		ValidatorID: hex.EncodeToString(key.PubKey().SerializeCompressed()),
		Send:        func(msg wire.Message) { request, _ = msg.(*wire.MsgDKVSSyncRequest) }}
	h.QueuePathSync("/svc/a.btc")
	h.QueuePathSync("/svc/b.btc")
	peerA.Close()
	if node.Ready() {
		t.Fatal("disconnect discarded outstanding directories")
	}
	h.Peer = peerB
	h.QueueSync(nil)
	if request.Filters[0].Target != "/svc/b.btc" {
		t.Fatalf("queued directory lost after close: %+v", request)
	}
	complete := func() {
		t.Helper()
		path := request.Filters[0].Target
		rows, err := pathSnapshotWireRecords(&dkvs.PathSnapshot{Path: path,
			PathMeta: &dkvs.PathMeta{Version: 3, Path: path, ViewHeight: 1}, ServerTimeMS: 1})
		if err != nil {
			t.Fatal(err)
		}
		response := &wire.MsgDKVSSyncResponse{SessionID: request.SessionID, Records: rows, Done: true}
		response.SourceSignature = ecdsa.Sign(key, chainhash.HashB(SyncAuthPayload(h.Net, request.Cursor, request.Filters, response))).Serialize()
		h.OnSyncResponse(response)
	}
	complete()
	if node.Ready() || request.Filters[0].Target != "/svc/a.btc" {
		t.Fatal("node finished while disconnected source's directory was incomplete")
	}
	store.applyError = dkvs.ErrStaleEndpoint
	complete()
	if node.Ready() {
		t.Fatal("failed installation opened Notify gate")
	}
	store.applyError = nil
	// A new eligible connection/request can resume the retained job immediately.
	h.QueueSync(nil)
	complete()
	if !node.Ready() {
		t.Fatal("all successful installations failed to finish node sync")
	}
}

func TestNodeNotifyDrainIncludesArrivalsDuringReplay(t *testing.T) {
	store := &nodeCurrentTestStore{handlerTestStore: &handlerTestStore{}}
	node, peer := &NodeState{}, &PeerState{}
	defer peer.Close()
	h := Handler{Store: store, Node: node, Peer: peer, TrustedSource: true, ValidatorID: "valid-core", LocalServices: wire.SFNodeMiner}
	first := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 1}
	second := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 2}
	var seen []uint64
	store.accept = func(r *wire.DKVSRecord) {
		seen = append(seen, r.Seq)
		if node.Ready() {
			t.Fatal("READY opened before pending Notify finished")
		}
		if r.Seq == 1 {
			h.OnNotify(NotifyForRecord(second))
			h.markReadyAndDrain() // Another finisher cannot start a second drain.
		}
	}
	h.OnNotify(NotifyForRecord(first))
	h.markReadyAndDrain()
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 || !node.Ready() {
		t.Fatalf("arrival order=%v ready=%t", seen, node.Ready())
	}
	if len(node.pending) != 0 || node.pendingBytes != 0 {
		t.Fatal("arrival during handoff was stranded")
	}
	if node.bufferPendingNotify(NotifyForRecord(second)) {
		t.Fatal("stale ingress check queued after READY")
	}
}

func TestNodeOverflowRediscoveryDoesNotOpenNotifyGate(t *testing.T) {
	node, peer := &NodeState{pendingOverflow: true}, &PeerState{}
	defer peer.Close()
	var request *wire.MsgDKVSSyncRequest
	h := Handler{Store: &nodeCurrentTestStore{handlerTestStore: &handlerTestStore{}}, Node: node, Peer: peer,
		TrustedSource: true, ValidatorID: "valid-core", Send: func(m wire.Message) { request, _ = m.(*wire.MsgDKVSSyncRequest) }}
	h.markReadyAndDrain()
	if node.Ready() || request == nil || len(request.Filters) != 0 {
		t.Fatal("overflow did not require full rediscovery")
	}
}

func TestAcceptedPageCannotBeExpiredByItsOldTimer(t *testing.T) {
	peer := &PeerState{}
	defer peer.Close()
	start, err := peer.StartPathSync("/svc/a.btc", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	action, err := peer.AcceptSyncResponse(&wire.MsgDKVSSyncResponse{SessionID: start.Request.SessionID, NextCursor: []byte("next")},
		func([]byte, []wire.DKVSSyncFilter, *wire.MsgDKVSSyncResponse) bool { return true }, nil, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if peer.TimeoutPathSyncRequest(start.Request.SessionID, start.Request.Cursor) {
		t.Fatal("old page timeout canceled an accepted response")
	}
	if _, err = peer.ContinuePathSync(action.NextCursor, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestCapturedSnapshotLifetime(t *testing.T) {
	for _, finish := range []string{"complete", "cancel", "close", "expire"} {
		t.Run(finish, func(t *testing.T) {
			peer := &PeerState{}
			defer peer.Close()
			path := "/name/lifetime.btc"
			store := &handlerTestStore{pathSnapshot: &dkvs.PathSnapshot{Path: path,
				PathMeta: &dkvs.PathMeta{Version: 3, Path: path}, ServerTimeMS: 1}}
			request := &wire.MsgDKVSSyncRequest{SessionID: 1, Filters: []wire.DKVSSyncFilter{{Type: pathSyncFilterType, Target: path}}}
			if !peer.BeginServe(request) {
				t.Fatal("serve not started")
			}
			snapshot, err := peer.pathServeSnapshot(request, store, path)
			if err != nil || snapshot == nil {
				t.Fatalf("capture: %v", err)
			}
			if finish == "expire" {
				// Exercise the same expiry callback with a short test timer.
				done := make(chan struct{})
				peer.scheduleSync("serve", 0, func() { peer.expirePathServe(request.SessionID, snapshot); close(done) })
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("expiry did not release snapshot")
				}
			} else if finish == "complete" {
				peer.FinishServe(request.SessionID)
			} else if finish == "cancel" {
				peer.CancelServe()
			} else {
				peer.Close()
			}
			if peer.serveSnapshot != nil || peer.serveActive {
				t.Fatal("finished session retained snapshot")
			}
			peer.syncLifecycle.mu.Lock()
			defer peer.syncLifecycle.mu.Unlock()
			if len(peer.syncLifecycle.timers) != 0 {
				t.Fatal("finished snapshot retained its timer")
			}
		})
	}
}

func TestOldServeTimeoutCannotDiscardReplacementSnapshot(t *testing.T) {
	peer := &PeerState{}
	defer peer.Close()
	path := "/name/lifetime.btc"
	store := &handlerTestStore{pathSnapshot: &dkvs.PathSnapshot{Path: path, PathMeta: &dkvs.PathMeta{Version: 3, Path: path}, ServerTimeMS: 1}}
	request := &wire.MsgDKVSSyncRequest{SessionID: 1, Filters: []wire.DKVSSyncFilter{{Type: pathSyncFilterType, Target: path}}}
	peer.BeginServe(request)
	old, err := peer.pathServeSnapshot(request, store, path)
	if err != nil {
		t.Fatal(err)
	}
	request.SessionID = 2
	peer.BeginServe(request)
	_, err = peer.pathServeSnapshot(request, store, path)
	if err != nil {
		t.Fatal(err)
	}
	peer.expirePathServe(1, old)
	if !peer.serveActive || peer.serveSnapshot == nil {
		t.Fatal("stale timer cleared a replacement serve session")
	}
}
