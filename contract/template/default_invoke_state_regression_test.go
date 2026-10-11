package template

import (
	"fmt"
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestDefaultInvokeLoadedStateMatchesRuntimePath(t *testing.T) {
	gas := DefaultGasConfig().GasAssetName
	runtime := testAutopayRuntime(t, "recipient-address", gas, "2000")
	other, ok := runtimeStoreWith(runtime).Clone().Get(runtime.Address())
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	req := ApplyInvokeRequest{
		Action: contractcommon.ContractInvokeAPIDefault, CallID: "loaded-state", Invoker: "delegate",
		FundingOutput: testContractOutput("funding", 0, runtime.Address(), 0, testAsset(gas, 1000)),
		Height:        100, Timestamp: 100,
	}
	want, err := runtime.ApplyDefaultInvoke(req)
	require.NoError(t, err)
	require.NotNil(t, want)
	// Poison the stored encoding after the one read. The helpers must use the
	// supplied call-local state, without silently loading a second snapshot.
	other.SetState(runtimeStateKey, []byte("invalid-json"))
	require.NoError(t, other.checkInvocationLifecycle(state, req.Action, req.Invoker))
	require.NoError(t, checkAutopayDelegateCapacity(other.Contract(), &state, req.Invoker))
	got, err := other.applyDefaultInvoke(state, req)
	require.NoError(t, err)
	require.Equal(t, want, got)
	wantBytes, _ := runtime.GetState(runtimeStateKey)
	gotBytes, _ := other.GetState(runtimeStateKey)
	require.Equal(t, wantBytes, gotBytes, "retain the exact persisted JSON and accounting")
}

func TestDefaultInvokeStateReuseSequentialOutputsAndCapacity(t *testing.T) {
	gas := DefaultGasConfig().GasAssetName
	runtime := testAutopayRuntime(t, "recipient-address", gas, "2000")
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate, AutopayMaxDelegates)
	for i := 0; i < AutopayMaxDelegates-1; i++ {
		state.AutopayData().AutopayDelegates[fmt.Sprintf("existing-%d", i)] = AutopayDelegate{}
	}
	require.NoError(t, runtime.saveRuntimeState(state))
	actor := "last-delegate"
	backend := NewBackend(BlockExecutionRequest{
		Store: runtimeStoreWith(runtime), BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return actor, nil },
	})
	fee := testTemplateGasFeeAmount(t, DefaultGasConfig().ResultBaseGas)
	tx := testTemplateDefaultInvokeTx(t, runtime.Address(), 0, testAsset(gas, 1000+fee))
	tx.AddTxOut(wire.NewTxOut(0, testAsset(gas, 1000+fee), testTemplateContractScript(runtime.Address())))
	require.NoError(t, backend.ExecuteTx(tx))
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.AutopayData().AutopayDelegates, AutopayMaxDelegates)
	requireDecimalString(t, "2000", state.AutopayData().AutopayDelegates[actor].Balance)
	require.Len(t, state.Items, 2)
	require.NotEqual(t, state.Items[0].ID, state.Items[1].ID)
	require.NotEqual(t, state.Items[0].CallID, state.Items[1].CallID)
	require.Len(t, backend.records, 2)
	for _, record := range backend.records {
		require.Equal(t, ResultStatusSuccess, record.Status)
	}
	before, _ := runtime.GetState(runtimeStateKey)
	actor = "one-too-many"
	require.NoError(t, backend.ExecuteTx(testTemplateDefaultInvokeTx(t, runtime.Address(), 0, testAsset(gas, 1000+fee))))
	after, _ := runtime.GetState(runtimeStateKey)
	require.Equal(t, before, after, "capacity rejection must not publish the loaded state")
	require.Equal(t, ResultStatusInvalid, backend.records[2].Status)
	require.True(t, backend.records[2].RequiresResult)
}

func TestDefaultInvokeStateReuseFailurePreservesState(t *testing.T) {
	for _, scenario := range []string{"closed", "closing", "foreign_asset", "corrupt_state", "failed_block"} {
		t.Run(scenario, func(t *testing.T) {
			gas := DefaultGasConfig().GasAssetName
			runtime := testAutopayRuntime(t, "recipient-address", gas, "2000")
			store := runtimeStoreWith(runtime)
			backend := NewBackend(BlockExecutionRequest{Store: store, BlockHeight: 100, ResolveInvoker: testTemplateInvokerResolver})
			fee := testTemplateGasFeeAmount(t, DefaultGasConfig().ResultBaseGas)
			tx := testTemplateDefaultInvokeTx(t, runtime.Address(), 0, testAsset(gas, 1000+fee))
			switch scenario {
			case "closed":
				state, err := runtime.RuntimeState()
				require.NoError(t, err)
				state.AutopayData().AutopayStatus = AutopayStatusClosed
				state.AutopayData().Closed = true
				require.NoError(t, runtime.saveRuntimeState(state))
			case "closing":
				address := runtime.Address()
				backend.closing[address.MustEncode()] = true
			case "foreign_asset":
				tx.TxOut[0].Assets = testAssets(gas, 1000+fee, "ordx:f:foreign", 1)
			case "corrupt_state":
				runtime.SetState(runtimeStateKey, []byte("invalid-json"))
			}
			before, _ := runtime.GetState(runtimeStateKey)
			managed := runtime.base.managed.Clone()
			if scenario == "failed_block" {
				bad := testTemplateDefaultInvokeTx(t, runtime.Address(), -1, testAsset(gas, 1000+fee))
				_, err := ExecuteBlock(BlockExecutionRequest{
					Store: store, Txs: []*wire.MsgTx{tx, bad}, BlockHeight: 100, ResolveInvoker: testTemplateInvokerResolver,
				})
				require.Error(t, err)
			} else {
				err := backend.ExecuteTx(tx)
				if scenario == "corrupt_state" {
					require.Error(t, err)
					require.Empty(t, backend.records)
				} else {
					require.NoError(t, err)
					require.Len(t, backend.records, 1)
					require.Equal(t, ResultStatusInvalid, backend.records[0].Status)
					require.True(t, backend.records[0].RequiresResult)
				}
			}
			after, _ := runtime.GetState(runtimeStateKey)
			require.Equal(t, before, after)
			require.Equal(t, managed, runtime.base.managed)
		})
	}
}
