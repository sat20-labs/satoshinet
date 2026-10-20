package blockchain

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
)

func TestRegressionConstructorChecksGenesisCheckpoint(t *testing.T) {
	reg := &chaincfg.RegressionNetParams
	checkpoint := func(height int32) chaincfg.Checkpoint {
		return chaincfg.Checkpoint{Height: height, Hash: reg.GenesisHash}
	}
	for _, tc := range []struct {
		name        string
		params      *chaincfg.Params
		checkpoints []chaincfg.Checkpoint
		valid       bool
	}{
		{"reg_genesis", reg, reg.Checkpoints, true},
		{"reg_negative", reg, []chaincfg.Checkpoint{checkpoint(-1)}, false},
		{"reg_duplicate", reg, []chaincfg.Checkpoint{checkpoint(0), checkpoint(0)}, false},
		{"reg_descending", reg, []chaincfg.Checkpoint{checkpoint(1), checkpoint(0)}, false},
		{"testnet_zero_unchanged", &chaincfg.TestNetParams, []chaincfg.Checkpoint{checkpoint(0)}, false},
		{"mainnet_zero_unchanged", &chaincfg.MainNetParams, []chaincfg.Checkpoint{checkpoint(0)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := database.Create("ffldb", t.TempDir(), tc.params.Net)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			params := *tc.params
			chain, err := New(&Config{DB: db, ChainParams: &params, Checkpoints: tc.checkpoints, TimeSource: NewMedianTime()})
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				if chain.BestSnapshot().Hash != *reg.GenesisHash || chain.BestSnapshot().Height != 0 {
					t.Fatal("constructor lost actual regtest genesis")
				}
			} else {
				if _, ok := err.(AssertError); !ok {
					t.Fatalf("invalid checkpoint order accepted: %v", err)
				}
			}
		})
	}
}
