package agent

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type InvokerResolver func(tx *wire.MsgTx, parsed ParsedTx) (string, error)

type BlockExecutionRequest struct {
	Txs            []*wire.MsgTx
	Store          *RuntimeStore
	ContractPrefix string
	RuntimeConfig  RuntimeConfig
	GasConfig      GasConfig
	BlockHeight    int64
	BlockTime      int64
	ResolveInvoker InvokerResolver
}

type BlockExecutionResult struct {
	Records         []ExecutionRecord
	SettlementPlans []*PredictionSettlementPlan
	ResultPlans     []ResultPlan
	StateRoot       [32]byte
}

type ExecutionKind byte

const (
	ExecutionKindDeploy ExecutionKind = iota + 1
	ExecutionKindInvoke
)

type ExecutionRecord struct {
	Height         int64
	TxID           string
	Type           TxType
	Kind           ExecutionKind
	CallID         string
	Contract       ContractAddress
	Status         ResultPayload
	GasLimit       uint64
	GasFee         *scommon.Decimal
	FundingInputs  []OutPoint
	RequiresResult bool
}

type BlockExecutor struct {
	Store          *RuntimeStore
	ContractPrefix string
	RuntimeConfig  RuntimeConfig
	GasConfig      GasConfig
	BlockHeight    int64
	BlockTime      int64
	ResolveInvoker InvokerResolver

	records         []ExecutionRecord
	settlementPlans []*PredictionSettlementPlan
	resultPlans     []ResultPlan
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
	store.ApplyConfig(req.RuntimeConfig)
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	return &BlockExecutor{
		Store:          store,
		ContractPrefix: prefix,
		RuntimeConfig:  req.RuntimeConfig,
		GasConfig:      req.GasConfig,
		BlockHeight:    req.BlockHeight,
		BlockTime:      req.BlockTime,
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
		return e.executeDefaultInvokes(tx)
	case TxTypeDeploy:
		return e.executeDeploy(tx)
	case TxTypeInvoke:
		return e.executeInvoke(tx, parsed)
	case TxTypeResult:
		return errors.New("agent RESULT transactions are built by block execution and are not accepted as external input")
	case TxTypeCoinbaseStateRoot:
		return nil
	default:
		return fmt.Errorf("unsupported agent tx type %d", parsed.Type)
	}
}

func (e *BlockExecutor) executeDefaultInvokes(tx *wire.MsgTx) error {
	outputs, err := contractcommon.FindDefaultInvokeOutputs(tx, e.ContractPrefix, ContractTypeAgent)
	if err != nil || len(outputs) == 0 {
		return err
	}
	for _, output := range outputs {
		converted := agentOutputFromDefault(output)
		if err := e.executeDefaultInvokeOutput(converted); err != nil {
			return err
		}
	}
	return nil
}

func (e *BlockExecutor) executeDefaultInvokeOutput(output ContractOutput) error {
	if !e.Store.Exists(output.Contract) {
		return errors.New("default invoke target contract does not exist")
	}
	fee, err := e.GasConfig.InvokeFee(e.BlockHeight)
	if err != nil {
		return err
	}
	if err := requireAgentDefaultInvokeGas(output, e.GasConfig.GasAssetName, fee); err != nil {
		return err
	}
	return nil
}

func requireAgentDefaultInvokeGas(output ContractOutput, gasAssetName string, fee *scommon.Decimal) error {
	if fee == nil || fee.Sign() == 0 || gasAssetName == "" {
		return nil
	}
	gas, err := output.AssetAmount(gasAssetName)
	if err != nil {
		return err
	}
	if gas.Cmp(fee) < 0 {
		return fmt.Errorf("default agent invoke output %s gas %s below required %s", output.OutPoint, gas.String(), fee.String())
	}
	return nil
}

func agentOutputFromDefault(output contractcommon.DefaultInvokeOutput) ContractOutput {
	return ContractOutput{
		OutPoint: OutPoint{TxID: output.TxID, Vout: output.Vout},
		Vout:     output.Vout,
		Contract: output.Contract,
		Value:    output.Value,
		Assets:   output.Assets.Clone(),
		PkScript: cloneBytes(output.PkScript),
	}
}

func (e *BlockExecutor) Finalize() (BlockExecutionResult, error) {
	resultPlans := AddGasFeesToResultPlans(e.resultPlans, e.records)
	return BlockExecutionResult{
		Records:         cloneExecutionRecords(e.records),
		SettlementPlans: cloneSettlementPlans(e.settlementPlans),
		ResultPlans:     cloneResultPlans(resultPlans),
		StateRoot:       e.Store.StateRoot(),
	}, nil
}

func (e *BlockExecutor) executeDeploy(tx *wire.MsgTx) error {
	validated, err := ValidateDeployTxBasic(tx, e.ContractPrefix, e.RuntimeConfig, e.GasConfig)
	if err != nil {
		return err
	}
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	e.Store.Add(validated.Runtime)
	record := ExecutionRecord{
		Height:        e.BlockHeight,
		TxID:          tx.TxID(),
		Type:          TxTypeDeploy,
		Kind:          ExecutionKindDeploy,
		CallID:        DeriveDeployCallID(tx.TxID(), validated.Address),
		Contract:      validated.Address,
		Status:        ResultPayload{},
		GasLimit:      validated.Payload.GasLimit,
		FundingInputs: contractOutputOutPoints(validated.FundingOutputs),
	}
	e.records = append(e.records, record)
	if resultPlan, ok := stateResultPlan(validated.Address, validated.FundingOutputs); ok {
		record.RequiresResult = true
		record.GasFee = resultFee
		e.records[len(e.records)-1] = record
		e.resultPlans = append(e.resultPlans, resultPlan)
	}
	return nil
}

func (e *BlockExecutor) executeInvoke(tx *wire.MsgTx, parsed ParsedTx) error {
	validated, err := ValidateParsedInvokeTxBasic(parsed, e.Store.Exists, e.GasConfig)
	if err != nil {
		return err
	}
	runtime, ok := e.Store.Get(validated.Contract)
	if !ok {
		return errors.New("invoke target contract does not exist")
	}
	invoker := ""
	if e.ResolveInvoker != nil {
		invoker, err = e.ResolveInvoker(tx, parsed)
		if err != nil {
			return err
		}
	}

	var settlement *PredictionSettlementPlan
	switch validated.Payload.Action {
	case InvokeAPIReady:
		err = runtime.ApplyReady(ApplyReadyRequest{Invoker: invoker})
	case InvokeAPIReject:
		err = e.applyReject(runtime, validated, invoker)
	case InvokeAPIBet:
		settlement, err = e.applyBet(runtime, validated, invoker)
	case InvokeAPIConfirm:
		settlement, err = e.applyConfirm(runtime, validated, invoker)
	default:
		err = fmt.Errorf("unsupported agent action %s", validated.Payload.Action)
	}
	if err != nil {
		return err
	}
	if settlement != nil {
		e.settlementPlans = append(e.settlementPlans, settlement)
		resultPlan, err := BuildSettlementResultPlan(settlement)
		if err != nil {
			return err
		}
		e.resultPlans = append(e.resultPlans, resultPlan)
	}

	requiresResult := settlement != nil
	var readyResultPlan ResultPlan
	if validated.Payload.Action == InvokeAPIReady || validated.Payload.Action == InvokeAPIReject {
		var ok bool
		readyResultPlan, ok = stateResultPlan(validated.Contract, validated.FundingOutputs)
		requiresResult = ok
	}
	record := ExecutionRecord{
		Height:         e.BlockHeight,
		TxID:           tx.TxID(),
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		CallID:         DeriveInvokeCallID(tx.TxID(), validated.FundingOutputs[0].Vout, validated.Contract),
		Contract:       validated.Contract,
		GasLimit:       validated.Payload.GasLimit,
		FundingInputs:  contractOutputOutPoints(validated.FundingOutputs),
		RequiresResult: requiresResult,
	}
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	if record.RequiresResult {
		record.GasFee = resultFee
	}
	e.records = append(e.records, record)
	if (validated.Payload.Action == InvokeAPIReady || validated.Payload.Action == InvokeAPIReject) && requiresResult {
		e.resultPlans = append(e.resultPlans, readyResultPlan)
	}
	return nil
}

func (e *BlockExecutor) applyReject(runtime *Runtime, validated InvokeValidation, invoker string) error {
	param, err := DecodePredictionRejectParam(validated.Payload.Param)
	if err != nil {
		return err
	}
	return runtime.ApplyReject(ApplyRejectRequest{
		Invoker: invoker,
		Param:   param,
	})
}

func (e *BlockExecutor) applyBet(runtime *Runtime, validated InvokeValidation, invoker string) (*PredictionSettlementPlan, error) {
	param, err := DecodePredictionBetParam(validated.Payload.Param)
	if err != nil {
		return nil, err
	}
	amount, err := fundingAmount(validated.FundingOutputs, runtime.Contract().BetAsset)
	if err != nil {
		return nil, err
	}
	return nil, runtime.ApplyBet(ApplyBetRequest{
		Invoker:   invoker,
		Param:     param,
		AssetName: runtime.Contract().BetAsset,
		Amount:    amount,
		TimeValue: e.predictionTimeValue(runtime.Contract()),
	})
}

func (e *BlockExecutor) applyConfirm(runtime *Runtime, validated InvokeValidation, invoker string) (*PredictionSettlementPlan, error) {
	param, err := DecodePredictionConfirmParam(validated.Payload.Param)
	if err != nil {
		return nil, err
	}
	settlement, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker:   invoker,
		Param:     param,
		TimeValue: e.predictionTimeValue(runtime.Contract()),
	})
	return settlement, err
}

func (e *BlockExecutor) predictionTimeValue(contract PredictionContract) int64 {
	if contract.TimeBase != TimeBaseUnix {
		return e.BlockHeight
	}
	if e.BlockTime != 0 {
		return e.BlockTime
	}
	return e.BlockHeight
}

func fundingAmount(outputs []ContractOutput, assetName string) (string, error) {
	total := zeroDecimal()
	for _, output := range outputs {
		amount, err := output.AssetAmount(assetName)
		if err != nil {
			return "", err
		}
		total = decimalAdd(total, amount)
	}
	return total.String(), nil
}

func contractOutputOutPoints(outputs []ContractOutput) []OutPoint {
	out := make([]OutPoint, 0, len(outputs))
	for _, output := range outputs {
		out = append(out, output.OutPoint)
	}
	return out
}

func stateResultPlan(contract ContractAddress, outputs []ContractOutput) (ResultPlan, bool) {
	inputs := make([]OutPoint, 0, len(outputs))
	for _, output := range outputs {
		if output.Value != 0 || len(output.Assets) != 0 {
			continue
		}
		inputs = append(inputs, output.OutPoint)
	}
	if len(inputs) == 0 {
		return ResultPlan{}, false
	}
	return ResultPlan{
		Contract: contract.EncodeAddress(),
		Inputs:   uniqueOutPoints(append([]OutPoint(nil), inputs...)),
	}, true
}

func cloneExecutionRecords(in []ExecutionRecord) []ExecutionRecord {
	out := make([]ExecutionRecord, len(in))
	copy(out, in)
	for i := range out {
		out[i].FundingInputs = append([]OutPoint(nil), in[i].FundingInputs...)
		out[i].GasFee = out[i].GasFee.Clone()
	}
	return out
}

func cloneSettlementPlans(in []*PredictionSettlementPlan) []*PredictionSettlementPlan {
	out := make([]*PredictionSettlementPlan, 0, len(in))
	for _, plan := range in {
		if plan == nil {
			out = append(out, nil)
			continue
		}
		cp := *plan
		cp.Transfers = append([]PredictionSettlementOutput(nil), plan.Transfers...)
		out = append(out, &cp)
	}
	return out
}
