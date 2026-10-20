package template

import (
	"fmt"
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestAutopayClosingOnlyAcceptsOperatingGas(t *testing.T) {
	for _, action := range []string{InvokeAPIClose, InvokeAPICancel, InvokeAPIConfig, InvokeAPIFunding, "gas-config", contractcommon.ContractInvokeAPIDefault} {
		t.Run(action, func(t *testing.T) {
			runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "1")
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			state.AutopayData().AutopayCloseStarted = true
			state.NextItemID = 1
			state.Items = []InvokeItem{{ID: 0, OrderType: OrderTypeClose, Action: InvokeAPIClose, Address: "deployer-address", Height: 99}}
			require.NoError(t, runtime.saveRuntimeState(state))
			before, err := runtime.RuntimeState()
			require.NoError(t, err)
			req := ApplyInvokeRequest{Action: action, Invoker: "deployer-address", CallID: action, Height: 100,
				GasAssetName: testAutopayGasConfig().GasAssetName, AssetPrecision: func(string) (int, bool) { return 8, true },
				FundingOutput: testContractOutput("closing-control", 0, runtime.Address(), 0, testAsset(testAutopayGasConfig().GasAssetName, 1))}
			allowed := action == InvokeAPIFunding || action == "gas-config" || action == contractcommon.ContractInvokeAPIDefault
			switch action {
			case InvokeAPIConfig:
				req.Param = mustAutopayFundingParam(t, AutopayConfigInvokeParam{AmountPerBlock: "1"})
			case InvokeAPIFunding, "gas-config":
				req.Param = mustAutopayFundingParam(t, AutopayConfigInvokeParam{GasFundingAmount: "1"})
				if action == "gas-config" {
					req.Action = InvokeAPIConfig
				}
			}
			if action == contractcommon.ContractInvokeAPIDefault {
				_, err = runtime.ApplyDefaultInvoke(req)
			} else {
				_, err = runtime.ApplyInvoke(req)
			}
			if allowed {
				require.NoError(t, err)
				after, err := runtime.RuntimeState()
				require.NoError(t, err)
				requireDecimalString(t, "1", after.AutopayData().GasBalance)
			} else {
				require.ErrorContains(t, err, "autopay is closing")
				after, err := runtime.RuntimeState()
				require.NoError(t, err)
				require.Equal(t, before, after)
			}
		})
	}
}

func TestBackendAutopayClosePausesWithoutBlockingOtherContracts(t *testing.T) {
	for _, precision := range []int{0, 3} {
		t.Run(fmt.Sprintf("precision_%d", precision), func(t *testing.T) {
			gas := testAutopayGasConfig()
			gas.ResultBaseGas = 50000
			runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "1")
			address := runtime.Address()
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			count := AutopayMaxCloseDelegateOutputs + 1
			state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate, count)
			for n := 0; n < count; n++ {
				state.AutopayData().AutopayDelegates[fmt.Sprintf("delegate-%05d", n)] = AutopayDelegate{
					AmountPerBlock: parseDecimalOrZero("1"), Balance: parseDecimalOrZero("1"), Status: AutopayStatusActive,
				}
			}
			state.AutopayData().FeeBalance = parseDecimalOrZero(fmt.Sprint(count))
			require.NoError(t, runtime.saveRuntimeState(state))
			creditTestManagedOutput(t, runtime, testContractOutput("pool", 0, runtime.Address(), 0, testAsset("ordx:f:fee", int64(count))))
			other := testAutopayRuntime(t, "other-recipient", "ordx:f:fee", "1")
			otherState, err := other.RuntimeState()
			require.NoError(t, err)
			otherState.AutopayData().AutopayDelegates = map[string]AutopayDelegate{
				"other-payer": {AmountPerBlock: parseDecimalOrZero("1"), Balance: parseDecimalOrZero("1")},
			}
			otherState.AutopayData().FeeBalance = parseDecimalOrZero("1")
			otherState.AutopayData().GasBalance = parseDecimalOrZero("100")
			otherState.AutopayData().NextPayHeight = 101
			require.NoError(t, other.saveRuntimeState(otherState))
			creditTestManagedOutput(t, other, testContractOutput("other-pool", 0, other.Address(), 0, testAssets("ordx:f:fee", 1, gas.GasAssetName, 100)))
			store := runtimeStoreWith(runtime)
			store.Add(other)
			resultFee, err := gas.ResultFee(100)
			require.NoError(t, err)
			gasAssets := func(net string) wire.TxAssets {
				assets := testAsset(gas.GasAssetName, 0)
				assets[0].Amount = *parseDecimalOrZero(net).AddAlignPrecision(resultFee)
				return assets
			}
			execute := func(height int64, txs ...*wire.MsgTx) BlockExecutionResult {
				t.Helper()
				providers := make(map[string]ContractUTXOProvider)
				for _, addr := range []ContractAddress{runtime.Address(), other.Address()} {
					current, ok := store.Get(addr)
					require.True(t, ok)
					providers[addr.MustEncode()] = managedPhysicalProvider(t, current)
				}
				result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: txs, Store: store, GasConfig: gas, BlockHeight: height,
					ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "deployer-address", nil },
					AssetPrecision: func(string) (int, bool) { return precision, true },
					ContractUTXOs:  func(addr ContractAddress) ([]UTXO, error) { return providers[addr.MustEncode()](addr) },
				})
				require.NoError(t, err, "one paused AUTOPAY must not abort block finalization")
				return result
			}
			script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: InvokeAPIClose})
			require.NoError(t, err)
			closeTx := wire.NewMsgTx(1)
			closeTx.AddTxIn(&wire.TxIn{})
			closeTx.AddTxOut(wire.NewTxOut(0, nil, script))
			closeTx.AddTxOut(wire.NewTxOut(0, gasAssets("0"), testTemplateContractScript(runtime.Address())))
			first := execute(100, closeTx)
			require.Equal(t, ResultStatusSuccess, first.Records[0].Status)
			require.Len(t, first.SettlementPlans, 1)
			require.Len(t, first.SettlementPlans[0].Transfers, AutopayMaxCloseDelegateOutputs)
			committed, _ := store.Get(runtime.Address())
			beforePause, err := committed.RuntimeState()
			require.NoError(t, err)
			require.True(t, beforePause.AutopayData().AutopayCloseStarted)
			require.False(t, beforePause.AutopayData().Closed)
			requireDecimalString(t, "1", beforePause.AutopayData().FeeBalance)
			requireDecimalString(t, "0", beforePause.AutopayData().GasBalance)
			encoded, err := store.MarshalBinary()
			require.NoError(t, err)
			store, err = DecodeRuntimeStore(encoded, nil)
			require.NoError(t, err)
			paused := execute(101, testTemplateDefaultInvokeTx(t, other.Address(), 0, gasAssets("25")))
			require.Equal(t, ResultStatusSuccess, paused.Records[0].Status)
			require.Len(t, paused.SettlementPlans, 1)
			otherAddress := other.Address()
			require.Equal(t, otherAddress.MustEncode(), paused.SettlementPlans[0].Contract)
			otherCommitted, _ := store.Get(other.Address())
			otherAfter, err := otherCommitted.RuntimeState()
			require.NoError(t, err)
			require.Equal(t, int64(101), otherAfter.AutopayData().AutopayDelegates["other-payer"].LastPayHeight)
			requireDecimalString(t, "0", otherAfter.AutopayData().FeeBalance)
			for _, plan := range paused.ResultPlans {
				require.NotEqual(t, address.MustEncode(), plan.Contract, "paused batch must leave no Result inputs or item IDs")
			}
			committed, _ = store.Get(runtime.Address())
			afterPause, err := committed.RuntimeState()
			require.NoError(t, err)
			require.Equal(t, beforePause, afterPause, "paused batch must not debit GAS, refund or finish its close item")
			resumed := execute(102, testTemplateDefaultInvokeTx(t, runtime.Address(), 0, gasAssets("100")))
			require.Len(t, resumed.Records, 1)
			require.Equal(t, ResultStatusSuccess, resumed.Records[0].Status, "%+v", resumed.Records[0])
			committed, _ = store.Get(runtime.Address())
			after, err := committed.RuntimeState()
			require.NoError(t, err)
			require.True(t, after.AutopayData().Closed)
			requireDecimalString(t, "0", after.AutopayData().FeeBalance)
			requireDecimalString(t, "0", after.AutopayData().GasBalance)
			seen := make(map[string]bool)
			for _, block := range []BlockExecutionResult{first, resumed} {
				for _, plan := range block.SettlementPlans {
					for _, transfer := range plan.Transfers {
						if transfer.AssetName == "ordx:f:fee" {
							require.False(t, seen[transfer.To], "delegate must never be refunded twice")
							seen[transfer.To] = true
							require.Equal(t, "1", transfer.AssetAmt)
						}
					}
				}
			}
			require.Len(t, seen, count)
			require.Len(t, resumed.SettlementPlans, 1)
			fee, err := gas.ContractFundingFee(ExecutionKindTrigger, gas.TriggerBaseGas, true, 102)
			require.NoError(t, err)
			fee = (contractframework.AssetPrecisionPolicy{Resolve: func(string) (int, bool) { return precision, true }}).NormalizeUp(gas.GasAssetName, fee)
			requireDecimalString(t, fee.String(), resumed.SettlementPlans[0].GasFee)
			require.Equal(t, parseDecimalOrZero("100").SubAlignPrecision(fee).String(), resumed.SettlementPlans[0].Transfers[1].AssetAmt)
			empty := execute(103)
			require.Empty(t, empty.SettlementPlans)
		})
	}
}

func TestAutopayConfigInvalidAmountRejectsAndRefunds(t *testing.T) {
	for _, text := range []string{"invalid", "0:0", "0.5:1"} {
		t.Run(text, func(t *testing.T) {
			gas := testAutopayGasConfig()
			gas.ResultBaseGas = 50000
			runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "1")
			param := mustAutopayFundingParam(t, AutopayConfigInvokeParam{AmountPerBlock: text})
			req := ApplyInvokeRequest{Action: InvokeAPIConfig, Param: param, Invoker: "payer", Height: 100,
				FundingOutput: testContractOutput("invalid-config", 0, runtime.Address(), 0, testAsset("ordx:f:fee", 50))}
			item, err := NewInvokeItemFromRequest(runtime.Contract(), 1, req)
			require.Error(t, err, "invalid config must fail before constructing a finished item")
			require.Nil(t, item)
			before, err := runtime.RuntimeState()
			require.NoError(t, err)
			_, err = runtime.ApplyInvoke(req)
			require.Error(t, err)
			afterRejected, err := runtime.RuntimeState()
			require.NoError(t, err)
			require.Equal(t, before, afterRejected)
			fee, err := gas.ResultFee(100)
			require.NoError(t, err)
			assets := testAssets("ordx:f:fee", 50, gas.GasAssetName, 0)
			assets[1].Amount = *fee
			script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: InvokeAPIConfig, Param: param})
			require.NoError(t, err)
			tx := wire.NewMsgTx(1)
			tx.AddTxIn(&wire.TxIn{})
			tx.AddTxOut(wire.NewTxOut(0, nil, script))
			tx.AddTxOut(wire.NewTxOut(0, assets, testTemplateContractScript(runtime.Address())))
			store := runtimeStoreWith(runtime)
			result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
				AssetPrecision: func(string) (int, bool) { return 3, true },
				ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "payer", nil },
			})
			require.NoError(t, err)
			require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
			require.Len(t, result.ResultPlans, 1)
			requireResultPlanAssetTo(t, result.ResultPlans[0], "payer", "ordx:f:fee", "50")
			committed, _ := store.Get(runtime.Address())
			after, err := committed.RuntimeState()
			require.NoError(t, err)
			require.Empty(t, after.AutopayData().AutopayDelegates)
			requireDecimalString(t, "0", after.AutopayData().FeeBalance)
			managed, ok := store.ManagedBalance(runtime.Address())
			require.True(t, ok)
			require.True(t, managed.IsZero(), "rejected principal must not remain in managed funds")
		})
	}
}

func TestAutopayConfigAmountUsesValidatedParser(t *testing.T) {
	for _, backend := range []bool{false, true} {
		t.Run(fmt.Sprintf("backend_%v", backend), func(t *testing.T) {
			gas := testAutopayGasConfig()
			gas.ResultBaseGas = 50000
			runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "1")
			address := runtime.Address()
			param := mustAutopayFundingParam(t, AutopayConfigInvokeParam{AmountPerBlock: "10:0", BlobKeyLimit: 1})
			store := runtimeStoreWith(runtime)
			if backend {
				resultFee, err := gas.ResultFee(100)
				require.NoError(t, err)
				assets := testAssets("ordx:f:fee", 50, gas.GasAssetName, 0)
				assets[1].Amount = *resultFee
				script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: InvokeAPIConfig, Param: param})
				require.NoError(t, err)
				tx := wire.NewMsgTx(1)
				tx.AddTxIn(&wire.TxIn{})
				tx.AddTxOut(wire.NewTxOut(0, nil, script))
				tx.AddTxOut(wire.NewTxOut(0, assets, testTemplateContractScript(runtime.Address())))
				result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
					AssetPrecision: func(string) (int, bool) { return 3, true },
					ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "payer", nil },
				})
				require.NoError(t, err)
				require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
				require.Len(t, result.ResultPlans, 1)
				requireResultPlanAssetTo(t, result.ResultPlans[0], address.MustEncode(), "ordx:f:fee", "50")
				requireNoResultPlanAssetTo(t, result.ResultPlans[0], "payer", "ordx:f:fee")
				runtime, _ = store.Get(runtime.Address())
			} else {
				item, err := runtime.ApplyInvoke(ApplyInvokeRequest{Action: InvokeAPIConfig, Param: param, Invoker: "payer", Height: 100,
					AssetPrecision: func(string) (int, bool) { return 3, true },
					FundingOutput:  testContractOutput("config", 0, runtime.Address(), 0, testAsset("ordx:f:fee", 50))})
				require.NoError(t, err)
				require.Equal(t, InvokeReasonNormal, item.Reason, "accepted config must be applied, not invalid and finished")
			}
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			requireDecimalString(t, "10", state.AutopayData().AutopayDelegates["payer"].AmountPerBlock)
			requireDecimalString(t, "50", state.AutopayData().AutopayDelegates["payer"].Balance)
			requireDecimalString(t, "50", state.AutopayData().FeeBalance)
		})
	}
}
