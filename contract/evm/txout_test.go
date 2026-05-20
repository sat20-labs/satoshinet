package evm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewContractTxOut(t *testing.T) {
	contract := testContract(t)
	txOut, err := NewContractTxOut(100, nil, contract)
	require.NoError(t, err)
	require.Equal(t, int64(100), txOut.Value)

	got, ok, err := ContractFromTxOut(txOut, TestnetContractPrefix)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, contract.Equal(got))
}
