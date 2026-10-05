package p2p

import (
	"encoding/hex"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestDKVSReviewSnapshotsPreserveNotifySubscriptions(t *testing.T) {
	peer := &PeerState{}
	defer peer.Close()
	paths := []string{"/svc/a", "/svc/b", "/svc/c"}
	notifies := make([]*wire.MsgDKVSNotify, len(paths))
	for n, path := range paths {
		notifies[n] = NotifyForRecord(&wire.DKVSRecord{Version: dkvs.Version, Key: path + "/value", Seq: 1})
	}
	var session uint64
	begin := func(filters ...wire.DKVSSyncFilter) *wire.MsgDKVSSyncRequest {
		session++
		request := &wire.MsgDKVSSyncRequest{SessionID: session, Filters: filters}
		if !peer.BeginServe(request) {
			t.Fatal("serve rejected")
		}
		return request
	}
	check := func(a, b, c bool) {
		t.Helper()
		for n, want := range []bool{a, b, c} {
			if got := peer.WantsNotify(notifies[n], false); got != want {
				t.Fatalf("session=%d path=%s notify=%t want=%t", session, paths[n], got, want)
			}
		}
	}
	// A snapshot alone grants its temporary serving scope, not an implicit
	// persistent subscription. Normal connections declare all subscriptions.
	request := begin(wire.DKVSSyncFilter{Type: pathSyncFilterType, Target: paths[0]})
	check(true, false, false)
	peer.FinishServe(request.SessionID)
	check(false, false, false)
	request = begin(wire.DKVSSyncFilter{Type: "prefix", Target: paths[0]}, wire.DKVSSyncFilter{Type: "prefix", Target: paths[1]})
	peer.FinishServe(request.SessionID)
	check(true, true, false)
	for _, path := range paths[:2] {
		request = begin(wire.DKVSSyncFilter{Type: pathSyncFilterType, Target: path})
		peer.FinishServe(request.SessionID)
		check(true, true, false)
	}
	request = begin(wire.DKVSSyncFilter{Type: pathSyncFilterType, Target: paths[2]})
	check(true, true, true)
	peer.CancelServe()
	check(true, true, false)
	// Exercise the actual snapshot-expiry callback without waiting two minutes.
	request = begin(wire.DKVSSyncFilter{Type: pathSyncFilterType, Target: paths[2]})
	store := &handlerTestStore{pathSnapshot: &dkvs.PathSnapshot{Path: paths[2], PathMeta: &dkvs.PathMeta{Version: 3, Path: paths[2]}, ServerTimeMS: 1}}
	snapshot, err := peer.pathServeSnapshot(request, store, paths[2])
	if err != nil {
		t.Fatal(err)
	}
	peer.expirePathServe(request.SessionID, snapshot)
	check(true, true, false)
	// A canceled declaration cannot unsubscribe A; a completed declaration can.
	request = begin(wire.DKVSSyncFilter{Type: "prefix", Target: paths[1]})
	peer.CancelServe()
	check(true, true, false)
	request = begin(wire.DKVSSyncFilter{Type: "prefix", Target: paths[1]})
	peer.FinishServe(request.SessionID)
	check(false, true, false)
}

func TestDKVSReviewRejectedDiscoveryYieldsToHealthyPeer(t *testing.T) {
	node := &NodeState{}
	peerA, peerB := &PeerState{}, &PeerState{}
	defer peerA.Close()
	defer peerB.Close()
	store := &nodeCurrentTestStore{handlerTestStore: &handlerTestStore{}}
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	var first, replacement *wire.MsgDKVSSyncRequest
	b := Handler{Store: store, Node: node, Peer: peerB, TrustedSource: true,
		ValidatorID: hex.EncodeToString(key.PubKey().SerializeCompressed()),
		Send:        func(m wire.Message) { replacement, _ = m.(*wire.MsgDKVSSyncRequest) }}
	a := b
	a.Peer = peerA
	a.Send = func(m wire.Message) { first, _ = m.(*wire.MsgDKVSSyncRequest) }
	a.RequestPathRepair = func(path string) {
		if path != "" {
			t.Fatalf("expected discovery repair, got %q", path)
		}
		b.QueueSync(nil)
	}
	a.QueueSync(nil)
	b.QueueSync(nil) // Healthy B is already connected and joins the same round.
	if first == nil || replacement != nil {
		t.Fatal("initial discovery ownership is incorrect")
	}
	// Correct session but missing source signature: reject it, retain discovery,
	// and use the existing repair callback to choose another connected source.
	a.OnSyncResponse(&wire.MsgDKVSSyncResponse{SessionID: first.SessionID, Done: true})
	if node.Ready() || node.ownsSync(peerA) {
		t.Fatal("rejected discovery was accepted or retained its failed session")
	}
	if replacement == nil || !node.ownsSync(peerB) {
		t.Fatalf("rejected discovery stranded the node: ready=%t pending=%q replacement=%v", node.Ready(), node.pendingPathSync, replacement)
	}
}
