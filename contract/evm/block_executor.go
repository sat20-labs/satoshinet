package evm

import (
	"errors"
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type CallerResolver func(tx *wire.MsgTx, parsed ParsedTx) (EVMAddress, error)
type GasRefundRecipientResolver func(tx *wire.MsgTx, parsed ParsedTx) (recipient string, ok bool, err error)
type ResultVerifier func(resultTx *wire.MsgTx, settled []ExecutionRecord) error
type TriggerResolver func(ctx TriggerResolutionContext) ([]TriggerCall, error)

type BlockExecutionRequest struct {
	Txs                       []*wire.MsgTx
	CoinbaseTx                *wire.MsgTx
	Runtime                   *Runtime
	ContractPrefix            string
	GasConfig                 GasConfig
	Block                     BlockContext
	ResolveCaller             CallerResolver
	ResolveGasRefundRecipient GasRefundRecipientResolver
	VerifyResult              ResultVerifier
	ResolveTriggers           TriggerResolver
	ContractUTXOs             ContractUTXOProvider
	Triggers                  []TriggerCall
}

type BlockExecutionResult struct {
	Records   []ExecutionRecord
	StateRoot [32]byte
}

type ExecutionRecord struct {
	Height             uint64
	TxID               string
	Type               TxType
	Kind               ExecutionKind
	CallID             string
	TriggerID          string
	Contract           ContractAddress
	Status             ResultStatus
	GasUsed            uint64
	FundingInputs      []OutPoint
	GasRefundRecipient string
	AssetIntents       []AssetIntent
	RequiresResult     bool
	ResultFeeMode      ResultFeeMode
}

type BlockExecutor struct {
	Runtime                   *Runtime
	ContractPrefix            string
	GasConfig                 GasConfig
	Block                     BlockContext
	ResolveCaller             CallerResolver
	ResolveGasRefundRecipient GasRefundRecipientResolver
	VerifyResult              ResultVerifier
	ResolveTriggers           TriggerResolver
	ContractUTXOs             ContractUTXOProvider

	records []ExecutionRecord
	pending []ExecutionRecord
	gasUsed uint64
}

func ExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	executor := NewBlockExecutor(req)
	results := make([]*wire.MsgTx, 0)
	for _, tx := range req.Txs {
		parsed, err := ParseTx(tx, StandardContractScriptResolver(executor.ContractPrefix))
		if err != nil {
			return BlockExecutionResult{}, err
		}
		if parsed.Type == TxTypeResult {
			results = append(results, tx)
			continue
		}
		if err := executor.ExecuteParsedTx(tx, parsed); err != nil {
			return BlockExecutionResult{}, err
		}
	}
	triggers := append([]TriggerCall(nil), req.Triggers...)
	triggers = append(triggers, executor.Runtime.DueTriggerCalls(executor.Block)...)
	if req.ResolveTriggers != nil {
		resolved, err := req.ResolveTriggers(TriggerResolutionContext{
			Block:          executor.Block,
			Runtime:        executor.Runtime,
			ContractPrefix: executor.ContractPrefix,
		})
		if err != nil {
			return BlockExecutionResult{}, err
		}
		triggers = append(triggers, resolved...)
	}
	for _, trigger := range triggers {
		if err := executor.ExecuteTrigger(trigger); err != nil {
			return BlockExecutionResult{}, err
		}
	}
	for _, tx := range results {
		if err := executor.ExecuteTx(tx); err != nil {
			return BlockExecutionResult{}, err
		}
	}
	return executor.Finalize()
}

func NewBlockExecutor(req BlockExecutionRequest) *BlockExecutor {
	runtime := req.Runtime
	if runtime == nil {
		runtime = NewRuntime(nil)
	}
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	runtime.ContractPrefix = prefix
	return &BlockExecutor{
		Runtime:                   runtime,
		ContractPrefix:            prefix,
		GasConfig:                 req.GasConfig,
		Block:                     req.Block,
		ResolveCaller:             req.ResolveCaller,
		ResolveGasRefundRecipient: req.ResolveGasRefundRecipient,
		VerifyResult:              req.VerifyResult,
		ResolveTriggers:           req.ResolveTriggers,
		ContractUTXOs:             req.ContractUTXOs,
	}
}

type TriggerCall struct {
	Trigger  Trigger
	GasLimit uint64
	Calldata []byte
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
		return e.executeDeploy(tx, parsed)
	case TxTypeInvoke:
		return e.executeInvoke(tx, parsed)
	case TxTypeResult:
		return e.executeResult(tx, parsed)
	case TxTypeCoinbaseStateRoot:
		return nil
	default:
		return fmt.Errorf("unsupported EVM tx type %d", parsed.Type)
	}
}

func (e *BlockExecutor) executeDefaultInvokes(tx *wire.MsgTx) error {
	outputs, err := contractcommon.FindDefaultInvokeOutputs(tx, e.ContractPrefix, ContractTypeEVM)
	if err != nil || len(outputs) == 0 {
		return err
	}
	for _, output := range outputs {
		converted := evmOutputFromDefault(output)
		if err := e.executeDefaultInvokeOutput(tx, converted); err != nil {
			return err
		}
	}
	return nil
}

func (e *BlockExecutor) executeDefaultInvokeOutput(tx *wire.MsgTx, output ContractOutput) error {
	if !e.contractExists(output.Contract) {
		return errors.New("default invoke target contract does not exist")
	}
	parsed := ParsedTx{Type: TxTypeInvoke, ContractOutputs: []ContractOutput{output}, Inputs: msgTxInputs(tx)}
	caller, err := e.resolveCaller(tx, parsed)
	if err != nil {
		return err
	}
	callID := DeriveInvokeCallID(tx.TxID(), output.Vout, output.Contract)
	intentStart := len(e.Runtime.AssetIntents)
	result := e.Runtime.Call(CallRequest{
		Caller: caller,
		Target: ContractAddressHash(output.Contract),
		CallID: callID,
		Input:  nil,
		Gas:    e.GasConfig.normalized().InvokeBaseGas,
		Value:  uint64Value(output.Value),
		Block:  e.Block,
	})
	intents := cloneAssetIntents(e.Runtime.AssetIntents[intentStart:])
	record := ExecutionRecord{
		Height:         e.Block.Number,
		TxID:           tx.TxID(),
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		CallID:         callID,
		Contract:       output.Contract,
		Status:         result.Status,
		GasUsed:        result.GasUsed,
		FundingInputs:  []OutPoint{output.OutPoint},
		AssetIntents:   intents,
		RequiresResult: true,
		ResultFeeMode:  ResultFeeModePlainTxFee,
	}
	return e.appendRecord(record)
}

func evmOutputFromDefault(output contractcommon.DefaultInvokeOutput) ContractOutput {
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
		inputs = append(inputs, WireOutPointToEVM(txIn.PreviousOutPoint))
	}
	return inputs
}

func uint64Value(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func (e *BlockExecutor) Finalize() (BlockExecutionResult, error) {
	if len(e.pending) != 0 {
		return BlockExecutionResult{}, fmt.Errorf("%d EVM executions remain unsettled", len(e.pending))
	}
	return BlockExecutionResult{
		Records:   append([]ExecutionRecord(nil), e.records...),
		StateRoot: e.Runtime.State.StateRoot(),
	}, nil
}

func (e *BlockExecutor) PendingRecords() []ExecutionRecord {
	return cloneExecutionRecords(e.pending)
}

func (e *BlockExecutor) Records() []ExecutionRecord {
	return cloneExecutionRecords(e.records)
}

func ExecuteBlockAndVerifyStateRoot(req BlockExecutionRequest) (BlockExecutionResult, error) {
	result, err := ExecuteBlock(req)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	if req.CoinbaseTx == nil {
		return BlockExecutionResult{}, errors.New("missing coinbase transaction")
	}
	if err := VerifyCoinbaseStateRoot(req.CoinbaseTx, result.StateRoot); err != nil {
		return BlockExecutionResult{}, err
	}
	return result, nil
}

func (e *BlockExecutor) executeDeploy(tx *wire.MsgTx, parsed ParsedTx) error {
	validated, err := ValidateDeployTxBasic(tx, e.GasConfig)
	if err != nil {
		return err
	}
	caller, err := e.resolveCaller(tx, parsed)
	if err != nil {
		return err
	}
	gasRefundRecipient, err := e.resolveGasRefundRecipient(tx, parsed)
	if err != nil {
		return err
	}
	expectedContract, err := DeriveCreateContractAddress(e.ContractPrefix, caller, validated.Payload.DeployNonce)
	if err != nil {
		return err
	}
	callID := DeriveDeployCallID(tx.TxID(), expectedContract)
	intentStart := len(e.Runtime.AssetIntents)
	result := e.Runtime.Deploy(DeployRequest{
		Caller:      caller,
		CallID:      callID,
		InitCode:    validated.Payload.InitCode,
		Gas:         validated.Payload.GasLimit,
		DeployNonce: validated.Payload.DeployNonce,
		Block:       e.Block,
	})
	if !result.Contract.Equal(expectedContract) {
		return fmt.Errorf("deploy contract mismatch: got %s want %s",
			result.Contract.MustEncode(), expectedContract.MustEncode())
	}
	fundingOutputs, err := FindContractOutputsForContract(tx,
		StandardContractScriptResolver(e.ContractPrefix), expectedContract)
	if err != nil {
		return err
	}
	if len(fundingOutputs) == 0 {
		return errors.New("EVM_DEPLOY has no contract funding output")
	}
	funding := make([]OutPoint, 0, len(fundingOutputs))
	for _, output := range fundingOutputs {
		funding = append(funding, output.OutPoint)
	}
	record := ExecutionRecord{
		Height:             e.Block.Number,
		TxID:               tx.TxID(),
		Type:               TxTypeDeploy,
		Kind:               ExecutionKindDeploy,
		CallID:             callID,
		Contract:           result.Contract,
		Status:             result.Status,
		GasUsed:            result.GasUsed,
		FundingInputs:      funding,
		GasRefundRecipient: gasRefundRecipient,
		AssetIntents:       cloneAssetIntents(e.Runtime.AssetIntents[intentStart:]),
		RequiresResult:     true,
	}
	return e.appendRecord(record)
}

func (e *BlockExecutor) executeInvoke(tx *wire.MsgTx, parsed ParsedTx) error {
	validated, err := ValidateInvokeTxBasic(tx, StandardContractScriptResolver(e.ContractPrefix), e.contractExists, e.GasConfig)
	if err != nil {
		return err
	}
	caller, err := e.resolveCaller(tx, parsed)
	if err != nil {
		return err
	}
	gasRefundRecipient, err := e.resolveGasRefundRecipient(tx, parsed)
	if err != nil {
		return err
	}
	funding := make([]OutPoint, 0, len(validated.FundingOutputs))
	for _, output := range validated.FundingOutputs {
		funding = append(funding, output.OutPoint)
	}
	callID := DeriveInvokeCallID(tx.TxID(), validated.FundingOutputs[0].Vout, validated.Contract)
	intentStart := len(e.Runtime.AssetIntents)
	result := e.Runtime.Call(CallRequest{
		Caller: caller,
		Target: ContractAddressHash(validated.Contract),
		CallID: callID,
		Input:  validated.Payload.Calldata,
		Gas:    validated.Payload.GasLimit,
		Value:  validated.MsgValue,
		Block:  e.Block,
	})
	intents := cloneAssetIntents(e.Runtime.AssetIntents[intentStart:])
	record := ExecutionRecord{
		Height:             e.Block.Number,
		TxID:               tx.TxID(),
		Type:               TxTypeInvoke,
		Kind:               ExecutionKindInvoke,
		CallID:             callID,
		Contract:           validated.Contract,
		Status:             result.Status,
		GasUsed:            result.GasUsed,
		FundingInputs:      funding,
		GasRefundRecipient: gasRefundRecipient,
		AssetIntents:       intents,
		RequiresResult:     true,
	}
	return e.appendRecord(record)
}

func (e *BlockExecutor) ExecuteTrigger(call TriggerCall) error {
	if err := call.Trigger.Validate(); err != nil {
		return err
	}
	env := BlockEnvironment{
		Height: int64(e.Block.Number),
	}
	if !call.Trigger.Due(env) {
		return fmt.Errorf("trigger %s is not due", call.Trigger.ID)
	}
	if call.GasLimit == 0 {
		return errors.New("trigger gas limit is zero")
	}
	if e.GasConfig.MaxGasPerInvoke > 0 && call.GasLimit > e.GasConfig.MaxGasPerInvoke {
		return errors.New("trigger gas limit exceeds maximum")
	}
	if !e.contractExists(call.Trigger.Contract) {
		return errors.New("trigger contract does not exist")
	}
	ready, err := e.triggerHasGasBudget(call.Trigger.Contract, call.GasLimit)
	if err != nil {
		return err
	}
	e.Runtime.State.RemoveTrigger(call.Trigger.Contract, call.Trigger.ID)
	if !ready {
		return nil
	}

	callID := DeriveTriggerCallID(call.Trigger.Contract, call.Trigger.ID, int64(e.Block.Number))
	intentStart := len(e.Runtime.AssetIntents)
	result := e.Runtime.Call(CallRequest{
		Caller: ContractAddressHash(call.Trigger.Contract),
		Target: ContractAddressHash(call.Trigger.Contract),
		CallID: callID,
		Input:  call.Calldata,
		Gas:    call.GasLimit,
		Block:  e.Block,
	})
	intents := cloneAssetIntents(e.Runtime.AssetIntents[intentStart:])
	record := ExecutionRecord{
		Height:         e.Block.Number,
		Kind:           ExecutionKindTrigger,
		CallID:         callID,
		TriggerID:      call.Trigger.ID,
		Contract:       call.Trigger.Contract,
		Status:         result.Status,
		GasUsed:        result.GasUsed,
		AssetIntents:   intents,
		RequiresResult: true,
	}
	return e.appendRecord(record)
}

func (e *BlockExecutor) triggerHasGasBudget(contract ContractAddress, gasLimit uint64) (bool, error) {
	if e.ContractUTXOs == nil {
		return true, nil
	}
	required, err := e.GasConfig.ContractFundingFee(ExecutionKindTrigger, gasLimit, true, e.Block.Number)
	if err != nil {
		return false, err
	}
	if required == nil || required.Sign() <= 0 {
		return true, nil
	}
	utxos, err := e.ContractUTXOs(contract)
	if err != nil {
		return false, err
	}
	total := zeroDecimal()
	for _, utxo := range utxos {
		if !utxo.Contract.Equal(contract) {
			continue
		}
		amount, err := utxo.AssetAmount(e.GasConfig.normalized().GasAssetName)
		if err != nil {
			return false, err
		}
		total = total.AddAlignPrecision(amount)
		if total.Cmp(required) >= 0 {
			return true, nil
		}
	}
	return false, nil
}

func (e *BlockExecutor) executeResult(tx *wire.MsgTx, parsed ParsedTx) error {
	if parsed.Result == nil {
		return errors.New("missing result payload")
	}
	count := int(parsed.Result.ResultCount)
	if count == 0 {
		return errors.New("result count is zero")
	}
	if count > len(e.pending) {
		return fmt.Errorf("result count %d exceeds pending executions %d", count, len(e.pending))
	}
	settled := e.pending[:count]
	if err := validateResultStatus(*parsed.Result, settled); err != nil {
		return err
	}
	if err := validateResultFundingInputs(tx, settled); err != nil {
		return err
	}
	if e.VerifyResult != nil {
		if err := e.VerifyResult(tx, cloneExecutionRecords(settled)); err != nil {
			return err
		}
	}
	e.pending = e.pending[count:]
	return nil
}

func (e *BlockExecutor) appendRecord(record ExecutionRecord) error {
	next, overflow := addUint64(e.gasUsed, record.GasUsed)
	if overflow {
		return fmt.Errorf("EVM block gas used overflows uint64")
	}
	if e.GasConfig.MaxGasPerBlock > 0 && next > e.GasConfig.MaxGasPerBlock {
		return fmt.Errorf("EVM block gas used %d exceeds limit %d", next, e.GasConfig.MaxGasPerBlock)
	}
	e.gasUsed = next
	e.records = append(e.records, record)
	if record.RequiresResult {
		e.pending = append(e.pending, record)
	}
	return nil
}

func (e *BlockExecutor) resolveCaller(tx *wire.MsgTx, parsed ParsedTx) (EVMAddress, error) {
	if e.ResolveCaller == nil {
		return EVMAddress{}, errors.New("missing EVM caller resolver")
	}
	return e.ResolveCaller(tx, parsed)
}

func (e *BlockExecutor) resolveGasRefundRecipient(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
	if e.ResolveGasRefundRecipient == nil {
		return "", nil
	}
	recipient, ok, err := e.ResolveGasRefundRecipient(tx, parsed)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return recipient, nil
}

func (e *BlockExecutor) contractExists(contract ContractAddress) bool {
	return e.Runtime.State.Exist(ContractGethAddress(contract))
}

func validateResultStatus(result ResultPayload, settled []ExecutionRecord) error {
	if len(settled) == 1 && result.Status != settled[0].Status {
		return fmt.Errorf("result status %d does not match execution status %d",
			result.Status, settled[0].Status)
	}
	return nil
}

func validateResultFundingInputs(tx *wire.MsgTx, settled []ExecutionRecord) error {
	inputs := make(map[OutPoint]struct{}, len(tx.TxIn))
	for _, txIn := range tx.TxIn {
		inputs[WireOutPointToEVM(txIn.PreviousOutPoint)] = struct{}{}
	}
	for _, record := range settled {
		for _, funding := range record.FundingInputs {
			if _, ok := inputs[funding]; !ok {
				return fmt.Errorf("EVM_RESULT missing execution funding input %s", funding)
			}
		}
	}
	return nil
}

func recordKind(record ExecutionRecord) ExecutionKind {
	if record.Kind != 0 {
		return record.Kind
	}
	switch record.Type {
	case TxTypeDeploy:
		return ExecutionKindDeploy
	case TxTypeInvoke:
		return ExecutionKindInvoke
	default:
		return 0
	}
}

func cloneAssetIntents(in []AssetIntent) []AssetIntent {
	out := make([]AssetIntent, len(in))
	copy(out, in)
	for i := range out {
		out[i].ExtraData = cloneBytes(out[i].ExtraData)
		out[i].Amount = cloneDecimal(out[i].Amount)
	}
	return out
}

func cloneExecutionRecords(in []ExecutionRecord) []ExecutionRecord {
	out := make([]ExecutionRecord, len(in))
	copy(out, in)
	for i := range out {
		out[i].FundingInputs = append([]OutPoint(nil), out[i].FundingInputs...)
		out[i].AssetIntents = cloneAssetIntents(out[i].AssetIntents)
	}
	return out
}
