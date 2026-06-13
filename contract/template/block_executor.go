package template

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
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
	GasFee         *scommon.Decimal
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
		return e.executeDefaultInvokes(tx)
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

func (e *BlockExecutor) executeDefaultInvokes(tx *wire.MsgTx) error {
	outputs, err := contractcommon.FindDefaultInvokeOutputs(tx, e.ContractPrefix, ContractTypeTemplate)
	if err != nil || len(outputs) == 0 {
		return err
	}
	for _, output := range outputs {
		converted := templateOutputFromDefault(output)
		if err := e.executeDefaultInvokeOutput(tx, converted); err != nil {
			return err
		}
	}
	return nil
}

func (e *BlockExecutor) executeDefaultInvokeOutput(tx *wire.MsgTx, output ContractOutput) error {
	if output.Contract.ContractType() != ContractTypeTemplate {
		return nil
	}
	runtime, ok := e.Store.Get(output.Contract)
	if !ok {
		return errors.New("default invoke target contract does not exist")
	}
	invoker := ""
	parsed := ParsedTx{Type: TxTypeInvoke, ContractOutputs: []ContractOutput{output}, Inputs: msgTxInputs(tx)}
	if e.ResolveInvoker != nil {
		var err error
		invoker, err = e.ResolveInvoker(tx, parsed)
		if err != nil {
			return err
		}
	}
	invokeFee, err := e.GasConfig.InvokeFee(e.BlockHeight)
	if err != nil {
		return err
	}
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	requiredGas, err := addGasFees(invokeFee, resultFee)
	if err != nil {
		return err
	}
	hasRequiredGas, err := defaultInvokeHasRequiredGas(output, e.GasConfig.normalized().GasAssetName, requiredGas)
	if err != nil {
		return err
	}
	item, err := runtime.ApplyDefaultInvoke(ApplyInvokeRequest{
		Action:                contractcommon.ContractInvokeAPIDefault,
		CallID:                DeriveInvokeCallID(tx.TxID(), output.Vout, output.Contract),
		Invoker:               invoker,
		FundingOutputs:        []ContractOutput{output},
		Height:                e.BlockHeight,
		Timestamp:             e.BlockHeight,
		ResultGasFee:          gasFeeIf(hasRequiredGas, resultFee),
		ApplyDefaultRetention: !hasRequiredGas,
	})
	if err != nil {
		return err
	}
	if item == nil {
		return nil
	}
	if err := runtime.ApplyGasFunding([]ContractOutput{output}, e.GasConfig.normalized().GasAssetName); err != nil {
		return err
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	runtime.IncrementInvokeCount()
	record := ExecutionRecord{
		Height:         e.BlockHeight,
		TxID:           tx.TxID(),
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		CallID:         DeriveInvokeCallID(tx.TxID(), output.Vout, output.Contract),
		Contract:       output.Contract,
		GasLimit:       e.GasConfig.normalized().InvokeBaseGas,
		FundingInputs:  []OutPoint{output.OutPoint},
		ItemIDs:        []int64{item.ID},
		RequiresResult: true,
	}
	record.GasFee = gasFeeIf(hasRequiredGas, resultFee)
	e.records = append(e.records, record)
	return nil
}

func addGasFees(a, b *scommon.Decimal) (*scommon.Decimal, error) {
	if a == nil || a.Sign() == 0 {
		if b == nil {
			return nil, nil
		}
		return b.Clone(), nil
	}
	if b == nil || b.Sign() == 0 {
		return a.Clone(), nil
	}
	return a.AddAlignPrecision(b), nil
}

func gasFeeIf(ok bool, fee *scommon.Decimal) *scommon.Decimal {
	if ok {
		return fee.Clone()
	}
	return nil
}

func defaultInvokeHasRequiredGas(output ContractOutput, gasAssetName string, required *scommon.Decimal) (bool, error) {
	if required == nil || required.Sign() == 0 {
		return true, nil
	}
	if gasAssetName == "" {
		return false, nil
	}
	amount, err := output.AssetAmount(gasAssetName)
	if err != nil {
		return false, err
	}
	return amount != nil && amount.Cmp(required) >= 0, nil
}

func fundingOutputsHaveGas(outputs []ContractOutput, gasAssetName string, required *scommon.Decimal) (bool, error) {
	if required == nil || required.Sign() == 0 {
		return true, nil
	}
	if gasAssetName == "" {
		return false, nil
	}
	total := scommon.NewDecimal(0, required.Precision)
	for _, output := range outputs {
		amount, err := output.AssetAmount(gasAssetName)
		if err != nil {
			return false, err
		}
		if amount != nil {
			total = total.AddAlignPrecision(amount)
		}
	}
	return total.Cmp(required) >= 0, nil
}

func templateOutputFromDefault(output contractcommon.DefaultInvokeOutput) ContractOutput {
	return ContractOutput{
		OutPoint: OutPoint{TxID: output.TxID, Vout: output.Vout},
		Vout:     output.Vout,
		Contract: output.Contract,
		Value:    output.Value,
		Assets:   output.Assets.Clone(),
		PkScript: cloneBytes(output.PkScript),
	}
}

func msgTxInputs(tx *wire.MsgTx) []OutPoint {
	inputs := make([]OutPoint, 0, len(tx.TxIn))
	for _, txIn := range tx.TxIn {
		if txIn == nil {
			continue
		}
		inputs = append(inputs, WireOutPointToTemplate(txIn.PreviousOutPoint))
	}
	return inputs
}

func (e *BlockExecutor) Finalize() (BlockExecutionResult, error) {
	plans, err := e.Store.SettleBlockWithGasConfig(e.BlockHeight, e.GasConfig.normalized())
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
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	invoker := ""
	if e.ResolveInvoker != nil {
		invoker, err = e.ResolveInvoker(tx, parsed)
		if err != nil {
			return err
		}
	}
	callID := DeriveInvokeCallID(tx.TxID(), validated.FundingOutputs[0].Vout, validated.Contract)
	if err := runtime.CheckInvoke(validated.Payload.Action, validated.Payload.Param); err != nil {
		return e.executeInvalidInvoke(tx, runtime, validated, invoker, callID, resultFee)
	}
	if err := runtime.CheckInvokeFunding(validated.Payload.Action, validated.Payload.Param, validated.FundingOutputs); err != nil {
		return e.executeInvalidInvoke(tx, runtime, validated, invoker, callID, resultFee)
	}
	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:         validated.Payload.Action,
		Param:          validated.Payload.Param,
		CallID:         callID,
		Invoker:        invoker,
		FundingOutputs: validated.FundingOutputs,
		Height:         e.BlockHeight,
		Timestamp:      e.BlockHeight,
		ResultGasFee:   resultFee,
	})
	if err != nil {
		return err
	}
	if err := runtime.ApplyGasFunding(validated.FundingOutputs, e.GasConfig.GasAssetName); err != nil {
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
		CallID:         callID,
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

func (e *BlockExecutor) executeInvalidInvoke(tx *wire.MsgTx, runtime *ContractRuntime, validated InvokeValidation, invoker string, callID string, resultFee *scommon.Decimal) error {
	var invalidResultFee *scommon.Decimal
	hasResultGas, err := fundingOutputsHaveGas(validated.FundingOutputs, e.GasConfig.normalized().GasAssetName, resultFee)
	if err != nil {
		return err
	}
	if hasResultGas {
		invalidResultFee = resultFee.Clone()
	}
	item, err := runtime.ApplyInvalidInvoke(ApplyInvokeRequest{
		Action:         validated.Payload.Action,
		Param:          validated.Payload.Param,
		CallID:         callID,
		Invoker:        invoker,
		FundingOutputs: validated.FundingOutputs,
		Height:         e.BlockHeight,
		Timestamp:      e.BlockHeight,
		ResultGasFee:   invalidResultFee,
	}, e.GasConfig.normalized().GasAssetName)
	if err != nil {
		return err
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	runtime.IncrementInvokeCount()
	record := ExecutionRecord{
		Height:         e.BlockHeight,
		TxID:           tx.TxID(),
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		CallID:         callID,
		Contract:       validated.Contract,
		Status:         ResultPayload{Status: ResultStatusInvalid},
		GasLimit:       validated.Payload.GasLimit,
		GasFee:         invalidResultFee,
		FundingInputs:  contractOutputOutPoints(validated.FundingOutputs),
		ItemIDs:        []int64{item.ID},
		RequiresResult: true,
	}
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
		out[i].GasFee = out[i].GasFee.Clone()
	}
	return out
}
