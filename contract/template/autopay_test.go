package template

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

func TestAutopayPaysFiniteRangeFromNextBlock(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:test", AutopayScheduleFixed, "10", "", 102)
	contractAddr := runtime.Address()
	gasFee, err := gasConfig.ContractFundingFee(ExecutionKindTrigger, gasConfig.TriggerBaseGas, true, 101)
	require.NoError(t, err)

	err = runtime.ApplyFunding(
		testContractOutput("fund", 0, contractAddr, 0, testAssets("ordx:f:test", 20, gasConfig.GasAssetName, 100)),
		gasConfig.GasAssetName,
	)
	require.NoError(t, err)

	plan, err := runtime.SettleBlockWithGasConfig(100, gasConfig)
	require.NoError(t, err)
	require.Empty(t, plan.Transfers)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, AutopayStatusActive, state.Running.AutopayStatus)
	require.Equal(t, int64(101), state.Running.NextPayHeight)

	plan, err = runtime.SettleBlockWithGasConfig(101, gasConfig)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "recipient-address", plan.Transfers[0].To)
	require.Equal(t, "ordx:f:test", plan.Transfers[0].AssetName)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, gasFee.String(), plan.GasFee.String())
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "10", state.Running.FeeBalance)
	requireDecimalString(t, "99.998", state.Running.GasBalance)
	require.Equal(t, int64(102), state.Running.NextPayHeight)
	require.Equal(t, int64(1), state.Running.PaidBlockCount)

	plan, err = runtime.SettleBlockWithGasConfig(102, gasConfig)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, AutopayStatusExpired, state.Running.AutopayStatus)
	requireDecimalString(t, "0", state.Running.FeeBalance)
	require.Equal(t, int64(2), state.Running.PaidBlockCount)
}

func TestAutopayDefaultFundingAndCloseReturnsBalances(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:test", AutopayScheduleFixed, "10", "", 0)
	contractAddr := runtime.Address()
	err := runtime.ApplyFunding(testContractOutput("fund", 0, contractAddr, 0, testAssets("ordx:f:test", 10, gasConfig.GasAssetName, 1)),
		gasConfig.GasAssetName)
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(100, gasConfig)
	require.NoError(t, err)

	item, err := runtime.ApplyDefaultInvoke(ApplyInvokeRequest{
		Action:        contractcommon.ContractInvokeAPIDefault,
		CallID:        DeriveInvokeCallID("invoke", 0, contractAddr),
		Invoker:       "funder-address",
		FundingOutput: testContractOutput("invoke", 0, contractAddr, 0, testAsset("ordx:f:test", 15)),
		Height:        101,
		Timestamp:     101,
	})
	require.NoError(t, err)
	require.NotNil(t, item)
	require.Equal(t, OrderTypeFund, item.OrderType)

	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIClose,
		CallID:        DeriveInvokeCallID("close", 0, contractAddr),
		Invoker:       "deployer-address",
		FundingOutput: testContractOutput("close", 0, contractAddr, 0, nil),
		Height:        101,
		Timestamp:     101,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlockWithGasConfig(101, gasConfig)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "deployer-address", plan.Transfers[0].To)
	require.Equal(t, "ordx:f:test", plan.Transfers[0].AssetName)
	require.Equal(t, "25", plan.Transfers[0].AssetAmt)
	require.Equal(t, gasConfig.GasAssetName, plan.Transfers[1].AssetName)
	require.Equal(t, "1", plan.Transfers[1].AssetAmt)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.Closed)
	require.Equal(t, AutopayStatusClosed, state.Running.AutopayStatus)
}

func testAutopayRuntime(t *testing.T, recipient, feeAsset, mode, base, step string, endHeight int64) *ContractRuntime {
	t.Helper()
	contract := NewAutopayContract(recipient, feeAsset, mode, base, step, endHeight)
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateAutopay,
		Version:         CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(contractcommon.TestnetContractPrefix, deploy.ContractContent, "deployer-address", deploy.DeployNonce)
	require.NoError(t, err)
	runtime, err := NewRuntimeWithDeployer(addr, deploy, NewDefaultRegistry(), "deployer-address")
	require.NoError(t, err)
	return runtime
}

func testAutopayGasConfig() GasConfig {
	return GasConfig{
		GasAssetName:    "ordx:f:gas",
		DeployBaseGas:   1,
		InvokeBaseGas:   1,
		ResultBaseGas:   1,
		TriggerBaseGas:  1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}
}
