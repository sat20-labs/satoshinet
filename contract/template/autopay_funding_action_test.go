package template

import (
	"fmt"
	"testing"

	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const autopayFundingAction = "funding"

func TestAutopayFundingUsesValidatedAmountParser(t *testing.T) {
	for _, action := range []string{InvokeAPIFunding, InvokeAPIConfig} {
		for _, tc := range []struct {
			name, text, value string
			precision         int
		}{
			{"formatted", "50:0", "50", 0},
			{"high-precision", "50.00000000001", "50.00000000001", 11},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				gas := testAutopayGasConfig()
				runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
				seedAutopayFundingState(t, runtime)
				before, err := runtime.RuntimeState()
				require.NoError(t, err)
				amount, err := contractframework.ParseDecimalAmountString(tc.value)
				require.NoError(t, err)
				req := autopayFundingActionRequest(t, runtime, "deployer-address", AutopayConfigInvokeParam{GasFundingAmount: tc.text}, "50", tc.precision, true)
				req.Action = action
				assets := testAsset(gas.GasAssetName, 0)
				assets[0].Amount = *amount
				req.FundingOutput = testContractOutput("validated-parser", 0, runtime.Address(), 0, assets)
				item, err := runtime.ApplyInvoke(req)
				require.NoError(t, err)
				require.Equal(t, InvokeReasonNormal, item.Reason)
				after, err := runtime.RuntimeState()
				require.NoError(t, err)
				requireDecimalString(t, before.AutopayData().GasBalance.AddAlignPrecision(amount).String(), after.AutopayData().GasBalance)
				require.Equal(t, before.AutopayData().FeeBalance, after.AutopayData().FeeBalance)
				require.Equal(t, before.AutopayData().AutopayDelegates, after.AutopayData().AutopayDelegates)
			})
		}
	}
}

func TestBackendAutopayFundingUsesValidatedAmountParser(t *testing.T) {
	for _, action := range []string{InvokeAPIFunding, InvokeAPIConfig} {
		for _, full := range []bool{false, true} {
			for _, tc := range []struct {
				name, text, value string
				precision         int
			}{
				{"formatted", "50:0", "50", 0},
				{"high-precision", "50.00000000001", "50.00000000001", 11},
			} {
				t.Run(fmt.Sprintf("%s/full_%v/%s", action, full, tc.name), func(t *testing.T) {
					gas := testAutopayGasConfig()
					gas.ResultBaseGas = 50000
					runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
					if full {
						state, err := runtime.RuntimeState()
						require.NoError(t, err)
						state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate, AutopayMaxDelegates)
						for n := 0; n < AutopayMaxDelegates; n++ {
							state.AutopayData().AutopayDelegates[fmt.Sprintf("existing-%05d", n)] = AutopayDelegate{}
						}
						require.NoError(t, runtime.saveRuntimeState(state))
					}
					before, err := runtime.RuntimeState()
					require.NoError(t, err)
					amount, err := contractframework.ParseDecimalAmountString(tc.value)
					require.NoError(t, err)
					resultFee, err := gas.ResultFee(100)
					require.NoError(t, err)
					assets := testAsset(gas.GasAssetName, 0)
					assets[0].Amount = *amount.AddAlignPrecision(resultFee)
					param := mustAutopayFundingParam(t, AutopayConfigInvokeParam{GasFundingAmount: tc.text})
					script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: action, Param: param})
					require.NoError(t, err)
					tx := wire.NewMsgTx(1)
					tx.AddTxIn(&wire.TxIn{})
					tx.AddTxOut(wire.NewTxOut(0, nil, script))
					tx.AddTxOut(wire.NewTxOut(0, assets, testTemplateContractScript(runtime.Address())))
					store := runtimeStoreWith(runtime)
					result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
						ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "deployer-address", nil },
						AssetPrecision: func(name string) (int, bool) { return tc.precision, name == gas.GasAssetName },
					})
					require.NoError(t, err)
					require.Len(t, result.Records, 1)
					require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
					committed, ok := store.Get(runtime.Address())
					require.True(t, ok)
					after, err := committed.RuntimeState()
					require.NoError(t, err)
					requireDecimalString(t, tc.value, after.AutopayData().GasBalance)
					requireDecimalString(t, "0", after.AutopayData().FeeBalance)
					require.Equal(t, before.AutopayData().AutopayDelegates, after.AutopayData().AutopayDelegates)
					require.Len(t, result.ResultPlans, 1)
					requireDecimalString(t, resultFee.String(), result.ResultPlans[0].GasFee)
					address := runtime.Address()
					requireResultPlanAssetTo(t, result.ResultPlans[0], address.MustEncode(), gas.GasAssetName, tc.value)
					requireNoResultPlanAssetTo(t, result.ResultPlans[0], "deployer-address", gas.GasAssetName)
				})
			}
		}
	}
}

func seedFullAutopayCloseState(t *testing.T, runtime *ContractRuntime) {
	t.Helper()
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().GasBalance = parseDecimalOrZero("1000")
	state.AutopayData().FeeBalance = parseDecimalOrZero(fmt.Sprint(AutopayMaxDelegates))
	state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate, AutopayMaxDelegates)
	for n := 0; n < AutopayMaxDelegates; n++ {
		state.AutopayData().AutopayDelegates[fmt.Sprintf("existing-%05d", n)] = AutopayDelegate{
			AmountPerBlock: parseDecimalOrZero("1"), Balance: parseDecimalOrZero("1"), Status: AutopayStatusActive,
		}
	}
	require.NoError(t, runtime.saveRuntimeState(state))
}

func TestAutopayFullDelegateTableDoesNotBlockClose(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "1")
	require.True(t, runtime.DeploymentFlags().Closable())
	seedFullAutopayCloseState(t, runtime)
	before, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.NotContains(t, before.AutopayData().AutopayDelegates, "deployer-address")
	param := mustAutopayFundingParam(t, AutopayConfigInvokeParam{AmountPerBlock: "1"})
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{Action: InvokeAPIConfig, Param: param, Invoker: "new-delegate", Height: 100,
		FundingOutput: testContractOutput("new-delegate", 0, runtime.Address(), 0, testAsset("ordx:f:fee", 10))})
	require.ErrorContains(t, err, "autopay delegate limit exceeded")
	afterRejected, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, before, afterRejected)
	_, err = runtime.ApplyDefaultInvoke(autopayDefaultGasRequest(runtime, "ordinary-user", gas.GasAssetName, "full-default", testAsset(gas.GasAssetName, 25)))
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(autopayFundingActionRequest(t, runtime, "deployer-address", AutopayConfigInvokeParam{GasFundingAmount: "50"}, "50", 0, true))
	require.NoError(t, err)
	afterFunding, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, before.AutopayData().AutopayDelegates, afterFunding.AutopayData().AutopayDelegates)
	requireDecimalString(t, "1075", afterFunding.AutopayData().GasBalance)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{Action: InvokeAPIClose, Invoker: "deployer-address", Height: 100, Timestamp: 100,
		FundingOutput: testContractOutput("full-close", 0, runtime.Address(), 0, nil)})
	require.NoError(t, err, "closing must not allocate a delegate slot")
	plan, err := runtime.SettleBlockWithGasConfig(100, gas)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, AutopayMaxCloseDelegateOutputs)
	after, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, after.AutopayData().AutopayCloseStarted)
	require.False(t, after.AutopayData().Closed, "remaining delegates must continue through existing batched settlement")
	require.Len(t, after.AutopayData().AutopayDelegates, AutopayMaxDelegates)
	require.NotContains(t, after.AutopayData().AutopayDelegates, "deployer-address")
}

func TestBackendAutopayFullDelegateTableDoesNotBlockClose(t *testing.T) {
	gas := testAutopayGasConfig()
	gas.ResultBaseGas = 50000
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "1")
	require.True(t, runtime.DeploymentFlags().Closable())
	seedFullAutopayCloseState(t, runtime)
	poolID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	poolAssets := testAssets("ordx:f:fee", AutopayMaxDelegates, gas.GasAssetName, 1000)
	creditTestManagedOutput(t, runtime, testContractOutput(poolID, 0, runtime.Address(), 0, poolAssets))
	fee, err := gas.ResultFee(100)
	require.NoError(t, err)
	assets := testAsset(gas.GasAssetName, 0)
	assets[0].Amount = *fee
	script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: InvokeAPIClose})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, assets, testTemplateContractScript(runtime.Address())))
	store := runtimeStoreWith(runtime)
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "deployer-address", nil },
		AssetPrecision: func(name string) (int, bool) { return 3, name == gas.GasAssetName || name == "ordx:f:fee" },
		ContractUTXOs: func(ContractAddress) ([]UTXO, error) {
			return []UTXO{testContractUTXO(poolID, 0, runtime.Address(), 0, poolAssets)}, nil
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	committed, ok := store.Get(runtime.Address())
	require.True(t, ok)
	state, err := committed.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AutopayData().AutopayCloseStarted)
	require.False(t, state.AutopayData().Closed)
	require.Len(t, state.AutopayData().AutopayDelegates, AutopayMaxDelegates)
	require.NotContains(t, state.AutopayData().AutopayDelegates, "deployer-address")
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Transfers, AutopayMaxCloseDelegateOutputs)
	require.NotEmpty(t, result.ResultPlans)
}

func TestAutopayFundingActionCreditsOnlyRequestedGas(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().GasBalance = parseDecimalOrZero("7.25")
	state.AutopayData().FeeBalance = parseDecimalOrZero("13.5")
	state.AutopayData().AutopayDelegates = map[string]AutopayDelegate{
		"existing-delegate": {
			AmountPerBlock: parseDecimalOrZero("1.25"),
			BlobKeyLimit:   3,
			Balance:        parseDecimalOrZero("13.5"),
			TotalPaid:      parseDecimalOrZero("2.5"),
			PaidBlockCount: 2,
			Status:         AutopayStatusActive,
		},
	}
	require.NoError(t, runtime.saveRuntimeState(state))
	beforeDelegates := state.AutopayData().AutopayDelegates
	beforeFeeBalance := decimalString(state.AutopayData().FeeBalance)

	item, err := runtime.ApplyInvoke(autopayFundingActionRequest(
		t, runtime, "deployer-address", AutopayConfigInvokeParam{GasFundingAmount: "50.25"}, "50.25", 2, true,
	))
	require.NoError(t, err, "deployer funding action should accept exact positive net GAS")
	require.NotNil(t, item)
	require.Equal(t, autopayFundingAction, item.Action)
	require.Equal(t, OrderTypeValidate, item.OrderType)

	after, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "57.5", after.AutopayData().GasBalance)
	require.Equal(t, beforeFeeBalance, decimalString(after.AutopayData().FeeBalance))
	require.Equal(t, beforeDelegates, after.AutopayData().AutopayDelegates)
}

func TestAutopayExplicitConfigFundsDelegate(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().GasBalance = parseDecimalOrZero("7.25")
	require.NoError(t, runtime.saveRuntimeState(state))

	item := applyAutopayDelegateConfigForTest(t, runtime, "ordinary-user", "explicit-delegate-funding", 100,
		"10", gas.GasAssetName,
		testContractOutput("explicit-delegate-funding", 0, runtime.Address(), 0, testAsset(gas.GasAssetName, 5000)))
	require.Equal(t, OrderTypeValidate, item.OrderType)

	after, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "7.25", after.AutopayData().GasBalance)
	requireDecimalString(t, "5000", after.AutopayData().FeeBalance)
	requireDecimalString(t, "5000", after.AutopayData().AutopayDelegates["ordinary-user"].Balance)
}

func TestAutopayFundingActionGuardsRejectWithoutStateChange(t *testing.T) {
	for _, tc := range []struct {
		name          string
		invoker       string
		param         AutopayConfigInvokeParam
		input         string
		precision     int
		known         bool
		wantErrorText string
	}{
		{
			name: "non-deployer", invoker: "ordinary-user",
			param: AutopayConfigInvokeParam{GasFundingAmount: "50.25"}, input: "50.25", precision: 2, known: true,
			wantErrorText: "only autopay deployer may fund operating gas",
		},
		{
			name: "amount-per-block-forbidden", invoker: "deployer-address",
			param: AutopayConfigInvokeParam{AmountPerBlock: "1", GasFundingAmount: "50.25"}, input: "50.25", precision: 2, known: true,
			wantErrorText: "autopay funding requires positive operating gas only",
		},
		{
			name: "blob-key-limit-forbidden", invoker: "deployer-address",
			param: AutopayConfigInvokeParam{BlobKeyLimit: 1, GasFundingAmount: "50.25"}, input: "50.25", precision: 2, known: true,
			wantErrorText: "autopay funding requires positive operating gas only",
		},
		{
			name: "empty-amount", invoker: "deployer-address",
			param: AutopayConfigInvokeParam{}, input: "50.25", precision: 2, known: true,
			wantErrorText: "autopay funding requires positive operating gas only",
		},
		{
			name: "zero-amount", invoker: "deployer-address",
			param: AutopayConfigInvokeParam{GasFundingAmount: "0"}, input: "50.25", precision: 2, known: true,
			wantErrorText: "autopay funding requires positive operating gas only",
		},
		{
			name: "negative-amount", invoker: "deployer-address",
			param: AutopayConfigInvokeParam{GasFundingAmount: "-1"}, input: "50.25", precision: 2, known: true,
			wantErrorText: "invalid autopay gas funding amount",
		},
		{
			name: "insufficient-net-funding", invoker: "deployer-address",
			param: AutopayConfigInvokeParam{GasFundingAmount: "50.25"}, input: "50.24", precision: 2, known: true,
			wantErrorText: "autopay gas funding exceeds net funding",
		},
		{
			name: "same-asset-extra-principal", invoker: "deployer-address",
			param: AutopayConfigInvokeParam{GasFundingAmount: "50.25"}, input: "51.25", precision: 2, known: true,
			wantErrorText: "gas-only autopay config contains delegate funding",
		},
		{
			name: "precision-mismatch", invoker: "deployer-address",
			param: AutopayConfigInvokeParam{GasFundingAmount: "50.251"}, input: "50.251", precision: 2, known: true,
			wantErrorText: "autopay gas funding is not exactly representable at precision 2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gas := testAutopayGasConfig()
			runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
			seedAutopayFundingState(t, runtime)
			before, err := runtime.RuntimeState()
			require.NoError(t, err)

			req := autopayFundingActionRequest(t, runtime, tc.invoker, tc.param, tc.input, tc.precision, tc.known)
			item, err := runtime.ApplyInvoke(req)
			require.Nil(t, item)
			require.ErrorContains(t, err, tc.wantErrorText)

			after, err := runtime.RuntimeState()
			require.NoError(t, err)
			require.Equal(t, before, after, "a rejected funding request must not mutate runtime state")
		})
	}
}

func TestAutopayFundingActionSupportsDifferentFeeAsset(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "10")
	seedAutopayFundingState(t, runtime)

	assets := testAssets(gas.GasAssetName, 0, "ordx:f:fee", 0)
	foundGas := false
	for i := range assets {
		if assets[i].Name.String() == gas.GasAssetName {
			assets[i].Amount = *parseDecimalOrZero("50.25")
			foundGas = true
		}
	}
	require.True(t, foundGas)
	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIFunding,
		Param:         mustAutopayFundingParam(t, AutopayConfigInvokeParam{GasFundingAmount: "50.25"}),
		Invoker:       "deployer-address",
		FundingOutput: testContractOutput("autopay-funding-different-fee-asset", 0, runtime.Address(), 0, assets),
		GasAssetName:  gas.GasAssetName,
		AssetPrecision: func(name string) (int, bool) {
			return 2, name == gas.GasAssetName
		},
		ResultGasFee: parseDecimalOrZero("0.25"),
	})
	require.NoError(t, err)
	require.NotNil(t, item)
	require.Equal(t, OrderTypeValidate, item.OrderType)

	after, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "57.5", after.AutopayData().GasBalance)
	requireDecimalString(t, "13.5", after.AutopayData().FeeBalance)
	require.Equal(t, beforeAutopayFundingDelegates(), after.AutopayData().AutopayDelegates)
}

func seedAutopayFundingState(t *testing.T, runtime *ContractRuntime) {
	t.Helper()
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().GasBalance = parseDecimalOrZero("7.25")
	state.AutopayData().FeeBalance = parseDecimalOrZero("13.5")
	state.AutopayData().AutopayDelegates = beforeAutopayFundingDelegates()
	require.NoError(t, runtime.saveRuntimeState(state))
}

func beforeAutopayFundingDelegates() map[string]AutopayDelegate {
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

func mustAutopayFundingParam(t *testing.T, param AutopayConfigInvokeParam) []byte {
	t.Helper()
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func autopayFundingActionRequest(t *testing.T, runtime *ContractRuntime, invoker string, param AutopayConfigInvokeParam, input string, precision int, known bool) ApplyInvokeRequest {
	t.Helper()
	encoded, err := param.Encode()
	require.NoError(t, err)
	assets := testAsset(testAutopayGasConfig().GasAssetName, 0)
	assets[0].Amount = *parseDecimalOrZero(input)
	return ApplyInvokeRequest{
		Action: autopayFundingAction, Param: encoded, Invoker: invoker,
		// FundingOutput is the net output after Result-fee deduction.
		FundingOutput: testContractOutput("autopay-funding-action", 0, runtime.Address(), 0, assets),
		GasAssetName:  testAutopayGasConfig().GasAssetName,
		ResultGasFee:  parseDecimalOrZero("0.25"),
		AssetPrecision: func(string) (int, bool) {
			return precision, known
		},
	}
}
