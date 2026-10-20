package netsync

import (
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	_ "github.com/sat20-labs/satoshinet/database/ffldb"
	"github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestRegtestFullNodeSyncCandidateAcrossHosts(t *testing.T) {
	params := &chaincfg.RegressionNetParams
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	chain, err := blockchain.New(&blockchain.Config{
		DB: db, ChainParams: params, TimeSource: blockchain.NewMedianTime(),
	})
	if err != nil {
		t.Fatal(err)
	}
	sm := &SyncManager{chain: chain, chainParams: params}
	for _, tc := range []struct {
		name     string
		address  string
		services wire.ServiceFlag
		want     bool
	}{
		{"loopback_test_tool", "127.0.0.1:39526", 0, true},
		{"loopback_full_node", "127.0.0.1:39526", wire.SFNodeNetwork, true},
		{"LAN_full_node", "192.168.1.104:39526", wire.SFNodeNetwork, true},
		{"LAN_without_block_service", "192.168.1.104:39526", 0, false},
		{"IPv6_full_node", "[::1]:39526", wire.SFNodeNetwork, true},
		{"IPv6_without_block_service", "[::1]:39526", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := peer.NewOutboundPeer(&peer.Config{
				ChainParams: params, Services: tc.services,
			}, tc.address)
			if err != nil {
				t.Fatal(err)
			}
			if got := sm.isSyncCandidate(p); got != tc.want {
				t.Fatalf("isSyncCandidate(%s, services=%v) = %v, want %v", tc.address, tc.services, got, tc.want)
			}
		})
	}
}
