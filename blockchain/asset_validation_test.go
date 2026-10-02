package blockchain

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func releaseTestAsset(amount int64, binding uint32) wire.AssetInfo {
	return wire.AssetInfo{Name: wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "release"},
		Amount: *indexer.NewDefaultDecimal(amount), BindingSat: binding}
}

func releaseTestTx(value int64, assets wire.TxAssets, salt byte) *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	prev := wire.OutPoint{Hash: chainhash.Hash{salt}, Index: 0}
	tx.AddTxIn(wire.NewTxIn(&prev, nil, nil))
	tx.AddTxOut(wire.NewTxOut(value, assets, []byte{0x51}))
	return tx
}

func TestReleaseConsensusAssetOutputRules(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value int64
		asset wire.AssetInfo
		bad   bool
	}{
		{"complete_group", 1, releaseTestAsset(10, 10), false},
		{"partial_group_retains_L2_rule", 0, releaseTestAsset(9, 10), false},
		{"group_plus_remainder", 1, releaseTestAsset(19, 10), false},
		{"missing_carrier", 0, releaseTestAsset(10, 10), true},
		{"negative_asset", 0, releaseTestAsset(-1, 0), true},
		{"reserved_zero_alias", 1, wire.AssetInfo{Name: wire.AssetName{}, Amount: *indexer.NewDefaultDecimal(0)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := releaseTestTx(tc.value, wire.TxAssets{tc.asset}, 1)
			err := CheckTransactionSanity(btcutil.NewTx(tx))
			if (err != nil) != tc.bad {
				t.Fatalf("output rule: bad=%v err=%v", tc.bad, err)
			}
		})
	}
}

func TestReleaseConsensusInputBindingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name         string
		inputBinding uint32
		second       bool
		outBinding   uint32
		bad          bool
	}{
		{"unchanged", 1, false, 1, false},
		{"cannot_remove_binding", 1, false, 0, true},
		{"cannot_change_binding", 1, false, 2, true},
		{"conflicting_input_metadata", 2, true, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := releaseTestTx(100, wire.TxAssets{releaseTestAsset(10, tc.inputBinding)}, 2)
			view := NewUtxoViewpoint()
			view.AddTxOuts(btcutil.NewTx(parent), 1)
			tx := wire.NewMsgTx(2)
			prev := wire.OutPoint{Hash: parent.TxHash(), Index: 0}
			tx.AddTxIn(wire.NewTxIn(&prev, nil, nil))
			amount, value := int64(10), int64(100)
			if tc.second {
				other := releaseTestTx(100, wire.TxAssets{releaseTestAsset(10, 1)}, 3)
				view.AddTxOuts(btcutil.NewTx(other), 1)
				prev := wire.OutPoint{Hash: other.TxHash(), Index: 0}
				tx.AddTxIn(wire.NewTxIn(&prev, nil, nil))
				amount, value = 20, 200
			}
			tx.AddTxOut(wire.NewTxOut(value, wire.TxAssets{releaseTestAsset(amount, tc.outBinding)}, []byte{0x51}))
			before := parent.TxHash()
			if err := CheckTransactionSanity(btcutil.NewTx(tx)); err != nil {
				t.Fatalf("fixture must pass output checks: %v", err)
			}
			_, _, err := CheckTransactionInputs(btcutil.NewTx(tx), false, 2, view, &chaincfg.RegressionNetParams)
			if (err != nil) != tc.bad {
				t.Fatalf("input metadata rule: bad=%v err=%v", tc.bad, err)
			}
			if parent.TxHash() != before {
				t.Fatal("transaction validation mutated the source output")
			}
		})
	}
}

func TestReleaseConsensusCoinbaseBindingMetadata(t *testing.T) {
	expected := wire.TxAssets{releaseTestAsset(10, 1)}
	for _, tc := range []struct {
		name string
		asset wire.AssetInfo
		bad bool
	}{
		{"matching_fee_metadata", releaseTestAsset(10, 1), false},
		{"miner_cannot_remove_binding", releaseTestAsset(10, 0), true},
		{"miner_cannot_change_binding", releaseTestAsset(10, 2), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coinbase := wire.NewMsgTx(2)
			coinbase.AddTxOut(wire.NewTxOut(10, wire.TxAssets{tc.asset}, []byte{0x51}))
			err := checkCoinbaseFees(coinbase, 10, expected)
			if (err != nil) != tc.bad {
				t.Fatalf("coinbase metadata rule: bad=%v err=%v", tc.bad, err)
			}
		})
	}
}
