package template

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecutionHistoryRecordsCloneExecutionData(t *testing.T) {
	addr := testTemplateContract(t)
	records := []ExecutionRecord{{
		Height:         100,
		TxID:           "tx",
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		CallID:         "call",
		Contract:       addr,
		GasLimit:       1000,
		FundingInputs:  []OutPoint{{TxID: "fund", Vout: 1}},
		ItemIDs:        []int64{7},
		RequiresResult: true,
	}}

	history := ExecutionHistoryRecords(records)
	require.Len(t, history, 1)
	require.Equal(t, HistoryKindInvoke, history[0].Kind)
	require.Equal(t, int64(100), history[0].Height)
	require.Equal(t, "tx", history[0].TxID)
	require.Equal(t, addr.EncodeAddress(), history[0].Contract)
	require.Equal(t, []int64{7}, history[0].ItemIDs)

	records[0].FundingInputs[0].TxID = "changed"
	records[0].ItemIDs[0] = 9
	require.Equal(t, "fund", history[0].FundingInputs[0].TxID)
	require.Equal(t, []int64{7}, history[0].ItemIDs)
}

func TestSettlementHistoryRecordClonesPlan(t *testing.T) {
	plan := &SettlementPlan{
		Contract: "tc-address",
		Height:   200,
		Deals: []SettlementDeal{{
			BuyItemID: 1,
			AssetAmt:  "10",
			SatValue:  20,
		}},
		Transfers: []SettlementTransfer{{
			ItemID:   1,
			SatValue: 20,
			Reason:   SettlementReasonDeal,
		}},
		ItemIDs: []int64{1},
	}

	history := SettlementHistoryRecord("result-tx", plan)
	require.Equal(t, HistoryKindSettlement, history.Kind)
	require.Equal(t, int64(200), history.Height)
	require.Equal(t, "result-tx", history.TxID)
	require.Equal(t, "tc-address", history.Contract)
	require.Equal(t, []int64{1}, history.ItemIDs)
	require.NotNil(t, history.Settlement)

	plan.Deals[0].AssetAmt = "changed"
	plan.ItemIDs[0] = 2
	require.Equal(t, "10", history.Settlement.Deals[0].AssetAmt)
	require.Equal(t, []int64{1}, history.ItemIDs)
}

func TestBlockHistoryRecordsCombinesExecutionAndSettlement(t *testing.T) {
	addr := testTemplateContract(t)
	contract := addr.EncodeAddress()
	result := BlockExecutionResult{
		Records: []ExecutionRecord{{
			Height:   100,
			TxID:     "invoke-tx",
			Type:     TxTypeInvoke,
			Kind:     ExecutionKindInvoke,
			Contract: addr,
			ItemIDs:  []int64{1},
		}},
		SettlementPlans: []*SettlementPlan{{
			Contract: contract,
			Height:   100,
			ItemIDs:  []int64{1},
		}},
		ResultPlans: []ResultPlan{{
			Contract: contract,
			Height:   100,
			ItemIDs:  []int64{1},
			Inputs:   []OutPoint{{TxID: "fund", Vout: 1}},
		}},
	}

	history := BlockHistoryRecords(result, map[string]string{contract: "result-tx"})
	require.Len(t, history, 2)
	require.Equal(t, HistoryKindInvoke, history[0].Kind)
	require.Equal(t, "invoke-tx", history[0].TxID)
	require.Equal(t, HistoryKindSettlement, history[1].Kind)
	require.Equal(t, "result-tx", history[1].TxID)
	require.Equal(t, []int64{1}, history[1].ItemIDs)
	require.NotNil(t, history[1].Result)
	require.Equal(t, []OutPoint{{TxID: "fund", Vout: 1}}, history[1].Result.Inputs)
}
