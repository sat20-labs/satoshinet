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

func TestAutopaySameAssetOperatingGasIsolation(t *testing.T) {
	gas := testAutopayGasConfig()
	gas.TriggerBaseGas, gas.ResultBaseGas = 150000, 50000
	runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
	for _, address := range []string{"a-user", "z-user"} {
		_, err := runtime.ApplyDefaultInvoke(ApplyInvokeRequest{
			Invoker: address, Height: 100,
			FundingOutput: testContractOutput(address, 0, runtime.Address(), 0, testAsset(gas.GasAssetName, 10000)),
		})
		require.NoError(t, err)
	}
	param, err := (&AutopayConfigInvokeParam{GasFundingAmount: "400"}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPIConfig, Param: param, Invoker: "deployer-address", GasAssetName: gas.GasAssetName,
		AssetPrecision: func(string) (int, bool) { return 0, true },
		FundingOutput:  testContractOutput("gas", 0, runtime.Address(), 0, testAsset(gas.GasAssetName, 400)),
	})
	require.NoError(t, err)
	_, err = runtime.SettleBlockWithGasConfig(100, gas)
	require.NoError(t, err)
	for height := int64(101); height <= 102; height++ {
		plan, err := runtime.SettleBlockWithGasConfig(height, gas)
		require.NoError(t, err)
		requireDecimalString(t, "200", plan.GasFee)
	}
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.AutopayData().AutopayDelegates, 2)
	requireDecimalString(t, "0", state.AutopayData().GasBalance)
	for _, delegate := range state.AutopayData().AutopayDelegates {
		requireDecimalString(t, "9980", delegate.Balance)
		requireDecimalString(t, "20", delegate.TotalPaid)
	}
	require.Equal(t, int64(2), state.AutopayData().PaidBlockCount)
	require.Equal(t, AutopayStatusFunding, state.AutopayData().AutopayStatus)
	before := state.AutopayData()
	plan, err := runtime.SettleBlockWithGasConfig(103, gas)
	require.NoError(t, err)
	require.Empty(t, plan.Transfers)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, before, state.AutopayData())
}

func TestAutopayGasOnlyFundingPreservesFullDelegateCatalog(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	contract := runtime.Contract().(*AutopayContract)
	for i := 0; i < AutopayMaxDelegates; i++ {
		contract.setDelegateConfig(state.AutopayData(), fmt.Sprintf("user-%05d", i), parseDecimalOrZero("10"), 2)
	}
	require.NoError(t, runtime.saveRuntimeState(state))
	before := state.AutopayData().AutopayDelegates
	param, err := (&AutopayConfigInvokeParam{GasFundingAmount: "400"}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPIConfig, Param: param, Invoker: "deployer-address", GasAssetName: gas.GasAssetName,
		AssetPrecision: func(string) (int, bool) { return 0, true },
		FundingOutput:  testContractOutput("gas", 0, runtime.Address(), 0, testAsset(gas.GasAssetName, 400)),
	})
	require.NoError(t, err)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, before, state.AutopayData().AutopayDelegates)
	requireDecimalString(t, "400", state.AutopayData().GasBalance)
}

func TestAutopayInvalidRefundSkipsHistoricalItems(t *testing.T) {
	gas := testAutopayGasConfig()
	gas.TriggerBaseGas, gas.ResultBaseGas = 150000, 50000
	runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
	for _, height := range []int64{99, 100} {
		_, err := runtime.ApplyInvalidInvoke(ApplyInvokeRequest{
			Action: InvokeAPIConfig, Invoker: fmt.Sprintf("caller-%d", height), Height: height,
			FundingOutput: testContractOutput(fmt.Sprintf("%064x", height), 0, runtime.Address(), 0, testAsset(gas.GasAssetName, 450)),
			ResultGasFee:  parseDecimalOrZero("50"),
		}, gas.GasAssetName)
		require.NoError(t, err)
	}
	plan, err := runtime.SettleBlockWithGasConfig(100, gas)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "caller-100", plan.Transfers[0].To)
	require.Equal(t, "400", plan.Transfers[0].AssetAmt)
	require.Equal(t, []int64{1}, plan.ItemIDs)
	require.Equal(t, []OutPoint{{TxID: fmt.Sprintf("%064x", 100), Vout: 0}}, plan.Inputs)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusInit, state.Items[0].Done)
	require.Equal(t, ItemStatusRefunded, state.Items[1].Done)
	requireDecimalString(t, "0", state.AutopayData().GasBalance)
	plan, err = runtime.SettleBlockWithGasConfig(101, gas)
	require.NoError(t, err)
	require.Empty(t, plan.Transfers)
	require.Empty(t, plan.Inputs)
}

func TestAutopayGasFundingRequiresExactAssetPrecision(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
	for _, amount := range []string{"", "10"} {
		param, err := (&AutopayConfigInvokeParam{AmountPerBlock: amount, GasFundingAmount: "400.5"}).Encode()
		require.NoError(t, err)
		assets := testAsset(gas.GasAssetName, 0)
		assets[0].Amount = *parseDecimalOrZero("400.5")
		if amount != "" {
			assets[0].Amount = *parseDecimalOrZero("500.5")
		}
		req := ApplyInvokeRequest{
			Action: InvokeAPIConfig, Param: param, Invoker: "deployer-address", GasAssetName: gas.GasAssetName,
			FundingOutput: testContractOutput("precision", 0, runtime.Address(), 0, assets),
		}
		_, err = runtime.splitAutopayGasFunding(req)
		require.ErrorContains(t, err, "precision resolver")
		req.AssetPrecision = func(string) (int, bool) { return 0, false }
		_, err = runtime.splitAutopayGasFunding(req)
		require.ErrorContains(t, err, "unknown autopay gas asset precision")
		req.AssetPrecision = func(string) (int, bool) { return 0, true }
		_, err = runtime.splitAutopayGasFunding(req)
		require.ErrorContains(t, err, "not exactly representable")
		req.AssetPrecision = func(string) (int, bool) { return 64, true }
		_, err = runtime.splitAutopayGasFunding(req)
		require.ErrorContains(t, err, "unknown autopay gas asset precision")
		req.AssetPrecision = func(string) (int, bool) { return 1, true }
		remainder, err := runtime.splitAutopayGasFunding(req)
		require.NoError(t, err)
		remaining, err := remainder.AssetAmount(gas.GasAssetName)
		require.NoError(t, err)
		if amount == "" {
			requireDecimalString(t, "0", remaining)
		} else {
			requireDecimalString(t, "100", remaining)
		}
		// Native satoshi gas must remain integral even if a resolver claims otherwise.
		req.GasAssetName = SatoshiAssetName
		req.FundingOutput = testContractOutput("native", 0, runtime.Address(), 1000, nil)
		_, err = runtime.splitAutopayGasFunding(req)
		require.ErrorContains(t, err, "not exactly representable")
	}
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
	funding := testContractOutput("fund", 0, contractAddr, 0, testAssets("ordx:f:test", 20, gasConfig.GasAssetName, 100))
	err := runtime.ApplyFunding(funding, gasConfig.GasAssetName)
	require.NoError(t, err)
	creditTestManagedOutput(t, runtime, funding)
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
		ID:        1,
		OrderType: OrderTypeClose,
		Address:   "deployer-address",
		Height:    100,
	}}
	require.NoError(t, runtime.saveRuntimeState(state))
	creditTestManagedOutput(t, runtime, testContractOutput(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 0, runtime.Address(), 0,
		testAssets("ordx:f:test", int64(AutopayMaxCloseDelegateOutputs), gasConfig.GasAssetName, 100)))

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
		GasAssetName:     "ordx:f:gas",
		BootstrapAddress: "bootstrap-address",
		DeployBaseGas:   1,
		InvokeBaseGas:   1,
		ResultBaseGas:   1,
		TriggerBaseGas:  1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}
}
