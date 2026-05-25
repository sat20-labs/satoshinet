package common

import "testing"

func TestCombineStateRootsHashesAllRoots(t *testing.T) {
	var templateRoot [32]byte
	var evmRoot [32]byte
	var agentRoot [32]byte
	templateRoot[0] = 1
	evmRoot[0] = 2
	agentRoot[0] = 3

	combined := CombineStateRoots(templateRoot, evmRoot, agentRoot)
	if combined == templateRoot || combined == evmRoot || combined == agentRoot {
		t.Fatalf("combined root must be a hash of all roots")
	}
	if combined != CombineStateRoots(templateRoot, evmRoot, agentRoot) {
		t.Fatalf("combined root must be deterministic")
	}
	if combined == CombineStateRoots(templateRoot, evmRoot, [32]byte{}) {
		t.Fatalf("agent root must affect combined root")
	}
}
