package template

import (
	"errors"
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type InvokerResolver func(tx *wire.MsgTx, contractTx contractcommon.Tx) (string, error)
type PreviousOutputScriptResolver = contractframework.PreviousOutputScriptResolver

func LastInputInvokerResolver(params *chaincfg.Params) InvokerResolver {
	return contractframework.LastInputContractActorResolver("template", params)
}

func LastInputPreviousOutputInvokerResolver(params *chaincfg.Params, resolve PreviousOutputScriptResolver) InvokerResolver {
	return contractframework.LastInputPreviousOutputContractActorResolver("template", params, resolve)
}

type BlockExecutionRequest struct {
	Txs            []*wire.MsgTx
	Store          *RuntimeStore
	Registry       *Registry
	ContractPrefix string
	GasConfig      GasConfig
	ContractUTXOs  ContractUTXOProvider
	AssetPrecision contractframework.AssetPrecisionResolver
	BlockHeight    int64
	ResolveInvoker InvokerResolver
}

type BlockExecutionResult = contractframework.BackendBlockExecutionResult

type BlockResultBuildRequest struct {
	Txs            []*wire.MsgTx
	Store          *RuntimeStore
	Registry       *Registry
	ContractPrefix string
	GasConfig      GasConfig
	ContractUTXOs  ContractUTXOProvider
	AssetPrecision contractframework.AssetPrecisionResolver
	BlockHeight    int64
	ResolveInvoker InvokerResolver
	ResolveScript  ResultRecipientScriptResolver
	ResolveOutput  ResultOutputResolver
}

type BlockResultBuildResult = contractframework.BackendBlockResultBuildResult

type Backend struct {
	Store          *RuntimeStore
	Registry       *Registry
	ContractPrefix string
	GasConfig      GasConfig
	ContractUTXOs  ContractUTXOProvider
	AssetPrecision contractframework.AssetPrecisionResolver
	BlockHeight    int64
	ResolveInvoker InvokerResolver

	records     []ExecutionRecord
	closing     map[string]bool
	utxoOverlay *contractframework.ContractUTXOOverlay
	finalized   *BlockExecutionResult
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
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	store := NewRuntimeStore()
	if req.Store != nil {
		store = req.Store.Clone()
	}
	executor := NewBackend(BlockExecutionRequest{
		Txs: req.Txs, Store: store, Registry: req.Registry, ContractPrefix: prefix,
		GasConfig: req.GasConfig, ContractUTXOs: req.ContractUTXOs,
		AssetPrecision: req.AssetPrecision, BlockHeight: req.BlockHeight,
		ResolveInvoker: req.ResolveInvoker,
	})
	result, err := contractframework.BuildSingleResultTxBlock(contractframework.SingleResultBlockRequest[BlockExecutionResult]{
		ModuleName: "template", Txs: req.Txs, Prefix: prefix,
		Classify: ClassifyTxForBlockOrder,
		IsModule: func(info TxOrderInfo) bool { return info.IsTemplate },
		IsResult: func(info TxOrderInfo) bool { return info.Type == TxTypeResult },
		ExecuteTx: executor.ExecuteTx, Finalize: executor.Finalize,
		Plans: func(exec BlockExecutionResult) []ResultPlan { return exec.ResultPlans },
		SetPlans: func(exec BlockExecutionResult, plans []ResultPlan) BlockExecutionResult {
			exec.ResultPlans = plans
			return exec
		},
		Policy: contractframework.SingleResultTxPolicy{
			Label: "template", Status: ResultStatusSuccess,
			GasAssetName: req.GasConfig.Normalize().GasAssetName,
			PlanCount: resultPlanCount, ResolveScript: req.ResolveScript, ResolveOutput: req.ResolveOutput,
		},
	})
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	execution, ok := result.Execution.(BlockExecutionResult)
	if !ok {
		return BlockResultBuildResult{}, errors.New("template result build execution has unexpected type")
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
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	overlay := contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
		Prefix: prefix, ContractType: ContractTypeTemplate,
		Base: contractframework.WithoutBlockOutputs(req.ContractUTXOs, req.Txs),
	})
	return &Backend{
		Store: store, Registry: req.Registry, ContractPrefix: prefix,
		GasConfig: req.GasConfig, ContractUTXOs: overlay.Provider,
		AssetPrecision: req.AssetPrecision, BlockHeight: req.BlockHeight,
		ResolveInvoker: req.ResolveInvoker, closing: make(map[string]bool), utxoOverlay: overlay,
	}
}

func (e *Backend) ExecuteTx(tx *wire.MsgTx) error {
	if e.finalized != nil {
		return fmt.Errorf("template block has already been finalized")
	}
	if err := contractframework.NewExecutor(e.executorConfig()).ExecuteTx(tx); err != nil {
		return err
	}
	return e.utxoOverlay.ApplyTx(tx, e.BlockHeight)
}

func (e *Backend) ExecuteParsedTx(tx *wire.MsgTx, parsed ParsedTx) error {
	if e.finalized != nil {
		return fmt.Errorf("template block has already been finalized")
	}
	if err := contractframework.NewExecutor(e.executorConfig()).ExecuteParsedTx(tx, parsed); err != nil {
		return err
	}
	return e.utxoOverlay.ApplyTx(tx, e.BlockHeight)
}

func (e *Backend) executorConfig() contractframework.ExecutorConfig {
	return contractframework.ExecutorConfig{
		Backend: e, Prefix: e.ContractPrefix, ParseSpec: templateParseSpec(),
		Resolver: StandardContractScriptResolver,
		Context: contractframework.ExecutionContext{
			Prefix: e.ContractPrefix, Height: e.BlockHeight, Time: e.BlockHeight, GasConfig: e.GasConfig,
		},
		ResolveActor: func(tx *wire.MsgTx, contractTx contractcommon.Tx) (string, error) {
			if e.ResolveInvoker == nil {
				return "", nil
			}
			return e.ResolveInvoker(tx, contractTx)
		},
	}
}

func (e *Backend) ContractType() byte { return ContractTypeTemplate }
func (e *Backend) Name() string       { return "template" }
func (e *Backend) Priority() int      { return 1 }

func (e *Backend) Deploy(ctx contractframework.ExecutionContext, tx contractcommon.Tx) (contractframework.ExecutionOutcome, error) {
	before := len(e.records)
	if err := e.executeDeployTx(ctx.RawTx, ctx.ParsedTx, tx); err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	return e.lastOutcomeSince(before)
}

func (e *Backend) Invoke(ctx contractframework.ExecutionContext, tx contractcommon.Tx) (contractframework.ExecutionOutcome, error) {
	before := len(e.records)
	if err := e.executeInvokeTx(ctx.RawTx, ctx.ParsedTx, tx); err != nil {
		return contractframework.ExecutionOutcome{}, err
	}
	return e.lastOutcomeSince(before)
}

func (e *Backend) DefaultInvoke(ctx contractframework.ExecutionContext,
	tx contractcommon.Tx, funding contractcommon.FundingOutput) (contractframework.ExecutionOutcome, bool, error) {

	before := len(e.records)
	output := contractframework.ContractOutputFromFunding(funding)
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
	result, err := e.Finalize()
	if err != nil {
		return nil, err
	}
	return contractframework.ExecutionOutcomesFromRecords(result.Records), nil
}

func (e *Backend) StateRoot() [32]byte { return e.Store.StateRoot() }
func (e *Backend) Snapshot() any      { return e.Store }

func (e *Backend) executeDefaultInvokeOutput(tx *wire.MsgTx, output ContractOutput) error {
	return e.ExecuteTx(tx)
}

func (e *Backend) resultFee(cfg GasConfig) (*scommon.Decimal, error) {
	fee, err := cfg.ResultFee(e.BlockHeight)
	if err != nil {
		return nil, err
	}
	policy := contractframework.AssetPrecisionPolicy{Fallback: contractcommon.GasFeePrecision, Resolve: e.AssetPrecision}
	return policy.NormalizeUp(cfg.Normalize().GasAssetName, fee), nil
}

func (e *Backend) executeDefaultInvokeOutputTx(tx *wire.MsgTx,
	contractTx contractcommon.Tx, output ContractOutput) error {

	if output.Contract.ContractType() != ContractTypeTemplate {
		return nil
	}
	runtime, ok := e.Store.Get(output.Contract)
	if !ok {
		return nil
	}
	gasConfig := GasConfigForRuntime(e.GasConfig, runtime)
	invoker := contractTx.Actor
	if invoker == "" && e.ResolveInvoker != nil {
		var err error
		invoker, err = e.ResolveInvoker(tx, contractTx)
		if err != nil {
			return err
		}
	}
	if invoker == "" {
		return fmt.Errorf("%w: missing template invoker", contractframework.ErrCallAdmission)
	}
	contractTx.Actor = invoker
	resultFee, err := e.resultFee(gasConfig)
	if err != nil {
		return err
	}
	reject := func() error {
		_, err := e.RejectFunding(contractframework.ExecutionContext{RawTx: tx}, contractTx)
		return err
	}
	if err := runtime.CheckInvocationLifecycle(contractcommon.ContractInvokeAPIDefault, invoker); err != nil {
		return reject()
	}
	if e.closing[output.Contract.MustEncode()] {
		return reject()
	}
	if err := checkTemplateFundingAssets(runtime.Contract(), output, gasConfig.GasAssetName); err != nil {
		return reject()
	}
	if err := checkRuntimeAutopayDelegateCapacity(runtime, invoker); err != nil {
		return reject()
	}
	hasResultGas, err := contractframework.OutputHasRequiredGas(output, gasConfig.GasAssetName, resultFee)
	if err != nil {
		return err
	}
	fundingOutput := output
	if hasResultGas && templateGasIsBusinessAsset(runtime.Contract(), gasConfig.GasAssetName) {
		if err := fundingOutput.SubAssetAmount(gasConfig.GasAssetName, resultFee); err != nil {
			return err
		}
	} else if _, autopay := runtime.Contract().(*AutopayContract); autopay && hasResultGas {
		if err := fundingOutput.SubAssetAmount(gasConfig.GasAssetName, resultFee); err != nil {
			return err
		}
	}
	txID := contractTx.TxID
	if txID == "" {
		txID = tx.TxID()
	}
	callID := DeriveInvokeCallID(txID, output.Vout, output.Contract)
	item, err := runtime.ApplyDefaultInvoke(ApplyInvokeRequest{
		Action: contractcommon.ContractInvokeAPIDefault, CallID: callID, Invoker: invoker,
		FundingOutput: fundingOutput, Height: e.BlockHeight, Timestamp: e.BlockHeight,
		ResultGasFee: contractframework.GasFeeIf(hasResultGas, resultFee),
		ApplyDefaultRetention: !hasResultGas,
	})
	if err != nil {
		return err
	}
	if item == nil {
		return nil
	}
	if !templateResultGasIsSeparate(runtime.Contract(), gasConfig.GasAssetName) &&
		!templateGasIsBusinessAsset(runtime.Contract(), gasConfig.GasAssetName) {
		if err := runtime.ApplyGasFunding(fundingOutput, gasConfig.GasAssetName); err != nil {
			return err
		}
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	runtime.IncrementInvokeCount()
	gasRefundRecipient := ""
	if templateUsesFrameworkGasRefund(runtime.Contract(), gasConfig.GasAssetName) {
		gasRefundRecipient = invoker
	}
	e.appendOutcome(contractframework.ExecutionOutcome{
		Height: e.BlockHeight, TxID: txID, Type: TxTypeInvoke, Kind: ExecutionKindInvoke,
		CallID: callID, Contract: output.Contract, GasLimit: gasConfig.InvokeBaseGas,
		FundingInputs: []OutPoint{output.OutPoint}, ItemIDs: []int64{item.ID},
		GasRefundRecipient: gasRefundRecipient, RequiresResult: true,
		GasFee: contractframework.GasFeeIf(hasResultGas, resultFee),
	})
	return nil
}

// Finalize computes the complete Result and the post-settlement quantities in
// the candidate state. Repeated reads do not settle, charge, or debit twice.
func (e *Backend) Finalize() (BlockExecutionResult, error) {
	if e.finalized != nil {
		return cloneTemplateBlockResult(*e.finalized), nil
	}
	plans, err := e.Store.SettleBlockWithGasConfigAndPrecision(e.BlockHeight, e.GasConfig.Normalize(), e.AssetPrecision)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	resultPlans, err := BuildSettlementResultPlans(plans, e.records, e.AssetPrecision)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	for _, record := range e.records {
		if !record.RequiresResult || (record.Status == ResultStatusSuccess && len(record.AssetIntents) == 0) {
			continue
		}
		plan, err := contractframework.RecordIntentResultPlan(record)
		if err != nil {
			return BlockExecutionResult{}, err
		}
		resultPlans = append(resultPlans, plan)
	}
	resultPlans = AddMissingGasResultPlans(resultPlans, e.records)
	resultPlans = contractframework.AddSatoshiFeesToResultPlans(resultPlans, e.records)
	resultPlans = contractframework.AttachCallFunding(resultPlans, e.records)
	records, err := e.recordsWithSettlementAssetIntents(plans)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	records = contractframework.BindExecutionBalances(records, e.ManagedBalance)
	resultPlans, err = AugmentResultPlans(resultPlans, e.Store, e.GasConfig, e.ContractUTXOs, e.AssetPrecision)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	if err := contractframework.ApplyManagedResultBalances(resultPlans, e.ManagedBalance, e.Store.ContractClosed); err != nil {
		return BlockExecutionResult{}, err
	}
	if err := e.Store.PruneFinishedItems(); err != nil {
		return BlockExecutionResult{}, err
	}
	result := BlockExecutionResult{
		Records: records, SettlementPlans: cloneSettlementPlans(plans),
		ResultPlans: contractframework.CloneResultPlans(resultPlans), StateRoot: e.Store.StateRoot(),
	}
	e.finalized = &result
	return cloneTemplateBlockResult(result), nil
}

func cloneTemplateBlockResult(result BlockExecutionResult) BlockExecutionResult {
	result.Records = contractframework.CloneExecutionRecords(result.Records)
	result.PendingRecords = contractframework.CloneExecutionRecords(result.PendingRecords)
	result.SettlementPlans = cloneSettlementPlans(result.SettlementPlans)
	result.ResultPlans = contractframework.CloneResultPlans(result.ResultPlans)
	return result
}

func (e *Backend) Records() []ExecutionRecord {
	if e.finalized != nil {
		return contractframework.CloneExecutionRecords(e.finalized.Records)
	}
	return contractframework.BindExecutionBalances(e.records, e.ManagedBalance)
}

func (e *Backend) recordsWithSettlementAssetIntents(plans []*SettlementPlan) ([]ExecutionRecord, error) {
	records := contractframework.CloneExecutionRecords(e.records)
	intentsByItem := make(map[settlementItemKey][]AssetIntent)
	for _, plan := range plans {
		planIntents, err := BuildSettlementAssetIntentsByItem(plan, e.AssetPrecision)
		if err != nil {
			return nil, err
		}
		for itemID, intents := range planIntents {
			key := settlementItemKey{plan.Contract, itemID}
			intentsByItem[key] = append(intentsByItem[key], intents...)
		}
	}
	for i := range records {
		for _, itemID := range records[i].ItemIDs {
			if intents := intentsByItem[settlementItemKey{records[i].Contract.MustEncode(), itemID}]; len(intents) != 0 {
				records[i].AssetIntents = append(records[i].AssetIntents, contractframework.CloneAssetIntents(intents)...)
			}
		}
	}
	return records, nil
}

func (e *Backend) executeDeploy(tx *wire.MsgTx) error {
	return e.ExecuteTx(tx)
}

func (e *Backend) executeDeployTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contractcommon.Tx) error {
	if parsed.Type != TxTypeDeploy || parsed.Deploy == nil {
		return nil
	}
	deployer := contractTx.Actor
	if deployer == "" {
		return fmt.Errorf("%w: template deployer is empty", contractframework.ErrCallAdmission)
	}
	deployPayload := templateDeployPayloadFromFramework(parsed.Deploy)
	addr, _, err := DeriveContractAddress(e.ContractPrefix, deployPayload.ContractContent, deployer, deployPayload.DeployNonce)
	if err != nil {
		return err
	}
	fundingOutputs, err := FindContractOutputsForContract(tx, StandardContractScriptResolver(e.ContractPrefix), addr)
	if err != nil {
		return err
	}
	if len(fundingOutputs) != 1 {
		return fmt.Errorf("%w: template DEPLOY must use exactly one derived contract output", contractframework.ErrCallAdmission)
	}
	fundingOutput := fundingOutputs[0]
	runtime, runtimeErr := NewRuntimeWithDeployer(addr, *deployPayload, e.Registry, deployer)
	gasConfig := e.GasConfig.Normalize()
	if runtime != nil {
		gasConfig = GasConfigForRuntime(e.GasConfig, runtime)
	}
	if err := contractframework.ValidateDeployGasLimit(parsed.Deploy.GasLimit, gasConfig); err != nil {
		return fmt.Errorf("%w: %v", contractframework.ErrCallAdmission, err)
	}
	resultFee, err := e.resultFee(gasConfig)
	if err != nil {
		return err
	}
	hasResultGas, err := contractframework.OutputHasRequiredGas(fundingOutput, gasConfig.GasAssetName, resultFee)
	if err != nil {
		return err
	}
	if !hasResultGas {
		return fmt.Errorf("%w: template deployment has insufficient Result gas", contractframework.ErrCallAdmission)
	}
	reject := func() error {
		_, err := e.appendFundingFailure(contractframework.FundingFailureRequest{
			Height: e.BlockHeight, TxID: tx.TxID(), Kind: ExecutionKindDeploy,
			Contract: addr, CallID: DeriveDeployCallID(tx.TxID(), addr), Recipient: deployer,
			Funding: fundingOutputs, GasLimit: parsed.Deploy.GasLimit,
			GasAsset: gasConfig.GasAssetName, GasFee: resultFee,
		})
		return err
	}
	if runtimeErr != nil || e.Store.Exists(addr) || e.Store.ActiveNetworkExclusiveExists(runtime) {
		return reject()
	}
	if err := checkTemplateFundingAssets(runtime.Contract(), fundingOutput, gasConfig.GasAssetName); err != nil {
		return reject()
	}
	netFunding := fundingOutput
	if templateGasIsBusinessAsset(runtime.Contract(), gasConfig.GasAssetName) {
		if err := netFunding.SubAssetAmount(gasConfig.GasAssetName, resultFee); err != nil {
			return err
		}
	} else {
		netFunding = stripTemplateResultGasFunding(runtime.Contract(), fundingOutput, gasConfig.GasAssetName, resultFee)
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	if err := runtime.ApplyFunding(netFunding, gasConfig.GasAssetName); err != nil {
		return reject()
	}
	e.Store.Add(runtime)
	gasRefundRecipient := ""
	if templateUsesFrameworkGasRefund(runtime.Contract(), gasConfig.GasAssetName) {
		gasRefundRecipient = deployer
	}
	e.appendOutcome(contractframework.ExecutionOutcome{
		Height: e.BlockHeight, TxID: tx.TxID(), Type: TxTypeDeploy, Kind: ExecutionKindDeploy,
		CallID: DeriveDeployCallID(tx.TxID(), addr), Contract: addr, Status: ResultStatusSuccess,
		GasLimit: parsed.Deploy.GasLimit, FundingInputs: []OutPoint{fundingOutput.OutPoint},
		GasRefundRecipient: gasRefundRecipient, RequiresResult: true, GasFee: resultFee,
	})
	return nil
}

func (e *Backend) executeInvoke(tx *wire.MsgTx) error {
	return e.ExecuteTx(tx)
}

func (e *Backend) executeInvokeTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contractcommon.Tx) error {
	if parsed.Type != TxTypeInvoke || parsed.Invoke == nil {
		return nil
	}
	if len(parsed.ContractOutputs) != 1 {
		return fmt.Errorf("%w: template INVOKE must use exactly one contract output", contractframework.ErrCallAdmission)
	}
	contractAddr := parsed.ContractOutputs[0].Contract
	if contractAddr.ContractType() != ContractTypeTemplate {
		return nil
	}
	msgValue, err := contractframework.SumContractOutputValue(parsed.ContractOutputs)
	if err != nil {
		return err
	}
	validated := InvokeValidation{
		Contract: contractAddr, FundingOutput: parsed.ContractOutputs[0], MsgValue: msgValue, Payload: *parsed.Invoke,
	}
	runtime, ok := e.Store.Get(validated.Contract)
	if !ok {
		_, err := e.RejectFunding(contractframework.ExecutionContext{RawTx: tx, ParsedTx: parsed}, contractTx)
		return err
	}
	gasConfig := GasConfigForRuntime(e.GasConfig, runtime)
	if err := contractframework.ValidateInvokeGasLimit(parsed.Invoke.GasLimit, gasConfig); err != nil {
		return fmt.Errorf("%w: %v", contractframework.ErrCallAdmission, err)
	}
	resultFee, err := e.resultFee(gasConfig)
	if err != nil {
		return err
	}
	invoker := contractTx.Actor
	if invoker == "" && e.ResolveInvoker != nil {
		invoker, err = e.ResolveInvoker(tx, contractTx)
		if err != nil {
			return err
		}
	}
	if invoker == "" {
		return fmt.Errorf("%w: missing template invoker", contractframework.ErrCallAdmission)
	}
	callID := DeriveInvokeCallID(tx.TxID(), validated.FundingOutput.Vout, validated.Contract)
	reject := func() error {
		return e.executeInvalidInvoke(tx, runtime, validated, invoker, callID, resultFee, gasConfig)
	}
	if err := runtime.CheckInvocationLifecycle(validated.Payload.Action, invoker); err != nil {
		return reject()
	}
	if e.closing[contractAddr.MustEncode()] {
		return reject()
	}
	if err := runtime.CheckInvoke(validated.Payload.Action, validated.Payload.Param); err != nil {
		return reject()
	}
	if err := checkTemplateFundingAssets(runtime.Contract(), validated.FundingOutput, gasConfig.GasAssetName); err != nil {
		return reject()
	}
	if autopay, ok := runtime.Contract().(*AutopayContract); ok {
		if err := autopay.CheckInvokePrecision(validated.Payload.Action, validated.Payload.Param, e.AssetPrecision); err != nil {
			return reject()
		}
	}
	if !autopayGasOnlyConfig(runtime.Contract(), validated.Payload.Action, validated.Payload.Param) {
		if err := checkRuntimeAutopayDelegateCapacity(runtime, invoker); err != nil {
			return reject()
		}
	}
	hasResultGas, err := contractframework.OutputHasRequiredGas(validated.FundingOutput, gasConfig.GasAssetName, resultFee)
	if err != nil {
		return err
	}
	if !hasResultGas {
		return reject()
	}
	fundingOutput := validated.FundingOutput
	isClose := validated.Payload.Action == InvokeAPIClose
	if isClose {
		fundingOutput = contractframework.ContractOutputFromFunding(contractcommon.FundingOutput{
			OutPoint: contractframework.ContractTxOutPoint(validated.FundingOutput.OutPoint),
			Vout: validated.FundingOutput.Vout, Contract: contractAddr,
		})
	} else if templateGasIsBusinessAsset(runtime.Contract(), gasConfig.GasAssetName) {
		if err := fundingOutput.SubAssetAmount(gasConfig.GasAssetName, resultFee); err != nil {
			return err
		}
	} else if _, autopay := runtime.Contract().(*AutopayContract); autopay {
		if err := fundingOutput.SubAssetAmount(gasConfig.GasAssetName, resultFee); err != nil {
			return err
		}
	}
	if err := runtime.CheckInvokeFunding(validated.Payload.Action, validated.Payload.Param, fundingOutput); err != nil {
		return reject()
	}
	request := ApplyInvokeRequest{
		Action: validated.Payload.Action, Param: validated.Payload.Param, CallID: callID, Invoker: invoker,
		FundingOutput: fundingOutput, GasAssetName: gasConfig.GasAssetName, AssetPrecision: e.AssetPrecision,
		Height: e.BlockHeight, Timestamp: e.BlockHeight, ResultGasFee: resultFee,
	}
	remainingFunding, err := runtime.splitAutopayGasFunding(request)
	if err != nil {
		return reject()
	}
	item, err := runtime.ApplyInvoke(request)
	if err != nil {
		return err
	}
	if item == nil {
		return fmt.Errorf("%w: successful template invoke has no item", contractframework.ErrAccountingInvariant)
	}
	if !isClose && !templateResultGasIsSeparate(runtime.Contract(), gasConfig.GasAssetName) &&
		!templateGasIsBusinessAsset(runtime.Contract(), gasConfig.GasAssetName) {
		if err := runtime.ApplyGasFunding(remainingFunding, gasConfig.GasAssetName); err != nil {
			return err
		}
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	runtime.IncrementInvokeCount()
	gasRefundRecipient := ""
	if isClose || templateUsesFrameworkGasRefund(runtime.Contract(), gasConfig.GasAssetName) {
		gasRefundRecipient = invoker
	}
	outcome := contractframework.ExecutionOutcome{
		Height: e.BlockHeight, TxID: tx.TxID(), Type: TxTypeInvoke, Kind: ExecutionKindInvoke,
		CallID: callID, Contract: validated.Contract, GasLimit: validated.Payload.GasLimit,
		FundingInputs: []OutPoint{validated.FundingOutput.OutPoint}, ItemIDs: []int64{item.ID},
		GasRefundRecipient: gasRefundRecipient, RequiresResult: true, GasFee: resultFee,
	}
	if isClose {
		outcome.AssetIntents, err = contractframework.NonGasFundingRefundIntents(validated.Contract,
			[]ContractOutput{validated.FundingOutput}, gasConfig.GasAssetName, invoker)
		if err != nil {
			return err
		}
		for i := range outcome.AssetIntents {
			outcome.AssetIntents[i].CallID = callID
		}
		e.closing[contractAddr.MustEncode()] = true
	}
	e.appendOutcome(outcome)
	return nil
}

func (e *Backend) executeInvalidInvoke(tx *wire.MsgTx, runtime *ContractRuntime, validated InvokeValidation,
	invoker string, callID string, resultFee *scommon.Decimal, gasConfig GasConfig) error {

	_, err := e.appendFundingFailure(contractframework.FundingFailureRequest{
		Height: e.BlockHeight, TxID: tx.TxID(), Kind: ExecutionKindInvoke,
		Contract: validated.Contract, CallID: callID, Recipient: invoker,
		Funding: []ContractOutput{validated.FundingOutput}, GasLimit: validated.Payload.GasLimit,
		GasAsset: gasConfig.GasAssetName, GasFee: resultFee,
	})
	return err
}

func (e *Backend) appendOutcome(outcome contractframework.ExecutionOutcome) {
	e.records = append(e.records, outcome.ToRecord())
}

func (e *Backend) lastOutcomeSince(before int) (contractframework.ExecutionOutcome, error) {
	return contractframework.LastExecutionOutcomeSince(e.records, before)
}

func templateGasIsBusinessAsset(c Contract, gasAssetName string) bool {
	assetA, assetB := defaultInvokePoolAssets(c)
	return gasAssetName != "" && (gasAssetName == assetA || gasAssetName == assetB)
}

func templateResultGasIsSeparate(c Contract, gasAssetName string) bool {
	if gasAssetName == "" || gasAssetName == SatoshiAssetName {
		return false
	}
	if _, ok := c.(*AutopayContract); ok {
		return false
	}
	assetA, assetB := defaultInvokePoolAssets(c)
	return gasAssetName != assetA && gasAssetName != assetB
}

func templateUsesFrameworkGasRefund(c Contract, gasAssetName string) bool {
	if _, ok := c.(*AutopayContract); ok {
		return false
	}
	return templateResultGasIsSeparate(c, gasAssetName)
}

func stripTemplateResultGasFunding(c Contract, output ContractOutput, gasAssetName string, resultGasFee *scommon.Decimal) ContractOutput {
	if _, ok := c.(*AutopayContract); ok {
		return stripCurrentResultGasFunding(output, gasAssetName, resultGasFee)
	}
	if !templateResultGasIsSeparate(c, gasAssetName) {
		return output
	}
	next := output
	gas, err := output.AssetAmount(gasAssetName)
	if err == nil && gas != nil && gas.Sign() > 0 {
		_ = next.SubAssetAmount(gasAssetName, gas)
	}
	return next
}

func stripCurrentResultGasFunding(output ContractOutput, gasAssetName string, resultGasFee *scommon.Decimal) ContractOutput {
	if gasAssetName == "" || resultGasFee == nil || resultGasFee.Sign() <= 0 {
		return output
	}
	next := output
	_ = next.SubAssetAmount(gasAssetName, resultGasFee)
	return next
}

func templateDeployRuntime(validated DeployValidation) (*ContractRuntime, error) {
	runtime, ok := validated.Runtime.(*ContractRuntime)
	if !ok || runtime == nil {
		return nil, errors.New("template deploy runtime has unexpected type")
	}
	return runtime, nil
}
