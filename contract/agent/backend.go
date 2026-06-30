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

func LastInputPreviousOutputInvokerResolver(params *chaincfg.Params,
	resolve PreviousOutputScriptResolver) InvokerResolver {

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
}

const agentClosePlanReason = "__agent_close__"

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
	contractUTXOs := contractframework.ContractUTXOProviderWithTxOutputs(req.ContractUTXOs, req.Txs, prefix, ContractTypeAgent)
	executor := NewBackend(BlockExecutionRequest{
		Store:          store,
		ContractPrefix: prefix,
		RuntimeConfig:  req.RuntimeConfig,
		GasConfig:      req.GasConfig,
		ContractUTXOs:  contractUTXOs,
		AssetPrecision: req.AssetPrecision,
		BlockHeight:    req.BlockHeight,
		BlockTime:      req.BlockTime,
		ResolveInvoker: req.ResolveInvoker,
	})
	result, err := contractframework.BuildSingleResultTxBlock(contractframework.SingleResultBlockRequest[BlockExecutionResult]{
		ModuleName: "agent",
		Txs:        req.Txs,
		Prefix:     prefix,
		Classify:   ClassifyTxForBlockOrder,
		IsModule:   func(info TxOrderInfo) bool { return info.IsAgent },
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
			Label:         "agent",
			Status:        ResultStatusSuccess,
			GasAssetName:  req.GasConfig.Normalize().GasAssetName,
			ResolveScript: req.ResolveScript,
			ResolveOutput: req.ResolveOutput,
			Augment: func(plans []ResultPlan) ([]ResultPlan, error) {
				return AugmentResultPlans(plans, contractUTXOs, executor.Store, req.AssetPrecision,
					req.GasConfig.Normalize().GasAssetName, req.RuntimeConfig.BootstrapAddress)
			},
		},
	})
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	execution, ok := result.Execution.(BlockExecutionResult)
	if !ok {
		return BlockResultBuildResult{}, errors.New("agent result build execution has unexpected type")
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
	req.RuntimeConfig.AssetPrecision = req.AssetPrecision
	store.ApplyConfig(req.RuntimeConfig)
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	return &Backend{
		Store:          store,
		ContractPrefix: prefix,
		RuntimeConfig:  req.RuntimeConfig,
		GasConfig:      req.GasConfig,
		ContractUTXOs:  req.ContractUTXOs,
		AssetPrecision: req.AssetPrecision,
		BlockHeight:    req.BlockHeight,
		BlockTime:      req.BlockTime,
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
		ParseSpec: agentParseSpec(),
		Resolver:  StandardContractScriptResolver,
		Context: contractframework.ExecutionContext{
			Prefix:    e.ContractPrefix,
			Height:    e.BlockHeight,
			Time:      e.BlockTime,
			GasConfig: e.GasConfig,
		},
		ResolveActor: func(tx *wire.MsgTx, contractTx contract.Tx) (string, error) {
			if e.ResolveInvoker == nil {
				return "", nil
			}
			return e.ResolveInvoker(tx, contractTx)
		},
	}
}

func (e *Backend) ContractType() byte {
	return ContractTypeAgent
}

func (e *Backend) Name() string {
	return "agent"
}

func (e *Backend) Priority() int {
	return 3
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
	output := contractframework.ContractOutputFromFunding(funding)
	if err := e.executeDefaultInvokeOutput(output); err != nil {
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

func (e *Backend) executeDefaultInvokeOutput(output ContractOutput) error {
	if !e.Store.Exists(output.Contract) {
		return errors.New("default invoke target contract does not exist")
	}
	fee, err := e.GasConfig.InvokeFee(e.BlockHeight)
	if err != nil {
		return err
	}
	if fee == nil || fee.Sign() == 0 || e.GasConfig.GasAssetName == "" {
		return nil
	}
	hasRequiredGas, err := contractframework.OutputHasRequiredGas(output, e.GasConfig.GasAssetName, fee)
	if err != nil {
		return err
	}
	if !hasRequiredGas {
		return fmt.Errorf("default agent invoke output %s gas below required %s", output.OutPoint, fee.String())
	}
	return nil
}

func (e *Backend) Finalize() (BlockExecutionResult, error) {
	e.Store.AdvancePredictionStatuses(e.BlockHeight, e.BlockTime)
	resultPlans := contractframework.AddGasFeesToResultPlans(e.resultPlans, e.records)
	return BlockExecutionResult{
		Records:         contractframework.CloneExecutionRecords(e.records),
		SettlementPlans: cloneSettlementPlans(e.settlementPlans),
		ResultPlans:     contractframework.CloneResultPlans(resultPlans),
		StateRoot:       e.Store.StateRoot(),
	}, nil
}

func (e *Backend) executeDeploy(tx *wire.MsgTx) error {
	parsed, err := ParseTx(tx, StandardContractScriptResolver(e.ContractPrefix))
	if err != nil {
		return err
	}
	contractTx := contractframework.ContractTxFromParsed(tx, parsed, ContractTypeAgent)
	if e.ResolveInvoker != nil {
		actor, err := e.ResolveInvoker(tx, contractTx)
		if err != nil {
			return err
		}
		contractTx.Actor = actor
	}
	return e.executeDeployTx(tx, parsed, contractTx)
}

func (e *Backend) executeDeployTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contract.Tx) error {
	validated, err := contractframework.ValidateParsedDeployBasic(parsed, "agent", e.GasConfig)
	if err != nil {
		return nil
	}
	deployer := contractTx.Actor
	if deployer == "" {
		return errors.New("agent deployer is empty")
	}
	deployPayload := agentDeployPayloadFromFramework(&validated.Payload)
	deployPayload.Type = ContractTypeAgent
	addr, _, err := DeriveContractAddress(
		e.ContractPrefix,
		deployPayload.SubType,
		deployPayload.ContractContent,
		deployer,
		deployPayload.DeployNonce,
	)
	if err != nil {
		return err
	}
	runtime, err := NewRuntimeWithDeployer(addr, *deployPayload, e.RuntimeConfig, deployer)
	if err != nil {
		return err
	}
	fundingOutputs, err := FindContractOutputsForContract(tx, StandardContractScriptResolver(e.ContractPrefix), addr)
	if err != nil {
		return err
	}
	if len(fundingOutputs) == 0 {
		return fmt.Errorf("agent DEPLOY output does not match derived contract %s", addr.MustEncode())
	}
	if len(fundingOutputs) > 1 {
		return fmt.Errorf("agent DEPLOY must use at most one contract output")
	}
	fundingOutput := fundingOutputs[0]
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	e.Store.Add(runtime)
	outcome := contractframework.ExecutionOutcome{
		Height:         e.BlockHeight,
		TxID:           tx.TxID(),
		Type:           TxTypeDeploy,
		Kind:           ExecutionKindDeploy,
		CallID:         DeriveDeployCallID(tx.TxID(), addr),
		Contract:       addr,
		Status:         ResultStatusSuccess,
		GasLimit:       validated.Payload.GasLimit,
		FundingInputs:  []OutPoint{fundingOutput.OutPoint},
		RequiresResult: true,
	}
	e.appendOutcome(outcome)
	if resultPlan, ok := stateResultPlan(addr, fundingOutput); ok {
		outcome.GasFee = resultFee
		e.records[len(e.records)-1] = outcome.ToRecord()
		e.resultPlans = append(e.resultPlans, resultPlan)
	}
	return nil
}

func (e *Backend) executeInvoke(tx *wire.MsgTx, parsed ParsedTx) error {
	return e.executeInvokeTx(tx, parsed, contractframework.ContractTxFromParsed(tx, parsed, ContractTypeAgent))
}

func (e *Backend) executeInvokeTx(tx *wire.MsgTx, parsed ParsedTx, contractTx contract.Tx) error {
	validated, err := ValidateParsedInvokeTxBasic(parsed, e.Store.Exists, e.GasConfig)
	if err != nil {
		return nil
	}
	runtime, ok := e.Store.Get(validated.Contract)
	if !ok {
		return nil
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

	var settlement *PredictionSettlementPlan
	var settlementIntents []AssetIntent
	switch validated.Payload.Action {
	case InvokeAPIReady:
		err = runtime.ApplyReady(ApplyReadyRequest{Invoker: invoker})
	case InvokeAPIReject:
		err = e.applyReject(runtime, validated, invoker)
	case InvokeAPIBet:
		settlement, err = e.applyBet(runtime, validated, invoker)
	case InvokeAPIConfirm:
		settlement, err = e.applyConfirm(runtime, validated, invoker)
	case InvokeAPIClose:
		settlement, err = e.applyClose(runtime, validated, invoker)
	default:
		err = fmt.Errorf("unsupported agent action %s", validated.Payload.Action)
	}
	if err != nil {
		return nil
	}
	if settlement != nil {
		e.settlementPlans = append(e.settlementPlans, settlement)
		resultPlan, err := contractframework.BuildSettlementResultPlan(settlement, e.settlementResultOptions())
		if err != nil {
			return err
		}
		if validated.Payload.Action == InvokeAPIClose {
			resultPlan.Outputs = append(resultPlan.Outputs, ResultOutput{
				To:     validated.Contract.EncodeAddress(),
				Reason: agentClosePlanReason,
			})
		}
		settlementIntents, err = contractframework.BuildSettlementAssetIntents(settlement, e.settlementResultOptions())
		if err != nil {
			return err
		}
		e.resultPlans = append(e.resultPlans, resultPlan)
	}

	requiresResult := settlement != nil
	var readyResultPlan ResultPlan
	if validated.Payload.Action == InvokeAPIReady || validated.Payload.Action == InvokeAPIReject {
		var ok bool
		readyResultPlan, ok = stateResultPlan(validated.Contract, validated.FundingOutput)
		requiresResult = ok
	}
	outcome := contractframework.ExecutionOutcome{
		Height:         e.BlockHeight,
		TxID:           tx.TxID(),
		Type:           TxTypeInvoke,
		Kind:           ExecutionKindInvoke,
		CallID:         DeriveInvokeCallID(tx.TxID(), validated.FundingOutput.Vout, validated.Contract),
		Contract:       validated.Contract,
		GasLimit:       validated.Payload.GasLimit,
		FundingInputs:  []OutPoint{validated.FundingOutput.OutPoint},
		AssetIntents:   settlementIntents,
		RequiresResult: requiresResult,
	}
	if validated.Payload.Action == InvokeAPIClose && err == nil {
		outcome.CloseContract = true
		outcome.DeployerAddress = runtime.deployer
		outcome.BootstrapAddress = e.RuntimeConfig.BootstrapAddress
	}
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	if outcome.RequiresResult {
		outcome.GasFee = resultFee
	}
	e.appendOutcome(outcome)
	if (validated.Payload.Action == InvokeAPIReady || validated.Payload.Action == InvokeAPIReject) && requiresResult {
		e.resultPlans = append(e.resultPlans, readyResultPlan)
	}
	return nil
}

func (e *Backend) appendOutcome(outcome contractframework.ExecutionOutcome) {
	e.records = append(e.records, outcome.ToRecord())
}

func (e *Backend) lastOutcomeSince(before int) (contractframework.ExecutionOutcome, error) {
	return contractframework.LastExecutionOutcomeSince(e.records, before)
}

func (e *Backend) applyReject(runtime *Runtime, validated InvokeValidation, invoker string) error {
	param, err := DecodePredictionRejectParam(validated.Payload.Param)
	if err != nil {
		return err
	}
	return runtime.ApplyReject(ApplyRejectRequest{
		Invoker: invoker,
		Param:   param,
	})
}

func (e *Backend) applyBet(runtime *Runtime, validated InvokeValidation, invoker string) (*PredictionSettlementPlan, error) {
	param, err := DecodePredictionBetParam(validated.Payload.Param)
	if err != nil {
		return nil, err
	}
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return nil, err
	}
	amount, gasAmount, err := betAndGasFundingAmount(
		validated.FundingOutput,
		runtime.Contract().BetAsset,
		e.GasConfig.Normalize().GasAssetName,
		resultFee,
	)
	if err != nil {
		return nil, err
	}
	return nil, runtime.ApplyBet(ApplyBetRequest{
		Invoker:   invoker,
		Param:     param,
		AssetName: runtime.Contract().BetAsset,
		Amount:    amount,
		GasAmount: gasAmount,
		TimeValue: e.predictionTimeValue(runtime.Contract()),
	})
}

func (e *Backend) applyConfirm(runtime *Runtime, validated InvokeValidation, invoker string) (*PredictionSettlementPlan, error) {
	param, err := DecodePredictionConfirmParam(validated.Payload.Param)
	if err != nil {
		return nil, err
	}
	probe := runtime.Clone()
	settlement, err := probe.ApplyConfirm(ApplyConfirmRequest{
		Invoker:   invoker,
		Param:     param,
		TimeValue: e.predictionTimeValue(runtime.Contract()),
	})
	if err != nil {
		return nil, err
	}
	if err := e.checkSettlementFunding(settlement); err != nil {
		return nil, err
	}
	if _, err := contractframework.BuildSettlementAssetIntents(settlement, e.settlementResultOptions()); err != nil {
		return nil, err
	}
	return runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker:   invoker,
		Param:     param,
		TimeValue: e.predictionTimeValue(runtime.Contract()),
	})
}

func (e *Backend) applyClose(runtime *Runtime, validated InvokeValidation, invoker string) (*PredictionSettlementPlan, error) {
	if len(validated.Payload.Param) != 0 {
		return nil, fmt.Errorf("agent close takes no parameters")
	}
	plan, err := runtime.ApplyClose(ApplyCloseRequest{Invoker: invoker})
	if err != nil {
		return nil, err
	}
	plan.Inputs = []OutPoint{validated.FundingOutput.OutPoint}
	return plan, nil
}

func (e *Backend) checkSettlementFunding(settlement *PredictionSettlementPlan) error {
	if settlement == nil || e.ContractUTXOs == nil {
		return nil
	}
	plan, err := contractframework.BuildSettlementResultPlan(settlement, e.settlementResultOptions())
	if err != nil {
		return err
	}
	resultFee, err := e.GasConfig.ResultFee(e.BlockHeight)
	if err != nil {
		return err
	}
	plan.GasFee = resultFee
	_, err = AugmentResultPlans([]ResultPlan{plan}, e.ContractUTXOs, e.Store, e.AssetPrecision,
		e.GasConfig.Normalize().GasAssetName, e.RuntimeConfig.BootstrapAddress)
	return err
}

func (e *Backend) settlementResultOptions() contractframework.SettlementResultOptions {
	return agentSettlementResultOptions(e.AssetPrecision)
}

func (e *Backend) predictionTimeValue(contract PredictionContract) int64 {
	if contract.TimeBase != TimeBaseUnix {
		return e.BlockHeight
	}
	if e.BlockTime != 0 {
		return e.BlockTime
	}
	return e.BlockHeight
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
		return "", "", fmt.Errorf("prediction bet funding %s is below required gas %s",
			betTotal.String(), gasReserve.String())
	}
	return betTotal.SubAlignPrecision(gasReserve).String(), gasReserve.String(), nil
}

func stateResultPlan(contract ContractAddress, output ContractOutput) (ResultPlan, bool) {
	if output.PhysicalValue() != 0 || len(output.TxAssets()) != 0 {
		return ResultPlan{}, false
	}
	return ResultPlan{
		Contract: contract.EncodeAddress(),
		Inputs:   []OutPoint{output.OutPoint},
	}, true
}

func cloneSettlementPlans(in []*PredictionSettlementPlan) []*PredictionSettlementPlan {
	return contractframework.CloneSettlementPlans(in)
}
