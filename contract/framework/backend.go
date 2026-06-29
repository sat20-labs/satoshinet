package framework

import (
	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractAssetView interface {
	ContractUTXOs(contract.ContractAddress) ([]UTXO, error)
}

type ExecutionContext struct {
	Prefix    string
	Height    int64
	Time      int64
	GasConfig GasConfig
	Assets    ContractAssetView
	RawTx     *wire.MsgTx
	ParsedTx  ParsedTx
}

type Backend interface {
	ContractType() byte
	Name() string
	Priority() int

	Deploy(ctx ExecutionContext, tx contract.Tx) (ExecutionOutcome, error)
	Invoke(ctx ExecutionContext, tx contract.Tx) (ExecutionOutcome, error)
	DefaultInvoke(ctx ExecutionContext, tx contract.Tx, funding contract.FundingOutput) (ExecutionOutcome, bool, error)
	FinalizeBlock(ctx ExecutionContext) ([]ExecutionOutcome, error)

	StateRoot() [32]byte
	Snapshot() any
}

type ExecutionOutcome struct {
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
	CloseContract      bool
	DeployerAddress    string
	BootstrapAddress   string
}

func (o ExecutionOutcome) Clone() ExecutionOutcome {
	out := o
	out.GasFee = CloneDecimal(o.GasFee)
	out.FundingInputs = append([]OutPoint(nil), o.FundingInputs...)
	out.ItemIDs = append([]int64(nil), o.ItemIDs...)
	out.AssetIntents = CloneAssetIntents(o.AssetIntents)
	return out
}

func (o ExecutionOutcome) ToRecord() ExecutionRecord {
	return ExecutionRecord{
		Height:             o.Height,
		TxID:               o.TxID,
		Type:               o.Type,
		Kind:               o.Kind,
		CallID:             o.CallID,
		TriggerID:          o.TriggerID,
		Contract:           o.Contract,
		Status:             o.Status,
		GasLimit:           o.GasLimit,
		GasUsed:            o.GasUsed,
		GasFee:             CloneDecimal(o.GasFee),
		FundingInputs:      append([]OutPoint(nil), o.FundingInputs...),
		ItemIDs:            append([]int64(nil), o.ItemIDs...),
		GasRefundRecipient: o.GasRefundRecipient,
		AssetIntents:       CloneAssetIntents(o.AssetIntents),
		RequiresResult:     o.RequiresResult,
		ResultFeeMode:      o.ResultFeeMode,
		CloseContract:      o.CloseContract,
		DeployerAddress:    o.DeployerAddress,
		BootstrapAddress:   o.BootstrapAddress,
	}
}

func ExecutionOutcomeFromRecord(record ExecutionRecord) ExecutionOutcome {
	return ExecutionOutcome{
		Height:             record.Height,
		TxID:               record.TxID,
		Type:               record.Type,
		Kind:               record.Kind,
		CallID:             record.CallID,
		TriggerID:          record.TriggerID,
		Contract:           record.Contract,
		Status:             record.Status,
		GasLimit:           record.GasLimit,
		GasUsed:            record.GasUsed,
		GasFee:             CloneDecimal(record.GasFee),
		FundingInputs:      append([]OutPoint(nil), record.FundingInputs...),
		ItemIDs:            append([]int64(nil), record.ItemIDs...),
		GasRefundRecipient: record.GasRefundRecipient,
		AssetIntents:       CloneAssetIntents(record.AssetIntents),
		RequiresResult:     record.RequiresResult,
		ResultFeeMode:      record.ResultFeeMode,
		CloseContract:      record.CloseContract,
		DeployerAddress:    record.DeployerAddress,
		BootstrapAddress:   record.BootstrapAddress,
	}
}

func ExecutionOutcomesFromRecords(records []ExecutionRecord) []ExecutionOutcome {
	out := make([]ExecutionOutcome, 0, len(records))
	for _, record := range records {
		out = append(out, ExecutionOutcomeFromRecord(record))
	}
	return out
}

func LastExecutionOutcomeSince(records []ExecutionRecord, before int) (ExecutionOutcome, error) {
	if len(records) <= before {
		return ExecutionOutcome{}, nil
	}
	return ExecutionOutcomeFromRecord(records[len(records)-1]), nil
}
