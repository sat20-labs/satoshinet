package p2p

import (
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDKVSReviewRetriedPageDuplicateKeepsHealthySourceSession(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	const path = "/svc/fifth-retry.btc"
	meta := &dkvs.PathMeta{Version: 4, Path: path, ViewHeight: 1}
	snapshot := &dkvs.PathSnapshot{Path: path, PathMeta: meta, ServerTimeMS: 1}
	// Including the metadata row, this naturally requires more than one page.
	for i := 0; i < 2*wire.MaxDKVSRecordsPerMsg; i++ {
		record, err := dkvs.NewRecord(fmt.Sprintf("%s/key%03d", path, i), []byte("value"),
			priv.PubKey().SerializeCompressed(), dkvs.RecordOptions{Seq: 1, IssueHeight: 1})
		require.NoError(t, err)
		hash := dkvs.SigningHash(record)
		record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
		require.NoError(t, dkvs.VerifyRecordForClient(record, dkvs.RecordVerificationOptions{}))
		snapshot.Records = append(snapshot.Records, record)
		meta.ActiveRecords++
		meta.ActiveTotalSize += uint64(dkvs.RecordSize(record))
		xorPathMetaRootForTest(&meta.StateRoot, record)
	}
	var sourcePeer, receiverPeer PeerState
	defer sourcePeer.Close()
	defer receiverPeer.Close()
	var sourceNode, receiverNode NodeState
	var requests []*wire.MsgDKVSSyncRequest
	var responses []*wire.MsgDKVSSyncResponse
	penalties := uint32(0)
	repairs := 0
	source := Handler{Store: &handlerTestStore{pathSnapshot: snapshot}, Peer: &sourcePeer, Node: &sourceNode,
		Net: chaincfg.TestNetParams.Net, MirrorAuthority: true,
		Sign: func(payload []byte) ([]byte, error) {
			return ecdsa.Sign(priv, chainhash.HashB(payload)).Serialize(), nil
		},
		Send: func(msg wire.Message) {
			if response, ok := msg.(*wire.MsgDKVSSyncResponse); ok {
				responses = append(responses, response)
			}
		}}
	receiverStore := &handlerTestStore{}
	receiver := Handler{Store: receiverStore, Peer: &receiverPeer, Node: &receiverNode,
		Net: chaincfg.TestNetParams.Net, TrustedSource: true, LocalServices: wire.SFNodeMiner,
		ValidatorID: hex.EncodeToString(priv.PubKey().SerializeCompressed()),
		Send: func(msg wire.Message) {
			if request, ok := msg.(*wire.MsgDKVSSyncRequest); ok {
				requests = append(requests, request)
			}
		},
		Penalize:          func(persistent, transient uint32, _ string) { penalties += persistent + transient },
		RequestPathRepair: func(string) { repairs++ }}
	receiver.QueuePathSync(path)
	require.Len(t, requests, 1)
	// Invoke the exact same-source retry branch without waiting ten seconds.
	receiver.sendPathSyncRequestAttempt(requests[0], 2)
	require.Len(t, requests, 2)
	require.Equal(t, requests[0].SessionID, requests[1].SessionID)
	require.Equal(t, requests[0].Cursor, requests[1].Cursor)
	source.OnSyncRequest(requests[0])
	source.OnSyncRequest(requests[1])
	require.Len(t, responses, 2)
	require.False(t, responses[0].Done)
	for _, response := range responses {
		require.True(t, VerifySyncSignature(receiver.Net, receiver.ValidatorID, requests[0].Cursor, requests[0].Filters, response),
			"both duplicate responses have genuine source signatures for the retried request")
	}
	receiver.OnSyncResponse(responses[0])
	require.Len(t, requests, 3, "the first accepted page must request the next cursor")
	require.NotEmpty(t, requests[2].Cursor)
	receiver.OnSyncResponse(responses[1])
	activePath, active := receiverPeer.ActivePathSync()
	t.Logf("after duplicate: active=%v path=%s penalties=%d repairs=%d", active, activePath, penalties, repairs)
	assert.True(t, active, "a correctly signed duplicate of the retried page must not discard the next-page session")
	assert.Equal(t, uint32(0), penalties, "a healthy source must not be penalized for the receiver's own retry")
	assert.Zero(t, repairs, "a duplicate response must not force source replacement")
	if active {
		receiver.sendPathSyncRequestAttempt(requests[2], 2)
		source.OnSyncRequest(requests[2])
		source.OnSyncRequest(requests[3])
		require.Len(t, responses, 4)
		require.False(t, responses[2].Done)
		receiver.OnSyncResponse(responses[2])
		receiver.OnSyncResponse(responses[3])
		_, active = receiverPeer.ActivePathSync()
		require.True(t, active, "a middle-page duplicate must also preserve the session")
		require.Len(t, requests, 5)
		source.OnSyncRequest(requests[4])
		require.Len(t, responses, 5)
		require.True(t, responses[4].Done)
		receiver.OnSyncResponse(responses[4])
		require.NotNil(t, receiverStore.appliedPathSnapshot)
		installed := receiverStore.appliedPathSnapshot
		receiver.OnSyncResponse(responses[4])
		assert.Same(t, installed, receiverStore.appliedPathSnapshot, "a late final page must not install twice")
		assert.Zero(t, penalties)
		assert.Zero(t, repairs)
		assert.Len(t, receiverStore.appliedPathSnapshot.Records, len(snapshot.Records))
		assert.True(t, receiverNode.Ready(), "the remaining page must complete the original snapshot")
	}
}

func TestDKVSReviewFirstPageRetryKeepsCapturedSnapshot(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	const path = "/svc/fifth-capture.btc"
	meta := &dkvs.PathMeta{Version: 4, Path: path, ViewHeight: 1}
	snapshot := &dkvs.PathSnapshot{Path: path, PathMeta: meta, ServerTimeMS: 1}
	for i := 0; i < 2; i++ {
		record, err := dkvs.NewRecord(fmt.Sprintf("%s/key%d", path, i), []byte("v1"),
			priv.PubKey().SerializeCompressed(), dkvs.RecordOptions{Seq: 1, IssueHeight: 1})
		require.NoError(t, err)
		hash := dkvs.SigningHash(record)
		record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
		snapshot.Records = append(snapshot.Records, record)
		meta.ActiveRecords++
		meta.ActiveTotalSize += uint64(dkvs.RecordSize(record))
		xorPathMetaRootForTest(&meta.StateRoot, record)
	}
	store := &handlerTestStore{pathSnapshot: snapshot}
	var peer PeerState
	defer peer.Close()
	var node NodeState
	var responses []*wire.MsgDKVSSyncResponse
	handler := Handler{Store: store, Peer: &peer, Node: &node, MirrorAuthority: true,
		Net: chaincfg.TestNetParams.Net,
		Sign: func(payload []byte) ([]byte, error) {
			return ecdsa.Sign(priv, chainhash.HashB(payload)).Serialize(), nil
		},
		Send: func(msg wire.Message) {
			if response, ok := msg.(*wire.MsgDKVSSyncResponse); ok {
				responses = append(responses, response)
			}
		}}
	// A small permitted page makes snapshot ownership observable without a
	// large fixture. The same BeginServe branch handles the default page size.
	request := &wire.MsgDKVSSyncRequest{SessionID: 17, Limit: 1,
		Filters: []wire.DKVSSyncFilter{{Type: pathSyncFilterType, Target: path}}}
	handler.OnSyncRequest(request)
	require.Len(t, responses, 1)
	require.False(t, responses[0].Done)
	firstCursor, firstRoot := append([]byte(nil), responses[0].NextCursor...), responses[0].CheckpointRoot

	// Replace the endpoint's current view after the first page was captured.
	// The serving session should continue owning the previous immutable view.
	newMeta := *meta
	newMeta.ViewHeight = 2
	newSnapshot := &dkvs.PathSnapshot{Path: path, PathMeta: &newMeta, ServerTimeMS: 2,
		Records: append([]*wire.DKVSRecord(nil), snapshot.Records...)}
	changed, err := dkvs.NewRecord(snapshot.Records[0].Key, []byte("v2"),
		priv.PubKey().SerializeCompressed(), dkvs.RecordOptions{Seq: 2, IssueHeight: 2})
	require.NoError(t, err)
	hash := dkvs.SigningHash(changed)
	changed.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	xorPathMetaRootForTest(&newMeta.StateRoot, snapshot.Records[0])
	xorPathMetaRootForTest(&newMeta.StateRoot, changed)
	newMeta.ActiveTotalSize = newMeta.ActiveTotalSize - uint64(dkvs.RecordSize(snapshot.Records[0])) + uint64(dkvs.RecordSize(changed))
	newSnapshot.Records[0] = changed
	store.pathSnapshot = newSnapshot
	require.NotEqual(t, firstRoot, newMeta.StateRoot)
	handler.OnSyncRequest(request) // same session, same first-page retry
	require.Len(t, responses, 2)
	require.True(t, VerifySyncSignature(handler.Net, hex.EncodeToString(priv.PubKey().SerializeCompressed()),
		nil, request.Filters, responses[1]))
	t.Logf("first root=%s retry root=%s", firstRoot, responses[1].CheckpointRoot)
	assert.Equal(t, firstRoot, responses[1].CheckpointRoot, "first-page retry must reuse the session's captured snapshot")
	wrongScope := *request
	wrongScope.Filters = []wire.DKVSSyncFilter{{Type: pathSyncFilterType, Target: "/svc/other.btc"}}
	handler.OnSyncRequest(&wrongScope)
	assert.Len(t, responses, 2, "a reused session cannot silently change its filters")
	next := *request
	next.Cursor, next.Limit = firstCursor, wire.MaxDKVSRecordsPerMsg
	handler.OnSyncRequest(&next)
	if assert.Len(t, responses, 3, "the cursor from the first response must still receive the rest of that snapshot") {
		assert.True(t, responses[2].Done)
		assert.Equal(t, firstRoot, responses[2].CheckpointRoot)
	}
	request.SessionID++
	handler.OnSyncRequest(request)
	require.Len(t, responses, 4)
	assert.Equal(t, newMeta.StateRoot, responses[3].CheckpointRoot, "a new session must capture current state")
}

func TestDKVSReviewDuplicatePagePreservesTimerAndRejectsTampering(t *testing.T) {
	for _, discovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "path", true: "discovery"}[discovery], func(t *testing.T) {
			priv, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			var peer PeerState
			defer peer.Close()
			var node NodeState
			var requests []*wire.MsgDKVSSyncRequest
			var penalties, repairs int
			handler := Handler{Store: &handlerTestStore{}, Peer: &peer, Node: &node,
				Net: chaincfg.TestNetParams.Net, TrustedSource: true,
				LocalServices: wire.SFNodeMiner, RemoteServices: wire.SFNodeMiner,
				ValidatorID: hex.EncodeToString(priv.PubKey().SerializeCompressed()),
				Send: func(msg wire.Message) {
					if request, ok := msg.(*wire.MsgDKVSSyncRequest); ok {
						requests = append(requests, request)
					}
				},
				Penalize:          func(persistent, transient uint32, _ string) { penalties += int(persistent + transient) },
				RequestPathRepair: func(string) { repairs++ }}
			if discovery {
				handler.QueueSync(nil)
			} else {
				handler.QueuePathSync("/svc/fifth-timer.btc")
			}
			require.Len(t, requests, 1)
			first := &wire.MsgDKVSSyncResponse{SessionID: requests[0].SessionID, NextCursor: []byte("next")}
			first.SourceSignature = ecdsa.Sign(priv, chainhash.HashB(SyncAuthPayload(handler.Net, nil, requests[0].Filters, first))).Serialize()
			handler.OnSyncResponse(first)
			require.Len(t, requests, 2)
			peer.syncLifecycle.mu.Lock()
			timer := peer.syncLifecycle.timers["request"]
			peer.syncLifecycle.mu.Unlock()
			require.NotNil(t, timer)
			handler.OnSyncResponse(first)
			peer.syncLifecycle.mu.Lock()
			currentTimer := peer.syncLifecycle.timers["request"]
			peer.syncLifecycle.mu.Unlock()
			assert.Same(t, timer, currentTimer, "ignoring a duplicate cannot cancel or replace the next page timeout")
			assert.Len(t, requests, 2, "ignoring a duplicate cannot submit another next-page request")
			assert.Zero(t, penalties)
			assert.Zero(t, repairs)
			otherSession := *first
			otherSession.SessionID++
			handler.OnSyncResponse(&otherSession)
			assert.Zero(t, penalties, "an unrelated session must not terminate the current one")
			tampered := *first
			tampered.SourceSignature = nil
			handler.OnSyncResponse(&tampered)
			assert.Equal(t, 20, penalties, "signature changes cannot be treated as an authenticated duplicate")
			assert.Equal(t, 1, repairs)
			peer.syncMtx.Lock()
			active := peer.syncActive
			peer.syncMtx.Unlock()
			assert.False(t, active)
		})
	}
}

func TestDKVSReviewSignedConflictingPreviousPageIsRejected(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	var peer PeerState
	defer peer.Close()
	start, err := peer.StartSync(nil, true, nil, time.Now())
	require.NoError(t, err)
	first := &wire.MsgDKVSSyncResponse{SessionID: start.Request.SessionID, NextCursor: []byte("next")}
	sign := func(msg *wire.MsgDKVSSyncResponse) {
		msg.SourceSignature = ecdsa.Sign(priv, chainhash.HashB(SyncAuthPayload(chaincfg.TestNetParams.Net, nil, nil, msg))).Serialize()
	}
	sign(first)
	verify := func(cursor []byte, filters []wire.DKVSSyncFilter, msg *wire.MsgDKVSSyncResponse) bool {
		return VerifySyncSignature(chaincfg.TestNetParams.Net, hex.EncodeToString(priv.PubKey().SerializeCompressed()), cursor, filters, msg)
	}
	_, err = peer.AcceptSyncResponse(first, verify, nil, time.Now(), true)
	require.NoError(t, err)
	conflict := *first
	conflict.CheckpointRoot[0] = 1
	sign(&conflict)
	_, err = peer.AcceptSyncResponse(&conflict, verify, nil, time.Now(), true)
	assert.ErrorIs(t, err, ErrUnauthenticatedSync, "a different response to an old cursor is not the accepted duplicate")
}
