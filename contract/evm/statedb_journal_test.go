package evm

import (
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

func TestMemoryStateDBReadsDoNotCreateAccounts(t *testing.T) {
	state := NewMemoryStateDB()
	addr := gethcommon.HexToAddress("0x1234")
	root := state.StateRoot()

	require.Zero(t, state.GetBalance(addr).Cmp(uint256.NewInt(0)))
	require.Zero(t, state.GetNonce(addr))
	require.Empty(t, state.GetCode(addr))
	require.Zero(t, state.GetState(addr, gethcommon.Hash{}))
	require.False(t, state.HasSelfDestructed(addr))
	require.False(t, state.IsNewContract(addr))
	require.Empty(t, state.accounts)
	require.Equal(t, root, state.StateRoot())
}

func TestMemoryStateDBCodeHashDistinguishesAbsentAndEmpty(t *testing.T) {
	state := NewMemoryStateDB()
	absent := gethcommon.HexToAddress("0x1234")
	empty := gethcommon.HexToAddress("0x5678")

	require.Zero(t, state.GetCodeHash(absent))
	state.CreateAccount(empty)
	require.Equal(t, types.EmptyCodeHash, state.GetCodeHash(empty))
}

func TestMemoryStateDBOriginalStorageIsLazyAndTransactionScoped(t *testing.T) {
	state := NewMemoryStateDB()
	addr := gethcommon.HexToAddress("0x1234")
	key := gethcommon.HexToHash("0x01")
	original := gethcommon.HexToHash("0x02")
	updated := gethcommon.HexToHash("0x03")
	state.SetState(addr, key, original)
	state.Prepare(params.Rules{}, gethcommon.Address{}, gethcommon.Address{}, nil, nil, nil)
	require.Empty(t, state.original)

	state.SetState(addr, key, updated)
	current, committed := state.GetStateAndCommittedState(addr, key)
	require.Equal(t, updated, current)
	require.Equal(t, original, committed)
	require.Len(t, state.original, 1)
	require.Len(t, state.original[addr], 1)
}

func TestMemoryStateDBNewContractOriginalStorageIsZero(t *testing.T) {
	state := NewMemoryStateDB()
	addr := gethcommon.HexToAddress("0x1234")
	key := gethcommon.HexToHash("0x01")
	updated := gethcommon.HexToHash("0x02")
	state.Prepare(params.Rules{}, gethcommon.Address{}, gethcommon.Address{}, nil, nil, nil)
	state.CreateContract(addr)
	state.SetState(addr, key, updated)

	current, committed := state.GetStateAndCommittedState(addr, key)
	require.Equal(t, updated, current)
	require.Zero(t, committed)
}

func TestMemoryStateDBJournalSnapshotsRevertOnlyChanges(t *testing.T) {
	state := NewMemoryStateDB()
	addr := gethcommon.HexToAddress("0x1234")
	key := gethcommon.HexToHash("0x01")
	value := gethcommon.HexToHash("0x02")
	state.AddBalance(addr, uint256.NewInt(10), tracing.BalanceChangeUnspecified)
	account := state.accounts[addr]

	outer := state.Snapshot()
	state.SetState(addr, key, value)
	inner := state.Snapshot()
	state.SetCode(addr, []byte{1, 2, 3}, tracing.CodeChangeUnspecified)
	require.Same(t, account, state.accounts[addr], "snapshot must not clone the world state")
	require.NotEmpty(t, state.journal)

	state.RevertToSnapshot(inner)
	require.Empty(t, state.GetCode(addr))
	require.Equal(t, value, state.GetState(addr, key))
	state.RevertToSnapshot(outer)
	require.Zero(t, state.GetState(addr, key))
	require.Empty(t, state.journal)
	require.Empty(t, state.revisions)
}

func TestRuntimeSuccessfulCallClearsSnapshotJournal(t *testing.T) {
	contract := ContractAddressHash(testContract(t))
	runtime := NewRuntime(nil)
	runtime.SetCode(contract, []byte{0x00})
	result := runtime.Call(CallRequest{
		CallerAddress: "0x11112233445566778899aabbccddeeff00112233",
		TargetAddress: contract.String(),
		Gas:           100000,
		Block:         BlockContext{Number: 1, Time: 1, GasLimit: 1000000},
	})
	require.NoError(t, result.Err)
	require.Empty(t, runtime.State.journal)
	require.Empty(t, runtime.State.revisions)
}
