package framework

import (
	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type ExecutionKind byte

const (
	ExecutionKindDeploy ExecutionKind = iota + 1
	ExecutionKindInvoke
	ExecutionKindTrigger
)

type ResultFeeMode byte

const (
	ResultFeeModeGasAsset ResultFeeMode = iota
	ResultFeeModePlainTxFee
)

type AssetIntent struct {
	CallID      string
	IntentIndex uint32
	From        contract.ContractAddress
	To          string
	AssetName   string
	Amount      *scommon.Decimal
	ExtraData   []byte
}

type ExecutionRecord struct {
	Height             int64
	TxID               string
	Type               contract.TxType
	Kind               ExecutionKind
	CallID             string
	TriggerID          string
	Contract           contract.ContractAddress
	Status             contract.ResultStatus
	GasLimit           int64
	GasUsed            int64
	GasFee             *scommon.Decimal
	FundingInputs      []OutPoint
	ItemIDs            []int64
	GasRefundRecipient string
	AssetIntents       []AssetIntent
	RequiresResult     bool
	ResultFeeMode      ResultFeeMode
}

func CloneExecutionRecords(in []ExecutionRecord) []ExecutionRecord {
	out := make([]ExecutionRecord, len(in))
	copy(out, in)
	for i := range out {
		out[i].FundingInputs = append([]OutPoint(nil), out[i].FundingInputs...)
		out[i].ItemIDs = append([]int64(nil), out[i].ItemIDs...)
		out[i].GasFee = CloneDecimal(out[i].GasFee)
		out[i].AssetIntents = CloneAssetIntents(out[i].AssetIntents)
	}
	return out
}

func CloneAssetIntents(in []AssetIntent) []AssetIntent {
	out := make([]AssetIntent, len(in))
	copy(out, in)
	for i := range out {
		out[i].ExtraData = CloneBytes(out[i].ExtraData)
		out[i].Amount = CloneDecimal(out[i].Amount)
	}
	return out
}

type ExecutionResult struct {
	ModuleType     ModuleType
	Records        []ExecutionRecord
	PendingRecords []ExecutionRecord
	StateRoot      [32]byte
	PostState      any
}

type BackendBlockExecutionResult struct {
	Records         []ExecutionRecord
	PendingRecords  []ExecutionRecord
	SettlementPlans []*SettlementPlan
	ResultPlans     []ResultPlan
	StateRoot       [32]byte
}

type BackendBlockResultBuildResult struct {
	ResultTxs []*wire.MsgTx
	Execution BackendBlockExecutionResult
}

func (r ExecutionResult) Root() [32]byte {
	return r.StateRoot
}

func (r ExecutionResult) Snapshot() any {
	return r.PostState
}

func NewExecutionResult(moduleType ModuleType, records []ExecutionRecord,
	pending []ExecutionRecord, stateRoot [32]byte, postState any) ExecutionResult {

	return ExecutionResult{
		ModuleType:     moduleType,
		Records:        CloneExecutionRecords(records),
		PendingRecords: CloneExecutionRecords(pending),
		StateRoot:      stateRoot,
		PostState:      postState,
	}
}

func ResultStateRoot(parentRoot, stateRoot [32]byte, recordCount, resultTxCount int) [32]byte {
	if recordCount == 0 && resultTxCount == 0 && stateRoot == parentRoot {
		return [32]byte{}
	}
	return stateRoot
}

type WorkExecutionRequest struct {
	Txs    []*wire.MsgTx
	Prefix string
}
