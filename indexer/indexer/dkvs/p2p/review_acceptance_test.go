package p2p

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestDKVSNoSignerHandoff(t *testing.T) {
	const path = "/svc/handoff.btc"
	for _, job := range []string{"discovery", "directory"} {
		t.Run(job, func(t *testing.T) {
			key, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			store := &nodeCurrentTestStore{handlerTestStore: &handlerTestStore{}}
			a := NewHandler(store, nil, nil, nil)
			a.TrustedSource, a.ValidatorID = true, hex.EncodeToString(key.PubKey().SerializeCompressed())
			b := a
			b.Peer = &PeerState{}
			source := NewHandler(&handlerTestStore{pathSnapshot: &dkvs.PathSnapshot{
				Path: path, PathMeta: &dkvs.PathMeta{Version: 1, Path: path}, ServerTimeMS: 1,
			}}, nil, nil, nil)
			source.MirrorAuthority = true
			signed := source
			signed.Peer = &PeerState{}
			for _, p := range []*PeerState{a.Peer, b.Peer, source.Peer, signed.Peer} {
				t.Cleanup(p.Close)
			}
			signed.Sign = func(payload []byte) ([]byte, error) {
				return ecdsa.Sign(key, chainhash.HashB(payload)).Serialize(), nil
			}
			b.Send = func(m wire.Message) { signed.OnSyncRequest(m.(*wire.MsgDKVSSyncRequest)) }
			signed.Send = func(m wire.Message) { b.OnSyncResponse(m.(*wire.MsgDKVSSyncResponse)) }
			attempts := 0
			a.Send = func(m wire.Message) {
				attempts++
				source.OnSyncRequest(m.(*wire.MsgDKVSSyncRequest))
			}
			source.Send = func(m wire.Message) {
				var buf bytes.Buffer
				require.NoError(t, wire.WriteMessage(&buf, m, wire.ProtocolVersion, wire.MainNet))
				response, _, err := wire.ReadMessage(&buf, wire.ProtocolVersion, wire.MainNet)
				require.NoError(t, err)
				reject := response.(*wire.MsgReject)
				require.False(t, b.OnSyncReject(reject), "idle endpoint sharing the public key")
				require.True(t, a.OnSyncReject(reject))
			}
			a.RequestPathRepair = func(p string) {
				if job == "discovery" {
					require.Empty(t, p)
				} else {
					require.Equal(t, path, p)
				}
				require.True(t, b.ResumeSync())
			}
			if job == "discovery" {
				a.QueueSync(nil)
			} else {
				a.QueuePathSync(path)
				require.NotNil(t, store.appliedPathSnapshot)
			}
			require.True(t, a.Node.Ready())
			require.True(t, b.ShouldRequestSync())
			require.False(t, a.ShouldRequestSync())
			a.QueueSync(nil)
			a.QueuePathSync(path)
			require.Equal(t, 1, attempts)
			if job == "discovery" {
				t.Run("repair_while_source_offline", func(t *testing.T) {
					b.Peer.Close()
					a.LocalServices = wire.SFNodeMiner
					a.RequestPathRepair = func(p string) {
						require.Equal(t, path, p)
						require.False(t, a.Node.Ready(), "repair must be queued before selecting a peer")
					}
					record, err := dkvs.NewRecord(path+"/item", []byte("value"),
						key.PubKey().SerializeCompressed(), dkvs.RecordOptions{Seq: 2, IssueHeight: 1})
					require.NoError(t, err)
					hash := dkvs.SigningHash(record)
					record.Signature = ecdsa.Sign(key, hash[:]).Serialize()
					a.Peer.TrackRequest(&wire.MsgDKVSGet{Keys: []string{record.Key}}, time.Now())
					a.OnData(&wire.MsgDKVSData{Records: []*wire.DKVSRecord{record}})
					require.False(t, a.Node.Ready())
					require.Equal(t, 1, attempts)

					b.Peer = &PeerState{}
					t.Cleanup(b.Peer.Close)
					b.QueueSync(nil)
					require.NotNil(t, store.appliedPathSnapshot)
					require.Equal(t, path, store.appliedPathSnapshot.Path)
					require.True(t, b.Node.Ready())
				})
			}
			reconnected := a
			reconnected.Peer = &PeerState{}
			defer reconnected.Peer.Close()
			require.True(t, reconnected.ShouldRequestSync())
		})
	}
}

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
