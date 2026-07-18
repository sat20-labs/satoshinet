package main

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestShouldRequestDKVSSync(t *testing.T) {
	tests := []struct {
		name              string
		localServices     wire.ServiceFlag
		remoteServices    wire.ServiceFlag
		subscriptionCount int
		want              bool
	}{
		{
			name:           "miner to miner",
			localServices:  wire.SFNodeMiner,
			remoteServices: wire.SFNodeMiner,
			want:           true,
		},
		{
			name:              "subscribed ordinary node from miner",
			remoteServices:    wire.SFNodeMiner,
			subscriptionCount: 1,
			want:              true,
		},
		{
			name:           "ordinary node without subscriptions",
			remoteServices: wire.SFNodeMiner,
			want:           false,
		},
		{
			name:              "remote non-miner",
			remoteServices:    wire.SFNodeNetwork,
			subscriptionCount: 1,
			want:              false,
		},
	}
	for _, test := range tests {
		if got := shouldRequestDKVSSync(test.localServices, test.remoteServices, test.subscriptionCount); got != test.want {
			t.Fatalf("%s: got %v want %v", test.name, got, test.want)
		}
	}
}

func TestShouldRunDKVSAntiEntropy(t *testing.T) {
	if !shouldRunDKVSAntiEntropy(wire.SFNodeMiner, 0) {
		t.Fatalf("miner must run DKVS anti-entropy")
	}
	if !shouldRunDKVSAntiEntropy(wire.SFNodeNetwork, 1) {
		t.Fatalf("subscribed ordinary node must run DKVS anti-entropy")
	}
	if shouldRunDKVSAntiEntropy(wire.SFNodeNetwork, 0) {
		t.Fatalf("unsubscribed ordinary node must not run DKVS anti-entropy")
	}
}

func TestDKVSSyncSessionExpired(t *testing.T) {
	now := time.Unix(1000, 0)
	if dkvsSyncSessionExpired(now.Add(-time.Minute), now, 2*time.Minute) {
		t.Fatalf("progressing session reported expired")
	}
	if !dkvsSyncSessionExpired(now.Add(-2*time.Minute), now, 2*time.Minute) {
		t.Fatalf("stalled session not expired")
	}
	if !dkvsSyncSessionExpired(time.Time{}, now, 2*time.Minute) {
		t.Fatalf("zero activity session not expired")
	}
}

func TestDKVSCheckpointRootMismatch(t *testing.T) {
	var root chainhash.Hash
	for i := range root {
		root[i] = byte(i)
	}
	if dkvsCheckpointRootMismatch(hex.EncodeToString(root[:]), root) {
		t.Fatalf("matching root reported mismatch")
	}
	other := root
	other[0] ^= 0xff
	if !dkvsCheckpointRootMismatch(hex.EncodeToString(other[:]), root) {
		t.Fatalf("different root not reported as mismatch")
	}
	if !dkvsCheckpointRootMismatch("bad", root) {
		t.Fatalf("invalid local root not reported as mismatch")
	}
}

func TestDKVSSyncFilterConversion(t *testing.T) {
	subs := []dkvsindexer.Subscription{
		{Type: dkvsindexer.SubscriptionKey, Target: "/tmp/a"},
		{Type: dkvsindexer.SubscriptionPrefix, Target: "/personal/abc"},
		{Type: dkvsindexer.SubscriptionMailbox, Target: "/mail/box"},
		{Type: dkvsindexer.SubscriptionService, Target: "/svc/wallet"},
	}
	filters := dkvsWireFiltersFromSubscriptions(subs)
	if len(filters) != len(subs) {
		t.Fatalf("filters len=%d want=%d", len(filters), len(subs))
	}
	roundTrip := dkvsSubscriptionsFromWireFilters(filters)
	if len(roundTrip) != len(subs) {
		t.Fatalf("round trip len=%d want=%d", len(roundTrip), len(subs))
	}
	for n := range subs {
		if roundTrip[n] != subs[n] {
			t.Fatalf("round trip[%d]=%#v want=%#v", n, roundTrip[n], subs[n])
		}
	}
}

func TestDKVSSyncRequestPayloadWithinWireBound(t *testing.T) {
	filters := make([]wire.DKVSSyncFilter, wire.MaxDKVSSyncFilters)
	for index := range filters {
		filters[index] = wire.DKVSSyncFilter{Type: "prefix", Target: "/" + string(make([]byte, wire.MaxDKVSKeySize-1))}
	}
	got := dkvsSyncRequestPayloadLen(make([]byte, wire.MaxDKVSCursorSize), filters)
	max := (&wire.MsgDKVSSyncRequest{}).MaxPayloadLength(wire.ProtocolVersion)
	if got > int(max) {
		t.Fatalf("sync request payload=%d max=%d", got, max)
	}
}

func TestDKVSRequestTrackingRejectsUnsolicitedData(t *testing.T) {
	sp := &serverPeer{}
	record := &wire.DKVSRecord{Version: dkvsindexer.Version, Key: "/tmp/a", Seq: 1}
	if sp.consumeDKVSRequest(record) {
		t.Fatal("unrequested record was accepted")
	}
	sp.trackDKVSRequest(&wire.MsgDKVSGet{Keys: []string{record.Key}})
	if !sp.consumeDKVSRequest(record) {
		t.Fatal("requested key record was rejected")
	}
	if sp.consumeDKVSRequest(record) {
		t.Fatal("request was reusable")
	}

	hash := dkvsindexer.RecordHash(record)
	sp.trackDKVSRequest(&wire.MsgDKVSGet{RecordHashes: []chainhash.Hash{hash}})
	if !sp.consumeDKVSRequest(record) {
		t.Fatal("requested record hash was rejected")
	}

	sp.dkvsRequestMtx.Lock()
	sp.dkvsRequested = map[chainhash.Hash]time.Time{
		dkvsKeyHash(record.Key): time.Now().Add(-dkvsRequestTTL - time.Second),
	}
	sp.dkvsRequestMtx.Unlock()
	if sp.consumeDKVSRequest(record) {
		t.Fatal("expired request was accepted")
	}
}

func TestDKVSServeSyncBuffersAndCoalescesNotifications(t *testing.T) {
	sp := &serverPeer{}
	filters := []wire.DKVSSyncFilter{{Type: string(dkvsindexer.SubscriptionKey), Target: "/tmp/a"}}
	request := &wire.MsgDKVSSyncRequest{SessionID: 7, Filters: filters}
	if !sp.beginDKVSServeSync(request) {
		t.Fatal("initial serve sync rejected")
	}
	if sp.beginDKVSServeSync(&wire.MsgDKVSSyncRequest{
		SessionID: 7,
		Cursor:    []byte{1},
		Filters:   []wire.DKVSSyncFilter{{Type: "key", Target: "/tmp/b"}},
	}) {
		t.Fatal("continuation with changed filters accepted")
	}
	first := &wire.MsgDKVSNotify{Key: "/tmp/a", Seq: 1, Size: 10}
	second := &wire.MsgDKVSNotify{Key: "/tmp/a", Seq: 2, Size: 20}
	if !sp.bufferDKVSNotify(first) || !sp.bufferDKVSNotify(second) {
		t.Fatal("matching notifications were not buffered")
	}
	if sp.bufferDKVSNotify(&wire.MsgDKVSNotify{Key: "/tmp/b", Seq: 1}) {
		t.Fatal("unrelated notification was buffered")
	}
	var rootA, rootB chainhash.Hash
	rootA[0] = 1
	rootB[0] = 2
	if got := sp.fixedDKVSServeRoot(rootA); got != rootA {
		t.Fatalf("first root=%v", got)
	}
	if got := sp.fixedDKVSServeRoot(rootB); got != rootA {
		t.Fatalf("root changed within session: %v", got)
	}
	pending := sp.finishDKVSServeSync(7)
	if len(pending) != 1 || pending[0].Seq != 2 || pending[0].Key != "/tmp/a" {
		t.Fatalf("pending=%#v", pending)
	}
	if sp.bufferDKVSNotify(first) {
		t.Fatal("notification buffered after session completion")
	}
}
