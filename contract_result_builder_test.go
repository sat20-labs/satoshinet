package main

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/mining"
)

func TestContractResultBuilderRunsEVMWithoutBlockWork(t *testing.T) {
	var root [32]byte
	root[0] = 0xaa
	called := false
	builder := newContractResultBuilder(&chaincfg.TestNetParams, nil,
		func(req mining.ContractBuildRequest) (mining.ContractBuildResult, error) {
			called = true
			if len(req.Txs) != 0 {
				t.Fatalf("unexpected txs: %d", len(req.Txs))
			}
			return mining.ContractBuildResult{StateRoot: root}, nil
		}, nil)

	got, err := builder(mining.ContractBuildRequest{})
	if err != nil {
		t.Fatalf("builder failed: %v", err)
	}
	if !called {
		t.Fatalf("expected EVM builder to run without block work")
	}
	expected := contractcommon.CombineStateRoots([32]byte{}, root, [32]byte{})
	if got.StateRoot != expected {
		t.Fatalf("state root mismatch")
	}
}
