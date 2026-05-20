package evm

import (
	"testing"

	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/stretchr/testify/require"
)

func TestContractPkScriptRoundTrip(t *testing.T) {
	contract := testContract(t)
	script, err := ContractPkScript(contract)
	require.NoError(t, err)

	require.Equal(t, []byte{
		txscript.OP_FALSE,
		txscript.OP_IF,
		0x02, 'C', 'T',
		0x16,
	}, script[:6])
	require.Equal(t, byte(txscript.OP_ENDIF), script[28])
	require.Equal(t, byte(txscript.OP_FALSE), script[29])

	got, ok, err := ParseContractPkScript(script, TestnetContractPrefix)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, contract.Equal(got))
	require.True(t, IsContractPkScript(script))
}

func TestParseContractPkScriptRejectsNonContractScript(t *testing.T) {
	_, ok, err := ParseContractPkScript([]byte{txscript.OP_TRUE}, TestnetContractPrefix)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestParseContractPkScriptUsesProvidedPrefix(t *testing.T) {
	contract := testContract(t)
	script, err := ContractPkScript(contract)
	require.NoError(t, err)

	got, ok, err := ParseContractPkScript(script, MainnetContractPrefix)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, MainnetContractPrefix, got.Prefix)
	require.Equal(t, contract.Hash, got.Hash)
}
