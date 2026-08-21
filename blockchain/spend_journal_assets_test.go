package blockchain

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func testSpendJournalAssetEntry() *UtxoEntry {
	return &UtxoEntry{
		amount: 1_000,
		txAssets: wire.TxAssets{{
			Name:       wire.AssetName{Protocol: "ordx", Type: "f", Ticker: "journal-test"},
			Amount:     *scommon.NewDecimal(12345, 2),
			BindingSat: 100,
		}},
		pkScript:    []byte{txscript.OP_TRUE},
		blockHeight: 1,
	}
}

func testCoinbaseTx() *wire.MsgTx {
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Index: wire.MaxPrevOutIndex},
		SignatureScript:  []byte{0x01, 0x01},
	})
	tx.AddTxOut(&wire.TxOut{Value: 50, PkScript: []byte{txscript.OP_TRUE}})
	return tx
}

func testSpendTx(previous wire.OutPoint) *wire.MsgTx {
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: previous})
	tx.AddTxOut(&wire.TxOut{Value: 900, PkScript: []byte{txscript.OP_TRUE}})
	return tx
}

func TestUtxoViewSpendJournalRestoresAssets(t *testing.T) {
	previous := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	view := NewUtxoViewpoint()
	view.entries[previous] = testSpendJournalAssetEntry()

	spendTx := testSpendTx(previous)
	var stxos []SpentTxOut
	if err := view.connectTransaction(btcutil.NewTx(spendTx), 2, &stxos); err != nil {
		t.Fatalf("connectTransaction: %v", err)
	}
	if len(stxos) != 1 || len(stxos[0].Assets) != 1 {
		t.Fatalf("spent assets were not journaled: %+v", stxos)
	}

	block := btcutil.NewBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{
		testCoinbaseTx(), spendTx,
	}})
	block.SetHeight(2)
	if err := view.disconnectTransactions(nil, block, stxos); err != nil {
		t.Fatalf("disconnectTransactions: %v", err)
	}
	restored := view.LookupEntry(previous)
	if restored == nil || restored.IsSpent() || len(restored.TxAssets()) != 1 {
		t.Fatalf("asset UTXO was not fully restored: %+v", restored)
	}
}

func TestUtxoCacheSpendJournalPreservesAssetsAndMemoryAccounting(t *testing.T) {
	previous := wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0}
	cache := newUtxoCache(nil, 1<<20)
	entry := testSpendJournalAssetEntry()
	cache.cachedEntries.put(previous, entry, 0)
	cache.totalEntryMemory = entry.memoryUsage()

	var stxos []SpentTxOut
	if err := cache.addTxIn(&wire.TxIn{PreviousOutPoint: previous}, &stxos); err != nil {
		t.Fatalf("addTxIn: %v", err)
	}
	if len(stxos) != 1 || len(stxos[0].Assets) != 1 {
		t.Fatalf("cached spent assets were not journaled: %+v", stxos)
	}
	if cache.totalEntryMemory != 0 {
		t.Fatalf("spent entry memory=%d, want 0", cache.totalEntryMemory)
	}
}
