package evm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueryContractStateViewReturnsDefaultAccountSummary(t *testing.T) {
	state := NewMemoryStateDB()
	contract := testContract(t)
	addr := GethAddress(ContractAddressHash(contract))
	state.SetCode(addr, []byte{0x60, 0x00}, 0)
	state.SetNonce(addr, 7, 0)
	state.SetContractDeployer(addr, "deployer")

	view, ok := QueryContractStateView(state, contract, BlockContext{Number: 10, Time: 20}, 0)
	require.True(t, ok)
	require.Equal(t, "0", view.Balance)
	require.Equal(t, uint64(7), view.Nonce)
	require.Equal(t, 2, view.CodeSize)
	require.Equal(t, "deployer", view.Deployer)
}
