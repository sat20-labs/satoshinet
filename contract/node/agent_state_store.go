package node

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/database"
)

var (
	agentStateBucketName        = []byte("agentstate")
	agentStateByBlockBucketName = []byte("byblock")
	agentStateTipKeyName        = []byte("tip")

	ErrAgentStateNotFound = errors.New("agent state not found")
)

type AgentRuntimeFactory func(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error)

type AgentStateStore struct {
	db database.DB
}

func NewAgentStateStore(db database.DB) *AgentStateStore {
	return &AgentStateStore{db: db}
}

func (s *AgentStateStore) LoadTip() (*chainhash.Hash, *agent.RuntimeStore, error) {
	if s == nil || s.db == nil {
		return nil, nil, errors.New("missing agent state database")
	}
	var tip *chainhash.Hash
	var store *agent.RuntimeStore
	err := s.db.View(func(dbTx database.Tx) error {
		parent := dbTx.Metadata().Bucket(agentStateBucketName)
		if parent == nil {
			store = agent.NewRuntimeStore()
			return nil
		}
		tipBytes := parent.Get(agentStateTipKeyName)
		if tipBytes == nil {
			store = agent.NewRuntimeStore()
			return nil
		}
		if len(tipBytes) != chainhash.HashSize {
			return errors.New("corrupt agent state tip")
		}
		var hash chainhash.Hash
		copy(hash[:], tipBytes)
		loaded, err := loadAgentStateFromBucket(parent, &hash)
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

func (s *AgentStateStore) LoadBlockState(hash *chainhash.Hash) (*agent.RuntimeStore, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("missing agent state database")
	}
	if hash == nil {
		return nil, errors.New("missing block hash")
	}
	var store *agent.RuntimeStore
	err := s.db.View(func(dbTx database.Tx) error {
		parent := dbTx.Metadata().Bucket(agentStateBucketName)
		if parent == nil {
			return ErrAgentStateNotFound
		}
		loaded, err := loadAgentStateFromBucket(parent, hash)
		if err != nil {
			return err
		}
		store = loaded
		return nil
	})
	return store, err
}

func (s *AgentStateStore) StoreBlockState(hash *chainhash.Hash, store *agent.RuntimeStore) error {
	if s == nil || s.db == nil {
		return errors.New("missing agent state database")
	}
	if hash == nil {
		return errors.New("missing block hash")
	}
	if store == nil {
		store = agent.NewRuntimeStore()
	}
	return s.db.Update(func(dbTx database.Tx) error {
		return dbStoreAgentBlockState(dbTx, hash, store)
	})
}

func (s *AgentStateStore) DeleteBlockState(hash *chainhash.Hash) error {
	if s == nil || s.db == nil {
		return errors.New("missing agent state database")
	}
	if hash == nil {
		return errors.New("missing block hash")
	}
	return s.db.Update(func(dbTx database.Tx) error {
		return dbDeleteAgentBlockState(dbTx, hash, nil)
	})
}

func (s *AgentStateStore) RuntimeFactory() AgentRuntimeFactory {
	return func(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error) {
		if block == nil {
			return nil, errors.New("missing block")
		}
		prevHash := block.MsgBlock().Header.PrevBlock
		store, err := s.LoadBlockState(&prevHash)
		if errors.Is(err, ErrAgentStateNotFound) {
			_, tipStore, tipErr := s.LoadTip()
			if tipErr != nil {
				return nil, tipErr
			}
			if tipStore != nil {
				store = tipStore
			} else {
				store = agent.NewRuntimeStore()
			}
		} else if err != nil {
			return nil, err
		}
		return store, nil
	}
}

func dbStoreAgentBlockState(dbTx database.Tx, hash *chainhash.Hash, store *agent.RuntimeStore) error {
	if hash == nil {
		return errors.New("missing block hash")
	}
	if store == nil {
		store = agent.NewRuntimeStore()
	}
	encoded, err := store.MarshalBinary()
	if err != nil {
		return err
	}
	if err := validatePersistedContractStateSize("agent", encoded); err != nil {
		return err
	}
	parent, err := dbTx.Metadata().CreateBucketIfNotExists(agentStateBucketName)
	if err != nil {
		return err
	}
	byBlock, err := parent.CreateBucketIfNotExists(agentStateByBlockBucketName)
	if err != nil {
		return err
	}
	if err := byBlock.Put(hash[:], encoded); err != nil {
		return err
	}
	return parent.Put(agentStateTipKeyName, hash[:])
}

func dbDeleteAgentBlockState(dbTx database.Tx, hash, newTip *chainhash.Hash) error {
	if hash == nil {
		return errors.New("missing block hash")
	}
	parent := dbTx.Metadata().Bucket(agentStateBucketName)
	if parent == nil {
		return nil
	}
	byBlock := parent.Bucket(agentStateByBlockBucketName)
	if byBlock != nil {
		if err := byBlock.Delete(hash[:]); err != nil {
			return err
		}
	}
	if newTip != nil {
		if byBlock != nil && byBlock.Get(newTip[:]) != nil {
			return parent.Put(agentStateTipKeyName, newTip[:])
		}
		return parent.Delete(agentStateTipKeyName)
	}
	currentTip := parent.Get(agentStateTipKeyName)
	if currentTip != nil && len(currentTip) == chainhash.HashSize && bytes.Equal(currentTip, hash[:]) {
		return parent.Delete(agentStateTipKeyName)
	}
	return nil
}

func loadAgentStateFromBucket(parent database.Bucket, hash *chainhash.Hash) (*agent.RuntimeStore, error) {
	byBlock := parent.Bucket(agentStateByBlockBucketName)
	if byBlock == nil {
		return nil, ErrAgentStateNotFound
	}
	encoded := byBlock.Get(hash[:])
	if encoded == nil {
		return nil, ErrAgentStateNotFound
	}
	store, err := agent.DecodeRuntimeStore(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode agent state for block %s: %w", hash, err)
	}
	return store, nil
}
