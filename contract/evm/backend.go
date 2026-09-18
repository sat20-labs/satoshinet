package evm

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type CallerResolver func(tx *wire.MsgTx, contractTx contract.Tx) (string, error)
type GasRefundRecipientResolver func(tx *wire.MsgTx, contractTx contract.Tx) (recipient string, ok bool, err error)
type ResultVerifier = contractframework.ResultVerifier
type TriggerResolver func(ctx TriggerResolutionContext) ([]TriggerCall, error)
type PreviousOutputScriptResolver = contractframework.PreviousOutputScriptResolver

func EVMAddressFromPublicKey(pubKey []byte) (EVMAddress, error) {
	var addr EVMAddress
	if !isSupportedPublicKey(pubKey) {
		return addr, fmt.Errorf("unsupported public key length %d", len(pubKey))
	}
	copy(addr[:], btcutil.Hash160(pubKey))
	return addr, nil
}

func EVMAddressFromAddressString(address string) EVMAddress {
	trimmed := strings.TrimSpace(address)
	if addr, err := DecodeContractAddress(trimmed); err == nil {
		return ContractAddressHash(addr)
	}
	if addr, err := ParseEVMAddressHex(trimmed); err == nil {
		return addr
	}
	var addr EVMAddress
	copy(addr[:], btcutil.Hash160([]byte(trimmed)))
	return addr
}

func LastInputCallerResolver(tx *wire.MsgTx, contractTx contract.Tx) (string, error) {
	if tx == nil || len(tx.TxIn) == 0 {
		return "", errors.New("transaction has no inputs")
	}
	pubKey, err := ExtractInputPublicKey(tx.TxIn[len(tx.TxIn)-1])
	if err != nil {
		return "", err
	}
	caller, err := EVMAddressFromPublicKey(pubKey)
	if err != nil {
		return "", err
	}
	return caller.String(), nil
}

func LastInputPreviousOutputCallerResolver(params *chaincfg.Params, resolve PreviousOutputScriptResolver) CallerResolver {
	return func(tx *wire.MsgTx, contractTx contract.Tx) (string, error) {
		if tx == nil || len(tx.TxIn) == 0 {
			return "", errors.New("transaction has no inputs")
		}
		if resolve != nil {
			if script, ok := resolve(tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint); ok {
				address, err := contractframework.PreviousOutputAddress(script, params)
				if err != nil {
					return "", err
				}
				if address != "" {
					return address, nil
				}
			}
		}
		return "", errors.New("missing caller previous output address")
	}
}

func LastInputPreviousOutputGasRefundRecipientResolver(params *chaincfg.Params, resolve PreviousOutputScriptResolver) GasRefundRecipientResolver {
	return func(tx *wire.MsgTx, contractTx contract.Tx) (string, bool, error) {
		if tx == nil || len(tx.TxIn) == 0 {
			return "", false, errors.New("transaction has no inputs")
		}
		if resolve == nil {
			return "", false, nil
		}
		script, ok := resolve(tx.TxIn[len(tx.TxIn)-1].PreviousOutPoint)
		if !ok {
			return "", false, nil
		}
		address, err := contractframework.PreviousOutputAddress(script, params)
		return address, address != "", err
	}
}

func ExtractInputPublicKey(txIn *wire.TxIn) ([]byte, error) {
	if txIn == nil {
		return nil, errors.New("missing transaction input")
	}
	if key := findPublicKeyPush(txIn.Witness); key != nil {
		return contractframework.CloneBytes(key), nil
	}
	pushes, err := txscript.PushedData(txIn.SignatureScript)
	if err != nil {
		return nil, err
	}
	if key := findPublicKeyPush(pushes); key != nil {
		return contractframework.CloneBytes(key), nil
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
	ResolveResultScript       ResultRecipientScriptResolver
	VerifyResult              ResultVerifier
	ResolveTriggers           TriggerResolver
	ContractUTXOs             ContractUTXOProvider
	Triggers                  []TriggerCall
	AssetPrecision            contractframework.AssetPrecisionResolver
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
	AssetPrecision            contractframework.AssetPrecisionResolver
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

	records             []ExecutionRecord
	pending             []ExecutionRecord
	gasUsed             int64
	settlementPrecision contractframework.AssetPrecisionPolicy
	fundingTx           *wire.MsgTx
	pendingGasReserved  map[EVMAddress]*scommon.Decimal
}

func SettlementPrecision(resolve contractframework.AssetPrecisionResolver) contractframework.AssetPrecisionPolicy {
	return contractframework.AssetPrecisionPolicy{Fallback: contract.GasFeePrecision, Resolve: resolve}
}

func ExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	return ExecuteWorkBlock(req)
}

// Construction and replay use the same work execution and quantity accounting.
// Changes are staged in a candidate runtime, never the supplied parent state.
func executeWorkBackend(req BlockExecutionRequest) (*Backend, []ResultPlan, error) {
	if req.Runtime == nil {
		req.Runtime = NewRuntime(nil)
	} else {
		req.Runtime = req.Runtime.Clone()
	}
	if req.ContractPrefix == "" {
		req.ContractPrefix = TestnetContractPrefix
	}
	overlay := newEVMBlockUTXOOverlay(req.ContractPrefix, req.ContractUTXOs, req.Txs)
	req.ContractUTXOs = overlay.Provider
	executor := NewBackend(req)
	fx := contractframework.NewExecutor(executor.executorConfig())
	for _, tx := range req.Txs {
		if tx == nil {
			return nil, nil, fmt.Errorf("%w: nil work transaction", contractframework.ErrCallAdmission)
		}
		kind, found, err := contract.ClassifyTxPayloadType(tx)
		if err != nil {
			return nil, nil, err
		}
		if found && kind == contract.TxTypeResult {
			return nil, nil, fmt.Errorf("EVM RESULT transactions are not accepted as external input")
		}
		info, err := ClassifyTxForBlockOrder(tx, req.ContractPrefix)
		if err != nil {
			return nil, nil, err
		}
		if info.IsEVM {
			if err := fx.ExecuteTx(tx); err != nil {
				return nil, nil, err
			}
		}
		if err := overlay.ApplyTx(tx, int64(req.Block.Number)); err != nil {
			return nil, nil, err
		}
	}
	if _, err := executor.FinalizeBlock(contractframework.ExecutionContext{}); err != nil {
		return nil, nil, err
	}
	executor.bindManagedSnapshots()
	plans, err := executor.resultPlans(executor.pending)
	if err != nil {
		return nil, nil, err
	}
	return executor, plans, nil
}

func ExecuteWorkBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	executor, plans, err := executeWorkBackend(req)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	if err := executor.applyResultBalances(plans); err != nil {
		return BlockExecutionResult{}, err
	}
	result := executor.executionResult(executor.pending)
	result.ResultPlans = contractframework.CloneResultPlans(plans)
	if req.Runtime != nil {
		*req.Runtime = *executor.Runtime
	}
	return result, nil
}

func BuildBlockResultTxs(req BlockResultBuildRequest) (BlockResultBuildResult, error) {
	executor, plans, err := executeWorkBackend(BlockExecutionRequest{
		Txs: req.Txs, Runtime: req.Runtime, ContractPrefix: req.ContractPrefix,
		GasConfig: req.GasConfig, Block: req.Block, ResolveCaller: req.ResolveCaller,
		ResolveGasRefundRecipient: req.ResolveGasRefundRecipient,
		ResolveResultScript:       req.ResolveScript, ResolveTriggers: req.ResolveTriggers,
		ContractUTXOs: req.ContractUTXOs, Triggers: req.Triggers, AssetPrecision: req.AssetPrecision,
	})
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	var resultTxs []*wire.MsgTx
	if len(executor.pending) != 0 {
		if len(executor.pending) > math.MaxUint16 {
			return BlockResultBuildResult{}, fmt.Errorf("too many EVM execution records")
		}
		resultTx, err := contractframework.BuildResultTx(contractframework.ResultTxBuildRequest{
			Status: blockResultStatus(executor.pending), ResultCount: uint16(len(executor.pending)),
			Plans: plans, ResolveScript: req.ResolveScript,
		}, contractframework.ResultTxBuildOptions{UseInputUTXOs: true})
		if err != nil {
			return BlockResultBuildResult{}, err
		}
		verifier := CanonicalResultVerifier{
			GasConfig: req.GasConfig, UTXOs: executor.ContractUTXOs,
			Precision:     SettlementPrecision(req.AssetPrecision),
			ResolveOutput: req.ResolveOutput, ResolveScript: req.ResolveScript,
		}
		if err := executor.VerifyAndSettleResultTx(resultTx, verifier.Verify); err != nil {
			return BlockResultBuildResult{}, err
		}
		resultTxs = append(resultTxs, resultTx)
	}
	execution, err := executor.Finalize()
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	execution.ResultPlans = contractframework.CloneResultPlans(plans)
	if req.Runtime != nil {
		*req.Runtime = *executor.Runtime
	}
	return BlockResultBuildResult{ResultTxs: resultTxs, Execution: execution}, nil
}

func blockResultStatus(records []ExecutionRecord) ResultStatus {
	return contractframework.AggregateResultStatus(records)
}

func newEVMBlockUTXOOverlay(prefix string, base ContractUTXOProvider, txs []*wire.MsgTx) *contractframework.ContractUTXOOverlay {
	return contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
		Prefix: prefix, ContractType: ContractTypeEVM, Base: withoutEVMBlockOutputs(base, txs),
	})
}

func withoutEVMBlockOutputs(base ContractUTXOProvider, txs []*wire.MsgTx) ContractUTXOProvider {
	return contractframework.WithoutBlockOutputs(base, txs)
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
	runtime.AssetIntents = nil
	if req.AssetPrecision != nil {
		runtime.AssetPrecision = SettlementPrecision(req.AssetPrecision)
	}
	if req.ResolveResultScript != nil {
		runtime.ResolveResultScript = req.ResolveResultScript
	}
	e := &Backend{
		Runtime: runtime, ContractPrefix: prefix, GasConfig: req.GasConfig, Block: req.Block,
		ResolveCaller: req.ResolveCaller, ResolveGasRefundRecipient: req.ResolveGasRefundRecipient,
		VerifyResult: req.VerifyResult, ResolveTriggers: req.ResolveTriggers,
		ContractUTXOs: req.ContractUTXOs, Triggers: append([]TriggerCall(nil), req.Triggers...),
		settlementPrecision: SettlementPrecision(req.AssetPrecision),
	}
	runtime.AssetBalances = pendingFeeAssetView{base: managedStateAssetView{state: runtime.State}, backend: e}
	return e
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
		Backend: e, Prefix: e.ContractPrefix, ParseSpec: evmParseSpec(), Resolver: StandardContractScriptResolver,
		Context: contractframework.ExecutionContext{
			Prefix: e.ContractPrefix, Height: int64(e.Block.Number), Time: int64(e.Block.Time), GasConfig: e.GasConfig,
		},
		ResolveActor: func(tx *wire.MsgTx, call contract.Tx) (string, error) {
			return e.resolveCaller(tx, call)
		},
		ResolveRefundRecipient: func(tx *wire.MsgTx, call contract.Tx) (string, bool, error) {
			recipient, err := e.resolveGasRefundRecipient(tx, call)
			return recipient, recipient != "", err
		},
	}
}

func (e *Backend) ContractType() byte  { return ContractTypeEVM }
func (e *Backend) Name() string        { return "EVM" }
func (e *Backend) Priority() int       { return 2 }
func (e *Backend) StateRoot() [32]byte { return e.Runtime.State.StateRoot() }
func (e *Backend) Snapshot() any       { return e.Runtime }

func (e *Backend) Deploy(ctx contractframework.ExecutionContext, tx contract.Tx) (contractframework.ExecutionOutcome, error) {
	e.fundingTx = ctx.RawTx
	defer func() { e.fundingTx = nil }()
	before := len(e.records)
	if err := e.executeDeployTx(ctx.RawTx, ctx.ParsedTx, tx); err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	return e.lastOutcomeSince(before)
}

func (e *Backend) Invoke(ctx contractframework.ExecutionContext, tx contract.Tx) (contractframework.ExecutionOutcome, error) {
	e.fundingTx = ctx.RawTx
	defer func() { e.fundingTx = nil }()
	before := len(e.records)
	if err := e.executeInvokeTx(ctx.RawTx, ctx.ParsedTx, tx); err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	return e.lastOutcomeSince(before)
}

func (e *Backend) DefaultInvoke(ctx contractframework.ExecutionContext, tx contract.Tx,
	funding contract.FundingOutput) (contractframework.ExecutionOutcome, bool, error) {

	before := len(e.records)
	if err := e.executeDefaultInvokeOutputTx(ctx.RawTx, tx, contractframework.ContractOutputFromFunding(funding)); err != nil {
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
			Block: e.Block, Runtime: e.Runtime, ContractPrefix: e.ContractPrefix,
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

func (e *Backend) executeDefaultInvokeOutput(tx *wire.MsgTx, output ContractOutput) error {
	call := contract.Tx{
		TxID: tx.TxID(), Kind: TxTypeInvoke, ContractType: ContractTypeEVM,
		Contract: output.Contract, Action: contract.ContractInvokeAPIDefault,
		GasLimit: contract.DefaultInvokeGasForType(ContractTypeEVM),
		Funding:  contractframework.ContractFundingOutputs([]ContractOutput{output}),
	}
	return e.executeDefaultInvokeOutputTx(tx, call, output)
}

func (e *Backend) executeDefaultInvokeOutputTx(tx *wire.MsgTx, call contract.Tx, output ContractOutput) error {
	e.fundingTx = tx
	defer func() { e.fundingTx = nil }()
	if !e.Runtime.State.KnownContract(ContractGethAddress(output.Contract)) {
		return nil
	}
	caller, err := e.callerFromContractTx(call, tx, ParsedTx{})
	if err != nil {
		return err
	}
	refund, err := e.refundRecipientFromContractTx(call, tx, ParsedTx{})
	if err != nil {
		return err
	}
	txID := call.TxID
	if txID == "" {
		txID = tx.TxID()
	}
	callID := DeriveInvokeCallID(txID, output.Vout, output.Contract)
	start := len(e.Runtime.AssetIntents)
	result := e.Runtime.Call(CallRequest{
		CallerAddress: caller, TargetAddress: output.Contract.MustEncode(), CallID: callID,
		Gas: call.GasLimit, FundingOutput: &output, Block: e.Block,
	})
	if errors.Is(result.Err, contractframework.ErrAccountingInvariant) {
		return result.Err
	}
	if result.Status != ResultStatusSuccess {
		_, err := e.appendFundingFailure(contractframework.FundingFailureRequest{
			Height: int64(e.Block.Number), TxID: txID, Kind: ExecutionKindInvoke,
			Contract: output.Contract, CallID: callID, Recipient: refund, Funding: []ContractOutput{output},
			GasLimit: call.GasLimit, GasUsed: result.GasUsed, Status: result.Status,
		})
		return err
	}
	return e.appendOutcome(contractframework.ExecutionOutcome{
		Height: int64(e.Block.Number), TxID: txID, Type: TxTypeInvoke, Kind: ExecutionKindInvoke,
		CallID: callID, Contract: output.Contract, Status: result.Status,
		GasLimit: call.GasLimit, GasUsed: result.GasUsed, FundingInputs: []OutPoint{output.OutPoint},
		GasRefundRecipient: refund, AssetIntents: contractframework.CloneAssetIntents(e.Runtime.AssetIntents[start:]),
		RequiresResult: true, ResultFeeMode: ResultFeeModePlainTxFee,
	})
}

func (e *Backend) Finalize() (BlockExecutionResult, error) {
	if len(e.pending) != 0 {
		return BlockExecutionResult{}, fmt.Errorf("%d EVM executions remain unsettled", len(e.pending))
	}
	return e.executionResult(nil), nil
}

func (e *Backend) FinalizeWork() (BlockExecutionResult, error) {
	e.bindManagedSnapshots()
	return e.executionResult(e.pending), nil
}

func (e *Backend) executionResult(pending []ExecutionRecord) BlockExecutionResult {
	return BlockExecutionResult{
		Records:        contractframework.CloneExecutionRecords(e.records),
		PendingRecords: contractframework.CloneExecutionRecords(pending), StateRoot: e.Runtime.State.StateRoot(),
	}
}

func (e *Backend) PendingRecords() []ExecutionRecord {
	return contractframework.CloneExecutionRecords(e.pending)
}
func (e *Backend) Records() []ExecutionRecord {
	return contractframework.CloneExecutionRecords(e.records)
}

func ExecuteBlockAndVerifyStateRoot(req BlockExecutionRequest) (BlockExecutionResult, error) {
	original := req.Runtime
	if original != nil {
		req.Runtime = original.Clone()
	}
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
	if original != nil {
		*original = *req.Runtime
	}
	return result, nil
}

func (e *Backend) executeDeploy(tx *wire.MsgTx, parsed ParsedTx) error {
	return e.ExecuteParsedTx(tx, parsed)
}

func (e *Backend) executeDeployTx(tx *wire.MsgTx, parsed ParsedTx, call contract.Tx) error {
	validated, err := ValidateDeployTxBasic(tx, e.GasConfig)
	if err != nil {
		return fmt.Errorf("%w: %v", contractframework.ErrCallAdmission, err)
	}
	caller, err := e.callerFromContractTx(call, tx, parsed)
	if err != nil {
		return err
	}
	refund, err := e.refundRecipientFromContractTx(call, tx, parsed)
	if err != nil {
		return err
	}
	expected, err := DeriveCreateContractAddress(e.ContractPrefix, EVMAddressFromAddressString(caller), validated.Payload.DeployNonce)
	if err != nil {
		return fmt.Errorf("%w: %v", contractframework.ErrCallAdmission, err)
	}
	outputs, err := FindContractOutputsForContract(tx, StandardContractScriptResolver(e.ContractPrefix), expected)
	if err != nil {
		return err
	}
	if len(outputs) == 0 {
		return fmt.Errorf("%w: EVM DEPLOY has no derived contract output", contractframework.ErrCallAdmission)
	}
	cfg := e.GasConfig.Normalize()
	callID := DeriveDeployCallID(tx.TxID(), expected)
	reject := func(status ResultStatus, used int64) error {
		_, err := e.appendFundingFailure(contractframework.FundingFailureRequest{
			Height: int64(e.Block.Number), TxID: tx.TxID(), Kind: ExecutionKindDeploy,
			Contract: expected, CallID: callID, Recipient: refund, Funding: outputs,
			GasLimit: validated.Payload.GasLimit, GasUsed: used, Status: status,
		})
		return err
	}
	if len(outputs) != 1 || e.Runtime.State.KnownContract(ContractGethAddress(expected)) {
		return reject(ResultStatusInvalid, cfg.DeployBaseGas)
	}
	reserve, err := cfg.ContractFundingFee(ExecutionKindDeploy, validated.Payload.GasLimit, true, e.Block.Number)
	if err != nil {
		return err
	}
	reserve = e.settlementPrecision.NormalizeUp(cfg.GasAssetName, reserve)
	ready, err := contractframework.OutputsHaveRequiredGas(outputs, cfg.GasAssetName, reserve)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("%w: EVM deployment has insufficient Result gas", contractframework.ErrCallAdmission)
	}
	start := len(e.Runtime.AssetIntents)
	result := e.Runtime.Deploy(DeployRequest{
		CallerAddress: caller, CallID: callID, InitCode: validated.Payload.ContractContent,
		Gas: validated.Payload.GasLimit, DeployNonce: validated.Payload.DeployNonce,
		ExpectedContract: expected, FundingOutput: &outputs[0],
		GasAssetName: cfg.GasAssetName, GasFeeReserve: reserve, Block: e.Block,
	})
	if errors.Is(result.Err, contractframework.ErrAccountingInvariant) {
		return result.Err
	}
	if result.Status != ResultStatusSuccess {
		return reject(result.Status, result.GasUsed)
	}
	if !result.Contract.Equal(expected) {
		return fmt.Errorf("%w: EVM deployment address mismatch", contractframework.ErrAccountingInvariant)
	}
	if err := e.Runtime.State.RegisterContractDeployment(ContractGethAddress(expected), caller, validated.Payload.Flags); err != nil {
		return fmt.Errorf("%w: %v", contractframework.ErrAccountingInvariant, err)
	}
	return e.appendOutcome(contractframework.ExecutionOutcome{
		Height: int64(e.Block.Number), TxID: tx.TxID(), Type: TxTypeDeploy, Kind: ExecutionKindDeploy,
		Contract: expected, CallID: callID, Status: ResultStatusSuccess,
		GasLimit: validated.Payload.GasLimit, GasUsed: result.GasUsed,
		RetainedGasFunding: result.RetainedGasFunding, FundingInputs: []OutPoint{outputs[0].OutPoint},
		GasRefundRecipient: refund, AssetIntents: contractframework.CloneAssetIntents(e.Runtime.AssetIntents[start:]),
		RequiresResult: true,
	})
}

func (e *Backend) executeInvoke(tx *wire.MsgTx, parsed ParsedTx) error {
	return e.ExecuteParsedTx(tx, parsed)
}

func (e *Backend) executeInvokeTx(tx *wire.MsgTx, parsed ParsedTx, call contract.Tx) error {
	validated, err := ValidateInvokeTxBasic(tx, StandardContractScriptResolver(e.ContractPrefix), e.contractExists, e.GasConfig)
	if err != nil {
		return fmt.Errorf("%w: %v", contractframework.ErrCallAdmission, err)
	}
	caller, err := e.callerFromContractTx(call, tx, parsed)
	if err != nil {
		return err
	}
	refund, err := e.refundRecipientFromContractTx(call, tx, parsed)
	if err != nil {
		return err
	}
	cfg := e.GasConfig.Normalize()
	funding := []OutPoint{validated.FundingOutput.OutPoint}
	callID := DeriveInvokeCallID(tx.TxID(), validated.FundingOutput.Vout, validated.Contract)
	if validated.Payload.Action != "call" && validated.Payload.Action != contract.ContractInvokeAPIDefault &&
		validated.Payload.Action != contract.ContractInvokeAPIClose {
		return e.executeInvalidInvokeTx(tx, validated, refund, funding, callID)
	}
	reserve, err := cfg.ContractFundingFee(ExecutionKindInvoke, validated.Payload.GasLimit, true, e.Block.Number)
	if err != nil {
		return err
	}
	reserve = e.settlementPrecision.NormalizeUp(cfg.GasAssetName, reserve)
	ready, err := contractframework.OutputHasRequiredGas(validated.FundingOutput, cfg.GasAssetName, reserve)
	if err != nil {
		return err
	}
	if !ready {
		return e.executeInvalidInvokeTx(tx, validated, refund, funding, callID)
	}
	if validated.Payload.Action == contract.ContractInvokeAPIClose {
		return e.executeCloseInvokeTx(tx, validated, caller, refund, funding)
	}
	start := len(e.Runtime.AssetIntents)
	result := e.Runtime.Call(CallRequest{
		CallerAddress: caller, TargetAddress: validated.Contract.MustEncode(), CallID: callID,
		Input: contractframework.CloneBytes(validated.Payload.Param), Gas: validated.Payload.GasLimit,
		FundingOutput: &validated.FundingOutput, GasAssetName: cfg.GasAssetName,
		GasFeeReserve: reserve, Block: e.Block,
	})
	if errors.Is(result.Err, contractframework.ErrAccountingInvariant) {
		return result.Err
	}
	if result.Status != ResultStatusSuccess {
		_, err := e.appendFundingFailure(contractframework.FundingFailureRequest{
			Height: int64(e.Block.Number), TxID: tx.TxID(), Kind: ExecutionKindInvoke,
			Contract: validated.Contract, CallID: callID, Recipient: refund,
			Funding: []ContractOutput{validated.FundingOutput}, GasLimit: validated.Payload.GasLimit,
			GasUsed: result.GasUsed, Status: result.Status,
		})
		return err
	}
	return e.appendOutcome(contractframework.ExecutionOutcome{
		Height: int64(e.Block.Number), TxID: tx.TxID(), Type: TxTypeInvoke, Kind: ExecutionKindInvoke,
		Contract: validated.Contract, CallID: callID, Status: result.Status,
		GasLimit: validated.Payload.GasLimit, GasUsed: result.GasUsed,
		RetainedGasFunding: result.RetainedGasFunding, FundingInputs: funding, GasRefundRecipient: refund,
		AssetIntents: contractframework.CloneAssetIntents(e.Runtime.AssetIntents[start:]), RequiresResult: true,
	})
}

func (e *Backend) executeInvalidInvokeTx(tx *wire.MsgTx, validated InvokeValidation,
	refund string, funding []OutPoint, callID string) error {

	_, err := e.appendFundingFailure(contractframework.FundingFailureRequest{
		Height: int64(e.Block.Number), TxID: tx.TxID(), Kind: ExecutionKindInvoke,
		Contract: validated.Contract, CallID: callID, Recipient: refund,
		Funding: []ContractOutput{validated.FundingOutput}, GasLimit: validated.Payload.GasLimit,
		GasUsed: e.GasConfig.Normalize().InvokeBaseGas, Status: ResultStatusInvalid,
	})
	return err
}

func (e *Backend) executeCloseInvokeTx(tx *wire.MsgTx, validated InvokeValidation,
	actor, refund string, funding []OutPoint) error {

	callID := DeriveInvokeCallID(tx.TxID(), validated.FundingOutput.Vout, validated.Contract)
	addr := ContractGethAddress(validated.Contract)
	if err := e.Runtime.State.ValidateContractClose(addr, actor); err != nil {
		return e.executeInvalidInvokeTx(tx, validated, refund, funding, callID)
	}
	deployer, _ := e.Runtime.State.ContractDeployer(addr)
	before := e.Runtime.Clone()
	start := len(e.Runtime.AssetIntents)
	// Close-call funding is refund escrow, not liquidation capital. The hook
	// may settle only the contract's existing managed quantities.
	result := e.Runtime.Call(CallRequest{
		CallerAddress: actor, TargetAddress: validated.Contract.MustEncode(), CallID: callID,
		Input: evmCloseHookCalldata(), Gas: validated.Payload.GasLimit, Block: e.Block,
	})
	if !evmCloseHookSucceeded(result) {
		// Keep the StateDB pointer stable for the transient quantity view.
		*e.Runtime.State = *before.State
		e.Runtime.AssetIntents = before.AssetIntents
		if errors.Is(result.Err, contractframework.ErrAccountingInvariant) {
			return result.Err
		}
		status := result.Status
		if status == ResultStatusSuccess {
			status = ResultStatusInvalid
		}
		_, err := e.appendFundingFailure(contractframework.FundingFailureRequest{
			Height: int64(e.Block.Number), TxID: tx.TxID(), Kind: ExecutionKindInvoke,
			Contract: validated.Contract, CallID: callID, Recipient: refund,
			Funding: []ContractOutput{validated.FundingOutput}, GasLimit: validated.Payload.GasLimit,
			GasUsed: result.GasUsed, Status: status,
		})
		return err
	}
	intents := contractframework.CloneAssetIntents(e.Runtime.AssetIntents[start:])
	refunds, err := contractframework.NonGasFundingRefundIntents(validated.Contract,
		[]ContractOutput{validated.FundingOutput}, e.GasConfig.Normalize().GasAssetName, refund)
	if err != nil {
		return err
	}
	for i := range refunds {
		refunds[i].CallID = callID
	}
	intents = append(intents, refunds...)
	e.Runtime.State.CloseContract(addr)
	return e.appendOutcome(contractframework.ExecutionOutcome{
		Height: int64(e.Block.Number), TxID: tx.TxID(), Type: TxTypeInvoke, Kind: ExecutionKindInvoke,
		Contract: validated.Contract, CallID: callID, Status: ResultStatusSuccess,
		GasLimit: validated.Payload.GasLimit, GasUsed: result.GasUsed,
		FundingInputs: funding, GasRefundRecipient: refund, AssetIntents: intents,
		RequiresResult: true, CloseContract: true, DeployerAddress: deployer,
		BootstrapAddress: e.GasConfig.Normalize().BootstrapAddress,
	})
}

var evmCloseHookSelector = methodSelector("close()")

func evmCloseHookCalldata() []byte { return appendMethod(evmCloseHookSelector, nil) }

func evmCloseHookSucceeded(result CallResult) bool {
	if result.Status != ResultStatusSuccess || len(result.ReturnData) != 32 {
		return false
	}
	for _, b := range result.ReturnData[:31] {
		if b != 0 {
			return false
		}
	}
	return result.ReturnData[31] == 1
}

func (e *Backend) ExecuteTrigger(call TriggerCall) error {
	if err := call.Trigger.Validate(); err != nil {
		return err
	}
	if !call.Trigger.Due(BlockEnvironment{Height: int64(e.Block.Number)}) {
		return fmt.Errorf("trigger %s is not due", call.Trigger.ID)
	}
	registered, exists := e.Runtime.State.Trigger(call.Trigger.Contract, call.Trigger.ID)
	if !exists {
		return nil
	}
	if registered.Kind != call.Trigger.Kind || registered.Height != call.Trigger.Height ||
		registered.GasLimit != call.GasLimit || !bytes.Equal(registered.Calldata, call.Calldata) {
		return fmt.Errorf("trigger %s does not match registered state", call.Trigger.ID)
	}
	if err := contractframework.ValidateTriggerGasLimit(call.GasLimit, e.GasConfig); err != nil {
		return err
	}
	if !e.contractExists(call.Trigger.Contract) {
		return errors.New("trigger contract does not exist")
	}
	max := e.GasConfig.Normalize().MaxGasPerBlock
	if max > 0 && (e.gasUsed > max || call.GasLimit > max-e.gasUsed) {
		return nil
	}
	reserve, ready, err := e.triggerGasBudget(call.Trigger.Contract, call.GasLimit)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}
	e.Runtime.State.RemoveTrigger(call.Trigger.Contract, call.Trigger.ID)
	callID := DeriveTriggerCallID(call.Trigger.Contract, call.Trigger.ID, int64(e.Block.Number))
	start := len(e.Runtime.AssetIntents)
	base := e.Runtime.AssetBalances
	e.Runtime.AssetBalances = triggerReservedAssetView{
		base: base, owner: ContractAddressHash(call.Trigger.Contract),
		assetName: e.GasConfig.Normalize().GasAssetName, reserve: reserve,
	}
	result := e.Runtime.Call(CallRequest{
		CallerAddress: call.Trigger.Contract.MustEncode(), TargetAddress: call.Trigger.Contract.MustEncode(),
		CallID: callID, Input: call.Calldata, Gas: call.GasLimit, Block: e.Block,
	})
	e.Runtime.AssetBalances = base
	if errors.Is(result.Err, contractframework.ErrAccountingInvariant) {
		return result.Err
	}
	return e.appendOutcome(contractframework.ExecutionOutcome{
		Height: int64(e.Block.Number), Kind: ExecutionKindTrigger,
		CallID: callID, TriggerID: call.Trigger.ID, Contract: call.Trigger.Contract,
		Status: result.Status, GasLimit: call.GasLimit, GasUsed: result.GasUsed,
		AssetIntents: contractframework.CloneAssetIntents(e.Runtime.AssetIntents[start:]), RequiresResult: true,
	})
}

func (e *Backend) triggerHasGasBudget(addr ContractAddress, gasLimit int64) (bool, error) {
	_, ready, err := e.triggerGasBudget(addr, gasLimit)
	return ready, err
}

func (e *Backend) resultPlans(records []ExecutionRecord) ([]ResultPlan, error) {
	return (contractframework.CanonicalResultPlanner{
		GasConfig: e.GasConfig, UTXOs: e.ContractUTXOs, Precision: e.settlementPrecision,
	}).BuildPlans(records)
}

func (e *Backend) applyResultBalances(plans []ResultPlan) error {
	if err := contractframework.ApplyManagedResultBalances(plans,
		e.Runtime.State.ManagedBalance, e.Runtime.State.ManagedContractClosed); err != nil {
		return err
	}
	// Quantities already include settled fees and intents. Drop block-local
	// reservations so a subsequent block does not subtract them twice.
	e.Runtime.AssetIntents = nil
	e.Runtime.AssetBalances = managedStateAssetView{state: e.Runtime.State}
	e.pendingGasReserved = nil
	return nil
}

func (e *Backend) VerifyAndSettleResultTx(tx *wire.MsgTx, verify ResultVerifier) error {
	parsed, err := ParseTx(tx, StandardContractScriptResolver(e.ContractPrefix))
	if err != nil {
		return err
	}
	return e.verifyAndSettleResult(tx, parsed, verify)
}

func (e *Backend) verifyAndSettleResult(tx *wire.MsgTx, parsed ParsedTx, verify ResultVerifier) error {
	e.bindManagedSnapshots()
	if verify == nil {
		verify = e.VerifyResult
	}
	if verify == nil {
		return errors.New("missing EVM canonical Result verifier")
	}
	plans, err := e.resultPlans(e.pending)
	if err != nil {
		return err
	}
	pending, err := verifyResultAgainstPending(tx, parsed, e.pending, verify)
	if err != nil {
		return err
	}
	if err := e.applyResultBalances(plans); err != nil {
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
		Label: "EVM_RESULT", ResultTx: tx, Payload: *parsed.Result, Pending: pending, Verify: verify,
	})
}

func (e *Backend) appendRecord(record ExecutionRecord) error {
	if record.GasUsed < 0 {
		return fmt.Errorf("%w: negative EVM gas used", contractframework.ErrAccountingInvariant)
	}
	next, overflow := contractframework.AddInt64(e.gasUsed, record.GasUsed)
	if overflow {
		return fmt.Errorf("EVM block gas used overflows int64")
	}
	if max := e.GasConfig.Normalize().MaxGasPerBlock; max > 0 && next > max {
		return fmt.Errorf("EVM block gas used %d exceeds limit %d", next, max)
	}
	if err := e.reservePendingGas(record); err != nil {
		return err
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

func (e *Backend) callerFromContractTx(call contract.Tx, raw *wire.MsgTx, parsed ParsedTx) (string, error) {
	if call.Actor != "" {
		return call.Actor, nil
	}
	return e.resolveCaller(raw, call)
}

func (e *Backend) refundRecipientFromContractTx(call contract.Tx, raw *wire.MsgTx, parsed ParsedTx) (string, error) {
	if call.GasRefundRecipient != "" {
		return call.GasRefundRecipient, nil
	}
	return e.resolveGasRefundRecipient(raw, call)
}

func (e *Backend) resolveCaller(tx *wire.MsgTx, call contract.Tx) (string, error) {
	if e.ResolveCaller == nil {
		return "", fmt.Errorf("%w: missing EVM caller resolver", contractframework.ErrCallAdmission)
	}
	caller, err := e.ResolveCaller(tx, call)
	if err != nil {
		return "", err
	}
	if caller == "" {
		return "", fmt.Errorf("%w: empty EVM caller", contractframework.ErrCallAdmission)
	}
	return caller, nil
}

func (e *Backend) resolveGasRefundRecipient(tx *wire.MsgTx, call contract.Tx) (string, error) {
	if e.ResolveGasRefundRecipient != nil {
		recipient, ok, err := e.ResolveGasRefundRecipient(tx, call)
		if err != nil {
			return "", err
		}
		if ok && recipient != "" {
			return recipient, nil
		}
	}
	if call.Actor != "" {
		return call.Actor, nil
	}
	return e.resolveCaller(tx, call)
}

func (e *Backend) contractExists(addr ContractAddress) bool {
	return e.Runtime.State.KnownContract(ContractGethAddress(addr)) && !e.Runtime.State.ContractClosed(ContractGethAddress(addr))
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
