package evm

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type CallerResolver func(tx *wire.MsgTx, contractTx contract.Tx) (EVMAddress, error)
type GasRefundRecipientResolver func(tx *wire.MsgTx, contractTx contract.Tx) (recipient string, ok bool, err error)
type ResultVerifier = contractframework.ResultVerifier
type TriggerResolver func(ctx TriggerResolutionContext) ([]TriggerCall, error)

type PreviousOutputScriptResolver = contractframework.PreviousOutputScriptResolver

func EVMAddressFromPublicKey(pubKey []byte) (EVMAddress, error) {
	var addr EVMAddress
	if !isSupportedPublicKey(pubKey) {
		return addr, fmt.Errorf("unsupported public key length %d", len(pubKey))
	}
	hash := btcutil.Hash160(pubKey)
	copy(addr[:], hash)
	return addr, nil
}

func LastInputCallerResolver(tx *wire.MsgTx, contractTx contract.Tx) (EVMAddress, error) {
	var zero EVMAddress
	if tx == nil {
		return zero, errors.New("missing transaction")
	}
	if len(tx.TxIn) == 0 {
		return zero, errors.New("transaction has no inputs")
	}
	pubKey, err := ExtractInputPublicKey(tx.TxIn[len(tx.TxIn)-1])
	if err != nil {
		return zero, err
	}
	return EVMAddressFromPublicKey(pubKey)
}

func LastInputPreviousOutputCallerResolver(params *chaincfg.Params,
	resolve PreviousOutputScriptResolver) CallerResolver {

	return func(tx *wire.MsgTx, contractTx contract.Tx) (EVMAddress, error) {
		var zero EVMAddress
		if tx == nil {
			return zero, errors.New("missing transaction")
		}
		if len(tx.TxIn) == 0 {
			return zero, errors.New("transaction has no inputs")
		}
		if resolve != nil {
			outpoint := tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint
			if pkScript, ok := resolve(outpoint); ok {
				address, err := contractframework.PreviousOutputAddress(pkScript, params)
				if err != nil {
					return zero, err
				}
				if address != "" {
					hash := btcutil.Hash160([]byte(address))
					copy(zero[:], hash)
					return zero, nil
				}
			}
		}
		return zero, errors.New("missing caller previous output address")
	}
}

func LastInputPreviousOutputGasRefundRecipientResolver(params *chaincfg.Params,
	resolve PreviousOutputScriptResolver) GasRefundRecipientResolver {

	return func(tx *wire.MsgTx, contractTx contract.Tx) (string, bool, error) {
		if tx == nil {
			return "", false, errors.New("missing transaction")
		}
		if len(tx.TxIn) == 0 {
			return "", false, errors.New("transaction has no inputs")
		}
		if resolve == nil {
			return "", false, nil
		}
		outpoint := tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint
		pkScript, ok := resolve(outpoint)
		if !ok {
			return "", false, nil
		}
		address, err := contractframework.PreviousOutputAddress(pkScript, params)
		if err != nil {
			return "", false, err
		}
		if address == "" {
			return "", false, nil
		}
		return address, true, nil
	}
}

func ExtractInputPublicKey(txIn *wire.TxIn) ([]byte, error) {
	if txIn == nil {
		return nil, errors.New("missing transaction input")
	}
	if pubKey := findPublicKeyPush(txIn.Witness); pubKey != nil {
		return contractframework.CloneBytes(pubKey), nil
	}
	pushes, err := txscript.PushedData(txIn.SignatureScript)
	if err != nil {
		return nil, err
	}
	if pubKey := findPublicKeyPush(pushes); pubKey != nil {
		return contractframework.CloneBytes(pubKey), nil
	}
	return nil, errors.New("input does not reveal a supported public key")
}

func findPublicKeyPush(pushes [][]byte) []byte {
	for i := len(pushes) - 1; i >= 0; i-- {
		if isSupportedPublicKey(pushes[i]) {
			return pushes[i]
		}
	}
	return nil
}

func isSupportedPublicKey(pubKey []byte) bool {
	switch len(pubKey) {
	case 33:
		return pubKey[0] == 0x02 || pubKey[0] == 0x03
	case 65:
		return pubKey[0] == 0x04 || pubKey[0] == 0x06 || pubKey[0] == 0x07
	default:
		return false
	}
}

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

type BlockExecutionResult = contractframework.BackendBlockExecutionResult

type BlockResultBuildRequest struct {
	Txs                       []*wire.MsgTx
	Runtime                   *Runtime
	ContractPrefix            string
	GasConfig                 GasConfig
	Block                     BlockContext
	ResolveCaller             CallerResolver
	ResolveGasRefundRecipient GasRefundRecipientResolver
	ContractUTXOs             ContractUTXOProvider
	ResolveScript             ResultRecipientScriptResolver
	ResolveOutput             ResultOutputResolver
	ResolveTriggers           TriggerResolver
	Triggers                  []TriggerCall
}

type BlockResultBuildResult = contractframework.BackendBlockResultBuildResult

type Backend struct {
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

	records []ExecutionRecord
	pending []ExecutionRecord
	gasUsed int64
}

func ExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	return ExecuteWorkBlock(req)
}

func BuildBlockResultTxs(req BlockResultBuildRequest) (BlockResultBuildResult, error) {
	runtime := req.Runtime
	if runtime == nil {
		runtime = NewRuntime(nil)
	}
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	runtime.ContractPrefix = prefix

	overlay := contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
		Prefix:       prefix,
		ContractType: ContractTypeEVM,
		Base:         req.ContractUTXOs,
	})
	resolveOutput := req.ResolveOutput
	if resolveOutput == nil {
		resolveOutput = func(resultTx *wire.MsgTx) ([]ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(resultTx, prefix, contract.ParseContractPkScript, nil)
		}
	}
	verifier := CanonicalResultVerifier{
		GasConfig:     req.GasConfig,
		UTXOs:         overlay.Provider,
		ResolveOutput: resolveOutput,
	}
	executor := NewBackend(BlockExecutionRequest{
		Runtime:                   runtime,
		ContractPrefix:            prefix,
		GasConfig:                 req.GasConfig,
		Block:                     req.Block,
		ResolveCaller:             req.ResolveCaller,
		ResolveGasRefundRecipient: req.ResolveGasRefundRecipient,
		VerifyResult:              verifier.Verify,
		ResolveTriggers:           req.ResolveTriggers,
		ContractUTXOs:             overlay.Provider,
	})

	for _, tx := range req.Txs {
		payloadType, foundPayload, payloadErr := contract.ClassifyTxPayloadType(tx)
		if payloadErr == nil && foundPayload && payloadType == contract.TxTypeResult {
			return BlockResultBuildResult{}, fmt.Errorf("EVM input already contains RESULT")
		}
		info, err := ClassifyTxForBlockOrder(tx, prefix)
		if err != nil || !info.IsEVM {
			continue
		}
		if info.Type == TxTypeResult {
			return BlockResultBuildResult{}, fmt.Errorf("EVM input already contains RESULT")
		}
		parsed, err := ParseTx(tx, StandardContractScriptResolver(prefix))
		if err != nil {
			continue
		}
		if err := executor.ExecuteParsedTx(tx, parsed); err != nil {
			return BlockResultBuildResult{}, err
		}
		if err := overlay.ApplyTx(tx, int64(req.Block.Number)); err != nil {
			return BlockResultBuildResult{}, err
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
			return BlockResultBuildResult{}, err
		}
		triggers = append(triggers, resolved...)
	}
	for _, trigger := range triggers {
		if err := executor.ExecuteTrigger(trigger); err != nil {
			return BlockResultBuildResult{}, err
		}
	}

	resultTxs := make([]*wire.MsgTx, 0)
	for {
		pending := executor.PendingRecords()
		if len(pending) == 0 {
			break
		}
		record := pending[0]
		resultTx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
			Status:        record.Status,
			Records:       []ExecutionRecord{record},
			GasConfig:     req.GasConfig,
			UTXOs:         overlay.Provider,
			ResolveScript: req.ResolveScript,
		})
		if err != nil {
			return BlockResultBuildResult{}, err
		}
		if err := executor.VerifyAndSettleResultTx(resultTx, verifier.Verify); err != nil {
			return BlockResultBuildResult{}, err
		}
		if err := overlay.ApplyTx(resultTx, int64(req.Block.Number)); err != nil {
			return BlockResultBuildResult{}, err
		}
		resultTxs = append(resultTxs, resultTx)
	}
	execution, err := executor.Finalize()
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	return BlockResultBuildResult{
		ResultTxs: resultTxs,
		Execution: execution,
	}, nil
}

func ExecuteWorkBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	executor := NewBackend(req)
	frameworkExecutor := contractframework.NewExecutor(executor.executorConfig())
	if _, err := frameworkExecutor.ExecuteBlock(req.Txs); err != nil {
		return BlockExecutionResult{}, err
	}
	return executor.FinalizeWork()
}

func NewBackend(req BlockExecutionRequest) *Backend {
	runtime := req.Runtime
	if runtime == nil {
		runtime = NewRuntime(nil)
	}
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	runtime.ContractPrefix = prefix
	runtime.GasConfig = req.GasConfig
	if req.ContractUTXOs != nil {
		runtime.AssetBalances = NewContractUTXOAssetView(prefix, req.ContractUTXOs)
	}
	return &Backend{
		Runtime:                   runtime,
		ContractPrefix:            prefix,
		GasConfig:                 req.GasConfig,
		Block:                     req.Block,
		ResolveCaller:             req.ResolveCaller,
		ResolveGasRefundRecipient: req.ResolveGasRefundRecipient,
		VerifyResult:              req.VerifyResult,
		ResolveTriggers:           req.ResolveTriggers,
		ContractUTXOs:             req.ContractUTXOs,
		Triggers:                  append([]TriggerCall(nil), req.Triggers...),
	}
}

type TriggerCall struct {
	Trigger  Trigger
	GasLimit int64
	Calldata []byte
}

func (e *Backend) ExecuteTx(tx *wire.MsgTx) error {
	return contractframework.NewExecutor(e.executorConfig()).ExecuteTx(tx)
}

func (e *Backend) ExecuteParsedTx(tx *wire.MsgTx, parsed ParsedTx) error {
	return contractframework.NewExecutor(e.executorConfig()).ExecuteParsedTx(tx, parsed)
}

func (e *Backend) executorConfig() contractframework.ExecutorConfig {
	return contractframework.ExecutorConfig{
		Backend:   e,
		Prefix:    e.ContractPrefix,
		ParseSpec: evmParseSpec(),
		Resolver:  StandardContractScriptResolver,
		Context: contractframework.ExecutionContext{
			Prefix:    e.ContractPrefix,
			Height:    int64(e.Block.Number),
			Time:      int64(e.Block.Time),
			GasConfig: e.GasConfig,
		},
		ResolveActor: func(tx *wire.MsgTx, contractTx contract.Tx) (string, error) {
			caller, err := e.resolveCaller(tx, contractTx)
			if err != nil {
				return "", err
			}
			return caller.String(), nil
		},
		ResolveRefundRecipient: func(tx *wire.MsgTx, contractTx contract.Tx) (string, bool, error) {
			recipient, err := e.resolveGasRefundRecipient(tx, contractTx)
			if err != nil || recipient == "" {
				return recipient, false, err
			}
			return recipient, true, nil
		},
	}
}

func (e *Backend) ContractType() byte {
	return ContractTypeEVM
}

func (e *Backend) Name() string {
	return "EVM"
}

func (e *Backend) Priority() int {
	return 2
}

func (e *Backend) Deploy(ctx contractframework.ExecutionContext, tx contract.Tx) (contractframework.ExecutionOutcome, error) {
	before := len(e.records)
	if err := e.executeDeployTx(ctx.RawTx, ctx.ParsedTx, tx); err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	return e.lastOutcomeSince(before)
}

func (e *Backend) Invoke(ctx contractframework.ExecutionContext, tx contract.Tx) (contractframework.ExecutionOutcome, error) {
	before := len(e.records)
	if err := e.executeInvokeTx(ctx.RawTx, ctx.ParsedTx, tx); err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	return e.lastOutcomeSince(before)
}

func (e *Backend) DefaultInvoke(ctx contractframework.ExecutionContext,
	tx contract.Tx, funding contract.FundingOutput) (contractframework.ExecutionOutcome, bool, error) {

	before := len(e.records)
	output := contractframework.ContractOutput{
		OutPoint: contractframework.OutPoint{TxID: funding.OutPoint.TxID, Vout: funding.OutPoint.Vout},
		Vout:     funding.Vout,
		Contract: funding.Contract,
		Value:    funding.Value,
		Assets:   funding.Assets.Clone(),
		PkScript: contractframework.CloneBytes(funding.PkScript),
	}
	if err := e.executeDefaultInvokeOutputTx(ctx.RawTx, tx, output); err != nil {
		return contractframework.ExecutionOutcome{}, false, err
	}
	if len(e.records) == before {
		return contractframework.ExecutionOutcome{}, false, nil
	}
	outcome, err := e.lastOutcomeSince(before)
	return outcome, true, err
}

func (e *Backend) FinalizeBlock(ctx contractframework.ExecutionContext) ([]contractframework.ExecutionOutcome, error) {
	triggers := append([]TriggerCall(nil), e.Triggers...)
	triggers = append(triggers, e.Runtime.DueTriggerCalls(e.Block)...)
	if e.ResolveTriggers != nil {
		resolved, err := e.ResolveTriggers(TriggerResolutionContext{
			Block:          e.Block,
			Runtime:        e.Runtime,
			ContractPrefix: e.ContractPrefix,
		})
		if err != nil {
			return nil, err
		}
		triggers = append(triggers, resolved...)
	}
	before := len(e.records)
	for _, trigger := range triggers {
		if err := e.ExecuteTrigger(trigger); err != nil {
			return nil, err
		}
	}
	return contractframework.ExecutionOutcomesFromRecords(e.records[before:]), nil
}

func (e *Backend) StateRoot() [32]byte {
	return e.Runtime.State.StateRoot()
}

func (e *Backend) Snapshot() any {
	return e.Runtime
}

func (e *Backend) executeDefaultInvokeOutput(tx *wire.MsgTx, output ContractOutput) error {
	contractTx := contract.Tx{
		TxID:         tx.TxID(),
		Kind:         TxTypeInvoke,
		ContractType: ContractTypeEVM,
		Contract:     output.Contract,
		Action:       contract.ContractInvokeAPIDefault,
		GasLimit:     e.GasConfig.Normalize().InvokeBaseGas,
		Funding:      contractframework.ContractFundingOutputs([]ContractOutput{output}),
	}
	return e.executeDefaultInvokeOutputTx(tx, contractTx, output)
}

func (e *Backend) executeDefaultInvokeOutputTx(tx *wire.MsgTx, contractTx contract.Tx, output ContractOutput) error {
	if !e.contractExists(output.Contract) {
		return nil
	}
	parsed := ParsedTx{Type: TxTypeInvoke, ContractOutputs: []ContractOutput{output}, Inputs: contractframework.MsgTxInputs(tx)}
	caller, err := e.callerFromContractTx(contractTx, tx, parsed)
	if err != nil {
		return nil
	}
	callID := DeriveInvokeCallID(tx.TxID(), output.Vout, output.Contract)
	intentStart := len(e.Runtime.AssetIntents)
	result := e.Runtime.Call(CallRequest{
		Caller: caller,
		Target: ContractAddressHash(output.Contract),
		CallID: callID,
		Input:  nil,
		Gas:    e.GasConfig.Normalize().InvokeBaseGas,
		Value:  output.Value,
		Block:  e.Block,
	})
	intents := contractframework.CloneAssetIntents(e.Runtime.AssetIntents[intentStart:])
	outcome := contractframework.ExecutionOutcome{
		Height:         int64(e.Block.Number),
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
	return e.appendOutcome(outcome)
}

func (e *Backend) Finalize() (BlockExecutionResult, error) {
	if len(e.pending) != 0 {
		return BlockExecutionResult{}, fmt.Errorf("%d EVM executions remain unsettled", len(e.pending))
	}
	return e.executionResult(nil), nil
}

func (e *Backend) FinalizeWork() (BlockExecutionResult, error) {
	return e.executionResult(e.pending), nil
}

func (e *Backend) executionResult(pending []ExecutionRecord) BlockExecutionResult {
	return BlockExecutionResult{
		Records:        contractframework.CloneExecutionRecords(e.records),
		PendingRecords: contractframework.CloneExecutionRecords(pending),
		StateRoot:      e.Runtime.State.StateRoot(),
	}
}

func (e *Backend) PendingRecords() []ExecutionRecord {
	return contractframework.CloneExecutionRecords(e.pending)
}

func (e *Backend) Records() []ExecutionRecord {
	return contractframework.CloneExecutionRecords(e.records)
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

func (e *Backend) executeDeploy(tx *wire.MsgTx, parsed ParsedTx) error {
	return e.executeDeployTx(tx, parsed, contractframework.ContractTxFromParsed(tx, parsed, ContractTypeEVM))
}

func (e *Backend) executeDeployTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contract.Tx) error {
	validated, err := ValidateDeployTxBasic(tx, e.GasConfig)
	if err != nil {
		return nil
	}
	caller, err := e.callerFromContractTx(contractTx, tx, parsed)
	if err != nil {
		return nil
	}
	gasRefundRecipient, err := e.refundRecipientFromContractTx(contractTx, tx, parsed)
	if err != nil {
		return nil
	}
	expectedContract, err := DeriveCreateContractAddress(e.ContractPrefix, caller, validated.Payload.DeployNonce)
	if err != nil {
		return nil
	}
	callID := DeriveDeployCallID(tx.TxID(), expectedContract)
	fundingOutputs, err := FindContractOutputsForContract(tx,
		StandardContractScriptResolver(e.ContractPrefix), expectedContract)
	if err != nil {
		return nil
	}
	if len(fundingOutputs) == 0 {
		return nil
	}
	intentStart := len(e.Runtime.AssetIntents)
	result := e.Runtime.Deploy(DeployRequest{
		Caller:      caller,
		CallID:      callID,
		InitCode:    validated.Payload.ContractContent,
		Gas:         validated.Payload.GasLimit,
		DeployNonce: validated.Payload.DeployNonce,
		Block:       e.Block,
	})
	if !result.Contract.Equal(expectedContract) {
		return fmt.Errorf("deploy contract mismatch: got %s want %s",
			result.Contract.MustEncode(), expectedContract.MustEncode())
	}
	funding := make([]OutPoint, 0, len(fundingOutputs))
	for _, output := range fundingOutputs {
		funding = append(funding, output.OutPoint)
	}
	outcome := contractframework.ExecutionOutcome{
		Height:             int64(e.Block.Number),
		TxID:               tx.TxID(),
		Type:               TxTypeDeploy,
		Kind:               ExecutionKindDeploy,
		CallID:             callID,
		Contract:           result.Contract,
		Status:             result.Status,
		GasUsed:            result.GasUsed,
		FundingInputs:      funding,
		GasRefundRecipient: gasRefundRecipient,
		AssetIntents:       contractframework.CloneAssetIntents(e.Runtime.AssetIntents[intentStart:]),
		RequiresResult:     true,
	}
	return e.appendOutcome(outcome)
}

func (e *Backend) executeInvoke(tx *wire.MsgTx, parsed ParsedTx) error {
	return e.executeInvokeTx(tx, parsed, contractframework.ContractTxFromParsed(tx, parsed, ContractTypeEVM))
}

func (e *Backend) executeInvokeTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contract.Tx) error {
	validated, err := ValidateInvokeTxBasic(tx, StandardContractScriptResolver(e.ContractPrefix), e.contractExists, e.GasConfig)
	if err != nil {
		return nil
	}
	caller, err := e.callerFromContractTx(contractTx, tx, parsed)
	if err != nil {
		return nil
	}
	gasRefundRecipient, err := e.refundRecipientFromContractTx(contractTx, tx, parsed)
	if err != nil {
		return nil
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
		Input:  validated.Payload.Param,
		Gas:    validated.Payload.GasLimit,
		Value:  validated.MsgValue,
		Block:  e.Block,
	})
	intents := contractframework.CloneAssetIntents(e.Runtime.AssetIntents[intentStart:])
	outcome := contractframework.ExecutionOutcome{
		Height:             int64(e.Block.Number),
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
	return e.appendOutcome(outcome)
}

func (e *Backend) ExecuteTrigger(call TriggerCall) error {
	if err := call.Trigger.Validate(); err != nil {
		return err
	}
	env := BlockEnvironment{
		Height: int64(e.Block.Number),
	}
	if !call.Trigger.Due(env) {
		return fmt.Errorf("trigger %s is not due", call.Trigger.ID)
	}
	if call.GasLimit <= 0 {
		return errors.New("trigger gas limit is zero")
	}
	callGasLimit := call.GasLimit
	if err := contractframework.ValidateTriggerGasLimit(callGasLimit, e.GasConfig); err != nil {
		return err
	}
	if !e.contractExists(call.Trigger.Contract) {
		return errors.New("trigger contract does not exist")
	}
	ready, err := e.triggerHasGasBudget(call.Trigger.Contract, callGasLimit)
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
	intents := contractframework.CloneAssetIntents(e.Runtime.AssetIntents[intentStart:])
	outcome := contractframework.ExecutionOutcome{
		Height:         int64(e.Block.Number),
		Kind:           ExecutionKindTrigger,
		CallID:         callID,
		TriggerID:      call.Trigger.ID,
		Contract:       call.Trigger.Contract,
		Status:         result.Status,
		GasUsed:        result.GasUsed,
		AssetIntents:   intents,
		RequiresResult: true,
	}
	return e.appendOutcome(outcome)
}

func (e *Backend) triggerHasGasBudget(contract ContractAddress, gasLimit int64) (bool, error) {
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
		amount, err := utxo.AssetAmount(e.GasConfig.Normalize().GasAssetName)
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

func (e *Backend) VerifyAndSettleResultTx(tx *wire.MsgTx, verify ResultVerifier) error {
	parsed, err := ParseTx(tx, StandardContractScriptResolver(e.ContractPrefix))
	if err != nil {
		return err
	}
	return e.verifyAndSettleResult(tx, parsed, verify)
}

func (e *Backend) verifyAndSettleResult(tx *wire.MsgTx, parsed ParsedTx, verify ResultVerifier) error {
	pending, err := verifyResultAgainstPending(tx, parsed, e.pending, func(resultTx *wire.MsgTx, settled []ExecutionRecord) error {
		if verify != nil {
			return verify(resultTx, settled)
		}
		if e.VerifyResult != nil {
			return e.VerifyResult(resultTx, settled)
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.pending = pending
	return nil
}

func verifyResultAgainstPending(tx *wire.MsgTx, parsed ParsedTx, pending []ExecutionRecord,
	verify ResultVerifier) ([]ExecutionRecord, error) {

	if parsed.Type != TxTypeResult {
		return pending, errors.New("not an EVM RESULT transaction")
	}
	if parsed.Result == nil {
		return pending, errors.New("missing result payload")
	}
	return contractframework.VerifyResultAgainstPending(contractframework.PendingResultVerifyRequest{
		Label:    "EVM_RESULT",
		ResultTx: tx,
		Payload:  *parsed.Result,
		Pending:  pending,
		Verify:   verify,
	})
}

func (e *Backend) appendRecord(record ExecutionRecord) error {
	next, overflow := contractframework.AddInt64(e.gasUsed, record.GasUsed)
	if overflow {
		return fmt.Errorf("EVM block gas used overflows int64")
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

func (e *Backend) appendOutcome(outcome contractframework.ExecutionOutcome) error {
	return e.appendRecord(outcome.ToRecord())
}

func (e *Backend) lastOutcomeSince(before int) (contractframework.ExecutionOutcome, error) {
	return contractframework.LastExecutionOutcomeSince(e.records, before)
}

func (e *Backend) callerFromContractTx(contractTx contract.Tx, raw *wire.MsgTx, parsed ParsedTx) (EVMAddress, error) {
	if contractTx.Actor != "" {
		return ParseEVMAddressHex(contractTx.Actor)
	}
	return e.resolveCaller(raw, contractTx)
}

func (e *Backend) refundRecipientFromContractTx(contractTx contract.Tx, raw *wire.MsgTx, parsed ParsedTx) (string, error) {
	if contractTx.GasRefundRecipient != "" {
		return contractTx.GasRefundRecipient, nil
	}
	return e.resolveGasRefundRecipient(raw, contractTx)
}

func (e *Backend) resolveCaller(tx *wire.MsgTx, contractTx contract.Tx) (EVMAddress, error) {
	if e.ResolveCaller == nil {
		return EVMAddress{}, errors.New("missing EVM caller resolver")
	}
	return e.ResolveCaller(tx, contractTx)
}

func (e *Backend) resolveGasRefundRecipient(tx *wire.MsgTx, contractTx contract.Tx) (string, error) {
	if e.ResolveGasRefundRecipient == nil {
		return "", nil
	}
	recipient, ok, err := e.ResolveGasRefundRecipient(tx, contractTx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return recipient, nil
}

func (e *Backend) contractExists(contract ContractAddress) bool {
	return e.Runtime.State.Exist(ContractGethAddress(contract))
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
