package evm

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

func TestManagedCloseHookUsesQuantities(t *testing.T) {
	const token = "ordx:ft:profit"
	addr := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(addr), evmCloseTransferCode(token, "tb1qdest", "40"))
	runtime.State.SetContractDeployer(ContractGethAddress(addr), "owner")
	seedEVMManagedFixture(t, runtime, addr, 0, map[string]string{token: "100"})
	backend := NewBackend(BlockExecutionRequest{
		Runtime: runtime, GasConfig: DefaultGasConfig(), Block: testBlockContext(100),
		ResolveResultScript: evmTestResultScriptResolver(t, addr),
		AssetPrecision: func(name string) (int, bool) { return 0, name == token || name == DefaultGasConfig().GasAssetName },
	})
	result := backend.Runtime.Call(CallRequest{
		CallerAddress: "owner", TargetAddress: addr.MustEncode(), CallID: "liquidation",
		Input: evmCloseHookCalldata(), Gas: contractcommon.InvokeBaseGas, Block: testBlockContext(100),
	})
	require.NoError(t, result.Err, "status=%d return=%x", result.Status, result.ReturnData)
	require.True(t, evmCloseHookSucceeded(result))
	require.Len(t, backend.Runtime.AssetIntents, 1)
	require.Equal(t, "40", backend.Runtime.AssetIntents[0].Amount.String())
}

func TestManagedRuntimeCannotSpendPhysicalAnomaly(t *testing.T) {
	addr := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(addr), callAssetPrecompileCode())
	seedEVMManagedFixture(t, runtime, addr, 10, nil)
	backend := NewBackend(BlockExecutionRequest{
		Runtime: runtime, GasConfig: DefaultGasConfig(), Block: testBlockContext(1),
		ContractUTXOs: func(ContractAddress) ([]UTXO, error) {
			return []UTXO{mustUTXO(t, OutPoint{TxID: chainhash.Hash{7}.String()}, addr, SatoshiAssetName, 1000, 0)}, nil
		},
		ResolveResultScript: evmTestResultScriptResolver(t, addr),
		AssetPrecision: func(string) (int, bool) { return 0, true },
	})
	result := backend.Runtime.Call(CallRequest{
		CallerAddress: "owner", TargetAddress: addr.MustEncode(), CallID: "overspend",
		Input: EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "11", nil),
		Gas: contractcommon.InvokeBaseGas, Block: testBlockContext(1),
	})
	require.Error(t, result.Err)
	require.NotEqual(t, ResultStatusSuccess, result.Status)
	require.Empty(t, backend.Runtime.AssetIntents)
	balance, ok := backend.ManagedBalance(addr)
	require.True(t, ok)
	require.Equal(t, int64(10), balance.Value)
}

func TestManagedTriggerBudgetIgnoresPhysicalAnomaly(t *testing.T) {
	addr := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(addr), []byte{0})
	gas := DefaultGasConfig().GasAssetName
	seedEVMManagedFixture(t, runtime, addr, 0, map[string]string{gas: "1"})
	backend := NewBackend(BlockExecutionRequest{
		Runtime: runtime, GasConfig: DefaultGasConfig(), Block: testBlockContext(1),
		ContractUTXOs: func(ContractAddress) ([]UTXO, error) {
			return []UTXO{mustUTXO(t, OutPoint{TxID: chainhash.Hash{8}.String()}, addr, gas, 1000000, 0)}, nil
		},
	})
	_, ready, err := backend.triggerGasBudget(addr, contractcommon.InvokeBaseGas)
	require.NoError(t, err)
	require.False(t, ready)
	_, ok := any(backend.Runtime.State).(contractframework.ManagedState)
	require.True(t, ok)
}
