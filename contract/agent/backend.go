package agent

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type InvokerResolver func(tx *wire.MsgTx, contractTx contract.Tx) (string, error)
type PreviousOutputScriptResolver = contractframework.PreviousOutputScriptResolver

func LastInputInvokerResolver(params *chaincfg.Params) InvokerResolver {
	return contractframework.LastInputContractActorResolver("agent", params)
}

func LastInputPreviousOutputInvokerResolver(params *chaincfg.Params, resolve PreviousOutputScriptResolver) InvokerResolver {
	return contractframework.LastInputPreviousOutputContractActorResolver("agent", params, resolve)
}

type BlockExecutionRequest struct {
	Txs            []*wire.MsgTx
	Store          *RuntimeStore
	ContractPrefix string
	RuntimeConfig  RuntimeConfig
	GasConfig      GasConfig
	ContractUTXOs  ContractUTXOProvider
	AssetPrecision contractframework.AssetPrecisionResolver
	BlockHeight    int64
	BlockTime      int64
	ResolveInvoker InvokerResolver
}

type BlockExecutionResult = contractframework.BackendBlockExecutionResult

type BlockResultBuildRequest struct {
	Txs            []*wire.MsgTx
	Store          *RuntimeStore
	ContractPrefix string
	RuntimeConfig  RuntimeConfig
	GasConfig      GasConfig
	ContractUTXOs  ContractUTXOProvider
	AssetPrecision contractframework.AssetPrecisionResolver
	BlockHeight    int64
	BlockTime      int64
	ResolveInvoker InvokerResolver
	ResolveScript  ResultRecipientScriptResolver
	ResolveOutput  ResultOutputResolver
}

type BlockResultBuildResult = contractframework.BackendBlockResultBuildResult

type Backend struct {
	Store          *RuntimeStore
	ContractPrefix string
	RuntimeConfig  RuntimeConfig
	GasConfig      GasConfig
	ContractUTXOs  ContractUTXOProvider
	AssetPrecision contractframework.AssetPrecisionResolver
	BlockHeight    int64
	BlockTime      int64
	ResolveInvoker InvokerResolver

	records         []ExecutionRecord
	settlementPlans []*PredictionSettlementPlan
	resultPlans     []ResultPlan
	utxoOverlay     *contractframework.ContractUTXOOverlay
	finalized       *BlockExecutionResult
}

func ExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	original := req.Store
	if original != nil {
		req.Store = original.Clone()
	}
	executor := NewBackend(req)
	for _, tx := range req.Txs {
		if err := executor.ExecuteTx(tx); err != nil {
			return BlockExecutionResult{}, err
		}
	}
	result, err := executor.Finalize()
	if err != nil {
		return BlockExecutionResult{}, err
	}
	if original != nil {
		*original = *executor.Store
	}
	return result, nil
}

func BuildBlockResultTxs(req BlockResultBuildRequest) (BlockResultBuildResult, error) {
	store := NewRuntimeStore()
	if req.Store != nil {
		store = req.Store.Clone()
	}
	executor := NewBackend(BlockExecutionRequest{
		Txs: req.Txs, Store: store, ContractPrefix: req.ContractPrefix, RuntimeConfig: req.RuntimeConfig,
		GasConfig: req.GasConfig, ContractUTXOs: req.ContractUTXOs, AssetPrecision: req.AssetPrecision,
		BlockHeight: req.BlockHeight, BlockTime: req.BlockTime, ResolveInvoker: req.ResolveInvoker,
	})
	result, err := contractframework.BuildSingleResultTxBlock(contractframework.SingleResultBlockRequest[BlockExecutionResult]{
		ModuleName: "agent", Txs: req.Txs, Prefix: executor.ContractPrefix,
		Classify:  ClassifyTxForBlockOrder,
		IsModule:  func(info TxOrderInfo) bool { return info.IsAgent },
		IsResult:  func(info TxOrderInfo) bool { return info.Type == TxTypeResult },
		ExecuteTx: executor.ExecuteTx, Finalize: executor.Finalize,
		Plans: func(exec BlockExecutionResult) []ResultPlan { return exec.ResultPlans },
		SetPlans: func(exec BlockExecutionResult, plans []ResultPlan) BlockExecutionResult {
			exec.ResultPlans = plans
			return exec
		},
		Policy: contractframework.SingleResultTxPolicy{
			Label: "agent", Status: ResultStatusSuccess,
			GasAssetName:  req.GasConfig.Normalize().GasAssetName,
			ResolveScript: req.ResolveScript, ResolveOutput: req.ResolveOutput,
		},
	})
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	execution, ok := result.Execution.(BlockExecutionResult)
	if !ok {
		return BlockResultBuildResult{}, errors.New("agent result build execution has unexpected type")
	}
	if req.Store != nil {
		*req.Store = *store
	}
	return BlockResultBuildResult{ResultTxs: result.ResultTxs, Execution: execution}, nil
}

func NewBackend(req BlockExecutionRequest) *Backend {
	store := req.Store
	if store == nil {
		store = NewRuntimeStore()
	}
	req.RuntimeConfig.AssetPrecision = req.AssetPrecision
	store.ApplyConfig(req.RuntimeConfig)
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	overlay := contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
		Prefix: prefix, ContractType: ContractTypeAgent,
		Base: contractframework.WithoutBlockOutputs(req.ContractUTXOs, req.Txs),
	})
	return &Backend{
		Store: store, ContractPrefix: prefix, RuntimeConfig: req.RuntimeConfig,
		GasConfig: req.GasConfig, ContractUTXOs: overlay.Provider, AssetPrecision: req.AssetPrecision,
		BlockHeight: req.BlockHeight, BlockTime: req.BlockTime, ResolveInvoker: req.ResolveInvoker,
		utxoOverlay: overlay,
	}
}

func (e *Backend) ExecuteTx(tx *wire.MsgTx) error {
	if e.finalized != nil {
		return fmt.Errorf("agent block has already been finalized")
	}
	if err := contractframework.NewExecutor(e.executorConfig()).ExecuteTx(tx); err != nil {
		return err
	}
	return e.utxoOverlay.ApplyTx(tx, e.BlockHeight)
}

func (e *Backend) ExecuteParsedTx(tx *wire.MsgTx, parsed ParsedTx) error {
	if e.finalized != nil {
		return fmt.Errorf("agent block has already been finalized")
	}
	if err := contractframework.NewExecutor(e.executorConfig()).ExecuteParsedTx(tx, parsed); err != nil {
		return err
	}
	return e.utxoOverlay.ApplyTx(tx, e.BlockHeight)
}

func (e *Backend) executorConfig() contractframework.ExecutorConfig {
	return contractframework.ExecutorConfig{
		Backend: e, Prefix: e.ContractPrefix, ParseSpec: agentParseSpec(), Resolver: StandardContractScriptResolver,
		Context: contractframework.ExecutionContext{
			Prefix: e.ContractPrefix, Height: e.BlockHeight, Time: e.BlockTime, GasConfig: e.GasConfig,
		},
		ResolveActor: func(tx *wire.MsgTx, contractTx contract.Tx) (string, error) {
			if e.ResolveInvoker == nil {
				return "", fmt.Errorf("%w: missing agent invoker resolver", contractframework.ErrCallAdmission)
			}
			return e.ResolveInvoker(tx, contractTx)
		},
	}
}

func (e *Backend) ContractType() byte  { return ContractTypeAgent }
func (e *Backend) Name() string        { return "agent" }
func (e *Backend) Priority() int       { return 3 }
func (e *Backend) StateRoot() [32]byte { return e.Store.StateRoot() }
func (e *Backend) Snapshot() any       { return e.Store }

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

	// Prediction has no implicit bet action. Unhandled funding is refunded by
	// the common executor instead of being absorbed into the prediction pool.
	return contractframework.ExecutionOutcome{}, false, nil
}

func (e *Backend) FinalizeBlock(ctx contractframework.ExecutionContext) ([]contractframework.ExecutionOutcome, error) {
	result, err := e.Finalize()
	if err != nil {
		return nil, err
	}
	return contractframework.ExecutionOutcomesFromRecords(result.Records), nil
}

func (e *Backend) Finalize() (BlockExecutionResult, error) {
	if e.finalized != nil {
		return cloneAgentBlockResult(*e.finalized), nil
	}
	e.Store.AdvancePredictionStatuses(e.BlockHeight, e.BlockTime)
	plans := contractframework.AddGasFeesToResultPlans(e.resultPlans, e.records)
	plans = contractframework.AttachCallFunding(plans, e.records)
	records := contractframework.BindExecutionBalances(e.records, e.ManagedBalance)
	var err error
	plans, err = AugmentResultPlans(plans, e.ContractUTXOs, e.Store, e.AssetPrecision,
		e.GasConfig.Normalize().GasAssetName, e.RuntimeConfig.BootstrapAddress)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	if err := contractframework.ApplyManagedResultBalances(plans, e.ManagedBalance, e.Store.ContractClosed); err != nil {
		return BlockExecutionResult{}, err
	}
	for _, key := range e.Store.sortedKeys() {
		if runtime := e.Store.runtimes[key]; runtime != nil && runtime.state.Closed {
			runtime.state.Prediction.GasBalance = ""
		}
	}
	result := BlockExecutionResult{
		Records: records, SettlementPlans: cloneSettlementPlans(e.settlementPlans),
		ResultPlans: contractframework.CloneResultPlans(plans), StateRoot: e.Store.StateRoot(),
	}
	e.finalized = &result
	return cloneAgentBlockResult(result), nil
}

func cloneAgentBlockResult(result BlockExecutionResult) BlockExecutionResult {
	result.Records = contractframework.CloneExecutionRecords(result.Records)
	result.PendingRecords = contractframework.CloneExecutionRecords(result.PendingRecords)
	result.SettlementPlans = cloneSettlementPlans(result.SettlementPlans)
	result.ResultPlans = contractframework.CloneResultPlans(result.ResultPlans)
	return result
}

func (e *Backend) resultFee() (*scommon.Decimal, error) {
	fee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return nil, err
	}
	policy := contractframework.AssetPrecisionPolicy{Fallback: contract.GasFeePrecision, Resolve: e.AssetPrecision}
	return policy.NormalizeUp(e.GasConfig.Normalize().GasAssetName, fee), nil
}

func (e *Backend) executeDeploy(tx *wire.MsgTx) error {
	return e.ExecuteTx(tx)
}

func (e *Backend) executeDeployTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contract.Tx) error {
	validated, err := contractframework.ValidateParsedDeployBasic(parsed, "agent", e.GasConfig)
	if err != nil {
		return fmt.Errorf("%w: %v", contractframework.ErrCallAdmission, err)
	}
	deployer := contractTx.Actor
	if deployer == "" {
		return fmt.Errorf("%w: agent deployer is empty", contractframework.ErrCallAdmission)
	}
	deployPayload := agentDeployPayloadFromFramework(&validated.Payload)
	addr, _, err := DeriveContractAddress(e.ContractPrefix, deployPayload.SubType,
		deployPayload.ContractContent, deployer, deployPayload.DeployNonce)
	if err != nil {
		return err
	}
	funding, err := FindContractOutputsForContract(tx, StandardContractScriptResolver(e.ContractPrefix), addr)
	if err != nil {
		return err
	}
	if len(funding) != 1 {
		return fmt.Errorf("%w: agent DEPLOY must use exactly one derived contract output", contractframework.ErrCallAdmission)
	}
	fee, err := e.resultFee()
	if err != nil {
		return err
	}
	gasName := e.GasConfig.Normalize().GasAssetName
	ready, err := contractframework.OutputHasRequiredGas(funding[0], gasName, fee)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("%w: agent deployment has insufficient Result gas", contractframework.ErrCallAdmission)
	}
	reject := func() error {
		_, err := e.appendFundingFailure(contractframework.FundingFailureRequest{
			Height: e.BlockHeight, TxID: tx.TxID(), Kind: ExecutionKindDeploy,
			Contract: addr, CallID: DeriveDeployCallID(tx.TxID(), addr), Recipient: deployer,
			Funding: funding, GasLimit: validated.Payload.GasLimit, GasAsset: gasName, GasFee: fee,
		})
		return err
	}
	runtime, runtimeErr := NewRuntimeWithDeployer(addr, *deployPayload, e.RuntimeConfig, deployer)
	if runtimeErr != nil || e.Store.Exists(addr) || e.Store.ActiveNetworkExclusiveExists(runtime) {
		return reject()
	}
	business, err := contractframework.SummarizeBusinessFunding(funding, gasName)
	if err != nil {
		return err
	}
	if business.PlainSat != 0 || len(business.Assets) != 0 {
		return reject()
	}
	gas, err := funding[0].AssetAmount(gasName)
	if err != nil {
		return err
	}
	runtime.addGasBalance(gas.SubAlignPrecision(fee).String())
	e.Store.Add(runtime)
	e.appendOutcome(contractframework.ExecutionOutcome{
		Height: e.BlockHeight, TxID: tx.TxID(), Type: TxTypeDeploy, Kind: ExecutionKindDeploy,
		CallID: DeriveDeployCallID(tx.TxID(), addr), Contract: addr, Status: ResultStatusSuccess,
		GasLimit: validated.Payload.GasLimit, FundingInputs: []OutPoint{funding[0].OutPoint},
		GasFee: fee, RequiresResult: true,
	})
	e.resultPlans = append(e.resultPlans, ResultPlan{Contract: addr.EncodeAddress(), Inputs: []OutPoint{funding[0].OutPoint}})
	return nil
}

func (e *Backend) executeInvoke(tx *wire.MsgTx, parsed ParsedTx) error {
	return e.ExecuteParsedTx(tx, parsed)
}

func (e *Backend) executeInvokeTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contract.Tx) error {
	validated, err := ValidateParsedInvokeTxBasic(parsed, e.Store.Exists, e.GasConfig)
	if err != nil {
		return fmt.Errorf("%w: %v", contractframework.ErrCallAdmission, err)
	}
	runtime, ok := e.Store.Get(validated.Contract)
	if !ok {
		return fmt.Errorf("%w: unknown agent contract", contractframework.ErrCallAdmission)
	}
	invoker := contractTx.Actor
	if invoker == "" {
		return fmt.Errorf("%w: missing agent invoker", contractframework.ErrCallAdmission)
	}
	fee, err := e.resultFee()
	if err != nil {
		return err
	}
	gasName := e.GasConfig.Normalize().GasAssetName
	reject := func() error { return e.executeInvalidInvoke(tx, validated, invoker) }
	if err := runtime.CheckInvocationLifecycle(validated.Payload.Action, invoker); err != nil {
		return reject()
	}
	switch validated.Payload.Action {
	case InvokeAPIReady, InvokeAPIReject, InvokeAPIBet, InvokeAPIConfirm, InvokeAPIClose:
	default:
		return reject()
	}
	ready, err := contractframework.OutputHasRequiredGas(validated.FundingOutput, gasName, fee)
	if err != nil {
		return err
	}
	if !ready {
		return reject()
	}
	if validated.Payload.Action != InvokeAPIBet && validated.Payload.Action != InvokeAPIClose {
		business, err := contractframework.SummarizeBusinessFunding([]ContractOutput{validated.FundingOutput}, gasName)
		if err != nil {
			return err
		}
		if business.PlainSat != 0 || len(business.Assets) != 0 {
			return reject()
		}
	}
	if validated.Payload.Action == InvokeAPIBet {
		for _, asset := range validated.FundingOutput.TxAssets() {
			name := asset.Name.String()
			if name != gasName && name != runtime.Contract().BetAsset {
				return reject()
			}
		}
		if gasName != SatoshiAssetName && runtime.Contract().BetAsset != SatoshiAssetName && validated.FundingOutput.PlainValue() != 0 {
			return reject()
		}
	}

	// Business validation runs on a local candidate. A rejected invocation may
	// not leave a partially mutated bet, confirmation or close state behind.
	candidate := runtime.Clone()
	var settlement *PredictionSettlementPlan
	switch validated.Payload.Action {
	case InvokeAPIReady:
		err = candidate.ApplyReady(ApplyReadyRequest{Invoker: invoker})
	case InvokeAPIReject:
		err = e.applyReject(candidate, validated, invoker)
	case InvokeAPIBet:
		settlement, err = e.applyBet(candidate, validated, invoker)
	case InvokeAPIConfirm:
		settlement, err = e.applyConfirm(candidate, validated, invoker)
	case InvokeAPIClose:
		settlement, err = e.applyClose(candidate, validated, invoker)
	default:
		err = fmt.Errorf("unsupported agent action %s", validated.Payload.Action)
	}
	if err != nil {
		if errors.Is(err, contractframework.ErrAccountingInvariant) {
			return err
		}
		return reject()
	}
	plan := ResultPlan{
		Contract: validated.Contract.MustEncode(), Height: e.BlockHeight,
		Inputs: []OutPoint{validated.FundingOutput.OutPoint},
	}
	var intents []AssetIntent
	if settlement != nil {
		settlement.Height = e.BlockHeight
		settlement.Inputs = []OutPoint{validated.FundingOutput.OutPoint}
		plan, err = contractframework.BuildSettlementResultPlan(settlement, e.settlementResultOptions())
		if err != nil {
			return err
		}
		intents, err = contractframework.BuildSettlementAssetIntents(settlement, e.settlementResultOptions())
		if err != nil {
			return err
		}
	}
	callID := DeriveInvokeCallID(tx.TxID(), validated.FundingOutput.Vout, validated.Contract)
	outcome := contractframework.ExecutionOutcome{
		Height: e.BlockHeight, TxID: tx.TxID(), Type: TxTypeInvoke, Kind: ExecutionKindInvoke,
		CallID: callID, Contract: validated.Contract, Status: ResultStatusSuccess,
		GasLimit: validated.Payload.GasLimit, FundingInputs: []OutPoint{validated.FundingOutput.OutPoint},
		GasFee: fee, GasRefundRecipient: invoker, AssetIntents: intents, RequiresResult: true,
	}
	if validated.Payload.Action == InvokeAPIBet && candidate.Contract().BetAsset == gasName {
		stake, _, err := betAndGasFundingAmount(validated.FundingOutput, gasName, gasName, fee)
		if err != nil {
			return err
		}
		outcome.RetainedGasFunding = parseDecimalOrZero(stake)
	}
	if validated.Payload.Action == InvokeAPIClose {
		outcome.CloseContract = true
		outcome.DeployerAddress = candidate.deployer
		outcome.BootstrapAddress = e.RuntimeConfig.BootstrapAddress
		refunds, err := contractframework.NonGasFundingRefundIntents(validated.Contract,
			[]ContractOutput{validated.FundingOutput}, gasName, invoker)
		if err != nil {
			return err
		}
		for i := range refunds {
			refunds[i].CallID = callID
		}
		refundOutputs, err := resultOutputsFromAssetIntents(refunds)
		if err != nil {
			return err
		}
		plan.Outputs = append(plan.Outputs, refundOutputs...)
		outcome.AssetIntents = append(outcome.AssetIntents, refunds...)
	}
	*runtime = *candidate
	if settlement != nil {
		e.settlementPlans = append(e.settlementPlans, settlement)
	}
	e.resultPlans = append(e.resultPlans, plan)
	e.appendOutcome(outcome)
	return nil
}

func (e *Backend) appendOutcome(outcome contractframework.ExecutionOutcome) {
	e.records = append(e.records, outcome.ToRecord())
}

func (e *Backend) executeInvalidInvoke(tx *wire.MsgTx, validated InvokeValidation, invoker string) error {
	fee, err := e.resultFee()
	if err != nil {
		return err
	}
	_, err = e.appendFundingFailure(contractframework.FundingFailureRequest{
		Height: e.BlockHeight, TxID: tx.TxID(), Kind: ExecutionKindInvoke,
		Contract:  validated.Contract,
		CallID:    DeriveInvokeCallID(tx.TxID(), validated.FundingOutput.Vout, validated.Contract),
		Recipient: invoker, Funding: []ContractOutput{validated.FundingOutput},
		GasLimit: validated.Payload.GasLimit, GasAsset: e.GasConfig.Normalize().GasAssetName, GasFee: fee,
	})
	return err
}

func resultOutputsFromAssetIntents(intents []AssetIntent) ([]ResultOutput, error) {
	outputs := make([]ResultOutput, 0, len(intents))
	for _, intent := range intents {
		output, err := contractframework.ResultOutputWithAsset(intent.To, intent.AssetName, intent.Amount)
		if err != nil {
			return nil, err
		}
		output.Reason = "refund"
		if !contractframework.ResultOutputIsZero(output) {
			outputs = append(outputs, output)
		}
	}
	return outputs, nil
}

func (e *Backend) lastOutcomeSince(before int) (contractframework.ExecutionOutcome, error) {
	return contractframework.LastExecutionOutcomeSince(e.records, before)
}

func (e *Backend) applyReject(runtime *Runtime, validated InvokeValidation, invoker string) error {
	param, err := DecodePredictionRejectParam(validated.Payload.Param)
	if err != nil {
		return err
	}
	return runtime.ApplyReject(ApplyRejectRequest{Invoker: invoker, Param: param})
}

func (e *Backend) applyBet(runtime *Runtime, validated InvokeValidation, invoker string) (*PredictionSettlementPlan, error) {
	param, err := DecodePredictionBetParam(validated.Payload.Param)
	if err != nil {
		return nil, err
	}
	fee, err := e.resultFee()
	if err != nil {
		return nil, err
	}
	amount, _, err := betAndGasFundingAmount(validated.FundingOutput,
		runtime.Contract().BetAsset, e.GasConfig.Normalize().GasAssetName, fee)
	if err != nil {
		return nil, err
	}
	timeValue, err := e.predictionTimeValue(runtime.Contract())
	if err != nil {
		return nil, err
	}
	// Result fees and unused call gas are accounted by the framework, not by
	// the operating-gas cache. Only the actual stake becomes a user liability.
	return nil, runtime.ApplyBet(ApplyBetRequest{
		Invoker: invoker, Param: param, AssetName: runtime.Contract().BetAsset,
		Amount: amount, TimeValue: timeValue,
	})
}

func (e *Backend) applyConfirm(runtime *Runtime, validated InvokeValidation, invoker string) (*PredictionSettlementPlan, error) {
	param, err := DecodePredictionConfirmParam(validated.Payload.Param)
	if err != nil {
		return nil, err
	}
	timeValue, err := e.predictionTimeValue(runtime.Contract())
	if err != nil {
		return nil, err
	}
	return runtime.ApplyConfirm(ApplyConfirmRequest{Invoker: invoker, Param: param, TimeValue: timeValue})
}

func (e *Backend) applyClose(runtime *Runtime, validated InvokeValidation, invoker string) (*PredictionSettlementPlan, error) {
	if len(validated.Payload.Param) != 0 {
		return nil, fmt.Errorf("agent close takes no parameters")
	}
	timeValue, err := e.predictionTimeValue(runtime.Contract())
	if err != nil {
		return nil, err
	}
	return runtime.ApplyClose(ApplyCloseRequest{Invoker: invoker, TimeValue: timeValue})
}

func (e *Backend) settlementResultOptions() contractframework.SettlementResultOptions {
	return agentSettlementResultOptions(e.AssetPrecision)
}

func (e *Backend) predictionTimeValue(contract PredictionContract) (int64, error) {
	if contract.TimeBase != TimeBaseUnix {
		return e.BlockHeight, nil
	}
	if e.BlockTime != 0 {
		return e.BlockTime, nil
	}
	return 0, fmt.Errorf("unix prediction requires block time")
}

func agentDeployRuntime(validated DeployValidation) (*Runtime, error) {
	runtime, ok := validated.Runtime.(*Runtime)
	if !ok || runtime == nil {
		return nil, errors.New("agent deploy runtime has unexpected type")
	}
	return runtime, nil
}

func fundingAmount(output ContractOutput, assetName string) (string, error) {
	amount, err := output.AssetAmount(assetName)
	if err != nil {
		return "", err
	}
	return decimalAdd(zeroDecimal(), amount).String(), nil
}

func betAndGasFundingAmount(output ContractOutput, betAssetName, gasAssetName string,
	requiredGas *scommon.Decimal) (string, string, error) {

	betTotalText, err := fundingAmount(output, betAssetName)
	if err != nil {
		return "", "", err
	}
	gasTotalText := ""
	if gasAssetName != "" {
		gasTotalText, err = fundingAmount(output, gasAssetName)
		if err != nil {
			return "", "", err
		}
	}
	if betAssetName == "" || betAssetName != gasAssetName {
		return betTotalText, gasTotalText, nil
	}
	betTotal := parseDecimalOrZero(betTotalText)
	gasReserve := zeroDecimal()
	if requiredGas != nil && requiredGas.Sign() > 0 {
		gasReserve = requiredGas.Clone()
	}
	if betTotal.Cmp(gasReserve) < 0 {
		return "", "", fmt.Errorf("prediction bet funding %s is below required gas %s", betTotal.String(), gasReserve.String())
	}
	return betTotal.SubAlignPrecision(gasReserve).String(), gasReserve.String(), nil
}

func cloneSettlementPlans(in []*PredictionSettlementPlan) []*PredictionSettlementPlan {
	return contractframework.CloneSettlementPlans(in)
}
