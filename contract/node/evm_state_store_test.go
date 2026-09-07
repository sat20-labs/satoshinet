package node

import (
	"errors"
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	_ "github.com/sat20-labs/satoshinet/database/ffldb"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestEVMStateStoreRoundTrip(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()

	store := NewEVMStateStore(db)
	tip, empty, err := store.LoadTip()
	if err != nil {
		t.Fatal(err)
	}
	if tip != nil {
		t.Fatal("new store should not have an EVM state tip")
	}
	if empty == nil || empty.StateRoot() != evm.NewMemoryStateDB().StateRoot() {
		t.Fatal("new store should return empty EVM state")
	}

	state := evm.NewMemoryStateDB()
	addr := gethcommon.HexToAddress("0x11112233445566778899aabbccddeeff00112233")
	state.SetNonce(addr, 7, 0)
	state.AddBalance(addr, uint256.NewInt(123), 0)
	state.SetCode(addr, []byte{0x60, 0x2a}, 0)
	blockHash := chainhash.Hash{1, 2, 3}

	if err := store.StoreBlockState(&blockHash, state); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.LoadBlockState(&blockHash)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StateRoot() != state.StateRoot() {
		t.Fatalf("unexpected loaded state root: got %x want %x", loaded.StateRoot(), state.StateRoot())
	}

	tip, tipState, err := store.LoadTip()
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil || *tip != blockHash {
		t.Fatalf("unexpected EVM state tip: %v", tip)
	}
	if tipState.StateRoot() != state.StateRoot() {
		t.Fatal("tip state root mismatch")
	}
}

func TestEVMStateStoreRuntimeFactoryLoadsParentState(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()

	store := NewEVMStateStore(db)
	parentHash := chainhash.Hash{9, 9, 9}
	state := evm.NewMemoryStateDB()
	addr := gethcommon.HexToAddress("0x11112233445566778899aabbccddeeff00112233")
	state.SetCode(addr, []byte{0x60, 0x2a}, 0)
	if err := store.StoreBlockState(&parentHash, state); err != nil {
		t.Fatal(err)
	}

	block := btcutilBlockWithPrev(parentHash)
	runtime, err := store.RuntimeFactory()(block, blockchain.NewUtxoViewpoint())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.State.StateRoot() != state.StateRoot() {
		t.Fatal("runtime did not load parent EVM state")
	}
}

func TestStateManagerReleasesStoredValidationSnapshots(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()
	hash := chainhash.Hash{7}

	templateValidator := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{})
	evmValidator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{})
	agentValidator := NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{})
	templateValidator.rememberPostState(&hash, template.NewRuntimeStore())
	evmValidator.rememberPostState(&hash, evm.NewMemoryStateDB())
	agentValidator.rememberPostState(&hash, agent.NewRuntimeStore())
	provider := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		TemplateValidator: templateValidator,
		EVMValidator:      evmValidator,
		AgentValidator:    agentValidator,
	})

	err := db.Update(func(dbTx database.Tx) error {
		return NewContractStateManager().StoreContractBlockState(dbTx, &hash, provider)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []contractframework.ModuleType{
		contractframework.ModuleTemplate,
		contractframework.ModuleEVM,
		contractframework.ModuleAgent,
	} {
		if _, ok := provider.ContractBlockPostState(module, &hash); !ok {
			t.Fatalf("module %d snapshot released before transaction commit", module)
		}
		provider.ReleaseContractBlockPostState(module, &hash)
		if _, ok := provider.ContractBlockPostState(module, &hash); ok {
			t.Fatalf("module %d validation snapshot was not released after commit", module)
		}
	}
}

func TestEVMStateStoreDelete(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()

	store := NewEVMStateStore(db)
	hash := chainhash.Hash{1}
	if err := store.StoreBlockState(&hash, evm.NewMemoryStateDB()); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteBlockState(&hash); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadBlockState(&hash); !errors.Is(err, ErrEVMStateNotFound) {
		t.Fatalf("got %v want ErrEVMStateNotFound", err)
	}
	tip, _, err := store.LoadTip()
	if err != nil {
		t.Fatal(err)
	}
	if tip != nil {
		t.Fatal("deleted tip should clear EVM state tip")
	}
}

func TestEVMStateStoreDeleteFallsBackToExistingParentTip(t *testing.T) {
	db := testEVMStateDB(t)
	defer db.Close()

	parent := chainhash.Hash{1}
	child := chainhash.Hash{2}
	store := NewEVMStateStore(db)
	if err := store.StoreBlockState(&parent, evm.NewMemoryStateDB()); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreBlockState(&child, evm.NewMemoryStateDB()); err != nil {
		t.Fatal(err)
	}

	err := db.Update(func(dbTx database.Tx) error {
		return dbDeleteEVMBlockState(dbTx, &child, &parent)
	})
	if err != nil {
		t.Fatal(err)
	}
	tip, _, err := store.LoadTip()
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil || *tip != parent {
		t.Fatalf("unexpected EVM state tip: %v", tip)
	}
}

func testEVMStateDB(t *testing.T) database.DB {
	t.Helper()
	db, err := database.Create("ffldb", t.TempDir(), wire.TestNet)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func btcutilBlockWithPrev(prevHash chainhash.Hash) *btcutil.Block {
	return btcutil.NewBlock(&wire.MsgBlock{
		Header: wire.BlockHeader{PrevBlock: prevHash},
		Transactions: []*wire.MsgTx{
			wire.NewMsgTx(2),
		},
	})
}
