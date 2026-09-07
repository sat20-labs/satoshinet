package node

import (
	"bytes"
	"fmt"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

func previousBlockHash(tx database.Tx, hash chainhash.Hash) (chainhash.Hash, error) {
	raw, err := tx.FetchBlockHeader(&hash)
	if err != nil {
		return chainhash.Hash{}, fmt.Errorf("load contract ancestor %s: %w", hash, err)
	}
	var header wire.BlockHeader
	if err := header.Deserialize(bytes.NewReader(raw)); err != nil {
		return chainhash.Hash{}, err
	}
	return header.PrevBlock, nil
}

// Snapshots are sparse: blocks without module activity inherit their parent's
// state. Follow the requested branch, never the global latest module snapshot.
func ancestorStateHash(tx database.Tx, parent database.Bucket, byBlockKey []byte, hash chainhash.Hash, transient func(*chainhash.Hash) bool) (*chainhash.Hash, error) {
	if parent == nil && transient == nil {
		return nil, nil
	}
	var byBlock database.Bucket
	if parent != nil {
		byBlock = parent.Bucket(byBlockKey)
	}
	if parent != nil && byBlock == nil {
		return nil, fmt.Errorf("missing contract snapshot bucket")
	}
	for hash != (chainhash.Hash{}) {
		if transient != nil && transient(&hash) {
			return &hash, nil
		}
		if byBlock != nil && byBlock.Get(hash[:]) != nil {
			return &hash, nil
		}
		previous, err := previousBlockHash(tx, hash)
		if err != nil {
			return nil, err
		}
		hash = previous
	}
	return nil, nil
}

func recentEVMBlockHashes(db database.DB, parent chainhash.Hash, height uint64) (map[uint64][32]byte, error) {
	hashes := make(map[uint64][32]byte)
	if height == 0 {
		return hashes, nil
	}
	if db == nil {
		return nil, fmt.Errorf("missing EVM block history database")
	}
	err := db.View(func(tx database.Tx) error {
		hash := parent
		for count := uint64(0); count < 256 && count < height; count++ {
			hashes[height-count-1] = [32]byte(hash)
			if count+1 == 256 || count+1 == height {
				break
			}
			previous, err := previousBlockHash(tx, hash)
			if err != nil {
				return err
			}
			hash = previous
		}
		return nil
	})
	return hashes, err
}
