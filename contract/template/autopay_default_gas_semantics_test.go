package template

import (
	"fmt"
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutopayDefaultInvokeCreditsOperatingGasForSameAsset(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
	seedAutopayDefaultGasState(t, runtime, "7.25", "13.5")
	expectedGas := parseDecimalOrZero("7.25")

	for _, actor := range []string{"ordinary-user", "deployer-address"} {
		before, err := runtime.RuntimeState()
		require.NoError(t, err)
		item, err := runtime.ApplyDefaultInvoke(autopayDefaultGasRequest(
			runtime, actor, gas.GasAssetName, actor, testAsset(gas.GasAssetName, 5000),
		))
		require.NoError(t, err)
		require.NotNil(t, item)
		require.Equal(t, OrderTypeFund, item.OrderType)
		require.Equal(t, gas.GasAssetName, item.AssetName)
		requireDecimalString(t, "5000", item.InAmt)

		expectedGas = expectedGas.AddAlignPrecision(parseDecimalOrZero("5000"))
		after, err := runtime.RuntimeState()
		require.NoError(t, err)
		assert.Equal(t, expectedGas.String(), decimalOrZero(after.AutopayData().GasBalance).String(),
			"default funding must credit operating GAS for both an ordinary user and the deployer")
		assert.Equal(t, decimalOrZero(before.AutopayData().FeeBalance).String(),
			decimalOrZero(after.AutopayData().FeeBalance).String(), "default funding must not change delegate fee balance")
		assert.Equal(t, before.AutopayData().AutopayDelegates, after.AutopayData().AutopayDelegates,
			"default funding must not create or change a delegate")
	}
}

func TestAutopayDefaultInvokeCreditsGasWhenFeeAssetDiffers(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "10")
	seedAutopayDefaultGasState(t, runtime, "7.25", "13.5")
	assets := testAsset(gas.GasAssetName, 0)
	assets[0].Amount = *parseDecimalOrZero("50.25")
	request := autopayDefaultGasRequest(runtime, "ordinary-user", gas.GasAssetName,
		"different-fee-gas", assets)
	request.ResultGasFee = parseDecimalOrZero("0.25")
	request.AssetPrecision = func(name string) (int, bool) { return 2, name == gas.GasAssetName }

	item, err := runtime.ApplyDefaultInvoke(request)
	require.NoError(t, err)
	require.NotNil(t, item, "GAS-only default funding must create an item when fee asset differs")
	require.Equal(t, OrderTypeFund, item.OrderType)
	require.Equal(t, gas.GasAssetName, item.AssetName)
	requireDecimalString(t, "50.25", item.InAmt)

	after, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "57.5", after.AutopayData().GasBalance)
	requireDecimalString(t, "13.5", after.AutopayData().FeeBalance)
	require.Equal(t, defaultGasSeedDelegates(), after.AutopayData().AutopayDelegates)
}

func TestAutopayDefaultInvokeDoesNotUseDelegateCapacity(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "10")
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().GasBalance = parseDecimalOrZero("7.25")
	state.AutopayData().FeeBalance = parseDecimalOrZero("13.5")
	state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate, AutopayMaxDelegates)
	for i := 0; i < AutopayMaxDelegates; i++ {
		state.AutopayData().AutopayDelegates[fmt.Sprintf("existing-%05d", i)] = AutopayDelegate{
			AmountPerBlock: parseDecimalOrZero("1"),
			Balance:        parseDecimalOrZero("1"),
			Status:         AutopayStatusActive,
		}
	}
	require.NoError(t, runtime.saveRuntimeState(state))
	beforeDelegates := state.AutopayData().AutopayDelegates

	item, err := runtime.ApplyDefaultInvoke(autopayDefaultGasRequest(
		runtime, "new-gas-funder", gas.GasAssetName, "full-delegate-catalog", testAsset(gas.GasAssetName, 25),
	))
	require.NoError(t, err)
	require.NotNil(t, item)

	after, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "32.25", after.AutopayData().GasBalance)
	require.Equal(t, beforeDelegates, after.AutopayData().AutopayDelegates)
}

func TestBackendAutopayDefaultGasFundingIgnoresFullDelegateCatalog(t *testing.T) {
	gas := testAutopayGasConfig()
	gas.ResultBaseGas = 50000
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "10")
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().GasBalance = parseDecimalOrZero("7.25")
	state.AutopayData().FeeBalance = parseDecimalOrZero("13.5")
	state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate, AutopayMaxDelegates)
	for i := 0; i < AutopayMaxDelegates; i++ {
		state.AutopayData().AutopayDelegates[fmt.Sprintf("existing-%05d", i)] = AutopayDelegate{
			AmountPerBlock: parseDecimalOrZero("1"),
			Balance:        parseDecimalOrZero("1"),
			Status:         AutopayStatusActive,
		}
	}
	beforeDelegates := state.AutopayData().AutopayDelegates
	require.NoError(t, runtime.saveRuntimeState(state))
	store := runtimeStoreWith(runtime)
	resultFee, err := gas.ResultFee(100)
	require.NoError(t, err)
	netGas := parseDecimalOrZero("25.25")
	grossGas := netGas.AddAlignPrecision(resultFee)
	assets := testAsset(gas.GasAssetName, 0)
	assets[0].Amount = *grossGas
	tx := testTemplateDefaultInvokeTx(t, runtime.Address(), 0, assets)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "ordinary-user", nil },
		AssetPrecision: func(name string) (int, bool) { return 2, name == gas.GasAssetName },
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.Len(t, result.Records[0].ItemIDs, 1)
	committedRuntime, ok := store.Get(runtime.Address())
	require.True(t, ok)
	after, err := committedRuntime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "32.5", after.AutopayData().GasBalance)
	require.Equal(t, beforeDelegates, after.AutopayData().AutopayDelegates)
}

func TestAutopayDefaultInvokeRejectsNonGasFundingWithoutStateChange(t *testing.T) {
	gas := testAutopayGasConfig()
	fee := "ordx:f:fee"
	foreign := "ordx:f:foreign"
	tests := []struct {
		name     string
		feeAsset string
		gasName  string
		assets   wire.TxAssets
		value    int64
	}{
		{name: "fee-only-with-different-gas", feeAsset: fee, gasName: gas.GasAssetName, assets: testAsset(fee, 5)},
		{name: "gas-and-fee-mixed", feeAsset: fee, gasName: gas.GasAssetName, assets: testAssets(gas.GasAssetName, 50, fee, 5)},
		{name: "gas-and-unrelated-mixed", feeAsset: fee, gasName: gas.GasAssetName, assets: testAssets(gas.GasAssetName, 50, foreign, 5)},
		{name: "fee-asset-equals-gas-with-unrelated", feeAsset: gas.GasAssetName, gasName: gas.GasAssetName, assets: testAssets(gas.GasAssetName, 50, foreign, 5)},
		{name: "plain-value-without-gas", feeAsset: fee, gasName: gas.GasAssetName, value: 1000},
		{name: "missing-gas-asset-name", feeAsset: fee, assets: testAsset(gas.GasAssetName, 50)},
		{name: "zero-gas", feeAsset: fee, gasName: gas.GasAssetName, assets: testAsset(gas.GasAssetName, 0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtime := testAutopayRuntime(t, "recipient-address", tc.feeAsset, "10")
			seedAutopayDefaultGasState(t, runtime, "7.25", "13.5")
			before, err := runtime.RuntimeState()
			require.NoError(t, err)
			request := autopayDefaultGasRequest(runtime, "ordinary-user", tc.gasName, tc.name, tc.assets)
			request.FundingOutput = testContractOutput(tc.name, 0, runtime.Address(), tc.value, tc.assets)

			item, err := runtime.ApplyDefaultInvoke(request)
			require.Error(t, err)
			require.Nil(t, item)
			after, err := runtime.RuntimeState()
			require.NoError(t, err)
			require.Equal(t, before, after, "rejected default funding must leave all AUTOPAY state unchanged")
		})
	}
}

func TestBackendAutopayDefaultGasFundingUsesNetGasOnce(t *testing.T) {
	gas := testAutopayGasConfig()
	gas.ResultBaseGas = 50000
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "10")
	seedAutopayDefaultGasState(t, runtime, "7.25", "13.5")
	store := runtimeStoreWith(runtime)
	resultFee, err := gas.ResultFee(100)
	require.NoError(t, err)
	netGas := parseDecimalOrZero("50.25")
	grossGas := netGas.AddAlignPrecision(resultFee)
	assets := testAsset(gas.GasAssetName, 0)
	assets[0].Amount = *grossGas
	tx := testTemplateDefaultInvokeTx(t, runtime.Address(), 0, assets)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "ordinary-user", nil },
		AssetPrecision: func(name string) (int, bool) { return 2, name == gas.GasAssetName },
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.Len(t, result.Records[0].ItemIDs, 1)
	require.Len(t, result.ResultPlans, 1)
	requireDecimalString(t, resultFee.String(), result.Records[0].GasFee)
	requireDecimalString(t, resultFee.String(), result.ResultPlans[0].GasFee)
	requireDecimalString(t, grossGas.String(), netGas.AddAlignPrecision(result.ResultPlans[0].GasFee))

	committedRuntime, ok := store.Get(runtime.Address())
	require.True(t, ok)
	after, err := committedRuntime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, "57.5", decimalOrZero(after.AutopayData().GasBalance).String(),
		"the Result fee must be deducted once, then only net GAS credited once")
	requireDecimalString(t, "13.5", after.AutopayData().FeeBalance)
	require.Equal(t, defaultGasSeedDelegates(), after.AutopayData().AutopayDelegates)
}

func TestBackendAutopayDefaultInvokeRejectsGasConsumedByResultFee(t *testing.T) {
	gas := testAutopayGasConfig()
	gas.ResultBaseGas = 50000
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "10")
	seedAutopayDefaultGasState(t, runtime, "7.25", "13.5")
	before, err := runtime.RuntimeState()
	require.NoError(t, err)
	store := runtimeStoreWith(runtime)
	resultFee, err := gas.ResultFee(100)
	require.NoError(t, err)
	assets := testAsset(gas.GasAssetName, 0)
	assets[0].Amount = *resultFee.Clone()
	tx := testTemplateDefaultInvokeTx(t, runtime.Address(), 0, assets)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "ordinary-user", nil },
		AssetPrecision: func(name string) (int, bool) { return 2, name == gas.GasAssetName },
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
	require.True(t, result.Records[0].RequiresResult)
	require.Len(t, result.ResultPlans, 1)
	requireDecimalString(t, resultFee.String(), result.Records[0].GasFee)
	requireDecimalString(t, resultFee.String(), result.ResultPlans[0].GasFee)

	committedRuntime, ok := store.Get(runtime.Address())
	require.True(t, ok)
	after, err := committedRuntime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, decimalString(before.AutopayData().GasBalance), after.AutopayData().GasBalance)
	requireDecimalString(t, decimalString(before.AutopayData().FeeBalance), after.AutopayData().FeeBalance)
	require.Equal(t, before.AutopayData().AutopayDelegates, after.AutopayData().AutopayDelegates)
	require.Empty(t, after.Items)
}

func TestBackendAutopayDefaultGasRejectsFeeAssetWithoutMutation(t *testing.T) {
	gas := testAutopayGasConfig()
	gas.ResultBaseGas = 50000
	feeAsset := "ordx:f:fee"
	runtime := testAutopayRuntime(t, "recipient-address", feeAsset, "10")
	seedAutopayDefaultGasState(t, runtime, "7.25", "13.5")
	before, err := runtime.RuntimeState()
	require.NoError(t, err)
	store := runtimeStoreWith(runtime)
	resultFee, err := gas.ResultFee(100)
	require.NoError(t, err)
	assets := testAsset(feeAsset, 10)
	gasAssets := testAsset(gas.GasAssetName, 0)
	gasAssets[0].Amount = *resultFee.Clone()
	require.NoError(t, assets.Merge(gasAssets))
	tx := testTemplateDefaultInvokeTx(t, runtime.Address(), 0, assets)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "ordinary-user", nil },
		AssetPrecision: func(name string) (int, bool) {
			return 2, name == gas.GasAssetName || name == feeAsset
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
	require.True(t, result.Records[0].RequiresResult)
	requireDecimalString(t, resultFee.String(), result.Records[0].GasFee)
	require.Len(t, result.ResultPlans, 1)
	requireDecimalString(t, resultFee.String(), result.ResultPlans[0].GasFee)
	require.Len(t, result.ResultPlans[0].Outputs, 1)
	refund := result.ResultPlans[0].Outputs[0]
	require.Equal(t, "ordinary-user", refund.To)
	require.Len(t, refund.Assets, 1)
	require.Equal(t, feeAsset, refund.Assets[0].Name.String())
	require.Equal(t, "10", refund.Assets[0].Amount.String())

	committedRuntime, ok := store.Get(runtime.Address())
	require.True(t, ok)
	after, err := committedRuntime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, decimalString(before.AutopayData().GasBalance), after.AutopayData().GasBalance)
	requireDecimalString(t, decimalString(before.AutopayData().FeeBalance), after.AutopayData().FeeBalance)
	require.Equal(t, before.AutopayData().AutopayDelegates, after.AutopayData().AutopayDelegates,
		"a mixed default funding output must refund its fee asset without touching delegates")
	require.Empty(t, after.Items)
}

func autopayDefaultGasRequest(runtime *ContractRuntime, actor, gasAssetName, callID string, assets wire.TxAssets) ApplyInvokeRequest {
	return ApplyInvokeRequest{
		Action:        contractcommon.ContractInvokeAPIDefault,
		CallID:        callID,
		Invoker:       actor,
		GasAssetName:  gasAssetName,
		FundingOutput: testContractOutput("default-gas-"+callID, 0, runtime.Address(), 0, assets),
		Height:        100,
		Timestamp:     100,
	}
}

func applyAutopayDelegateConfigForTest(t *testing.T, runtime *ContractRuntime, actor, callID string,
	height int64, amountPerBlock, gasAssetName string, funding ContractOutput) *InvokeItem {
	t.Helper()
	param, err := (&AutopayConfigInvokeParam{AmountPerBlock: amountPerBlock}).Encode()
	require.NoError(t, err)
	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPIConfig, Param: param, CallID: callID, Invoker: actor,
		GasAssetName: gasAssetName, Height: height, Timestamp: height,
		AssetPrecision: func(string) (int, bool) { return 8, true },
		FundingOutput:  funding,
	})
	require.NoError(t, err)
	require.NotNil(t, item)
	return item
}

func seedAutopayDefaultGasState(t *testing.T, runtime *ContractRuntime, gasBalance, feeBalance string) {
	t.Helper()
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().GasBalance = parseDecimalOrZero(gasBalance)
	state.AutopayData().FeeBalance = parseDecimalOrZero(feeBalance)
	state.AutopayData().AutopayDelegates = defaultGasSeedDelegates()
	require.NoError(t, runtime.saveRuntimeState(state))
}

func defaultGasSeedDelegates() map[string]AutopayDelegate {
	return map[string]AutopayDelegate{
		"existing-delegate": {
			AmountPerBlock: parseDecimalOrZero("1.25"),
			BlobKeyLimit:   3,
			Balance:        parseDecimalOrZero("13.5"),
			TotalPaid:      parseDecimalOrZero("2.5"),
			PaidBlockCount: 2,
			Status:         AutopayStatusActive,
		},
	}
}
