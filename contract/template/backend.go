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

func LastInputPreviousOutputInvokerResolver(params *chaincfg.Params,
	resolve PreviousOutputScriptResolver) InvokerResolver {

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

	records []ExecutionRecord
}

func ExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	executor := NewBackend(req)
	if err := contractframework.NewExecutor(executor.executorConfig()).ExecuteTxs(req.Txs); err != nil {
		return BlockExecutionResult{}, err
	}
	return executor.Finalize()
}

func BuildBlockResultTxs(req BlockResultBuildRequest) (BlockResultBuildResult, error) {
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	store := req.Store
	if store == nil {
		store = NewRuntimeStore()
	}
	contractUTXOs := contractframework.ContractUTXOProviderWithTxOutputs(req.ContractUTXOs, req.Txs, prefix, ContractTypeTemplate)
	executor := NewBackend(BlockExecutionRequest{
		Store:          store,
		Registry:       req.Registry,
		ContractPrefix: prefix,
		GasConfig:      req.GasConfig,
		ContractUTXOs:  contractUTXOs,
		AssetPrecision: req.AssetPrecision,
		BlockHeight:    req.BlockHeight,
		ResolveInvoker: req.ResolveInvoker,
	})
	result, err := contractframework.BuildSingleResultTxBlock(contractframework.SingleResultBlockRequest[BlockExecutionResult]{
		ModuleName: "template",
		Txs:        req.Txs,
		Prefix:     prefix,
		Classify:   ClassifyTxForBlockOrder,
		IsModule:   func(info TxOrderInfo) bool { return info.IsTemplate },
		IsResult:   func(info TxOrderInfo) bool { return info.Type == TxTypeResult },
		ExecuteTx:  executor.ExecuteTx,
		Finalize: func() (BlockExecutionResult, error) {
			return executor.Finalize()
		},
		Plans: func(exec BlockExecutionResult) []ResultPlan {
			return exec.ResultPlans
		},
		SetPlans: func(exec BlockExecutionResult, plans []ResultPlan) BlockExecutionResult {
			result := exec
			result.ResultPlans = plans
			return result
		},
		Policy: contractframework.SingleResultTxPolicy{
			Label:         "template",
			Status:        ResultStatusSuccess,
			GasAssetName:  req.GasConfig.Normalize().GasAssetName,
			PlanCount:     resultPlanCount,
			ResolveScript: req.ResolveScript,
			ResolveOutput: req.ResolveOutput,
			Augment: func(plans []ResultPlan) ([]ResultPlan, error) {
				augmentStore := store
				if augmentStore != nil {
					augmentStore = augmentStore.Clone()
				}
				return AugmentResultPlans(plans, augmentStore, req.GasConfig, contractUTXOs, req.AssetPrecision)
			},
		},
	})
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	execution, ok := result.Execution.(BlockExecutionResult)
	if !ok {
		return BlockResultBuildResult{}, errors.New("template result build execution has unexpected type")
	}
	return BlockResultBuildResult{
		ResultTxs: result.ResultTxs,
		Execution: execution,
	}, nil
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
	return &Backend{
		Store:          store,
		Registry:       req.Registry,
		ContractPrefix: prefix,
		GasConfig:      req.GasConfig,
		ContractUTXOs:  req.ContractUTXOs,
		AssetPrecision: req.AssetPrecision,
		BlockHeight:    req.BlockHeight,
		ResolveInvoker: req.ResolveInvoker,
	}
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
		ParseSpec: templateParseSpec(),
		Resolver:  StandardContractScriptResolver,
		Context: contractframework.ExecutionContext{
			Prefix:    e.ContractPrefix,
			Height:    e.BlockHeight,
			Time:      e.BlockHeight,
			GasConfig: e.GasConfig,
		},
		ResolveActor: func(tx *wire.MsgTx, contractTx contractcommon.Tx) (string, error) {
			if e.ResolveInvoker == nil {
				return "", nil
			}
			return e.ResolveInvoker(tx, contractTx)
		},
	}
}

func (e *Backend) ContractType() byte {
	return ContractTypeTemplate
}

func (e *Backend) Name() string {
	return "template"
}

func (e *Backend) Priority() int {
	return 1
}

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

func (e *Backend) StateRoot() [32]byte {
	return e.Store.StateRoot()
}

func (e *Backend) Snapshot() any {
	return e.Store
}

func (e *Backend) executeDefaultInvokeOutput(tx *wire.MsgTx, output ContractOutput) error {
	contractTx := contractcommon.Tx{
		TxID:         tx.TxID(),
		Kind:         TxTypeInvoke,
		ContractType: ContractTypeTemplate,
		Contract:     output.Contract,
		Action:       contractcommon.ContractInvokeAPIDefault,
		GasLimit:     e.GasConfig.Normalize().InvokeBaseGas,
		Funding:      contractframework.ContractFundingOutputs([]ContractOutput{output}),
	}
	return e.executeDefaultInvokeOutputTx(tx, contractTx, output)
}

func (e *Backend) executeDefaultInvokeOutputTx(tx *wire.MsgTx,
	contractTx contractcommon.Tx, output ContractOutput) error {

	if output.Contract.ContractType() != ContractTypeTemplate {
		return nil
	}
	runtime, ok := e.Store.Get(output.Contract)
	if !ok {
		return errors.New("default invoke target contract does not exist")
	}
	gasConfig := GasConfigForRuntime(e.GasConfig, runtime)
	invoker := ""
	if contractTx.Actor != "" {
		invoker = contractTx.Actor
	} else if e.ResolveInvoker != nil {
		var err error
		invoker, err = e.ResolveInvoker(tx, contractTx)
		if err != nil {
			return err
		}
	}
	resultFee, err := gasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	callID := DeriveInvokeCallID(tx.TxID(), output.Vout, output.Contract)
	if err := checkRuntimeAutopayDelegateCapacity(runtime, invoker); err != nil {
		return e.executeInvalidInvoke(tx, runtime, InvokeValidation{
			Contract:      output.Contract,
			FundingOutput: output,
			Payload: contractframework.InvokePayload{
				GasLimit: gasConfig.InvokeBaseGas,
				Action:   contractcommon.ContractInvokeAPIDefault,
			},
		}, invoker, callID, resultFee, gasConfig)
	}
	hasResultGas, err := contractframework.OutputHasRequiredGas(output, gasConfig.GasAssetName, resultFee)
	if err != nil {
		return err
	}
	fundingOutput := output
	if _, ok := runtime.Contract().(*AutopayContract); ok {
		fundingOutput = stripCurrentResultGasFunding(output, gasConfig.GasAssetName,
			contractframework.GasFeeIf(hasResultGas, resultFee))
	}
	item, err := runtime.ApplyDefaultInvoke(ApplyInvokeRequest{
		Action:                contractcommon.ContractInvokeAPIDefault,
		CallID:                callID,
		Invoker:               invoker,
		FundingOutput:         fundingOutput,
		Height:                e.BlockHeight,
		Timestamp:             e.BlockHeight,
		ResultGasFee:          contractframework.GasFeeIf(hasResultGas, resultFee),
		ApplyDefaultRetention: !hasResultGas,
	})
	if err != nil {
		return err
	}
	if item == nil {
		return nil
	}
	if !templateResultGasIsSeparate(runtime.Contract(), gasConfig.GasAssetName) {
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
	outcome := contractframework.ExecutionOutcome{
		Height:             e.BlockHeight,
		TxID:               tx.TxID(),
		Type:               TxTypeInvoke,
		Kind:               ExecutionKindInvoke,
		CallID:             callID,
		Contract:           output.Contract,
		GasLimit:           gasConfig.InvokeBaseGas,
		FundingInputs:      []OutPoint{output.OutPoint},
		ItemIDs:            []int64{item.ID},
		GasRefundRecipient: gasRefundRecipient,
		RequiresResult:     true,
	}
	outcome.GasFee = contractframework.GasFeeIf(hasResultGas, resultFee)
	e.appendOutcome(outcome)
	return nil
}

func (e *Backend) Finalize() (BlockExecutionResult, error) {
	if err := e.reconcileAssetCachesBeforeSettlement(); err != nil {
		return BlockExecutionResult{}, err
	}
	plans, err := e.Store.SettleBlockWithGasConfigAndPrecision(e.BlockHeight, e.GasConfig.Normalize(), e.AssetPrecision)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	resultPlans, err := BuildSettlementResultPlans(plans, e.records, e.AssetPrecision)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	resultPlans = AddMissingGasResultPlans(resultPlans, e.records)
	records, err := e.recordsWithSettlementAssetIntents(plans)
	if err != nil {
		return BlockExecutionResult{}, err
	}
	if err := e.Store.PruneFinishedItems(); err != nil {
		return BlockExecutionResult{}, err
	}
	return BlockExecutionResult{
		Records:         records,
		SettlementPlans: cloneSettlementPlans(plans),
		ResultPlans:     contractframework.CloneResultPlans(resultPlans),
		StateRoot:       e.Store.StateRoot(),
	}, nil
}

func (e *Backend) reconcileAssetCachesBeforeSettlement() error {
	if e.Store == nil || e.ContractUTXOs == nil {
		return nil
	}
	currentFunding := currentBlockFundingOutpoints(e.records)
	if len(currentFunding) == 0 {
		return e.Store.ReconcileAssetCaches(e.ContractUTXOs, e.GasConfig)
	}
	provider := func(contract ContractAddress) ([]UTXO, error) {
		utxos, err := e.ContractUTXOs(contract)
		if err != nil {
			return nil, err
		}
		out := make([]UTXO, 0, len(utxos))
		for _, utxo := range utxos {
			if _, ok := currentFunding[utxo.OutPoint]; ok {
				continue
			}
			out = append(out, utxo)
		}
		return out, nil
	}
	return e.Store.reconcileAssetCaches(provider, e.GasConfig, true)
}

func currentBlockFundingOutpoints(records []ExecutionRecord) map[OutPoint]struct{} {
	out := make(map[OutPoint]struct{})
	for _, record := range records {
		for _, funding := range record.FundingInputs {
			out[funding] = struct{}{}
		}
	}
	return out
}

func (e *Backend) Records() []ExecutionRecord {
	return contractframework.CloneExecutionRecords(e.records)
}

func (e *Backend) recordsWithSettlementAssetIntents(plans []*SettlementPlan) ([]ExecutionRecord, error) {
	records := contractframework.CloneExecutionRecords(e.records)
	intentsByItem := make(map[int64][]AssetIntent)
	for _, plan := range plans {
		planIntents, err := BuildSettlementAssetIntentsByItem(plan, e.AssetPrecision)
		if err != nil {
			return nil, err
		}
		for itemID, intents := range planIntents {
			intentsByItem[itemID] = append(intentsByItem[itemID], intents...)
		}
	}
	for i := range records {
		for _, itemID := range records[i].ItemIDs {
			if intents := intentsByItem[itemID]; len(intents) != 0 {
				records[i].AssetIntents = append(records[i].AssetIntents,
					contractframework.CloneAssetIntents(intents)...)
			}
		}
	}
	return records, nil
}

func (e *Backend) executeDeploy(tx *wire.MsgTx) error {
	parsed, err := ParseTx(tx, StandardContractScriptResolver(e.ContractPrefix))
	if err != nil {
		return err
	}
	return e.executeDeployTx(tx, parsed, contractframework.ContractTxFromParsed(tx, parsed, ContractTypeTemplate))
}

func (e *Backend) executeDeployTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contractcommon.Tx) error {
	if parsed.Type != TxTypeDeploy || parsed.Deploy == nil {
		return nil
	}
	deployer := contractTx.Actor
	if deployer == "" {
		return errors.New("template deployer is empty")
	}
	deployPayload := templateDeployPayloadFromFramework(parsed.Deploy)
	deployPayload.Type = ContractTypeTemplate
	addr, _, err := DeriveContractAddress(
		e.ContractPrefix,
		deployPayload.ContractContent,
		deployer,
		deployPayload.DeployNonce,
	)
	if err != nil {
		return err
	}
	fundingOutputs, err := FindContractOutputsForContract(tx, StandardContractScriptResolver(e.ContractPrefix), addr)
	if err != nil {
		return err
	}
	if len(fundingOutputs) == 0 {
		return fmt.Errorf("template DEPLOY output does not match derived contract %s", addr.MustEncode())
	}
	if len(fundingOutputs) > 1 {
		return fmt.Errorf("template DEPLOY must use at most one contract output")
	}
	fundingOutput := fundingOutputs[0]
	runtime, err := NewRuntimeWithDeployer(addr, *deployPayload, e.Registry, deployer)
	if err != nil {
		return err
	}
	gasConfig := GasConfigForRuntime(e.GasConfig, runtime)
	if err := contractframework.ValidateDeployGasLimit(parsed.Deploy.GasLimit, gasConfig); err != nil {
		return nil
	}
	resultFee, err := gasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	hasResultGas, err := contractframework.OutputHasRequiredGas(fundingOutput, gasConfig.GasAssetName, resultFee)
	if err != nil {
		return err
	}
	if e.Store.Exists(addr) {
		gasRefundRecipient := ""
		if hasResultGas {
			gasRefundRecipient = deployer
		}
		e.appendOutcome(contractframework.ExecutionOutcome{
			Height:             e.BlockHeight,
			TxID:               tx.TxID(),
			Type:               TxTypeDeploy,
			Kind:               ExecutionKindDeploy,
			CallID:             DeriveDeployCallID(tx.TxID(), addr),
			Contract:           addr,
			Status:             ResultStatusInvalid,
			GasLimit:           parsed.Deploy.GasLimit,
			FundingInputs:      []OutPoint{fundingOutput.OutPoint},
			GasFee:             contractframework.GasFeeIf(hasResultGas, resultFee),
			GasRefundRecipient: gasRefundRecipient,
			RequiresResult:     true,
		})
		return nil
	}
	if e.Store.ActiveNetworkExclusiveExists(runtime) {
		gasRefundRecipient := ""
		if hasResultGas {
			gasRefundRecipient = deployer
		}
		e.appendOutcome(contractframework.ExecutionOutcome{
			Height:             e.BlockHeight,
			TxID:               tx.TxID(),
			Type:               TxTypeDeploy,
			Kind:               ExecutionKindDeploy,
			CallID:             DeriveDeployCallID(tx.TxID(), addr),
			Contract:           addr,
			Status:             ResultStatusInvalid,
			GasLimit:           parsed.Deploy.GasLimit,
			FundingInputs:      []OutPoint{fundingOutput.OutPoint},
			GasFee:             contractframework.GasFeeIf(hasResultGas, resultFee),
			GasRefundRecipient: gasRefundRecipient,
			RequiresResult:     true,
		})
		return nil
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	if err := runtime.ApplyFunding(stripTemplateResultGasFunding(runtime.Contract(), fundingOutput, gasConfig.GasAssetName,
		contractframework.GasFeeIf(hasResultGas, resultFee)), gasConfig.GasAssetName); err != nil {
		return nil
	}
	e.Store.Add(runtime)
	gasRefundRecipient := ""
	if hasResultGas && templateUsesFrameworkGasRefund(runtime.Contract(), gasConfig.GasAssetName) {
		gasRefundRecipient = deployer
	}
	outcome := contractframework.ExecutionOutcome{
		Height:             e.BlockHeight,
		TxID:               tx.TxID(),
		Type:               TxTypeDeploy,
		Kind:               ExecutionKindDeploy,
		CallID:             DeriveDeployCallID(tx.TxID(), addr),
		Contract:           addr,
		Status:             ResultStatusSuccess,
		GasLimit:           parsed.Deploy.GasLimit,
		FundingInputs:      []OutPoint{fundingOutput.OutPoint},
		GasRefundRecipient: gasRefundRecipient,
		RequiresResult:     true,
	}
	outcome.GasFee = contractframework.GasFeeIf(hasResultGas, resultFee)
	e.appendOutcome(outcome)
	return nil
}

func (e *Backend) executeInvoke(tx *wire.MsgTx) error {
	parsed, err := ParseTx(tx, StandardContractScriptResolver(e.ContractPrefix))
	if err != nil {
		return err
	}
	return e.executeInvokeTx(tx, parsed, contractframework.ContractTxFromParsed(tx, parsed, ContractTypeTemplate))
}

func (e *Backend) executeInvokeTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contractcommon.Tx) error {
	if parsed.Type != TxTypeInvoke || parsed.Invoke == nil {
		return nil
	}
	if parsed.Invoke.GasLimit == 0 {
		return nil
	}
	if len(parsed.ContractOutputs) != 1 {
		return nil
	}
	contractAddr := parsed.ContractOutputs[0].Contract
	if contractAddr.ContractType() != ContractTypeTemplate {
		return nil
	}
	msgValue, err := contractframework.SumContractOutputValue(parsed.ContractOutputs)
	if err != nil {
		return nil
	}
	validated := InvokeValidation{
		Contract:      contractAddr,
		FundingOutput: parsed.ContractOutputs[0],
		MsgValue:      msgValue,
		Payload:       *parsed.Invoke,
	}
	runtime, ok := e.Store.Get(validated.Contract)
	if !ok {
		return nil
	}
	gasConfig := GasConfigForRuntime(e.GasConfig, runtime)
	if err := contractframework.ValidateInvokeGasLimit(parsed.Invoke.GasLimit, gasConfig); err != nil {
		return nil
	}
	resultFee, err := gasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	invoker := ""
	if contractTx.Actor != "" {
		invoker = contractTx.Actor
	} else if e.ResolveInvoker != nil {
		invoker, err = e.ResolveInvoker(tx, contractTx)
		if err != nil {
			return nil
		}
	}
	callID := DeriveInvokeCallID(tx.TxID(), validated.FundingOutput.Vout, validated.Contract)
	if err := runtime.CheckInvoke(validated.Payload.Action, validated.Payload.Param); err != nil {
		return e.executeInvalidInvoke(tx, runtime, validated, invoker, callID, resultFee, gasConfig)
	}
	if autopay, ok := runtime.Contract().(*AutopayContract); ok {
		if err := autopay.CheckInvokePrecision(validated.Payload.Action,
			validated.Payload.Param, e.AssetPrecision); err != nil {
			return e.executeInvalidInvoke(tx, runtime, validated, invoker, callID, resultFee, gasConfig)
		}
	}
	if err := checkRuntimeAutopayDelegateCapacity(runtime, invoker); err != nil {
		return e.executeInvalidInvoke(tx, runtime, validated, invoker, callID, resultFee, gasConfig)
	}
	if err := runtime.CheckInvokeFunding(validated.Payload.Action, validated.Payload.Param, validated.FundingOutput); err != nil {
		return e.executeInvalidInvoke(tx, runtime, validated, invoker, callID, resultFee, gasConfig)
	}
	fundingOutput := validated.FundingOutput
	if _, ok := runtime.Contract().(*AutopayContract); ok {
		fundingOutput = stripCurrentResultGasFunding(validated.FundingOutput, gasConfig.GasAssetName, resultFee)
	}
	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        validated.Payload.Action,
		Param:         validated.Payload.Param,
		CallID:        callID,
		Invoker:       invoker,
		FundingOutput: fundingOutput,
		Height:        e.BlockHeight,
		Timestamp:     e.BlockHeight,
		ResultGasFee:  resultFee,
	})
	if err != nil {
		return err
	}
	if !templateResultGasIsSeparate(runtime.Contract(), gasConfig.GasAssetName) {
		if err := runtime.ApplyGasFunding(fundingOutput, gasConfig.GasAssetName); err != nil {
			return err
		}
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	runtime.IncrementInvokeCount()

	funding := []OutPoint{validated.FundingOutput.OutPoint}
	gasRefundRecipient := ""
	if templateUsesFrameworkGasRefund(runtime.Contract(), gasConfig.GasAssetName) {
		gasRefundRecipient = invoker
	}
	outcome := contractframework.ExecutionOutcome{
		Height:             e.BlockHeight,
		TxID:               tx.TxID(),
		Type:               TxTypeInvoke,
		Kind:               ExecutionKindInvoke,
		CallID:             callID,
		Contract:           validated.Contract,
		GasLimit:           validated.Payload.GasLimit,
		FundingInputs:      funding,
		ItemIDs:            []int64{item.ID},
		GasRefundRecipient: gasRefundRecipient,
		RequiresResult:     true,
	}
	outcome.GasFee = resultFee
	e.appendOutcome(outcome)
	return nil
}

func (e *Backend) executeInvalidInvoke(tx *wire.MsgTx, runtime *ContractRuntime, validated InvokeValidation,
	invoker string, callID string, resultFee *scommon.Decimal, gasConfig GasConfig) error {
	var invalidResultFee *scommon.Decimal
	hasResultGas, err := contractframework.OutputHasRequiredGas(validated.FundingOutput, gasConfig.GasAssetName, resultFee)
	if err != nil {
		return err
	}
	if hasResultGas {
		invalidResultFee = resultFee.Clone()
	}
	item, err := runtime.ApplyInvalidInvoke(ApplyInvokeRequest{
		Action:        validated.Payload.Action,
		Param:         validated.Payload.Param,
		CallID:        callID,
		Invoker:       invoker,
		FundingOutput: validated.FundingOutput,
		Height:        e.BlockHeight,
		Timestamp:     e.BlockHeight,
		ResultGasFee:  invalidResultFee,
	}, gasConfig.GasAssetName)
	if err != nil {
		return err
	}
	runtime.SetCurrentBlock(e.BlockHeight)
	runtime.IncrementInvokeCount()
	gasRefundRecipient := ""
	if templateUsesFrameworkGasRefund(runtime.Contract(), gasConfig.GasAssetName) {
		gasRefundRecipient = invoker
	}
	outcome := contractframework.ExecutionOutcome{
		Height:             e.BlockHeight,
		TxID:               tx.TxID(),
		Type:               TxTypeInvoke,
		Kind:               ExecutionKindInvoke,
		CallID:             callID,
		Contract:           validated.Contract,
		Status:             ResultStatusInvalid,
		GasLimit:           validated.Payload.GasLimit,
		FundingInputs:      []OutPoint{validated.FundingOutput.OutPoint},
		ItemIDs:            []int64{item.ID},
		GasRefundRecipient: gasRefundRecipient,
		RequiresResult:     true,
	}
	outcome.GasFee = invalidResultFee
	e.appendOutcome(outcome)
	return nil
}

func (e *Backend) appendOutcome(outcome contractframework.ExecutionOutcome) {
	e.records = append(e.records, outcome.ToRecord())
}

func (e *Backend) lastOutcomeSince(before int) (contractframework.ExecutionOutcome, error) {
	return contractframework.LastExecutionOutcomeSince(e.records, before)
}

func templateResultGasIsSeparate(contract Contract, gasAssetName string) bool {
	if gasAssetName == "" || gasAssetName == SatoshiAssetName {
		return false
	}
	if _, ok := contract.(*AutopayContract); ok {
		return false
	}
	if _, ok := contract.(*ExchangeContract); ok {
		return false
	}
	assetA, assetB := defaultInvokePoolAssets(contract)
	return gasAssetName != assetA && gasAssetName != assetB
}

func templateUsesFrameworkGasRefund(contract Contract, gasAssetName string) bool {
	if _, ok := contract.(*AutopayContract); ok {
		return false
	}
	return templateResultGasIsSeparate(contract, gasAssetName)
}

func stripTemplateResultGasFunding(contract Contract, output ContractOutput, gasAssetName string, resultGasFee *scommon.Decimal) ContractOutput {
	if _, ok := contract.(*AutopayContract); ok {
		return stripCurrentResultGasFunding(output, gasAssetName, resultGasFee)
	}
	if !templateResultGasIsSeparate(contract, gasAssetName) {
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
