package blockchain

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func parentWorkBlock(count int) (*btcutil.Block, *wire.MsgTx) {
	coinbase := wire.NewMsgTx(2)
	coinbase.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.MaxPrevOutIndex}, nil, nil))
	coinbase.AddTxOut(wire.NewTxOut(0, nil, []byte{txscript.OP_TRUE}))
	parent := wire.NewMsgTx(2)
	parent.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{9}}, nil, nil))
	for i := 0; i < count; i++ {
		parent.AddTxOut(wire.NewTxOut(0, wire.TxAssets{releaseTestAsset(500, 1000)}, []byte{txscript.OP_TRUE}))
	}
	block := btcutil.NewBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{coinbase, parent}})
	block.SetHeight(10)
	return block, parent
}

func parentWorkChild(parent *wire.MsgTx, indices ...uint32) *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	for _, index := range indices {
		tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: parent.TxHash(), Index: index}, nil, nil))
		if int(index) < len(parent.TxOut) {
			out := parent.TxOut[index]
			tx.AddTxOut(wire.NewTxOut(out.Value, out.Assets.Clone(), append([]byte(nil), out.PkScript...)))
		}
	}
	return tx
}

func TestUTXOParentWorkPreservesViewAndDependencies(t *testing.T) {
	block, parent := parentWorkBlock(8)
	first := parentWorkChild(parent, 0, 1)
	second := parentWorkChild(first, 0)
	second.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: parent.TxHash(), Index: 2}, nil, nil))
	second.AddTxOut(wire.NewTxOut(0, parent.TxOut[2].Assets.Clone(), []byte{txscript.OP_TRUE}))
	block.MsgBlock().Transactions = append(block.MsgBlock().Transactions, first, second)
	view := NewUtxoViewpoint()
	// Parent loading must still refresh stale entries from an earlier view.
	stale := wire.OutPoint{Hash: parent.TxHash(), Index: 7}
	view.entries[stale] = NewUtxoEntry(wire.NewTxOut(99, nil, []byte{txscript.OP_FALSE}), 1, true)
	view.entries[stale].Spend()
	needed := view.findInputsToFetch(block)
	require.Equal(t, []wire.OutPoint{parent.TxIn[0].PreviousOutPoint}, needed)
	want := NewUtxoViewpoint()
	want.AddTxOuts(btcutil.NewTx(parent), block.Height())
	want.AddTxOuts(btcutil.NewTx(first), block.Height())
	require.Equal(t, want.entries, view.entries, "all outputs and flags of referenced parents must be retained")
	require.NotSame(t, parent.TxOut[0].Assets[0].Amount.Value, view.entries[wire.OutPoint{Hash: parent.TxHash()}].txAssets[0].Amount.Value)
	for _, tx := range []*wire.MsgTx{first, second} {
		fee, assets, err := CheckTransactionInputs(btcutil.NewTx(tx), false, block.Height(), view, &chaincfg.TestNetParams)
		require.NoError(t, err)
		require.Zero(t, fee)
		require.Empty(t, assets)
		require.NoError(t, view.connectTransaction(btcutil.NewTx(tx), block.Height(), nil))
	}
	duplicate := parentWorkChild(parent, 1)
	_, _, err := CheckTransactionInputs(btcutil.NewTx(duplicate), false, block.Height(), view, &chaincfg.TestNetParams)
	require.Error(t, err, "a reserved original cannot be consumed twice")
	// The loaded-parent marker must be private to each invocation.
	require.Equal(t, needed, view.findInputsToFetch(block))
	want.AddTxOuts(btcutil.NewTx(second), block.Height())
	require.Equal(t, want.entries, view.entries)
	t.Log("decision=accept-dependent-children,reject-double-spend; view=all-parent-outputs")
}

func TestUTXOParentWorkRejectsUnavailableReferences(t *testing.T) {
	for _, name := range []string{"out_of_range", "forward"} {
		t.Run(name, func(t *testing.T) {
			block, parent := parentWorkBlock(2)
			child := parentWorkChild(parent, 2)
			if name == "forward" {
				child = parentWorkChild(parent, 0)
				block.MsgBlock().Transactions = []*wire.MsgTx{block.MsgBlock().Transactions[0], child, parent}
			} else {
				block.MsgBlock().Transactions = append(block.MsgBlock().Transactions, child)
			}
			view := NewUtxoViewpoint()
			needed := view.findInputsToFetch(block)
			require.Nil(t, view.LookupEntry(child.TxIn[0].PreviousOutPoint))
			if name != "out_of_range" {
				require.Contains(t, needed, child.TxIn[0].PreviousOutPoint)
			}
			_, _, err := CheckTransactionInputs(btcutil.NewTx(child), false, block.Height(), view, &chaincfg.TestNetParams)
			require.Error(t, err)
			t.Log("decision=reject-unavailable-reference")
		})
	}
}

func BenchmarkUTXOParentLoadingWork(b *testing.B) {
	for _, count := range []int{128, 512, 1001} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			block, parent := parentWorkBlock(count)
			indices := make([]uint32, count)
			for i := range indices {
				indices[i] = uint32(i)
			}
			block.MsgBlock().Transactions = append(block.MsgBlock().Transactions, parentWorkChild(parent, indices...))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				view := NewUtxoViewpoint()
				if len(view.findInputsToFetch(block)) != 1 {
					b.Fatal("incorrect external input set")
				}
			}
		})
	}
}
