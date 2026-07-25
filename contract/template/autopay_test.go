package template

import (
	"fmt"
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

func TestAutopayPaysDelegatedFeesFromNextBlock(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:test", "10")
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
	require.Equal(t, AutopayStatusActive, state.AutopayData().AutopayStatus)
	require.Equal(t, int64(101), state.AutopayData().NextPayHeight)

	plan, err = runtime.SettleBlockWithGasConfig(101, gasConfig)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "recipient-address", plan.Transfers[0].To)
	require.Equal(t, "ordx:f:test", plan.Transfers[0].AssetName)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, gasFee.String(), plan.GasFee.String())
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "10", state.AutopayData().FeeBalance)
	requireDecimalString(t, "99.998", state.AutopayData().GasBalance)
	require.Equal(t, int64(102), state.AutopayData().NextPayHeight)
	require.Equal(t, int64(1), state.AutopayData().PaidBlockCount)

	plan, err = runtime.SettleBlockWithGasConfig(102, gasConfig)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, AutopayStatusFunding, state.AutopayData().AutopayStatus)
	requireDecimalString(t, "0", state.AutopayData().FeeBalance)
	require.Equal(t, int64(2), state.AutopayData().PaidBlockCount)
}

func TestAutopayDefaultFundingAndCloseReturnsBalances(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:test", "10")
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
	requireInvokeItemOrderType(t, OrderTypeFund, item)

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
	require.Len(t, plan.Transfers, 3)
	require.Equal(t, "deployer-address", plan.Transfers[0].To)
	require.Equal(t, "ordx:f:test", plan.Transfers[0].AssetName)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "funder-address", plan.Transfers[1].To)
	require.Equal(t, "ordx:f:test", plan.Transfers[1].AssetName)
	require.Equal(t, "15", plan.Transfers[1].AssetAmt)
	require.Equal(t, gasConfig.GasAssetName, plan.Transfers[2].AssetName)
	require.Equal(t, "0.998", plan.Transfers[2].AssetAmt)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AutopayData().Closed)
	require.Equal(t, AutopayStatusClosed, state.AutopayData().AutopayStatus)
}

func TestAutopayEmptyRecipientPaysMinerFee(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "", "ordx:f:test", "10")
	contractAddr := runtime.Address()
	err := runtime.ApplyFunding(
		testContractOutput("fund", 0, contractAddr, 0, testAssets("ordx:f:test", 20, gasConfig.GasAssetName, 100)),
		gasConfig.GasAssetName,
	)
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(100, gasConfig)
	require.NoError(t, err)

	plan, err := runtime.SettleBlockWithGasConfig(101, gasConfig)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.True(t, plan.Transfers[0].AsFee)
	require.Empty(t, plan.Transfers[0].To)
	require.Equal(t, AutopayReasonMinerFee, plan.Transfers[0].Reason)
	require.Equal(t, "ordx:f:test", plan.Transfers[0].AssetName)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)

	resultPlans, err := BuildSettlementResultPlans([]*SettlementPlan{plan}, nil, nil)
	require.NoError(t, err)
	require.Len(t, resultPlans, 1)
	require.Empty(t, resultPlans[0].Outputs)

	provider := func(contract ContractAddress) ([]UTXO, error) {
		require.True(t, contract.Equal(contractAddr))
		return []UTXO{
			testContractUTXO("fund", 0, contractAddr, 0, testAssets("ordx:f:test", 20, gasConfig.GasAssetName, 100)),
		}, nil
	}
	augmented, err := AugmentResultPlans(resultPlans, runtimeStoreWith(runtime), gasConfig,
		contractframework.ContractUTXOProvider(provider), nil)
	require.NoError(t, err)
	require.Len(t, augmented, 1)
	requireResultPlanAssetTo(t, augmented[0], contractAddr.MustEncode(), "ordx:f:test", "10")
	requireNoResultPlanOutputTo(t, augmented[0], "")
	requireNoResultPlanOutputTo(t, augmented[0], "recipient-address")
}

func TestAutopayCloseBatchRequiresGasBeforeRefunding(t *testing.T) {
	contract := NewAutopayContract("dkvs", "recipient", "ordx:f:test", "1")
	state := &TemplateRuntimeState{}
	state.AutopayData().AutopayCloseStarted = true
	state.AutopayData().GasBalance = parseDecimalOrZero("0")
	_, err := contract.closeBatchGasFee(state, &InvokeItem{}, testAutopayGasConfig(), 100)
	require.ErrorContains(t, err, "insufficient autopay gas")
}

func TestAutopayCloseFitsResultOutputLimit(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient", "ordx:f:test", "1")
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate,
		AutopayMaxCloseDelegateOutputs)
	for i := 0; i < AutopayMaxCloseDelegateOutputs; i++ {
		state.AutopayData().AutopayDelegates[fmt.Sprintf("delegate-%04d", i)] = AutopayDelegate{
			AmountPerBlock: parseDecimalOrZero("1"),
			Balance:        parseDecimalOrZero("1"),
			Status:         AutopayStatusActive,
		}
	}
	state.AutopayData().FeeBalance = parseDecimalOrZero(fmt.Sprintf("%d", AutopayMaxCloseDelegateOutputs))
	state.AutopayData().GasBalance = parseDecimalOrZero("100")
	state.AutopayData().AutopayStatus = AutopayStatusActive
	state.Items = []InvokeItem{{
		ID:      1,
		Action:  InvokeAPIClose,
		Address: "deployer-address",
		Height:  100,
	}}
	require.NoError(t, runtime.saveRuntimeState(state))

	plan, err := runtime.SettleBlockWithGasConfig(100, gasConfig)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, AutopayMaxCloseDelegateOutputs+1)

	plans, err := BuildSettlementResultPlans([]*SettlementPlan{plan}, nil, nil)
	require.NoError(t, err)
	contractAddr := runtime.Address()
	provider := contractframework.ContractUTXOProvider(func(contract ContractAddress) ([]contractframework.UTXO, error) {
		require.True(t, contractAddr.Equal(contract))
		assets := testAssets("ordx:f:test", int64(AutopayMaxCloseDelegateOutputs),
			gasConfig.GasAssetName, 100)
		assets = append(assets, testAsset("ordx:f:extra", 10)...)
		return []contractframework.UTXO{testContractUTXO(
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 0, contractAddr, 0,
			assets)}, nil
	})
	plans, err = AugmentResultPlans(plans, runtimeStoreWith(runtime), gasConfig, provider,
		func(string) (int, bool) { return 0, true })
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Len(t, plans[0].Outputs, contractframework.MaxContractResultOutputs)

	_, err = contractframework.BuildResultTx(contractframework.ResultTxBuildRequest{
		Status:        ResultStatusSuccess,
		ResultCount:   1,
		Plans:         plans,
		ResolveScript: func(contractframework.ResultOutput) ([]byte, error) { return []byte{0x51}, nil },
	}, contractframework.ResultTxBuildOptions{})
	require.NoError(t, err)
}

func TestAutopayRejectsFractionalSatsConfig(t *testing.T) {
	contract := NewAutopayContract("service", "recipient", SatoshiAssetName, "1")
	param, err := (&AutopayConfigInvokeParam{AmountPerBlock: "1.5"}).Encode()
	require.NoError(t, err)
	require.ErrorContains(t, contract.CheckInvoke(InvokeAPIConfig, param), "must be an integer")

	param, err = (&AutopayConfigInvokeParam{AmountPerBlock: "2"}).Encode()
	require.NoError(t, err)
	require.NoError(t, contract.CheckInvoke(InvokeAPIConfig, param))
}

func TestAutopayConfigRequiresExactAssetPrecision(t *testing.T) {
	contract := NewAutopayContract("service", "recipient", "brc20:f:test", "1")
	param, err := (&AutopayConfigInvokeParam{AmountPerBlock: "1.234"}).Encode()
	require.NoError(t, err)
	resolve := func(name string) (int, bool) { return 2, name == "brc20:f:test" }
	require.ErrorContains(t, contract.CheckInvokePrecision(InvokeAPIConfig, param, resolve), "not exactly representable")

	param, err = (&AutopayConfigInvokeParam{AmountPerBlock: "1.23"}).Encode()
	require.NoError(t, err)
	require.NoError(t, contract.CheckInvokePrecision(InvokeAPIConfig, param, resolve))
	require.ErrorContains(t, contract.CheckInvokePrecision(InvokeAPIConfig, param, nil), "missing")
}

func TestAutopayTransferRejectsFractionalSats(t *testing.T) {
	amount, err := contractframework.ParseDecimalAmountString("0.9")
	require.NoError(t, err)
	_, err = autopayTransfer("recipient", SatoshiAssetName, amount)
	require.Error(t, err)
}

func TestAutopayDelegateLimit(t *testing.T) {
	contract := NewAutopayContract("service", "recipient", "ordx:f:test", "1")
	state := &TemplateRuntimeState{}
	state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate, AutopayMaxDelegates)
	for i := 0; i < AutopayMaxDelegates; i++ {
		state.AutopayData().AutopayDelegates[fmt.Sprintf("delegate-%d", i)] = AutopayDelegate{}
	}
	require.ErrorContains(t, checkAutopayDelegateCapacity(contract, state, "new-delegate"), "limit exceeded")
	require.NoError(t, checkAutopayDelegateCapacity(contract, state, "delegate-1"))
}

func testAutopayRuntime(t *testing.T, recipient, feeAsset, minAmount string) *ContractRuntime {
	t.Helper()
	contract := NewAutopayContract("dkvs", recipient, feeAsset, minAmount)
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

func runtimeStoreWith(runtime *ContractRuntime) *RuntimeStore {
	store := NewRuntimeStore()
	store.Add(runtime)
	return store
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
