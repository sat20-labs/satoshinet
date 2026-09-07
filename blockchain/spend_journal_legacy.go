package blockchain

import (
	"bytes"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

// A separate key preserves the journal encoding while distinguishing an empty
// asset list from the missing asset fields written by older SatoshiNet nodes.
// New journals and this marker are committed in the same database transaction.
func spendJournalAssetsKey(hash *chainhash.Hash) []byte {
	key := make([]byte, chainhash.HashSize+1)
	copy(key, hash[:])
	key[chainhash.HashSize] = 1
	return key
}

// Ordinary journal queries (for example optional-index catch-up) retain their
// existing behavior. Only disconnect requires complete assets and the retained
// origin blocks needed to repair old entries.
func dbFetchSpendJournalEntryForDisconnect(dbTx database.Tx, block *btcutil.Block) ([]SpentTxOut, error) {
	stxos, err := dbFetchSpendJournalEntry(dbTx, block)
	if err != nil {
		return nil, err
	}
	bucket := dbTx.Metadata().Bucket(spendJournalBucketName)
	if !bytes.Equal(bucket.Get(spendJournalAssetsKey(block.Hash())), []byte{1}) {
		if err := restoreLegacySpendJournalAssets(dbTx, block, stxos); err != nil {
			return nil, err
		}
	}
	return stxos, nil
}

// April-era journals recorded the origin height, value and script, but omitted
// Assets. Recover them from retained consensus block data, never an indexer.
// Missing/pruned or inconsistent source data must stop the disconnect.
func restoreLegacySpendJournalAssets(dbTx database.Tx, block *btcutil.Block, stxos []SpentTxOut) error {
	blocks := make(map[int32]map[chainhash.Hash]*wire.MsgTx)
	// disconnectBlock removes this block's height index before fetching the
	// journal for optional indexes. In-block spends must use the supplied block.
	if block.Height() > 0 {
		transactions := make(map[chainhash.Hash]*wire.MsgTx)
		for _, tx := range block.MsgBlock().Transactions {
			transactions[tx.TxHash()] = tx
		}
		blocks[block.Height()] = transactions
	}
	stxoIndex := 0
	for _, tx := range block.MsgBlock().Transactions[1:] {
		if !transactionConsumesSpendJournal(tx, false) {
			continue
		}
		for _, input := range tx.TxIn {
			stxo := &stxos[stxoIndex]
			stxoIndex++
			if len(stxo.Assets) != 0 {
				continue
			}
			outpoint := input.PreviousOutPoint
			if stxo.Height <= 0 {
				return fmt.Errorf("cannot recover legacy spend assets for %v: missing origin height", outpoint)
			}
			transactions, ok := blocks[stxo.Height]
			if !ok {
				hash, err := dbFetchHashByHeight(dbTx, stxo.Height)
				if err != nil {
					return fmt.Errorf("recover legacy spend assets at height %d: %w", stxo.Height, err)
				}
				origin := block
				if *hash != *block.Hash() {
					serialized, err := dbTx.FetchBlock(hash)
					if err != nil {
						return fmt.Errorf("recover legacy spend assets from block %v: %w", hash, err)
					}
					origin, err = btcutil.NewBlockFromBytes(serialized)
					if err != nil {
						return err
					}
				}
				transactions = make(map[chainhash.Hash]*wire.MsgTx)
				for _, originTx := range origin.MsgBlock().Transactions {
					transactions[originTx.TxHash()] = originTx
				}
				blocks[stxo.Height] = transactions
			}
			originTx := transactions[outpoint.Hash]
			if originTx == nil || uint64(outpoint.Index) >= uint64(len(originTx.TxOut)) {
				return fmt.Errorf("cannot recover legacy spend assets: output %v missing at height %d", outpoint, stxo.Height)
			}
			output := originTx.TxOut[outpoint.Index]
			if output.Value != stxo.Amount || !bytes.Equal(output.PkScript, stxo.PkScript) ||
				IsCoinBaseTx(originTx) != stxo.IsCoinBase {
				return fmt.Errorf("cannot recover legacy spend assets: journal disagrees with output %v", outpoint)
			}
			stxo.Assets = output.Assets.Clone()
		}
	}
	return nil
}
