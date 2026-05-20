package common

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"
)

func testContract(t *testing.T) ContractAddress {
	t.Helper()
	addr, err := ParseEVMAddressHex("00112233445566778899aabbccddeeff00112233")
	require.NoError(t, err)
	contract, err := NewContractAddress(TestnetContractPrefix, AddressVersionV1, ContractTypeEVM, addr)
	require.NoError(t, err)
	return contract
}

func TestContractPkScriptRoundTrip(t *testing.T) {
	contract := testContract(t)
	script, err := ContractPkScript(contract)
	require.NoError(t, err)

	got, ok, err := ParseContractPkScript(script, TestnetContractPrefix)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, contract.Equal(got))
	require.True(t, IsContractPkScript(script))
}

func TestContractPkScriptRoundTripWithTemplateHash(t *testing.T) {
	hash := sha256.Sum256([]byte("template contract"))
	contract, err := NewContractAddressFromHash(TestnetContractPrefix, AddressVersionV1, ContractTypeTemplate, hash[:])
	require.NoError(t, err)
	script, err := ContractPkScript(contract)
	require.NoError(t, err)

	got, ok, err := ParseContractPkScript(script, TestnetContractPrefix)
	require.NoError(t, err)
	require.True(t, ok)
	require.True(t, contract.Equal(got))
	require.Equal(t, hash[:], ContractAddressHashBytes(got))
}

func TestInvokeNullDataScriptRoundTrip(t *testing.T) {
	invoke := InvokePayload{GasLimit: 123, CallNonce: 4, Calldata: []byte{0xaa}}
	script, err := InvokeNullDataScript(invoke)
	require.NoError(t, err)
	got, err := ReadInvokeNullDataScript(script)
	require.NoError(t, err)
	require.Equal(t, invoke, got)
}

func TestDeployNullDataScriptsSplitPayload(t *testing.T) {
	initCode := make([]byte, MaxNullDataPayloadLen+17)
	for i := range initCode {
		initCode[i] = byte(i)
	}
	scripts, err := DeployNullDataScripts(DeployPayload{
		GasLimit:    1000,
		DeployNonce: 9,
		InitCode:    initCode,
	})
	require.NoError(t, err)
	require.Len(t, scripts, 2)

	var payload []byte
	for _, script := range scripts {
		txType, part, err := ReadNullDataScript(script)
		require.NoError(t, err)
		require.Equal(t, TxTypeDeploy, txType)
		require.LessOrEqual(t, len(part), MaxNullDataPayloadLen)
		payload = append(payload, part...)
	}
	got, err := DecodeDeployPayload(payload)
	require.NoError(t, err)
	require.Equal(t, uint64(1000), got.GasLimit)
	require.Equal(t, uint64(9), got.DeployNonce)
	require.Equal(t, initCode, got.InitCode)
}

func TestStateRootPayloadRoundTrip(t *testing.T) {
	root := sha256.Sum256([]byte("state"))
	got, err := DecodeStateRootPayload(EncodeStateRootPayload(StateRootPayload{StateRoot: root}))
	require.NoError(t, err)
	require.Equal(t, root, got.StateRoot)
}
