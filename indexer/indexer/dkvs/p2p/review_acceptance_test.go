package p2p

import (
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/wire"
)

// A stays connected but never answers. The existing repair callback represents
// the server's selection of another eligible, connected source; no new scheduler
// or alternate synchronization implementation is supplied by this test.
func TestDKVSReviewTimedOutSourceYieldsToHealthyPeer(t *testing.T) {
	for _, path := range []string{"/svc/review.btc", ""} {
		name := "directory"
		if path == "" {
			name = "discovery"
		}
		t.Run(name, func(t *testing.T) {
			node := &NodeState{}
			peerA, peerB := &PeerState{}, &PeerState{}
			defer peerA.Close()
			defer peerB.Close()
			store := &nodeCurrentTestStore{handlerTestStore: &handlerTestStore{}}
			aRequests, bRequests := make(chan *wire.MsgDKVSSyncRequest, 4), make(chan *wire.MsgDKVSSyncRequest, 4)
			b := Handler{Store: store, Node: node, Peer: peerB, TrustedSource: true, ValidatorID: "healthy-core",
				Send: func(m wire.Message) {
					if r, ok := m.(*wire.MsgDKVSSyncRequest); ok {
						bRequests <- r
					}
				}}
			a := Handler{Store: store, Node: node, Peer: peerA, TrustedSource: true, ValidatorID: "silent-core",
				RequestPathRepair: func(p string) {
					if p == "" {
						b.QueueSync(nil)
					} else {
						b.QueuePathSync(p)
					}
				},
				Send: func(m wire.Message) {
					if r, ok := m.(*wire.MsgDKVSSyncRequest); ok {
						aRequests <- r
					}
				}}
			if path == "" {
				a.QueueSync(nil)
			} else {
				a.QueuePathSync(path)
			}
			first := <-aRequests
			if path != "" {
				peerA.syncMtx.Lock()
				peerA.syncBaseline.Generation = 7
				peerA.syncMtx.Unlock()
			}
			b.QueueSync(nil) // B is available throughout A's outstanding request.
			select {
			case <-bRequests:
				t.Fatal("source switched before its second attempt")
			case request := <-aRequests:
				if request.SessionID != first.SessionID || string(request.Cursor) != string(first.Cursor) {
					t.Fatal("retry changed the outstanding page")
				}
			case <-time.After(PathSyncRequestTimeout + 2*time.Second):
				t.Fatal("same source did not receive its second attempt")
			}
			if !node.ownsSync(peerA) || node.Ready() {
				t.Fatal("first timeout lost ownership or released READY")
			}
			if path != "" {
				peerA.syncMtx.Lock()
				generation := peerA.syncBaseline.Generation
				peerA.syncMtx.Unlock()
				if generation != 7 {
					t.Fatal("retry recaptured the installation baseline")
				}
			}
			select {
			case request := <-bRequests:
				if got, _ := pathSyncFilter(request.Filters); got != path {
					t.Fatalf("wrong repair: %+v", request)
				}
			case request := <-aRequests:
				t.Fatalf("timeout selected the silent source again: old session=%d new session=%d; healthy peer received no request", first.SessionID, request.SessionID)
			case <-time.After(PathSyncRequestTimeout + 2*time.Second):
				t.Fatal("timeout did not dispatch the retained directory to the healthy peer")
			}
			if peerA.Closed() || node.Ready() {
				t.Fatal("handoff must retain the connection and keep READY false before installation")
			}

		})
	}
}
