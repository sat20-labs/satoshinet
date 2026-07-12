package evm

import (
	"fmt"
	"testing"
)

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

func TestRegisterTriggerEnforcesPerContractLimit(t *testing.T) {
	state := NewMemoryStateDB()
	contract := testContract(t)
	for i := 0; i < MaxEVMTriggersPerContract; i++ {
		err := state.RegisterTrigger(Trigger{
			ID: fmt.Sprintf("trigger-%d", i), Contract: contract,
			Kind: TriggerAtHeight, Height: int64(i + 1), GasLimit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	err := state.RegisterTrigger(Trigger{
		ID: "overflow", Contract: contract, Kind: TriggerAtHeight, Height: 1000, GasLimit: 1,
	})
	if err == nil {
		t.Fatal("trigger beyond per-contract limit should fail")
	}
	if err := state.RegisterTrigger(Trigger{
		ID: "trigger-0", Contract: contract, Kind: TriggerAtHeight, Height: 2000, GasLimit: 1,
	}); err != nil {
		t.Fatalf("updating an existing trigger at the limit failed: %v", err)
	}
}

func TestRegisterTriggerEnforcesGlobalLimit(t *testing.T) {
	state := NewMemoryStateDB()
	for i := 0; i < MaxEVMTriggersTotal; i++ {
		var address EVMAddress
		address[0] = byte(i / MaxEVMTriggersPerContract)
		contract := testContractWithHash(t, address)
		err := state.RegisterTrigger(Trigger{
			ID: fmt.Sprintf("trigger-%d", i), Contract: contract,
			Kind: TriggerAtHeight, Height: int64(i + 1), GasLimit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	var overflowAddress EVMAddress
	overflowAddress[0] = 0xff
	overflowContract := testContractWithHash(t, overflowAddress)
	err := state.RegisterTrigger(Trigger{
		ID: "overflow", Contract: overflowContract,
		Kind: TriggerAtHeight, Height: 5000, GasLimit: 1,
	})
	if err == nil {
		t.Fatal("trigger beyond global limit should fail")
	}
}
