package blockchain

import (
	"testing"

	indexerCommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestOpenAIReviewNegativeAssetOutputAccepted(t *testing.T) {
	name := wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "review"}
	prev := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&prev, nil, nil))
	tx.AddTxOut(wire.NewTxOut(90, wire.TxAssets{{
		Name: name, Amount: *indexerCommon.NewDefaultDecimal(-5),
	}}, []byte{txscript.OP_TRUE}))
	view := NewUtxoViewpoint()
	view.Entries()[prev] = NewUtxoEntry(wire.NewTxOut(100, wire.TxAssets{{
		Name: name, Amount: *indexerCommon.NewDefaultDecimal(10),
	}}, []byte{txscript.OP_TRUE}), 1, false)

	if err := CheckTransactionSanity(btcutil.NewTx(tx)); err != nil {
		t.Fatalf("negative asset rejected by sanity check: %v", err)
	}
	_, fees, err := CheckTransactionInputs(btcutil.NewTx(tx), false, 100, view, &chaincfg.TestNetParams)
	if err != nil {
		t.Fatalf("negative asset rejected by input check: %v", err)
	}
	if len(fees) != 1 || fees[0].Amount.String() != "15" {
		t.Fatalf("unexpected fee assets: %#v", fees)
	}
}

func TestOpenAIReviewBindingMetadataMutationAccepted(t *testing.T) {
	name := wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "review"}
	prev := wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&prev, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name: name, Amount: *indexerCommon.NewDefaultDecimal(10), BindingSat: 0,
	}}, []byte{txscript.OP_TRUE}))
	tx.AddTxOut(wire.NewTxOut(10, nil, []byte{txscript.OP_TRUE}))
	view := NewUtxoViewpoint()
	view.Entries()[prev] = NewUtxoEntry(wire.NewTxOut(10, wire.TxAssets{{
		Name: name, Amount: *indexerCommon.NewDefaultDecimal(10), BindingSat: 1,
	}}, []byte{txscript.OP_TRUE}), 1, false)

	if err := CheckTransactionSanity(btcutil.NewTx(tx)); err != nil {
		t.Fatalf("binding mutation rejected by sanity check: %v", err)
	}
	fee, feeAssets, err := CheckTransactionInputs(btcutil.NewTx(tx), false, 100, view, &chaincfg.TestNetParams)
	if err != nil {
		t.Fatalf("binding mutation rejected by input check: %v", err)
	}
	if fee != 0 || len(feeAssets) != 0 {
		t.Fatalf("unexpected fees: sats=%d assets=%#v", fee, feeAssets)
	}
}
