package template

const (
	HistoryKindDeploy     = "deploy"
	HistoryKindInvoke     = "invoke"
	HistoryKindSettlement = "settlement"
)

type HistoryRecord struct {
	Kind           string          `json:"kind"`
	Height         int64           `json:"height"`
	TxID           string          `json:"txId,omitempty"`
	Type           TxType          `json:"type,omitempty"`
	ExecutionKind  ExecutionKind   `json:"executionKind,omitempty"`
	CallID         string          `json:"callId,omitempty"`
	Contract       string          `json:"contract"`
	Status         ResultPayload   `json:"status,omitempty"`
	GasLimit       uint64          `json:"gasLimit,omitempty"`
	FundingInputs  []OutPoint      `json:"fundingInputs,omitempty"`
	ItemIDs        []int64         `json:"itemIds,omitempty"`
	RequiresResult bool            `json:"requiresResult,omitempty"`
	Settlement     *SettlementPlan `json:"settlement,omitempty"`
	Result         *ResultPlan     `json:"result,omitempty"`
}

func BlockHistoryRecords(result BlockExecutionResult, resultTxIDs map[string]string) []HistoryRecord {
	out := ExecutionHistoryRecords(result.Records)
	resultPlans := make(map[string]ResultPlan)
	for _, plan := range result.ResultPlans {
		resultPlans[plan.Contract] = plan
	}
	for _, plan := range result.SettlementPlans {
		txid := ""
		if resultTxIDs != nil && plan != nil {
			txid = resultTxIDs[plan.Contract]
		}
		record := SettlementHistoryRecord(txid, plan)
		if plan != nil {
			if resultPlan, ok := resultPlans[plan.Contract]; ok {
				cloned := cloneResultPlan(resultPlan)
				record.Result = &cloned
			}
		}
		out = append(out, record)
	}
	return out
}

func ExecutionHistoryRecords(records []ExecutionRecord) []HistoryRecord {
	out := make([]HistoryRecord, 0, len(records))
	for _, record := range records {
		out = append(out, ExecutionHistoryRecord(record))
	}
	return out
}

func ExecutionHistoryRecord(record ExecutionRecord) HistoryRecord {
	kind := HistoryKindInvoke
	if record.Kind == ExecutionKindDeploy {
		kind = HistoryKindDeploy
	}
	return HistoryRecord{
		Kind:           kind,
		Height:         record.Height,
		TxID:           record.TxID,
		Type:           record.Type,
		ExecutionKind:  record.Kind,
		CallID:         record.CallID,
		Contract:       record.Contract.EncodeAddress(),
		Status:         record.Status,
		GasLimit:       record.GasLimit,
		FundingInputs:  append([]OutPoint(nil), record.FundingInputs...),
		ItemIDs:        append([]int64(nil), record.ItemIDs...),
		RequiresResult: record.RequiresResult,
	}
}

func SettlementHistoryRecord(resultTxID string, plan *SettlementPlan) HistoryRecord {
	if plan == nil {
		return HistoryRecord{Kind: HistoryKindSettlement, TxID: resultTxID}
	}
	return HistoryRecord{
		Kind:       HistoryKindSettlement,
		Height:     plan.Height,
		TxID:       resultTxID,
		Contract:   plan.Contract,
		ItemIDs:    append([]int64(nil), plan.ItemIDs...),
		Settlement: cloneSettlementPlan(plan),
	}
}

func cloneSettlementPlan(plan *SettlementPlan) *SettlementPlan {
	if plan == nil {
		return nil
	}
	out := *plan
	out.Deals = append([]SettlementDeal(nil), plan.Deals...)
	out.Transfers = append([]SettlementTransfer(nil), plan.Transfers...)
	out.ItemIDs = append([]int64(nil), plan.ItemIDs...)
	out.Inputs = append([]OutPoint(nil), plan.Inputs...)
	return &out
}
