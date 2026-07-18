package p2p

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestSyncSignatureBindsSessionState(t *testing.T) {
	privateKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	filters := []wire.DKVSSyncFilter{{Type: "prefix", Target: "/tmp"}}
	cursor := []byte("cursor")
	msg := &wire.MsgDKVSSyncResponse{
		SessionID:      7,
		NextCursor:     []byte("next"),
		Done:           true,
		CheckpointRoot: chainhash.DoubleHashH([]byte("root")),
		Records:        []*wire.DKVSRecord{{Version: 1, Key: "/tmp/a", Seq: 1}},
	}
	payload := SyncAuthPayload(chaincfg.TestNetParams.Net, cursor, filters, msg)
	msg.SourceSignature = ecdsa.Sign(privateKey, chainhash.HashB(payload)).Serialize()
	validatorID := hex.EncodeToString(privateKey.PubKey().SerializeCompressed())
	if !VerifySyncSignature(chaincfg.TestNetParams.Net, validatorID, cursor, filters, msg) {
		t.Fatal("valid DKVS mirror signature rejected")
	}
	msg.CheckpointRoot[0] ^= 1
	if VerifySyncSignature(chaincfg.TestNetParams.Net, validatorID, cursor, filters, msg) {
		t.Fatal("tampered DKVS mirror root accepted")
	}
}

func TestShouldRequestSync(t *testing.T) {
	tests := []struct {
		name              string
		localServices     wire.ServiceFlag
		remoteServices    wire.ServiceFlag
		subscriptionCount int
		want              bool
	}{
		{name: "miner to miner", localServices: wire.SFNodeMiner, remoteServices: wire.SFNodeMiner, want: true},
		{name: "subscribed ordinary", remoteServices: wire.SFNodeMiner, subscriptionCount: 1, want: true},
		{name: "ordinary without subscriptions", remoteServices: wire.SFNodeMiner, want: false},
		{name: "remote non-miner", remoteServices: wire.SFNodeNetwork, subscriptionCount: 1, want: false},
	}
	for _, test := range tests {
		if got := ShouldRequestSync(test.localServices, test.remoteServices, test.subscriptionCount); got != test.want {
			t.Fatalf("%s: got %v want %v", test.name, got, test.want)
		}
	}
}

func TestShouldRunAntiEntropy(t *testing.T) {
	if !ShouldRunAntiEntropy(wire.SFNodeMiner, 0) {
		t.Fatal("miner must run DKVS anti-entropy")
	}
	if !ShouldRunAntiEntropy(wire.SFNodeNetwork, 1) {
		t.Fatal("subscribed ordinary node must run DKVS anti-entropy")
	}
	if ShouldRunAntiEntropy(wire.SFNodeNetwork, 0) {
		t.Fatal("unsubscribed ordinary node must not run DKVS anti-entropy")
	}
}

func TestSyncSessionExpired(t *testing.T) {
	now := time.Unix(1000, 0)
	if syncSessionExpired(now.Add(-time.Minute), now) {
		t.Fatal("progressing session reported expired")
	}
	if !syncSessionExpired(now.Add(-SyncSessionTimeout), now) {
		t.Fatal("stalled session not expired")
	}
	if !syncSessionExpired(time.Time{}, now) {
		t.Fatal("zero activity session not expired")
	}
}

func TestTrustedGapRequiresMirror(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	if TrustedGapRequiresMirror(0, now) {
		t.Fatal("unknown trusted-source time forced a mirror")
	}
	lastSeen := now.Add(-TrustedMirrorGap + time.Second).Unix()
	if TrustedGapRequiresMirror(lastSeen, now) {
		t.Fatal("short trusted-source gap forced a mirror")
	}
	lastSeen = now.Add(-TrustedMirrorGap).Unix()
	if !TrustedGapRequiresMirror(lastSeen, now) {
		t.Fatal("long trusted-source gap did not force a mirror")
	}
}

func TestCheckpointRootMismatch(t *testing.T) {
	var root chainhash.Hash
	for i := range root {
		root[i] = byte(i)
	}
	if CheckpointRootMismatch(hex.EncodeToString(root[:]), root) {
		t.Fatal("matching root reported mismatch")
	}
	other := root
	other[0] ^= 0xff
	if !CheckpointRootMismatch(hex.EncodeToString(other[:]), root) || !CheckpointRootMismatch("bad", root) {
		t.Fatal("mismatching root was accepted")
	}
}

func TestSyncFilterConversionAndPayloadBound(t *testing.T) {
	subs := []dkvs.Subscription{
		{Type: dkvs.SubscriptionKey, Target: "/tmp/a"},
		{Type: dkvs.SubscriptionPrefix, Target: "/personal/abc"},
		{Type: dkvs.SubscriptionMailbox, Target: "/mail/box"},
		{Type: dkvs.SubscriptionService, Target: "/svc/wallet"},
	}
	filters := FiltersFromSubscriptions(subs)
	roundTrip := SubscriptionsFromFilters(filters)
	for n := range subs {
		if roundTrip[n] != subs[n] {
			t.Fatalf("round trip[%d]=%#v want=%#v", n, roundTrip[n], subs[n])
		}
	}
	filters = make([]wire.DKVSSyncFilter, wire.MaxDKVSSyncFilters)
	for index := range filters {
		filters[index] = wire.DKVSSyncFilter{Type: "prefix", Target: "/" + string(make([]byte, wire.MaxDKVSKeySize-1))}
	}
	got := SyncRequestPayloadLen(make([]byte, wire.MaxDKVSCursorSize), filters)
	if max := (&wire.MsgDKVSSyncRequest{}).MaxPayloadLength(wire.ProtocolVersion); got > int(max) {
		t.Fatalf("sync request payload=%d max=%d", got, max)
	}
}

func TestRequestTrackingRejectsUnsolicitedData(t *testing.T) {
	var state PeerState
	now := time.Unix(1000, 0)
	record := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 1}
	if state.ConsumeRequest(record, now) {
		t.Fatal("unrequested record was accepted")
	}
	state.TrackRequest(&wire.MsgDKVSGet{Keys: []string{record.Key}}, now)
	if !state.ConsumeRequest(record, now) || state.ConsumeRequest(record, now) {
		t.Fatal("key request was not single-use")
	}
	hash := dkvs.RecordHash(record)
	state.TrackRequest(&wire.MsgDKVSGet{RecordHashes: []chainhash.Hash{hash}}, now)
	if !state.ConsumeRequest(record, now) {
		t.Fatal("requested record hash was rejected")
	}
	state.TrackRequest(&wire.MsgDKVSGet{Keys: []string{record.Key}}, now)
	if state.ConsumeRequest(record, now.Add(RequestTTL)) {
		t.Fatal("expired request was accepted")
	}
}

func TestServeSyncBuffersAndCoalescesNotifications(t *testing.T) {
	var state PeerState
	filters := []wire.DKVSSyncFilter{{Type: string(dkvs.SubscriptionKey), Target: "/tmp/a"}}
	if !state.BeginServe(&wire.MsgDKVSSyncRequest{SessionID: 7, Filters: filters}) {
		t.Fatal("initial serve sync rejected")
	}
	if state.BeginServe(&wire.MsgDKVSSyncRequest{
		SessionID: 7, Cursor: []byte{1}, Filters: []wire.DKVSSyncFilter{{Type: "key", Target: "/tmp/b"}},
	}) {
		t.Fatal("continuation with changed filters accepted")
	}
	first := &wire.MsgDKVSNotify{Key: "/tmp/a", Seq: 1, Size: 10}
	second := &wire.MsgDKVSNotify{Key: "/tmp/a", Seq: 2, Size: 20}
	if !state.BufferNotify(first) || !state.BufferNotify(second) {
		t.Fatal("matching notifications were not buffered")
	}
	if state.BufferNotify(&wire.MsgDKVSNotify{Key: "/tmp/b", Seq: 1}) {
		t.Fatal("unrelated notification was buffered")
	}
	pending := state.FinishServe(7)
	if len(pending) != 1 || pending[0].Seq != 2 || pending[0].Key != "/tmp/a" {
		t.Fatalf("pending=%#v", pending)
	}
}

func TestMirrorResponseStagesUntilDone(t *testing.T) {
	var state PeerState
	now := time.Unix(1000, 0)
	start, err := state.StartSync(nil, true, true, nil, now)
	if err != nil || start.Request == nil {
		t.Fatalf("start=%#v err=%v", start, err)
	}
	record := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/a", Seq: 1}
	deleted := &wire.DKVSRecord{Version: dkvs.Version, Key: "/tmp/b", Seq: 2, Flags: dkvs.FlagTombstone}
	action, err := state.AcceptSyncResponse(&wire.MsgDKVSSyncResponse{
		SessionID: start.Request.SessionID, Records: []*wire.DKVSRecord{record, deleted}, Done: true,
	}, func([]byte, []wire.DKVSSyncFilter, *wire.MsgDKVSSyncResponse) bool { return true }, nil, now)
	if err != nil || !action.Done || !action.Mirror || len(action.MirrorRecords) != 1 || len(action.MirrorDeletes) != 1 {
		t.Fatalf("action=%#v err=%v", action, err)
	}
}
