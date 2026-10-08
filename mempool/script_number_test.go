package mempool

import (
	"math"
	"testing"

	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestMalformedScriptNumbersRejectedByMempool(t *testing.T) {
	for _, kind := range []string{"legacy-anchor", "stp-marker"} {
		t.Run(kind, func(t *testing.T) {
			params := chaincfg.RegressionNetParams
			params.POSV2Height = 1
			harness, outs, err := newPoolHarness(&params)
			require.NoError(t, err)
			oversized := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0x80}
			var tx *wire.MsgTx
			if kind == "legacy-anchor" {
				// The next block is below H, so activated encoding checks do
				// not protect this amount parser. No invoice signature is needed.
				params.POSV2Height = harness.chain.BestHeight() + 2
				script, err := txscript.NewScriptBuilder().AddData([]byte("funding:0")).
					AddData([]byte{txscript.OP_TRUE}).AddData(oversized).
					AddOp(txscript.OP_0).AddData([]byte{1}).Script()
				require.NoError(t, err)
				tx = wire.NewMsgTx(wire.TxVersion)
				tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.AnchorTxOutIndex}, script, nil))
				tx.AddTxOut(wire.NewTxOut(0, nil, []byte{txscript.OP_TRUE}))
			} else {
				ordinary, err := harness.CreateSignedTx(outs[:1], 1, 0, false)
				require.NoError(t, err)
				tx = ordinary.MsgTx().Copy()
				script, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).
					AddOp(common.SAT20_MAGIC_NUMBER).AddData(oversized).AddData([]byte("x")).Script()
				require.NoError(t, err)
				tx.AddTxOut(wire.NewTxOut(0, nil, script))
				// Parsing precedes both standardness and signature checks.
				harness.txPool.cfg.Policy.AcceptNonStd = false
			}
			require.NotPanics(t, func() {
				_, err := harness.txPool.CheckMempoolAcceptance(btcutil.NewTx(tx))
				require.ErrorContains(t, err, "eight bytes")
			})
			require.Empty(t, harness.txPool.anchorOutpoints)
		})
	}
}

func TestSharedScriptParsersRejectOversizedNumbers(t *testing.T) {
	for _, negative := range []bool{false, true} {
		name := "positive"
		if negative {
			name = "negative"
		}
		t.Run(name, func(t *testing.T) {
			oversized := make([]byte, 9)
			oversized[0] = contract.ContentTypeContractDeploy
			if negative {
				oversized[8] = 0x80
			}
			anchor, err := txscript.NewScriptBuilder().AddData([]byte("funding:0")).
				AddData([]byte{txscript.OP_TRUE}).AddData(oversized).
				AddOp(txscript.OP_0).AddData([]byte{1}).Script()
			require.NoError(t, err)
			require.NotPanics(t, func() {
				_, err := anchortx.ParseAnchorScript(anchor)
				require.ErrorContains(t, err, "eight bytes")
			})
			require.NotPanics(t, func() {
				_, _, _, _, _, err := common.ParseStandardAnchorScript(anchor)
				require.ErrorContains(t, err, "eight bytes")
			})
			marker, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).
				AddOp(common.SAT20_MAGIC_NUMBER).AddData(oversized).AddData([]byte("x")).Script()
			require.NoError(t, err)
			require.NotPanics(t, func() {
				_, _, err := contract.ReadNullDataScript(marker)
				require.ErrorContains(t, err, "eight bytes")
			})
			require.NotPanics(t, func() {
				_, _, err := common.ReadDataFromNullDataScript(marker)
				require.ErrorContains(t, err, "eight bytes")
			})
			require.NotPanics(t, func() { require.False(t, common.IsSTPNullDataScript(marker)) })
		})
	}
}

func TestSharedAnchorParsersAcceptEightByteAmounts(t *testing.T) {
	for _, amount := range []int64{math.MaxInt64, -math.MaxInt64} {
		script, err := common.StandardAnchorScriptWithSig("funding:0", []byte{txscript.OP_TRUE}, amount, nil, []byte{1})
		require.NoError(t, err)
		parsed, err := anchortx.ParseAnchorScript(script)
		require.NoError(t, err)
		require.Equal(t, amount, parsed.Value)
		_, _, decoded, _, _, err := common.ParseStandardAnchorScript(script)
		require.NoError(t, err)
		require.Equal(t, amount, decoded)
	}
}
