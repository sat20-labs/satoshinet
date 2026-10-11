package agent

import (
	"bytes"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/stretchr/testify/require"
)

func TestRuntimeStoreRejectsMissingManagedBalance(t *testing.T) {
	store := NewRuntimeStore()
	store.Add(newTestRuntime(t))
	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	// A present zero balance is valid; absence cannot be inferred as zero.
	_, err = DecodeRuntimeStore(encoded)
	require.NoError(t, err)
	// The binary schema requires a complete managed-balance field. Legacy
	// JSON, a missing field and a truncated field must all fail closed.
	_, err = DecodeRuntimeStore([]byte(`[{"managed":null}]`))
	require.Error(t, err)
	for i := len(agentStoreHeader); i < len(encoded); i++ {
		_, err = DecodeRuntimeStore(bytes.Clone(encoded[:i]))
		require.Error(t, err)
	}

}

func TestRuntimeStoreCodecRoundTrip(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")

	store := NewRuntimeStore()
	store.Add(runtime)
	encoded, err := store.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}
	decoded, err := DecodeRuntimeStore(encoded)
	if err != nil {
		t.Fatalf("DecodeRuntimeStore failed: %v", err)
	}
	if decoded.StateRoot() != store.StateRoot() {
		t.Fatalf("state root mismatch after decode")
	}
	clone := store.Clone()
	if clone.StateRoot() != store.StateRoot() {
		t.Fatalf("state root mismatch after clone")
	}
}

func TestRuntimeStoreClonePreservesStateWithChainParamsConfig(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.config.ChainParams = &chaincfg.TestNetParams

	store := NewRuntimeStore()
	store.Add(runtime)
	clone := store.Clone()
	if clone.StateRoot() != store.StateRoot() {
		t.Fatalf("state root mismatch after clone with chain params")
	}
}
