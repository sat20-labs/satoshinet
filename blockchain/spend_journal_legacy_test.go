package blockchain

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

func legacyJournalFixture() (*btcutil.Block, *btcutil.Block, []SpentTxOut) {
	entry := testSpendJournalAssetEntry()
	originTx := testSpendTx(wire.OutPoint{Hash: chainhash.Hash{10}})
	originTx.TxOut[0] = &wire.TxOut{Value: entry.amount, Assets: entry.txAssets.Clone(), PkScript: entry.pkScript}
	originTx.AddTxOut(&wire.TxOut{Value: 50, PkScript: entry.pkScript})
	origin := btcutil.NewBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{testCoinbaseTx(), originTx}})
	origin.SetHeight(1)
	spend := testSpendTx(wire.OutPoint{Hash: originTx.TxHash(), Index: 0})
	spend.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: originTx.TxHash(), Index: 1}})
	spend.TxOut[0].Assets = entry.txAssets.Clone()
	inBlockSpend := testSpendTx(wire.OutPoint{Hash: spend.TxHash(), Index: 0})
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{PrevBlock: *origin.Hash(), Nonce: 2},
		Transactions: []*wire.MsgTx{testCoinbaseTx(), spend, inBlockSpend},
	})
	block.SetHeight(2)
	// Exactly the omitted-Assets entries written by the April connect paths.
	return origin, block, []SpentTxOut{
		{Amount: entry.amount, PkScript: entry.pkScript, Height: 1},
		{Amount: 50, PkScript: entry.pkScript, Height: 1},
		{Amount: 900, PkScript: entry.pkScript, Height: 2},
	}
}

func createLegacyJournalDB(t *testing.T, origin, block *btcutil.Block, stxos []SpentTxOut, retainOrigin, modern bool) (database.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blocks")
	db, err := database.Create("ffldb", path, wire.SimNet)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx database.Tx) error {
		for _, key := range [][]byte{spendJournalBucketName, heightIndexBucketName, hashIndexBucketName, utxoSetBucketName} {
			if _, err := tx.Metadata().CreateBucket(key); err != nil {
				return err
			}
		}
		if retainOrigin {
			if err := tx.StoreBlock(origin); err != nil {
				return err
			}
		}
		if err := dbPutBlockIndex(tx, origin.Hash(), 1); err != nil {
			return err
		}
		if err := tx.StoreBlock(block); err != nil {
			return err
		}
		if err := dbPutBlockIndex(tx, block.Hash(), 2); err != nil {
			return err
		}
		if modern {
			return dbPutSpendJournalEntry(tx, block.Hash(), stxos)
		}
		return tx.Metadata().Bucket(spendJournalBucketName).Put(block.Hash()[:], serializeSpendJournalEntry(stxos))
	})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db, path
}

func TestLegacySpendJournalReopenAndDisconnect(t *testing.T) {
	origin, block, stxos := legacyJournalFixture()
	db, path := createLegacyJournalDB(t, origin, block, stxos, true, false)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	db, err = database.Open("ffldb", path, wire.SimNet)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	err = db.Update(func(tx database.Tx) error {
		// Match disconnectBlock: its optional-index journal read follows removal
		// of the detached block from the main-chain height index.
		if err := dbRemoveBlockIndex(tx, block.Hash(), block.Height()); err != nil {
			return err
		}
		var err error
		stxos, err = dbFetchSpendJournalEntryForDisconnect(tx, block)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := origin.MsgBlock().Transactions[1].TxOut[0].Assets
	if !reflect.DeepEqual(stxos[0].Assets, want) || len(stxos[1].Assets) != 0 || !reflect.DeepEqual(stxos[2].Assets, want) {
		t.Fatalf("legacy journal assets were not recovered: %+v", stxos)
	}
	view := NewUtxoViewpoint()
	if err := view.disconnectTransactions(db, block, stxos); err != nil {
		t.Fatal(err)
	}
	outpoint := block.MsgBlock().Transactions[1].TxIn[0].PreviousOutPoint
	restored := view.LookupEntry(outpoint)
	if restored == nil || restored.IsSpent() || !reflect.DeepEqual(restored.TxAssets(), want) {
		t.Fatalf("legacy disconnect did not restore asset UTXO: %+v", restored)
	}
	// Outputs created and consumed inside the detached block must stay spent.
	inBlock := block.MsgBlock().Transactions[2].TxIn[0].PreviousOutPoint
	if entry := view.LookupEntry(inBlock); entry != nil && !entry.IsSpent() {
		t.Fatal("in-block output survived disconnect")
	}
	if err := db.Update(func(tx database.Tx) error { return dbPutUtxoView(tx, view) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open("ffldb", path, wire.SimNet)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(tx database.Tx) error {
		entry, err := dbFetchUtxoEntry(tx, tx.Metadata().Bucket(utxoSetBucketName), outpoint)
		if err != nil {
			return err
		}
		if entry == nil || entry.Amount() != stxos[0].Amount || !reflect.DeepEqual(entry.TxAssets(), want) {
			t.Fatalf("restored asset UTXO did not survive restart: %+v", entry)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacySpendJournalFailsClosed(t *testing.T) {
	for _, scenario := range []string{"pruned", "height", "amount", "script", "coinbase", "outpoint"} {
		t.Run(scenario, func(t *testing.T) {
			origin, block, stxos := legacyJournalFixture()
			switch scenario {
			case "height":
				stxos[0].Height = 0
			case "amount":
				stxos[0].Amount++
			case "script":
				stxos[0].PkScript = []byte{0}
			case "coinbase":
				stxos[0].IsCoinBase = true
			case "outpoint":
				block.MsgBlock().Transactions[1].TxIn[0].PreviousOutPoint.Index = 100
			}
			db, _ := createLegacyJournalDB(t, origin, block, stxos, scenario != "pruned", false)
			defer db.Close()
			if scenario == "pruned" {
				// Read-only index catch-up must not gain a dependency on old
				// origin blocks just because rollback repair is available.
				if err := db.View(func(tx database.Tx) error {
					_, err := dbFetchSpendJournalEntry(tx, block)
					return err
				}); err != nil {
					t.Fatalf("ordinary journal read regressed: %v", err)
				}
			}
			if err := db.View(func(tx database.Tx) error { _, err := dbFetchSpendJournalEntryForDisconnect(tx, block); return err }); err == nil {
				t.Fatal("unsafe legacy journal was accepted")
			}
		})
	}
}

func TestModernSpendJournalNeedsNoOriginBlocks(t *testing.T) {
	for _, prune := range []bool{false, true} {
		origin, block, stxos := legacyJournalFixture()
		stxos[0].Assets = origin.MsgBlock().Transactions[1].TxOut[0].Assets.Clone()
		stxos[2].Assets = stxos[0].Assets.Clone()
		db, _ := createLegacyJournalDB(t, origin, block, stxos, false, true)
		err := db.View(func(tx database.Tx) error { _, err := dbFetchSpendJournalEntryForDisconnect(tx, block); return err })
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		err = db.Update(func(tx database.Tx) error {
			if prune {
				return dbPruneSpendJournalEntry(tx, []chainhash.Hash{*block.Hash()})
			}
			return dbRemoveSpendJournalEntry(tx, block.Hash())
		})
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		err = db.View(func(tx database.Tx) error {
			bucket := tx.Metadata().Bucket(spendJournalBucketName)
			if bucket.Get(block.Hash()[:]) != nil || bucket.Get(spendJournalAssetsKey(block.Hash())) != nil {
				t.Error("journal removal left data or asset-completeness marker")
			}
			return nil
		})
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
