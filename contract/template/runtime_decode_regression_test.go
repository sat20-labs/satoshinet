package template

import (
	"bytes"
	"encoding/json"
	scommon "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRuntimeDecodePreservesValidationAndState(t *testing.T) {
	runtime := testAutopayRuntime(t, "recipient-address", DefaultGasConfig().GasAssetName, "2000")
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	state.AutopayData().AutopayDelegates = map[string]AutopayDelegate{"alice": {Balance: scommon.NewDefaultDecimal(1000)}}
	raw, err := encodeTemplateRuntimeState(state)
	require.NoError(t, err)
	legacy, err := json.Marshal(state)
	require.NoError(t, err)
	var want TemplateRuntimeState
	require.NoError(t, want.UnmarshalJSON(legacy))
	runtime.SetState(runtimeStateKey, raw)
	root := runtime.StateRoot()
	got, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, want, got)
	got.AutopayData().AutopayDelegates["alice"].Balance.Value.SetInt64(1)
	again, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, want, again)
	require.Equal(t, root, runtime.StateRoot())
	for _, bad := range [][]byte{legacy, []byte("invalid-json"), raw[:len(raw)-1], append(bytes.Clone(raw), 0)} {
		runtime.SetState(runtimeStateKey, bad)
		got, err = runtime.RuntimeState()
		require.Error(t, err)
		require.Equal(t, TemplateRuntimeState{}, got)
		saved, _ := runtime.GetState(runtimeStateKey)
		require.Equal(t, bad, saved)
	}
	state.Autopay.GasBalance = &scommon.Decimal{Precision: 64, Value: scommon.NewDefaultDecimal(1).Value}
	_, err = encodeTemplateRuntimeState(state)
	require.Error(t, err)
	for _, absent := range []bool{false, true} {
		if absent {
			delete(runtime.base.state, runtimeStateKey)
		} else {
			runtime.SetState(runtimeStateKey, nil)
		}
		got, err = runtime.RuntimeState()
		require.NoError(t, err)
		require.Equal(t, TemplateRuntimeState{}, got)
	}
}
