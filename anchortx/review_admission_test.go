package anchortx

import (
	"fmt"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	common "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func reviewAnchor(t *testing.T, assets wire.TxAssets, value int64) (*wire.MsgTx, []byte) {
	t.Helper()
	core, client := testAnchorKeys(t)
	witness, script, err := GetP2WSHscript(core.PubKey().SerializeCompressed(), client.PubKey().SerializeCompressed())
	require.NoError(t, err)
	funding := "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd:0"
	display := make([]*indexer.DisplayAsset, 0, len(assets))
	for _, a := range assets {
		display = append(display, &indexer.DisplayAsset{AssetName: a.Name, Amount: a.Amount.String(), Precision: a.Amount.Precision, BindingSat: int(a.BindingSat)})
	}
	server := fakeL1IndexerServer(t, map[string]*indexer.AssetsInUtxo{funding: {OutPoint: funding, Value: value, PkScript: script, Assets: display}})
	t.Cleanup(server.Close)
	startTestAnchorManager(t, server)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.AnchorTxOutIndex}, signedAnchorScript(t, funding, witness, value, assets, core), nil))
	tx.AddTxOut(wire.NewTxOut(value, assets, script))
	addAscendingTicker(t, tx, assets)
	return tx, script
}

func TestAnchorAdmissionAllowsDepositAndFeeDestinations(t *testing.T) {
	_, client := testAnchorKeys(t)
	recipient, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(client.PubKey().SerializeCompressed()), &chaincfg.TestNetParams)
	require.NoError(t, err)
	recipientScript, err := txscript.PayToAddrScript(recipient)
	require.NoError(t, err)
	feeKey, _ := btcec.PrivKeyFromBytes([]byte{3})
	feeRecipient, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(feeKey.PubKey().SerializeCompressed()), &chaincfg.TestNetParams)
	require.NoError(t, err)
	feeScript, err := txscript.PayToAddrScript(feeRecipient)
	require.NoError(t, err)
	for _, mode := range []string{"split", "deposit", "asset-only", "deposit-asset-only", "deposit-bound-asset", "channel-and-dao", "wrong-value", "wrong-asset-amount"} {
		t.Run(mode, func(t *testing.T) {
			var assets wire.TxAssets
			value := int64(2000)
			if mode == "asset-only" || mode == "deposit-asset-only" || mode == "wrong-asset-amount" {
				value = 0
				assets = wire.TxAssets{{Name: *indexer.NewAssetNameFromString("brc20:f:test"), Amount: *indexer.NewDecimal(100, 2)}}
			}
			if mode == "deposit-bound-asset" {
				assets = wire.TxAssets{{Name: *indexer.NewAssetNameFromString("ordx:f:deposit"), Amount: *indexer.NewDefaultDecimal(100), BindingSat: 1}}
			}
			tx, script := reviewAnchor(t, assets, value)
			if mode == "split" {
				tx.TxOut[0].Value = 1000
				tx.AddTxOut(wire.NewTxOut(1000, nil, script))
			}
			if mode == "deposit" || mode == "deposit-asset-only" || mode == "deposit-bound-asset" || mode == "wrong-value" || mode == "wrong-asset-amount" {
				tx.TxOut[0].PkScript = recipientScript
			}
			if mode == "channel-and-dao" {
				tx.TxOut[0].Value = 1900
				tx.AddTxOut(wire.NewTxOut(100, nil, feeScript))
			}
			if mode == "wrong-value" {
				tx.TxOut[0].Value++
			}
			if mode == "wrong-asset-amount" {
				tx.TxOut[0].Assets = assets.Clone()
				tx.TxOut[0].Assets[0].Amount = *indexer.NewDecimal(101, 2)
			}
			info, err := ParseAnchorScript(tx.TxIn[0].SignatureScript)
			require.NoError(t, err)
			tx.Version = wire.TxVersion
			tx.TxIn[0].Sequence = wire.AnchorTxOutIndex
			invoice, err := common.AnchorInvoice(tx, true)
			require.NoError(t, err)
			core, _ := testAnchorKeys(t)
			tx.TxIn[0].SignatureScript, err = common.StandardAnchorScriptWithSig(info.Utxo, info.WitnessScript, info.Value, info.TxAssets, ecdsa.Sign(core, chainhash.HashB(invoice)).Serialize())
			require.NoError(t, err)
			_, err = CheckAnchorTxValid(tx, true, true)
			if mode == "wrong-value" {
				require.ErrorContains(t, err, "Anchor amount")
			} else if mode == "wrong-asset-amount" {
				require.ErrorContains(t, err, "anchor tx assets not equal")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAnchorAdmissionRejectsOrdinaryInputOperations(t *testing.T) {
	for _, operation := range []byte{common.CONTENT_TYPE_DESCENDING, common.CONTENT_TYPE_BINDREFERRER, common.CONTENT_TYPE_UNSTAKE} {
		t.Run(fmt.Sprint(operation), func(t *testing.T) {
			tx, _ := reviewAnchor(t, nil, 2000)
			script, err := common.NullDataScript(operation, []byte("payload"))
			require.NoError(t, err)
			tx.AddTxOut(wire.NewTxOut(0, nil, script))
			_, err = CheckAnchorTxValid(tx, true, false)
			require.Error(t, err)
		})
	}
}

func TestAnchorAdmissionRejectsConflictingTickerFacts(t *testing.T) {
	for _, metadata := range []string{"::-2100000000000000-1-1", "::-2100000000000000-0-4294967297", "invalid-1-0-1"} {
		t.Run(metadata, func(t *testing.T) {
			tx, _ := reviewAnchor(t, nil, 2000)
			script, err := common.NullDataScript(common.CONTENT_TYPE_ASCENDING, []byte(metadata))
			require.NoError(t, err)
			tx.TxOut[1].PkScript = script
			require.NotPanics(t, func() { _, err = CheckAnchorTxValid(tx, true, false) })
			require.Error(t, err)
		})
	}
}

func TestAnchorSplitKeepsSignedBinding(t *testing.T) {
	for _, mode := range []string{"valid", "wrong-first", "wrong-last", "wrong-first-reversed"} {
		t.Run(mode, func(t *testing.T) {
			assets := wire.TxAssets{{Name: *indexer.NewAssetNameFromString("ordx:f:split"), Amount: *indexer.NewDefaultDecimal(100), BindingSat: 1}}
			tx, script := reviewAnchor(t, assets, 2000)
			first, second := assets.Clone(), assets.Clone()
			first[0].Amount = *indexer.NewDefaultDecimal(50)
			second[0].Amount = *indexer.NewDefaultDecimal(50)
			if mode == "wrong-first" || mode == "wrong-first-reversed" {
				first[0].BindingSat = 0
			}
			if mode == "wrong-last" {
				second[0].BindingSat = 0
			}
			tx.TxOut[0] = wire.NewTxOut(1000, first, script)
			tx.AddTxOut(wire.NewTxOut(1000, second, script))
			if mode == "wrong-first-reversed" {
				tx.TxOut[0], tx.TxOut[2] = tx.TxOut[2], tx.TxOut[0]
			}
			_, err := CheckAnchorTxValid(tx, true, false)
			if mode == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
