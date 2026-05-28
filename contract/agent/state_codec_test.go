package agent

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
)

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
