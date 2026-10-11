package template

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestDefaultInvokeLifecycleReuseMatchesUncachedExecution(t *testing.T) {
	for _, parsedEntry := range []bool{false, true} {
		name := "raw"
		if parsedEntry {
			name = "parsed"
		}
		t.Run(name, func(t *testing.T) {
			gas := DefaultGasConfig().GasAssetName
			runtime := testAutopayRuntime(t, "recipient-address", gas, "2000")
			store := runtimeStoreWith(runtime)
			actor := "delegate-a"
			request := BlockExecutionRequest{
				Store: store, BlockHeight: 100,
				ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return actor, nil },
			}
			got := NewBackend(request)
			request.Store = store.Clone()
			want := NewBackend(request)
			fee := testTemplateGasFeeAmount(t, DefaultGasConfig().ResultBaseGas)
			for index, scenario := range []string{"multi-output", "another-delegate", "foreign-asset", "repeat-delegate"} {
				actor = "delegate-a"
				if scenario == "another-delegate" {
					actor = "delegate-b"
				}
				tx := testTemplateDefaultInvokeTx(t, runtime.Address(), 0, testAsset(gas, 1000+fee))
				tx.LockTime = uint32(index)
				if scenario == "multi-output" {
					tx.AddTxOut(wire.NewTxOut(0, testAsset(gas, 1000+fee), testTemplateContractScript(runtime.Address())))
				}
				if scenario == "foreign-asset" {
					tx.TxOut[0].Assets = testAssets(gas, 1000+fee, "ordx:f:foreign", 1)
				}
				// The reference enters the same framework directly, without the
				// backend's execution scope, and therefore reads state twice.
				reference := contractframework.NewExecutor(want.executorConfig())
				if parsedEntry {
					parsed, err := contractframework.ParseTx(tx, StandardContractScriptResolver(TestnetContractPrefix), templateParseSpec())
					require.NoError(t, err)
					require.NoError(t, reference.ExecuteParsedTx(tx, parsed))
					require.NoError(t, got.ExecuteParsedTx(tx, parsed))
				} else {
					require.NoError(t, reference.ExecuteTx(tx))
					require.NoError(t, got.ExecuteTx(tx))
				}
				require.NoError(t, want.utxoOverlay.ApplyTx(tx, want.BlockHeight))
				wantBytes, err := want.Store.MarshalBinary()
				require.NoError(t, err)
				gotBytes, err := got.Store.MarshalBinary()
				require.NoError(t, err)
				require.Equal(t, wantBytes, gotBytes, scenario)
				require.Equal(t, want.Store.StateRoot(), got.Store.StateRoot(), scenario)
				require.Equal(t, want.records, got.records, scenario)
			}
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			requireDecimalString(t, "3000", state.AutopayData().AutopayDelegates["delegate-a"].Balance)
			requireDecimalString(t, "1000", state.AutopayData().AutopayDelegates["delegate-b"].Balance)
			require.Equal(t, ResultStatusInvalid, got.records[3].Status)
			wantResult, err := want.Finalize()
			require.NoError(t, err)
			gotResult, err := got.Finalize()
			require.NoError(t, err)
			require.Equal(t, wantResult, gotResult)
			wantBytes, err := want.Store.MarshalBinary()
			require.NoError(t, err)
			gotBytes, err := got.Store.MarshalBinary()
			require.NoError(t, err)
			require.Equal(t, wantBytes, gotBytes)
		})
	}
}

func TestDefaultInvokeLifecycleReuseDoesNotEscapeExecution(t *testing.T) {
	for _, scenario := range []string{"success", "rejection", "error", "parsed"} {
		t.Run(scenario, func(t *testing.T) {
			gas := DefaultGasConfig().GasAssetName
			runtime := testAutopayRuntime(t, "recipient-address", gas, "2000")
			backend := NewBackend(BlockExecutionRequest{Store: runtimeStoreWith(runtime), BlockHeight: 100, ResolveInvoker: testTemplateInvokerResolver})
			fee := testTemplateGasFeeAmount(t, DefaultGasConfig().ResultBaseGas)
			tx := testTemplateDefaultInvokeTx(t, runtime.Address(), 0, testAsset(gas, 1000+fee))
			initial, _ := runtime.GetState(runtimeStateKey)
			if scenario == "rejection" {
				tx.TxOut[0].Assets = testAssets(gas, 1000+fee, "ordx:f:foreign", 1)
			}
			if scenario == "error" {
				runtime.SetState(runtimeStateKey, []byte(`{"autopay":{"gasBalance":{"Precision":64,"Value":"1"}}}`))
				require.Error(t, backend.ExecuteTx(tx))
				require.Empty(t, backend.records)
				runtime.SetState(runtimeStateKey, initial)
			} else if scenario == "parsed" {
				parsed, err := contractframework.ParseTx(tx, StandardContractScriptResolver(TestnetContractPrefix), templateParseSpec())
				require.NoError(t, err)
				require.NoError(t, backend.ExecuteParsedTx(tx, parsed))
			} else {
				require.NoError(t, backend.ExecuteTx(tx))
			}
			// A public lifecycle read outside execution must not become a
			// stale admission snapshot for a later direct DefaultInvoke call.
			_, found, err := backend.Lifecycle(runtime.Address())
			require.NoError(t, err)
			require.True(t, found)
			runtime.SetState(runtimeStateKey, []byte("invalid-json"))
			badState, _ := runtime.GetState(runtimeStateKey)
			managed := runtime.base.managed.Clone()
			tx = testTemplateDefaultInvokeTx(t, runtime.Address(), 0, testAsset(gas, 1000+fee))
			output := testContractOutput(tx.TxID(), 0, runtime.Address(), 0, testAsset(gas, 1000+fee))
			funding := contractframework.ContractFundingOutputs([]ContractOutput{output})[0]
			outcome, handled, err := backend.DefaultInvoke(contractframework.ExecutionContext{RawTx: tx}, contractcommon.Tx{
				TxID: tx.TxID(), Actor: "direct-delegate", Contract: runtime.Address(), Funding: []contractcommon.FundingOutput{funding},
			}, funding)
			require.NoError(t, err)
			require.True(t, handled)
			require.Equal(t, ResultStatusInvalid, outcome.Status)
			require.True(t, outcome.RequiresResult)
			after, _ := runtime.GetState(runtimeStateKey)
			require.Equal(t, badState, after)
			require.Equal(t, managed, runtime.base.managed)
		})
	}
}
