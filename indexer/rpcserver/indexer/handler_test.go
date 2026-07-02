package indexer

import (
	"encoding/json"
	"fmt"
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

func TestContractListFilterSortAndPagination(t *testing.T) {
	agentTypeID := int(contractcommon.ContractTypeAgent)
	query := contractListQuery{
		start:          0,
		limit:          2,
		contractTypeID: &agentTypeID,
		subtype:        "prediction",
		sortBy:         "created_height",
		desc:           true,
	}

	contracts := []contractcommon.ContractSummary{
		{Address: "agent-old", ContractTypeID: contractcommon.ContractTypeAgent, Subtype: "prediction", CreatedHeight: 10},
		{Address: "evm-prediction", ContractTypeID: contractcommon.ContractTypeEVM, Subtype: "prediction", CreatedHeight: 40},
		{Address: "agent-other", ContractTypeID: contractcommon.ContractTypeAgent, Subtype: "other", CreatedHeight: 50},
		{Address: "agent-newest", ContractTypeID: contractcommon.ContractTypeAgent, Subtype: "prediction", CreatedHeight: 30},
		{Address: "agent-middle", ContractTypeID: contractcommon.ContractTypeAgent, Subtype: "prediction", CreatedHeight: 20},
	}

	filtered := filterContractSummaries(contracts, query)
	sortContractSummaries(filtered, query)
	page := paginateContractSummaries(filtered, query.start, query.limit)

	if len(filtered) != 3 {
		t.Fatalf("filtered length = %d, want 3", len(filtered))
	}
	if len(page) != 2 {
		t.Fatalf("page length = %d, want 2", len(page))
	}
	if page[0].Address != "agent-newest" || page[1].Address != "agent-middle" {
		t.Fatalf("unexpected order: %v", contractURLsFromSummaries(page))
	}
}

func TestFilterRuntimeExistingContractSummaries(t *testing.T) {
	oldCall := contractStateCall
	defer func() {
		contractStateCall = oldCall
	}()

	contractStateCall = func(method string, params []interface{}) (json.RawMessage, error) {
		if method != "getcontractstate" {
			t.Fatalf("method = %s, want getcontractstate", method)
		}
		address := params[0].(string)
		switch address {
		case "invalid":
			return json.RawMessage(`{"details":{"exists":false}}`), nil
		case "valid":
			return json.RawMessage(`{"details":{"exists":true}}`), nil
		case "legacy":
			return json.RawMessage(`{"state":{"status":"Ready"}}`), nil
		case "rpc-error":
			return nil, fmt.Errorf("temporary rpc error")
		default:
			t.Fatalf("unexpected address %s", address)
			return nil, nil
		}
	}

	contracts := []contractcommon.ContractSummary{
		{Address: "invalid"},
		{Address: "valid"},
		{Address: "legacy"},
		{Address: "rpc-error"},
	}

	filtered := filterRuntimeExistingContractSummaries(contracts, contractListQuery{})
	got := contractURLsFromSummaries(filtered)
	want := []string{"valid", "legacy", "rpc-error"}
	if len(got) != len(want) {
		t.Fatalf("filtered addresses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filtered addresses = %v, want %v", got, want)
		}
	}

	all := filterRuntimeExistingContractSummaries(contracts, contractListQuery{includeInvalid: true})
	if len(all) != len(contracts) {
		t.Fatalf("include invalid length = %d, want %d", len(all), len(contracts))
	}
}

func TestDefaultEVMCompilerConfig(t *testing.T) {
	cfg := defaultEVMCompilerConfig()
	if cfg.SolcVersion != "0.8.30" {
		t.Fatalf("solc version = %s, want 0.8.30", cfg.SolcVersion)
	}
	if cfg.EVMVersion != "paris" {
		t.Fatalf("evm version = %s, want paris", cfg.EVMVersion)
	}
	if !cfg.Optimizer.Enabled || cfg.Optimizer.Runs != 200 {
		t.Fatalf("optimizer = %+v, want enabled runs=200", cfg.Optimizer)
	}
	if cfg.Metadata.BytecodeHash != "none" {
		t.Fatalf("metadata bytecode hash = %s, want none", cfg.Metadata.BytecodeHash)
	}
	if !cfg.SingleFileOnly || cfg.AllowImports {
		t.Fatalf("source policy singleFileOnly=%v allowImports=%v, want true/false",
			cfg.SingleFileOnly, cfg.AllowImports)
	}
}
