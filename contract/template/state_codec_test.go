package template

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeStoreMarshalRoundTrip(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	store := NewRuntimeStore()
	store.Add(runtime)

	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	decoded, err := DecodeRuntimeStore(encoded, nil)
	require.NoError(t, err)
	require.Equal(t, store.StateRoot(), decoded.StateRoot())

	got, ok := decoded.Get(runtime.Address())
	require.True(t, ok)
	state, err := got.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.TradingReady)
	require.Equal(t, "100", state.Running.AssetAmtInPool)
}
