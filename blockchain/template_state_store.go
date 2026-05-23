package blockchain

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
)

var (
	templateStateBucketName        = []byte("templatestate")
	templateStateByBlockBucketName = []byte("byblock")
	templateStateTipKeyName        = []byte("tip")

	ErrTemplateStateNotFound = errors.New("template state not found")
)

type TemplateStateStore struct {
	db database.DB
}

func NewTemplateStateStore(db database.DB) *TemplateStateStore {
	return &TemplateStateStore{db: db}
}

func (s *TemplateStateStore) LoadTip() (*chainhash.Hash, *template.RuntimeStore, error) {
	if s == nil || s.db == nil {
		return nil, nil, errors.New("missing template state database")
	}
	var tip *chainhash.Hash
	var store *template.RuntimeStore
	err := s.db.View(func(dbTx database.Tx) error {
		parent := dbTx.Metadata().Bucket(templateStateBucketName)
		if parent == nil {
			store = template.NewRuntimeStore()
			return nil
		}
		tipBytes := parent.Get(templateStateTipKeyName)
		if tipBytes == nil {
			store = template.NewRuntimeStore()
			return nil
		}
		if len(tipBytes) != chainhash.HashSize {
			return errors.New("corrupt template state tip")
		}
		var hash chainhash.Hash
		copy(hash[:], tipBytes)
		loaded, err := loadTemplateStateFromBucket(parent, &hash)
		if err != nil {
			return err
		}
		tip = &hash
		store = loaded
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return tip, store, nil
}

func (s *TemplateStateStore) LoadBlockState(hash *chainhash.Hash) (*template.RuntimeStore, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("missing template state database")
	}
	if hash == nil {
		return nil, errors.New("missing block hash")
	}
	var store *template.RuntimeStore
	err := s.db.View(func(dbTx database.Tx) error {
		parent := dbTx.Metadata().Bucket(templateStateBucketName)
		if parent == nil {
			return ErrTemplateStateNotFound
		}
		loaded, err := loadTemplateStateFromBucket(parent, hash)
		if err != nil {
			return err
		}
		store = loaded
		return nil
	})
	return store, err
}

func (s *TemplateStateStore) StoreBlockState(hash *chainhash.Hash, store *template.RuntimeStore) error {
	if s == nil || s.db == nil {
		return errors.New("missing template state database")
	}
	if hash == nil {
		return errors.New("missing block hash")
	}
	if store == nil {
		store = template.NewRuntimeStore()
	}
	return s.db.Update(func(dbTx database.Tx) error {
		return dbStoreTemplateBlockState(dbTx, hash, store)
	})
}

func (s *TemplateStateStore) DeleteBlockState(hash *chainhash.Hash) error {
	if s == nil || s.db == nil {
		return errors.New("missing template state database")
	}
	if hash == nil {
		return errors.New("missing block hash")
	}
	return s.db.Update(func(dbTx database.Tx) error {
		return dbDeleteTemplateBlockState(dbTx, hash, nil)
	})
}

func (s *TemplateStateStore) RuntimeFactory() TemplateRuntimeFactory {
	return func(block *btcutil.Block, view *UtxoViewpoint) (*template.RuntimeStore, error) {
		if block == nil {
			return nil, errors.New("missing block")
		}
		prevHash := block.MsgBlock().Header.PrevBlock
		store, err := s.LoadBlockState(&prevHash)
		if errors.Is(err, ErrTemplateStateNotFound) {
			_, tipStore, tipErr := s.LoadTip()
			if tipErr != nil {
				return nil, tipErr
			}
			if tipStore != nil {
				store = tipStore
			} else {
				store = template.NewRuntimeStore()
			}
		} else if err != nil {
			return nil, err
		}
		return store, nil
	}
}

func dbStoreTemplateBlockState(dbTx database.Tx, hash *chainhash.Hash, store *template.RuntimeStore) error {
	if hash == nil {
		return errors.New("missing block hash")
	}
	if store == nil {
		store = template.NewRuntimeStore()
	}
	encoded, err := store.MarshalBinary()
	if err != nil {
		return err
	}
	parent, err := dbTx.Metadata().CreateBucketIfNotExists(templateStateBucketName)
	if err != nil {
		return err
	}
	byBlock, err := parent.CreateBucketIfNotExists(templateStateByBlockBucketName)
	if err != nil {
		return err
	}
	if err := byBlock.Put(hash[:], encoded); err != nil {
		return err
	}
	return parent.Put(templateStateTipKeyName, hash[:])
}

func dbDeleteTemplateBlockState(dbTx database.Tx, hash, newTip *chainhash.Hash) error {
	if hash == nil {
		return errors.New("missing block hash")
	}
	parent := dbTx.Metadata().Bucket(templateStateBucketName)
	if parent == nil {
		return nil
	}
	byBlock := parent.Bucket(templateStateByBlockBucketName)
	if byBlock != nil {
		if err := byBlock.Delete(hash[:]); err != nil {
			return err
		}
	}
	if newTip != nil {
		if byBlock != nil && byBlock.Get(newTip[:]) != nil {
			return parent.Put(templateStateTipKeyName, newTip[:])
		}
		return parent.Delete(templateStateTipKeyName)
	}
	currentTip := parent.Get(templateStateTipKeyName)
	if currentTip != nil && len(currentTip) == chainhash.HashSize && bytes.Equal(currentTip, hash[:]) {
		return parent.Delete(templateStateTipKeyName)
	}
	return nil
}

func loadTemplateStateFromBucket(parent database.Bucket, hash *chainhash.Hash) (*template.RuntimeStore, error) {
	byBlock := parent.Bucket(templateStateByBlockBucketName)
	if byBlock == nil {
		return nil, ErrTemplateStateNotFound
	}
	encoded := byBlock.Get(hash[:])
	if encoded == nil {
		return nil, ErrTemplateStateNotFound
	}
	store, err := template.DecodeRuntimeStore(encoded, nil)
	if err != nil {
		return nil, fmt.Errorf("decode template state for block %s: %w", hash, err)
	}
	return store, nil
}
