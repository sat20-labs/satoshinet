package mempool

import (
	"testing"

	idx "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Exercise the public admission entry point, including its orphan and
// AcceptNonStd paths. Asset policy must run before either can accept a tx.
func TestAssetEnvelopeAdmissionBeforeOrphans(t *testing.T) {
	name := wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "test#1@2"}
	for _, tc := range []struct {
		label     string
		name      wire.AssetName
		amount    int64
		wantError string
	}{
		{"positive_asset_zero_sats", name, 1, ""},
		{"zero_asset", name, 0, "asset amount must be positive"},
		{"negative_asset", name, -1, ""}, // Already rejected by chain sanity.
		{"missing_protocol", wire.AssetName{Type: "f", Ticker: "test"}, 1, "invalid asset name"},
		{"missing_type", wire.AssetName{Protocol: "ordx", Ticker: "test"}, 1, "invalid asset name"},
		{"missing_ticker", wire.AssetName{Protocol: "ordx", Type: "f"}, 1, "invalid asset name"},
		{"colon_protocol", wire.AssetName{Protocol: "ordx:x", Type: "f", Ticker: "test"}, 1, "invalid asset name"},
		{"colon_type", wire.AssetName{Protocol: "ordx", Type: "f:x", Ticker: "test"}, 1, "invalid asset name"},
		{"colon_ticker", wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "test:x"}, 1, "invalid asset name"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			harness, _, err := newPoolHarness(&chaincfg.MainNetParams)
			require.NoError(t, err)
			harness.txPool.cfg.Policy.AcceptNonStd = true
			tx := wire.NewMsgTx(1)
			prev := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
			tx.AddTxIn(wire.NewTxIn(&prev, nil, nil))
			tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{Name: tc.name, Amount: *idx.NewDefaultDecimal(tc.amount)}}, []byte{0x51}))
			result, err := harness.txPool.CheckMempoolAcceptance(btcutil.NewTx(tx))
			if tc.label == "negative_asset" {
				require.Error(t, err)
			} else if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
			} else {
				require.NoError(t, err)
				require.Len(t, result.MissingParents, 1)
			}
		})
	}
}
