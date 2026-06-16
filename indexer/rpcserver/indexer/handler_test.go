package indexer

import (
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
