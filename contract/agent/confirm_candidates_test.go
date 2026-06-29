package agent

import "testing"

func TestRuntimeStorePendingPredictionConfirms(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")

	store := NewRuntimeStore()
	store.Add(runtime)

	before, err := store.PendingPredictionConfirms(0, validPredictionContract().ConfirmAfter-1)
	if err != nil {
		t.Fatalf("PendingPredictionConfirms before failed: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("unexpected before-confirm candidates: %d", len(before))
	}

	after, err := store.PendingPredictionConfirms(0, validPredictionContract().ConfirmAfter)
	if err != nil {
		t.Fatalf("PendingPredictionConfirms after failed: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("candidate count mismatch: %d", len(after))
	}
	if after[0].Contract.Title != validPredictionContract().Title {
		t.Fatalf("contract title mismatch: %s", after[0].Contract.Title)
	}
}
