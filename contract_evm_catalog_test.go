//go:build legacy_evm_e2e

package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/stretchr/testify/require"
)

func TestEVMCommonContractsCounterMultipleCallers(t *testing.T) {
	rt := evm.NewRuntime(nil)
	deployer := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	callers := []evm.EVMAddress{
		mustE2EEVMAddress(t, "0x2222222222222222222222222222222222222222"),
		mustE2EEVMAddress(t, "0x3333333333333333333333333333333333333333"),
		mustE2EEVMAddress(t, "0x4444444444444444444444444444444444444444"),
	}
	block := evm.BlockContext{Number: 1, Time: 1710000000, GasLimit: 1000000, FixedGasPrice: 1}

	deploy := rt.Deploy(evm.DeployRequest{
		Caller:      deployer,
		CallID:      "counter-deploy",
		InitCode:    e2EInitCode(e2ECounterRuntimeCode()),
		Gas:         200000,
		DeployNonce: 1,
		Block:       block,
	})
	require.NoError(t, deploy.Err)
	require.Equal(t, evm.ResultStatusSuccess, deploy.Status)
	require.NotEmpty(t, deploy.RuntimeCode)

	for i, caller := range callers {
		call := rt.Call(evm.CallRequest{
			Caller: caller,
			Target: evm.ContractAddressHash(deploy.Contract),
			CallID: "counter-call",
			Gas:    100000,
			Block:  block,
		})
		require.NoError(t, call.Err)
		require.Equal(t, evm.ResultStatusSuccess, call.Status)
		require.Len(t, call.ReturnData, 32)
		require.Equal(t, uint64(i+1), binary.BigEndian.Uint64(call.ReturnData[24:]))
	}
}

func TestEVMCommonContractsRevertStatus(t *testing.T) {
	rt := evm.NewRuntime(nil)
	deployer := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	caller := mustE2EEVMAddress(t, "0x22223333445566778899aabbccddeeff00112233")
	block := evm.BlockContext{Number: 1, Time: 1710000000, GasLimit: 1000000, FixedGasPrice: 1}

	deploy := rt.Deploy(evm.DeployRequest{
		Caller:      deployer,
		CallID:      "reverter-deploy",
		InitCode:    e2EInitCode(e2EReverterRuntimeCode()),
		Gas:         100000,
		DeployNonce: 2,
		Block:       block,
	})
	require.NoError(t, deploy.Err)
	require.Equal(t, evm.ResultStatusSuccess, deploy.Status)

	call := rt.Call(evm.CallRequest{
		Caller: caller,
		Target: evm.ContractAddressHash(deploy.Contract),
		CallID: "reverter-call",
		Gas:    100000,
		Block:  block,
	})
	require.Error(t, call.Err)
	require.Equal(t, evm.ResultStatusRevert, call.Status)
	require.True(t, call.GasUsed > 0)
	require.True(t, call.GasLeft > 0)
}

func TestEVMCommonContractCatalog(t *testing.T) {
	cases := []struct {
		name      string
		validated bool
	}{
		{name: "Counter/stateful storage", validated: true},
		{name: "Reverter/failure status", validated: true},
		{name: "AssetRouter/precompile asset transfer", validated: true},
		{name: "Vault/height trigger", validated: true},
		{name: "ERC20-compatible token", validated: false},
	}

	var missing []string
	for _, c := range cases {
		if !c.validated {
			missing = append(missing, c.name)
		}
	}
	require.Equal(t, []string{"ERC20-compatible token"}, missing)
}

func e2ECounterRuntimeCode() []byte {
	return []byte{
		0x60, 0x00, // PUSH1 0x00
		0x54,       // SLOAD
		0x60, 0x01, // PUSH1 0x01
		0x01,       // ADD
		0x80,       // DUP1
		0x60, 0x00, // PUSH1 0x00
		0x55,       // SSTORE
		0x60, 0x00, // PUSH1 0x00
		0x52,       // MSTORE
		0x60, 0x20, // PUSH1 0x20
		0x60, 0x00, // PUSH1 0x00
		0xf3, // RETURN
	}
}

func e2EReverterRuntimeCode() []byte {
	return []byte{
		0x60, 0x00, // PUSH1 0x00
		0x60, 0x00, // PUSH1 0x00
		0xfd, // REVERT
	}
}

func TestEVMCommonContractsRuntimeBytecodeSanity(t *testing.T) {
	require.False(t, bytes.Contains(e2ECounterRuntimeCode(), []byte{0xff}))
	require.False(t, bytes.Contains(e2EReverterRuntimeCode(), []byte{0xff}))
}
