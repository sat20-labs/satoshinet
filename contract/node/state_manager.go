package node

import (
	"fmt"
	"sort"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
)

// ModuleStateCodec binds an engine's existing persistence implementation to the
// common state manager. Registration happens during node construction, before
// the manager is used by block validation. No additional database is created.
type ModuleStateCodec struct {
	Module contractframework.ModuleType
	Name   string
	Store  func(database.Tx, *chainhash.Hash, any) error
	Delete func(database.Tx, *chainhash.Hash, *chainhash.Hash) error
}

type ContractStateManager struct {
	codecs map[contractframework.ModuleType]ModuleStateCodec
}

const MaxPersistedContractStateBytes = 16 << 20

func validatePersistedContractStateSize(module string, encoded []byte) error {
	if len(encoded) > MaxPersistedContractStateBytes {
		return fmt.Errorf("%s contract state exceeds %d bytes", module, MaxPersistedContractStateBytes)
	}
	return nil
}

func NewContractStateManager() *ContractStateManager {
	return &ContractStateManager{codecs: defaultModuleStateCodecs()}
}

func (m *ContractStateManager) RegisterStateCodec(codec ModuleStateCodec) error {
	if m == nil {
		return fmt.Errorf("missing contract state manager")
	}
	if codec.Module == 0 || codec.Name == "" || codec.Store == nil || codec.Delete == nil {
		return fmt.Errorf("incomplete contract state codec registration")
	}
	if m.codecs == nil {
		m.codecs = defaultModuleStateCodecs()
	}
	if _, exists := m.codecs[codec.Module]; exists {
		return fmt.Errorf("contract state module %d is already registered", codec.Module)
	}
	m.codecs[codec.Module] = codec
	return nil
}

func (m *ContractStateManager) registeredCodecs() []ModuleStateCodec {
	codecs := m.codecs
	if codecs == nil {
		codecs = defaultModuleStateCodecs()
	}
	ordered := make([]ModuleStateCodec, 0, len(codecs))
	for _, codec := range codecs {
		ordered = append(ordered, codec)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Module < ordered[j].Module })
	return ordered
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
	for _, codec := range m.registeredCodecs() {
		state, ok := provider.ContractBlockPostState(codec.Module, hash)
		if !ok || state == nil {
			continue
		}
		if err := codec.Store(dbTx, hash, state.Snapshot()); err != nil {
			return fmt.Errorf("persist %s contract state: %w", codec.Name, err)
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
	for _, codec := range m.registeredCodecs() {
		if err := codec.Delete(dbTx, hash, newTip); err != nil {
			return fmt.Errorf("delete %s contract state: %w", codec.Name, err)
		}
	}
	return nil
}

func storeContractBlockState(dbTx database.Tx, hash *chainhash.Hash,
	module contractframework.ModuleType, snapshot any) error {

	codec, ok := defaultModuleStateCodecs()[module]
	if !ok {
		return fmt.Errorf("unsupported contract state module %d", module)
	}
	return codec.Store(dbTx, hash, snapshot)
}

func typedModuleStateCodec[T any](module contractframework.ModuleType, name string,
	store func(database.Tx, *chainhash.Hash, *T) error,
	remove func(database.Tx, *chainhash.Hash, *chainhash.Hash) error) ModuleStateCodec {

	return ModuleStateCodec{
		Module: module, Name: name, Delete: remove,
		Store: func(tx database.Tx, hash *chainhash.Hash, snapshot any) error {
			state, ok := snapshot.(*T)
			if !ok || state == nil {
				return fmt.Errorf("%s state snapshot has type %T or is nil", name, snapshot)
			}
			return store(tx, hash, state)
		},
	}
}

// Built-in engine wiring is declared once. Store/delete iteration has no
// Template/EVM/Agent switch, and an additional engine registers its own codec.
func defaultModuleStateCodecs() map[contractframework.ModuleType]ModuleStateCodec {
	codecs := []ModuleStateCodec{
		typedModuleStateCodec[tmplcontract.RuntimeStore](contractframework.ModuleTemplate, "template",
			dbStoreTemplateBlockState, dbDeleteTemplateBlockState),
		typedModuleStateCodec[evm.MemoryStateDB](contractframework.ModuleEVM, "EVM",
			dbStoreEVMBlockState, dbDeleteEVMBlockState),
		typedModuleStateCodec[agentcontract.RuntimeStore](contractframework.ModuleAgent, "agent",
			dbStoreAgentBlockState, dbDeleteAgentBlockState),
	}
	out := make(map[contractframework.ModuleType]ModuleStateCodec, len(codecs))
	for _, codec := range codecs {
		out[codec.Module] = codec
	}
	return out
}
