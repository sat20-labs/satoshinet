package indexer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

func TestDKVSLocalOnlyMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/admin", dkvsLocalOnly, func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, test := range []struct {
		remote string
		want   int
	}{
		{remote: "127.0.0.1:1000", want: http.StatusNoContent},
		{remote: "[::1]:1000", want: http.StatusNoContent},
		{remote: "203.0.113.1:1000", want: http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.RemoteAddr = test.remote
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != test.want {
			t.Fatalf("remote=%s status=%d want=%d", test.remote, resp.Code, test.want)
		}
	}
}

func TestBindDKVSJSONBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resp := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(resp)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"value":"too-large"}`))
	var target map[string]string
	if err := bindDKVSJSON(ctx, &target, 8); err == nil {
		t.Fatal("oversized DKVS JSON body accepted")
	}
}

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
		{Address: "closed", Status: "invalid"},
		{Address: "reverted", Status: "revert"},
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

func TestContractSummaryStatusActive(t *testing.T) {
	active := []string{
		"", "success", "active", "ready", "tradable", " SUCCESS ",
		"PendingReady", "Betting", "ClosedForBet", "PendingResult", "Settled",
	}
	for _, status := range active {
		if !contractSummaryStatusActive(status) {
			t.Fatalf("status %q should be active", status)
		}
	}

	inactive := []string{"invalid", "revert", "reverted", "out_of_gas", "closed", "Rejected"}
	for _, status := range inactive {
		if contractSummaryStatusActive(status) {
			t.Fatalf("status %q should be inactive", status)
		}
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
