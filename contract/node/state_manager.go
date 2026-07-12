package node

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
)

type ContractStateManager struct{}

const MaxPersistedContractStateBytes = 16 << 20

func validatePersistedContractStateSize(module string, encoded []byte) error {
	if len(encoded) > MaxPersistedContractStateBytes {
		return fmt.Errorf("%s contract state exceeds %d bytes", module, MaxPersistedContractStateBytes)
	}
	return nil
}

func NewContractStateManager() *ContractStateManager {
	return &ContractStateManager{}
}

func (m *ContractStateManager) StoreContractBlockState(dbTx database.Tx,
	hash *chainhash.Hash, provider blockchain.ContractBlockStateProvider) error {

	if dbTx == nil {
		return fmt.Errorf("missing contract state db transaction")
	}
	if hash == nil {
		return fmt.Errorf("missing block hash")
	}
	if provider == nil {
		return nil
	}
	for _, module := range []contractframework.ModuleType{
		contractframework.ModuleTemplate,
		contractframework.ModuleEVM,
		contractframework.ModuleAgent,
	} {
		state, ok := provider.ContractBlockPostState(module, hash)
		if !ok || state == nil {
			continue
		}
		if err := storeContractBlockState(dbTx, hash, module, state.Snapshot()); err != nil {
			return err
		}
	}
	return nil
}

func (m *ContractStateManager) DeleteContractBlockState(dbTx database.Tx,
	hash *chainhash.Hash, newTip *chainhash.Hash) error {

	if dbTx == nil {
		return fmt.Errorf("missing contract state db transaction")
	}
	if hash == nil {
		return fmt.Errorf("missing block hash")
	}
	if err := dbDeleteTemplateBlockState(dbTx, hash, newTip); err != nil {
		return err
	}
	if err := dbDeleteEVMBlockState(dbTx, hash, newTip); err != nil {
		return err
	}
	return dbDeleteAgentBlockState(dbTx, hash, newTip)
}

func storeContractBlockState(dbTx database.Tx, hash *chainhash.Hash,
	module contractframework.ModuleType, snapshot any) error {

	switch module {
	case contractframework.ModuleTemplate:
		store, ok := snapshot.(*tmplcontract.RuntimeStore)
		if !ok {
			return fmt.Errorf("template state snapshot has type %T", snapshot)
		}
		return dbStoreTemplateBlockState(dbTx, hash, store)
	case contractframework.ModuleEVM:
		state, ok := snapshot.(*evm.MemoryStateDB)
		if !ok {
			return fmt.Errorf("EVM state snapshot has type %T", snapshot)
		}
		return dbStoreEVMBlockState(dbTx, hash, state)
	case contractframework.ModuleAgent:
		store, ok := snapshot.(*agentcontract.RuntimeStore)
		if !ok {
			return fmt.Errorf("agent state snapshot has type %T", snapshot)
		}
		return dbStoreAgentBlockState(dbTx, hash, store)
	default:
		return fmt.Errorf("unsupported contract state module %d", module)
	}
}
