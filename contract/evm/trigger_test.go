package evm

import "testing"

func TestTriggerDueAtHeight(t *testing.T) {
	trigger := Trigger{ID: "vault-release", Kind: TriggerAtHeight, Height: 100}
	if trigger.Due(BlockEnvironment{Height: 99}) {
		t.Fatal("trigger should not be due before height")
	}
	if !trigger.Due(BlockEnvironment{Height: 100}) {
		t.Fatal("trigger should be due at height")
	}
}

func TestTriggerValidate(t *testing.T) {
	if err := (Trigger{Kind: TriggerAtHeight, Height: 1}).Validate(); err == nil {
		t.Fatal("missing trigger id should fail")
	}
	if err := (Trigger{ID: "t", Contract: testContract(t), Kind: TriggerAtHeight, Height: 1, GasLimit: 1000}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryStateDBTriggerRegistry(t *testing.T) {
	state := NewMemoryStateDB()
	contract := testContract(t)
	trigger := Trigger{
		ID:       "vault-release",
		Contract: contract,
		Kind:     TriggerAtHeight,
		Height:   100,
		GasLimit: 50000,
		Calldata: []byte{1, 2, 3},
	}
	if err := state.RegisterTrigger(trigger); err != nil {
		t.Fatal(err)
	}
	if got := state.DueTriggerCalls(BlockEnvironment{Height: 99}); len(got) != 0 {
		t.Fatalf("trigger should not be due: %v", got)
	}
	due := state.DueTriggerCalls(BlockEnvironment{Height: 100})
	if len(due) != 1 {
		t.Fatalf("expected one due trigger, got %d", len(due))
	}
	if due[0].Trigger.ID != "vault-release" || due[0].GasLimit != 50000 {
		t.Fatalf("unexpected due trigger: %+v", due[0])
	}
	state.RemoveTrigger(contract, "vault-release")
	if got := state.Triggers(); len(got) != 0 {
		t.Fatalf("trigger should be removed: %v", got)
	}
}

func TestMemoryStateDBRejectsNonPositiveTriggerGas(t *testing.T) {
	state := NewMemoryStateDB()
	contract := testContract(t)
	trigger := Trigger{
		ID:       "vault-release",
		Contract: contract,
		Kind:     TriggerAtHeight,
		Height:   100,
	}
	if err := state.RegisterTrigger(trigger); err == nil {
		t.Fatal("zero trigger gas limit should fail")
	}
	trigger.GasLimit = -1
	if err := state.RegisterTrigger(trigger); err == nil {
		t.Fatal("negative trigger gas limit should fail")
	}
}
