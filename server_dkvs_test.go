package main

import (
	"encoding/hex"
	"testing"

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

func TestDKVSSyncRequestPayloadLenLegacyBound(t *testing.T) {
	small := []wire.DKVSSyncFilter{{Type: "prefix", Target: "/tmp/a"}}
	if got := dkvsSyncRequestPayloadLen(nil, small); got > legacyDKVSSyncRequestMaxPayload {
		t.Fatalf("small filtered request payload len=%d exceeds legacy max=%d", got, legacyDKVSSyncRequestMaxPayload)
	}
	large := []wire.DKVSSyncFilter{
		{Type: "prefix", Target: "/" + string(make([]byte, wire.MaxDKVSKeySize))},
		{Type: "prefix", Target: "/" + string(make([]byte, wire.MaxDKVSKeySize))},
	}
	if got := dkvsSyncRequestPayloadLen(nil, large); got <= legacyDKVSSyncRequestMaxPayload {
		t.Fatalf("large filtered request payload len=%d should exceed legacy max=%d", got, legacyDKVSSyncRequestMaxPayload)
	}
}
