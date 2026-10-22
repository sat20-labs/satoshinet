package blockchain

import (
	"bytes"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	common "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestNativeAssetOnlyExitRemainsValidWithAdmissionDisabled(t *testing.T) {
	params := chaincfg.TestNetParams
	params.NativeAssetHeight = 0
	channel, err := btcutil.NewAddressWitnessScriptHash(bytes.Repeat([]byte{7}, 32), &params)
	require.NoError(t, err)
	script, err := txscript.PayToAddrScript(channel)
	require.NoError(t, err)
	assets := wire.TxAssets{{Name: wire.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT,
		Ticker: strings.Repeat("a", 64)}, Amount: *indexer.NewDecimalWithScale(12345, 2)}}
	payload, err := common.EncodeDescendPayloadV2(strings.Repeat("b", 64), common.DESCEND_OP_CLOSE, nil)
	require.NoError(t, err)
	descending, err := common.NullDataScript(common.CONTENT_TYPE_DESCENDING, payload)
	require.NoError(t, err)
	marker, err := common.NullDataScript(common.CONTENT_TYPE_CHANNELID, []byte(channel.EncodeAddress()))
	require.NoError(t, err)
	tx := wire.NewMsgTx(wire.TxVersion)
	previous := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	tx.AddTxIn(wire.NewTxIn(&previous, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, assets.Clone(), descending))
	tx.AddTxOut(wire.NewTxOut(0, nil, marker))
	view := NewUtxoViewpoint()
	view.Entries()[previous] = NewUtxoEntry(wire.NewTxOut(0, assets.Clone(), script), 1, false)
	require.True(t, IsDeAnchorTx(tx))
	require.NoError(t, CheckTransactionSanity(btcutil.NewTx(tx)))
	require.NoError(t, CheckDeAnchorFeeExemption(tx, view, &params))
	fee, assetFee, err := CheckTransactionInputs(btcutil.NewTx(tx), true, 20, view, &params)
	require.NoError(t, err)
	require.Zero(t, fee)
	require.Empty(t, assetFee)
}
