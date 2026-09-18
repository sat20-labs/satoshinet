package agent

import (
	"encoding/json"
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
	for _, missing := range []bool{true, false} {
		var snapshots []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &snapshots))
		if missing {
			delete(snapshots[0], "managed")
		} else {
			snapshots[0]["managed"] = json.RawMessage(`null`)
		}
		legacy, err := json.Marshal(snapshots)
		require.NoError(t, err)
		_, err = DecodeRuntimeStore(legacy)
		require.ErrorContains(t, err, "no managed balance")
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
