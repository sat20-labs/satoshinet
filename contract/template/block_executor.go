package template

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type InvokerResolver func(tx *wire.MsgTx, parsed ParsedTx) (string, error)

type BlockExecutionRequest struct {
	Txs            []*wire.MsgTx
	Store          *RuntimeStore
	Registry       *Registry
	ContractPrefix string
	GasConfig      GasConfig
	BlockHeight    int64
	ResolveInvoker InvokerResolver
}

type BlockExecutionResult struct {
	Records         []ExecutionRecord
	SettlementPlans []*SettlementPlan
	ResultPlans     []ResultPlan
	StateRoot       [32]byte
}

type ExecutionRecord struct {
	Height         int64
	TxID           string
	Type           TxType
	Kind           ExecutionKind
	CallID         string
	Contract       ContractAddress
	Status         ResultPayload
	GasLimit       uint64
	GasFee         uint64
	FundingInputs  []OutPoint
	ItemIDs        []int64
	RequiresResult bool
}

type BlockExecutor struct {
	Store          *RuntimeStore
	Registry       *Registry
	ContractPrefix string
	GasConfig      GasConfig
	BlockHeight    int64
	ResolveInvoker InvokerResolver

	records []ExecutionRecord
}

func ExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	executor := NewBlockExecutor(req)
	for _, tx := range req.Txs {
		if err := executor.ExecuteTx(tx); err != nil {
			return BlockExecutionResult{}, err
		}
	}
	return executor.Finalize()
}

func NewBlockExecutor(req BlockExecutionRequest) *BlockExecutor {
	store := req.Store
	if store == nil {
		store = NewRuntimeStore()
	}
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	return &BlockExecutor{
		Store:          store,
		Registry:       req.Registry,
		ContractPrefix: prefix,
		GasConfig:      req.GasConfig,
		BlockHeight:    req.BlockHeight,
		ResolveInvoker: req.ResolveInvoker,
	}
}

func (e *BlockExecutor) ExecuteTx(tx *wire.MsgTx) error {
	parsed, err := ParseTx(tx, StandardContractScriptResolver(e.ContractPrefix))
	if err != nil {
		return err
	}
	return e.ExecuteParsedTx(tx, parsed)
}

func (e *BlockExecutor) ExecuteParsedTx(tx *wire.MsgTx, parsed ParsedTx) error {
	switch parsed.Type {
	case 0:
		return nil
	case TxTypeDeploy:
		return e.executeDeploy(tx)
	case TxTypeInvoke:
		return e.executeInvoke(tx)
	case TxTypeResult:
		return errors.New("template RESULT transactions are built by block execution and are not accepted as external input")
	case TxTypeCoinbaseStateRoot:
		return nil
	default:
		return fmt.Errorf("unsupported template tx type %d", parsed.Type)
	}
}

func (e *BlockExecutor) Finalize() (BlockExecutionResult, error) {
	plans, err := e.Store.SettleBlock(e.BlockHeight)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	resultPlans, err := BuildSettlementResultPlans(plans, e.records)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	return BlockExecutionResult{
		Records:         cloneExecutionRecords(e.records),
		SettlementPlans: cloneSettlementPlans(plans),
		ResultPlans:     cloneResultPlans(resultPlans),
		StateRoot:       e.Store.StateRoot(),
	}, nil
}

func (e *BlockExecutor) Records() []ExecutionRecord {
	return cloneExecutionRecords(e.records)
}

func (e *BlockExecutor) executeDeploy(tx *wire.MsgTx) error {
	validated, err := ValidateDeployTxBasic(tx, e.ContractPrefix, e.Registry, e.GasConfig)
	if err != nil {
		return err
	}
	validated.Runtime.SetCurrentBlock(e.BlockHeight)
	if err := validated.Runtime.ApplyFunding(validated.FundingOutputs, e.GasConfig.GasAssetName); err != nil {
		return err
	}
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	e.Store.Add(validated.Runtime)
	record := ExecutionRecord{
		Height:         e.BlockHeight,
		TxID:           tx.TxID(),
		Type:           TxTypeDeploy,
		Kind:           ExecutionKindDeploy,
		CallID:         DeriveDeployCallID(tx.TxID(), validated.Address),
		Contract:       validated.Address,
		Status:         ResultPayload{},
		GasLimit:       validated.Payload.GasLimit,
		FundingInputs:  contractOutputOutPoints(validated.FundingOutputs),
		RequiresResult: true,
	}
	record.GasFee = resultFee
	e.records = append(e.records, record)
	return nil
}

func (e *BlockExecutor) executeInvoke(tx *wire.MsgTx) error {
	parsed, err := ParseTx(tx, StandardContractScriptResolver(e.ContractPrefix))
	if err != nil {
		return err
	}
	validated, err := ValidateParsedInvokeTxBasic(parsed, e.Store.Exists, e.GasConfig)
	if err != nil {
		return err
	}
	runtime, ok := e.Store.Get(validated.Contract)
	if !ok {
		return errors.New("invoke target contract does not exist")
	}
	if err := runtime.CheckInvoke(validated.Payload.Action, validated.Payload.Param); err != nil {
		return err
	}
	invoker := ""
	if e.ResolveInvoker != nil {
		invoker, err = e.ResolveInvoker(tx, parsed)
		if err != nil {
			return err
		}
	}
	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:         validated.Payload.Action,
		Param:          validated.Payload.Param,
		CallID:         DeriveInvokeCallID(tx.TxID(), validated.FundingOutputs[0].Vout, validated.Contract),
		Invoker:        invoker,
		FundingOutputs: validated.FundingOutputs,
		Height:         e.BlockHeight,
		Timestamp:      e.BlockHeight,
	})
	if err != nil {
		return err
	}
	if err := runtime.ApplyGasFunding(validated.FundingOutputs, e.GasConfig.GasAssetName); err != nil {
		return err
	}
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	runtime.IncrementInvokeCount()

	funding := make([]OutPoint, 0, len(validated.FundingOutputs))
	for _, output := range validated.FundingOutputs {
		funding = append(funding, output.OutPoint)
	}
	record := ExecutionRecord{
		Height:         e.BlockHeight,
		TxID:           tx.TxID(),
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		CallID:         DeriveInvokeCallID(tx.TxID(), validated.FundingOutputs[0].Vout, validated.Contract),
		Contract:       validated.Contract,
		GasLimit:       validated.Payload.GasLimit,
		FundingInputs:  funding,
		ItemIDs:        []int64{item.ID},
		RequiresResult: true,
	}
	record.GasFee = resultFee
	e.records = append(e.records, record)
	return nil
}

func contractOutputOutPoints(outputs []ContractOutput) []OutPoint {
	out := make([]OutPoint, 0, len(outputs))
	for _, output := range outputs {
		out = append(out, output.OutPoint)
	}
	return out
}

func cloneExecutionRecords(in []ExecutionRecord) []ExecutionRecord {
	out := make([]ExecutionRecord, len(in))
	copy(out, in)
	for i := range out {
		out[i].FundingInputs = append([]OutPoint(nil), out[i].FundingInputs...)
		out[i].ItemIDs = append([]int64(nil), out[i].ItemIDs...)
	}
	return out
}
