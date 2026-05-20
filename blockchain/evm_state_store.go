package blockchain

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/contract/evm"
)

var (
	evmStateBucketName        = []byte("evmstate")
	evmStateByBlockBucketName = []byte("byblock")
	evmStateTipKeyName        = []byte("tip")

	ErrEVMStateNotFound = errors.New("EVM state not found")
)

// EVMStateStore persists deterministic EVM state snapshots by SatoshiNet block
// hash. The store is intentionally separate from validation so callers can
// decide exactly when a validated block should commit its post-state.
type EVMStateStore struct {
	db database.DB
}

func NewEVMStateStore(db database.DB) *EVMStateStore {
	return &EVMStateStore{db: db}
}

func (s *EVMStateStore) LoadTip() (*chainhash.Hash, *evm.MemoryStateDB, error) {
	if s == nil || s.db == nil {
		return nil, nil, errors.New("missing EVM state database")
	}
	var tip *chainhash.Hash
	var state *evm.MemoryStateDB
	err := s.db.View(func(dbTx database.Tx) error {
		parent := dbTx.Metadata().Bucket(evmStateBucketName)
		if parent == nil {
			state = evm.NewMemoryStateDB()
			return nil
		}
		tipBytes := parent.Get(evmStateTipKeyName)
		if tipBytes == nil {
			state = evm.NewMemoryStateDB()
			return nil
		}
		if len(tipBytes) != chainhash.HashSize {
			return errors.New("corrupt EVM state tip")
		}
		var hash chainhash.Hash
		copy(hash[:], tipBytes)
		loaded, err := loadEVMStateFromBucket(parent, &hash)
		if err != nil {
			return err
		}
		tip = &hash
		state = loaded
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return tip, state, nil
}

func (s *EVMStateStore) LoadBlockState(hash *chainhash.Hash) (*evm.MemoryStateDB, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("missing EVM state database")
	}
	if hash == nil {
		return nil, errors.New("missing block hash")
	}
	var state *evm.MemoryStateDB
	err := s.db.View(func(dbTx database.Tx) error {
		parent := dbTx.Metadata().Bucket(evmStateBucketName)
		if parent == nil {
			return ErrEVMStateNotFound
		}
		loaded, err := loadEVMStateFromBucket(parent, hash)
		if err != nil {
			return err
		}
		state = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (s *EVMStateStore) StoreBlockState(hash *chainhash.Hash, state *evm.MemoryStateDB) error {
	if s == nil || s.db == nil {
		return errors.New("missing EVM state database")
	}
	if hash == nil {
		return errors.New("missing block hash")
	}
	if state == nil {
		state = evm.NewMemoryStateDB()
	}
	return s.db.Update(func(dbTx database.Tx) error {
		return dbStoreEVMBlockState(dbTx, hash, state)
	})
}

func (s *EVMStateStore) DeleteBlockState(hash *chainhash.Hash) error {
	if s == nil || s.db == nil {
		return errors.New("missing EVM state database")
	}
	if hash == nil {
		return errors.New("missing block hash")
	}
	return s.db.Update(func(dbTx database.Tx) error {
		return dbDeleteEVMBlockState(dbTx, hash, nil)
	})
}

func dbStoreEVMBlockState(dbTx database.Tx, hash *chainhash.Hash, state *evm.MemoryStateDB) error {
	if hash == nil {
		return errors.New("missing block hash")
	}
	if state == nil {
		state = evm.NewMemoryStateDB()
	}
	encoded, err := state.MarshalBinary()
	if err != nil {
		return err
	}
	parent, err := dbTx.Metadata().CreateBucketIfNotExists(evmStateBucketName)
	if err != nil {
		return err
	}
	byBlock, err := parent.CreateBucketIfNotExists(evmStateByBlockBucketName)
	if err != nil {
		return err
	}
	if err := byBlock.Put(hash[:], encoded); err != nil {
		return err
	}
	return parent.Put(evmStateTipKeyName, hash[:])
}

func dbDeleteEVMBlockState(dbTx database.Tx, hash, newTip *chainhash.Hash) error {
	if hash == nil {
		return errors.New("missing block hash")
	}
	parent := dbTx.Metadata().Bucket(evmStateBucketName)
	if parent == nil {
		return nil
	}
	byBlock := parent.Bucket(evmStateByBlockBucketName)
	if byBlock != nil {
		if err := byBlock.Delete(hash[:]); err != nil {
			return err
		}
	}
	if newTip != nil {
		if byBlock != nil && byBlock.Get(newTip[:]) != nil {
			return parent.Put(evmStateTipKeyName, newTip[:])
		}
		return parent.Delete(evmStateTipKeyName)
	}
	currentTip := parent.Get(evmStateTipKeyName)
	if currentTip != nil && len(currentTip) == chainhash.HashSize && bytes.Equal(currentTip, hash[:]) {
		return parent.Delete(evmStateTipKeyName)
	}
	return nil
}

func (s *EVMStateStore) RuntimeFactory() EVMRuntimeFactory {
	return func(block *btcutil.Block, view *UtxoViewpoint) (*evm.Runtime, error) {
		if block == nil {
			return nil, errors.New("missing block")
		}
		prevHash := block.MsgBlock().Header.PrevBlock
		state, err := s.LoadBlockState(&prevHash)
		if errors.Is(err, ErrEVMStateNotFound) {
			state = evm.NewMemoryStateDB()
		} else if err != nil {
			return nil, err
		}
		return evm.NewRuntime(state), nil
	}
}

func loadEVMStateFromBucket(parent database.Bucket, hash *chainhash.Hash) (*evm.MemoryStateDB, error) {
	byBlock := parent.Bucket(evmStateByBlockBucketName)
	if byBlock == nil {
		return nil, ErrEVMStateNotFound
	}
	encoded := byBlock.Get(hash[:])
	if encoded == nil {
		return nil, ErrEVMStateNotFound
	}
	state, err := evm.DecodeMemoryStateDB(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode EVM state for block %s: %w", hash, err)
	}
	return state, nil
}
