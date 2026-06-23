package node

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	contractengine "github.com/sat20-labs/satoshinet/contract/engine"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/mining"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type Services struct {
	BlockValidator       blockchain.ContractBlockValidator
	ContractStateManager blockchain.ContractStateManager
	ResultBuilder        mining.ContractResultBuilder
	MempoolPolicy        contractframework.MempoolPolicy
	QueryService         contractframework.QueryService
}

type GasConfig = contractframework.GasConfig
type AgentRuntimeConfig = agentcontract.RuntimeConfig
type ScriptRecipientResolver = contractframework.ScriptRecipientResolver
type AssetPrecisionResolver = contractframework.AssetPrecisionResolver

type ContractUTXO struct {
	OutPoint       wire.OutPoint
	Value          int64
	Assets         wire.TxAssets
	Height         int64
	IsGasFunding   bool
	SourceCallID   string
	ReservedReason string
}

type ContractUTXOProvider func(contract contractcommon.ContractAddress) ([]ContractUTXO, error)

func DefaultGasConfig() GasConfig {
	return contractframework.DefaultGasConfig()
}

type Config struct {
	DB          database.DB
	ChainParams *chaincfg.Params

	GasConfig        GasConfig
	BootstrapAddress string
	AgentRuntime     AgentRuntimeConfig

	EVMContractUTXOs      ContractUTXOProvider
	TemplateContractUTXOs ContractUTXOProvider
	AgentContractUTXOs    ContractUTXOProvider
	AssetPrecision        AssetPrecisionResolver

	EVMResolveRecipient      ScriptRecipientResolver
	TemplateResolveRecipient ScriptRecipientResolver
	AgentResolveRecipient    ScriptRecipientResolver

	BlockValidator       blockchain.ContractBlockValidator
	ContractStateManager blockchain.ContractStateManager
	ResultBuilder        mining.ContractResultBuilder
	MempoolPolicy        contractframework.MempoolPolicy
	QueryService         contractframework.QueryService

	SkipStateRootVerify bool
}

func NewServices(cfg Config) (*Services, error) {
	blockValidator := cfg.BlockValidator
	if blockValidator == nil {
		validator, err := newBlockValidator(cfg)
		if err != nil {
			return nil, err
		}
		blockValidator = validator
	}

	resultBuilder := cfg.ResultBuilder
	if resultBuilder == nil {
		builder, err := NewResultBuilder(cfg)
		if err != nil {
			return nil, err
		}
		resultBuilder = builder
	}

	contractStateManager := cfg.ContractStateManager
	if contractStateManager == nil {
		contractStateManager = NewContractStateManager()
	}

	return &Services{
		BlockValidator:       blockValidator,
		ContractStateManager: contractStateManager,
		ResultBuilder:        resultBuilder,
		MempoolPolicy:        cfg.MempoolPolicy,
		QueryService:         cfg.QueryService,
	}, nil
}

func newBlockValidator(cfg Config) (blockchain.ContractBlockValidator, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("missing contract service DB")
	}
	evmValidator, err := NewEVMBlockValidator(cfg)
	if err != nil {
		return nil, err
	}
	templateValidator, err := NewTemplateBlockValidator(cfg)
	if err != nil {
		return nil, err
	}
	agentValidator, err := NewAgentBlockValidator(cfg)
	if err != nil {
		return nil, err
	}
	return NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams:       cfg.ChainParams,
		TemplateValidator: templateValidator,
		EVMValidator:      evmValidator,
		AgentValidator:    agentValidator,
	}), nil
}

func NewEVMBlockValidator(cfg Config) (ContractModuleBlockValidator, error) {
	gasConfig := evmGasConfigFromCommon(cfg.GasConfig)
	if gasConfig == (evm.GasConfig{}) {
		gasConfig = evm.DefaultGasConfig()
	}
	if err := gasConfig.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewEVMStateStore(cfg.DB)
	return NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{
		ChainParams:         cfg.ChainParams,
		GasConfig:           gasConfig,
		NewRuntime:          stateStore.RuntimeFactory(),
		ContractUTXOs:       evmContractUTXOProvider(cfg.EVMContractUTXOs),
		ResolveRecipient:    contractframework.ScriptRecipientResolver(cfg.EVMResolveRecipient),
		SkipStateRootVerify: cfg.SkipStateRootVerify,
	}), nil
}

func NewTemplateBlockValidator(cfg Config) (ContractModuleBlockValidator, error) {
	gasConfig := templateGasConfigFromCommon(cfg.GasConfig, cfg)
	if gasConfig == (tmplcontract.GasConfig{}) {
		gasConfig = tmplcontract.DefaultGasConfig()
	}
	if err := gasConfig.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewTemplateStateStore(cfg.DB)
	return NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{
		ChainParams:         cfg.ChainParams,
		ContractPrefix:      templateContractPrefix(cfg.ChainParams),
		GasConfig:           gasConfig,
		Registry:            templateRegistry(),
		NewRuntime:          stateStore.RuntimeFactory(),
		ResolveOutput:       templateResultOutputResolver(cfg),
		ContractUTXOs:       templateContractUTXOProvider(cfg.TemplateContractUTXOs),
		AssetPrecision:      cfg.AssetPrecision,
		SkipStateRootVerify: cfg.SkipStateRootVerify,
	}), nil
}

func NewAgentBlockValidator(cfg Config) (ContractModuleBlockValidator, error) {
	gasConfig := agentGasConfigFromCommon(cfg.GasConfig)
	if gasConfig == (agentcontract.GasConfig{}) {
		gasConfig = agentcontract.DefaultGasConfig()
	}
	stateStore := NewAgentStateStore(cfg.DB)
	return NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
		ChainParams:         cfg.ChainParams,
		ContractPrefix:      agentContractPrefix(cfg.ChainParams),
		RuntimeConfig:       cfg.AgentRuntime,
		GasConfig:           gasConfig,
		NewRuntime:          stateStore.RuntimeFactory(),
		ResolveResultOutput: agentResultOutputResolver(cfg),
		ContractUTXOs:       agentContractUTXOProvider(cfg.AgentContractUTXOs),
		AssetPrecision:      cfg.AssetPrecision,
		SkipStateRootVerify: cfg.SkipStateRootVerify,
	}), nil
}

func NewResultBuilder(cfg Config) (mining.ContractResultBuilder, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("missing contract service DB")
	}
	return func(req mining.ContractBuildRequest) (mining.ContractBuildResult, error) {
		modules, err := miningModulesForRequest(cfg, req)
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		if len(modules) == 0 {
			return mining.ContractBuildResult{}, nil
		}
		coordinator := contractframework.BlockCoordinator{
			Modules: modules,
			Prefix:  contractPrefixForParams(cfg.ChainParams),
		}
		result, err := coordinator.BuildResults(contractframework.ResultCoordinatorBuildRequest{
			Txs:        msgTxs(req.Txs),
			ParentView: contractNodeUTXOView{view: req.UtxoView},
		})
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		return mining.ContractBuildResult{
			ResultTxs: result.ResultTxs,
			StateRoot: result.CombinedRoot,
		}, nil
	}, nil
}

func miningModulesForRequest(cfg Config, req mining.ContractBuildRequest) ([]contractframework.Module, error) {
	modules := make([]contractframework.Module, 0, 3)
	templateModule, err := newTemplateMiningModule(cfg, req)
	if err != nil {
		return nil, err
	}
	modules = append(modules, templateModule)

	evmModule, err := newEVMMiningModule(cfg, req)
	if err != nil {
		return nil, err
	}
	modules = append(modules, evmModule)

	agentModule, err := newAgentMiningModule(cfg, req)
	if err != nil {
		return nil, err
	}
	modules = append(modules, agentModule)
	return modules, nil
}

func contractPrefixForRequest(reqPrefix, cfgPrefix string) string {
	if reqPrefix != "" {
		return reqPrefix
	}
	return cfgPrefix
}

func evmModuleDescriptor() contractframework.ModuleDescriptor {
	return contractframework.ModuleDescriptor{
		NameValue:     "evm",
		TypeValue:     contractframework.ModuleEVM,
		PriorityValue: 2,
		DefaultPrefix: contractcommon.TestnetContractPrefix,
		ClassifyOrder: evm.ClassifyTxForBlockOrder,
		MatchesOrder:  func(info contractframework.TxOrderInfo) bool { return info.IsEVM },
	}
}

func templateModuleDescriptor() contractframework.ModuleDescriptor {
	return contractframework.ModuleDescriptor{
		NameValue:     "template",
		TypeValue:     contractframework.ModuleTemplate,
		PriorityValue: 1,
		DefaultPrefix: contractcommon.TestnetContractPrefix,
		ClassifyOrder: tmplcontract.ClassifyTxForBlockOrder,
		MatchesOrder:  func(info contractframework.TxOrderInfo) bool { return info.IsTemplate },
	}
}

func agentModuleDescriptor() contractframework.ModuleDescriptor {
	return contractframework.ModuleDescriptor{
		NameValue:     "agent",
		TypeValue:     contractframework.ModuleAgent,
		PriorityValue: 3,
		DefaultPrefix: contractcommon.TestnetContractPrefix,
		ClassifyOrder: agentcontract.ClassifyTxForBlockOrder,
		MatchesOrder:  func(info contractframework.TxOrderInfo) bool { return info.IsAgent },
	}
}

func newEVMMiningModule(cfg Config, req mining.ContractBuildRequest) (contractframework.Module, error) {
	gasConfig := evmGasConfigFromCommon(cfg.GasConfig)
	if gasConfig == (evm.GasConfig{}) {
		gasConfig = evm.DefaultGasConfig()
	}
	if err := gasConfig.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewEVMStateStore(cfg.DB)
	runtime, err := stateStore.RuntimeFactory()(buildParentBlock(req), nil)
	if err != nil {
		return nil, err
	}
	blockGasConfig := gasConfig
	blockGasConfig.GasAssetName = contractGasAssetNameForParams(cfg.ChainParams)
	contractPrefix := evmContractPrefix(cfg.ChainParams)
	block := evm.BlockContext{
		Number:        uint64(req.Height),
		Time:          uint64(req.Timestamp.Unix()),
		GasLimit:      blockGasConfig.MaxGasPerBlock,
		FixedGasPrice: blockGasConfig.FixedGasPrice,
	}
	resolveCaller := evm.LastInputPreviousOutputCallerResolver(cfg.ChainParams,
		previousOutputScriptResolver(req.UtxoView))
	resolveRefund := evm.LastInputPreviousOutputGasRefundRecipientResolver(cfg.ChainParams,
		previousOutputScriptResolver(req.UtxoView))
	contractUTXOs := evmContractUTXOProvider(cfg.EVMContractUTXOs)
	resolveScript := evmResultScriptResolver(cfg.ChainParams)
	resolveOutput := evmResultOutputResolver(cfg)
	return contractframework.ModuleAdapter{
		ModuleDescriptor: evmModuleDescriptor(),
		ExecuteWorkBlockFunc: func(work contractframework.WorkExecutionRequest) (contractframework.ExecutionResult, error) {
			executed, err := evm.ExecuteWorkBlock(evm.BlockExecutionRequest{
				Txs:                       work.Txs,
				Runtime:                   runtime,
				ContractPrefix:            contractPrefixForRequest(work.Prefix, contractPrefix),
				GasConfig:                 blockGasConfig,
				Block:                     block,
				ResolveCaller:             resolveCaller,
				ResolveGasRefundRecipient: resolveRefund,
				ContractUTXOs:             contractUTXOs,
			})
			if err != nil {
				return contractframework.ExecutionResult{}, err
			}
			return contractframework.NewExecutionResult(
				contractframework.ModuleEVM,
				executed.Records,
				executed.PendingRecords,
				executed.StateRoot,
				executed,
			), nil
		},
		BuildBlockResultsFunc: func(work contractframework.ResultBuildRequest) (
			contractframework.ResultBuildResult, contractframework.ExecutionResult, error) {
			var parentRoot [32]byte
			if runtime != nil && runtime.State != nil {
				parentRoot = runtime.State.StateRoot()
			}
			result, err := evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
				Txs:                       work.Txs,
				Runtime:                   runtime,
				ContractPrefix:            contractPrefixForRequest(work.Prefix, contractPrefix),
				GasConfig:                 blockGasConfig,
				Block:                     block,
				ResolveCaller:             resolveCaller,
				ResolveGasRefundRecipient: resolveRefund,
				ContractUTXOs:             contractUTXOs,
				ResolveScript:             resolveScript,
				ResolveOutput:             resolveOutput,
			})
			if err != nil {
				return contractframework.ResultBuildResult{}, contractframework.ExecutionResult{}, err
			}
			build, exec := contractframework.WrapModuleBlockResult(contractframework.ModuleBlockResultSpec{
				ModuleType: contractframework.ModuleEVM,
				ParentRoot: parentRoot,
				Result: contractframework.BlockResultBuildResult{
					ResultTxs: result.ResultTxs,
					Execution: result.Execution,
				},
				Records: func(exec any) []contractframework.ExecutionRecord {
					return exec.(evm.BlockExecutionResult).Records
				},
				Pending: func(exec any) []contractframework.ExecutionRecord {
					return exec.(evm.BlockExecutionResult).PendingRecords
				},
				StateRoot: func(exec any) [32]byte {
					return exec.(evm.BlockExecutionResult).StateRoot
				},
			})
			return build, exec, nil
		},
		BuildResultTxsFunc: func(work contractframework.ResultBuildRequest,
			exec contractframework.ExecutionResult) (contractframework.ResultBuildResult, error) {
			result, err := evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
				Txs:                       work.Txs,
				Runtime:                   runtime,
				ContractPrefix:            contractPrefixForRequest(work.Prefix, contractPrefix),
				GasConfig:                 blockGasConfig,
				Block:                     block,
				ResolveCaller:             resolveCaller,
				ResolveGasRefundRecipient: resolveRefund,
				ContractUTXOs:             contractUTXOs,
				ResolveScript:             resolveScript,
				ResolveOutput:             resolveOutput,
			})
			if err != nil {
				return contractframework.ResultBuildResult{}, err
			}
			return contractframework.ResultBuildResult{
				ResultTxs: result.ResultTxs,
				StateRoot: result.Execution.StateRoot,
			}, nil
		},
		VerifyResultTxsFunc: func(verifyReq contractframework.ResultVerifyRequest, exec contractframework.ExecutionResult) error {
			evmExec, ok := exec.PostState.(evm.BlockExecutionResult)
			if !ok {
				evmExec = evm.BlockExecutionResult{
					Records:        contractframework.CloneExecutionRecords(exec.Records),
					PendingRecords: contractframework.CloneExecutionRecords(exec.PendingRecords),
					StateRoot:      exec.StateRoot,
				}
			}
			return evm.VerifyResultTxs(evm.ResultVerifyRequest{
				ResultTxs:      verifyReq.ResultTxs,
				Execution:      evmExec,
				ContractPrefix: contractPrefixForRequest(verifyReq.Prefix, contractPrefix),
			})
		},
	}, nil
}

func newTemplateMiningModule(cfg Config, req mining.ContractBuildRequest) (contractframework.Module, error) {
	gasConfig := templateGasConfigFromCommon(cfg.GasConfig, cfg)
	if gasConfig == (tmplcontract.GasConfig{}) {
		gasConfig = tmplcontract.DefaultGasConfig()
	}
	if err := gasConfig.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewTemplateStateStore(cfg.DB)
	store, err := stateStore.RuntimeFactory()(buildParentBlock(req), nil)
	if err != nil {
		return nil, err
	}
	blockGasConfig := templateGasConfigForBlock(gasConfig, cfg.ChainParams)
	registry := templateRegistry()
	contractPrefix := templateContractPrefix(cfg.ChainParams)
	contractUTXOs := templateContractUTXOProvider(cfg.TemplateContractUTXOs)
	resolveInvoker := tmplcontract.LastInputPreviousOutputInvokerResolver(cfg.ChainParams,
		previousOutputScriptResolver(req.UtxoView))
	resolveScript := templateResultScriptResolver(cfg.ChainParams)
	resolveOutput := templateResultOutputResolver(cfg)
	return contractframework.ModuleAdapter{
		ModuleDescriptor: templateModuleDescriptor(),
		ExecuteWorkBlockFunc: func(work contractframework.WorkExecutionRequest) (contractframework.ExecutionResult, error) {
			prefix := contractPrefixForRequest(work.Prefix, contractPrefix)
			executed, err := tmplcontract.ExecuteBlock(tmplcontract.BlockExecutionRequest{
				Txs:            work.Txs,
				Store:          store,
				Registry:       registry,
				ContractPrefix: prefix,
				GasConfig:      blockGasConfig,
				ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(
					contractUTXOs, work.Txs, prefix, tmplcontract.ContractTypeTemplate),
				AssetPrecision: cfg.AssetPrecision,
				BlockHeight:    int64(req.Height),
				ResolveInvoker: resolveInvoker,
			})
			if err != nil {
				return contractframework.ExecutionResult{}, err
			}
			overlay := contractframework.ContractUTXOProviderWithTxOutputs(
				contractUTXOs, work.Txs, prefix, tmplcontract.ContractTypeTemplate)
			resultPlans, err := tmplcontract.AugmentResultPlans(executed.ResultPlans, store, blockGasConfig, overlay, cfg.AssetPrecision)
			if err != nil {
				return contractframework.ExecutionResult{}, err
			}
			executed.ResultPlans = resultPlans
			return contractframework.NewExecutionResult(
				contractframework.ModuleTemplate,
				executed.Records,
				nil,
				executed.StateRoot,
				executed,
			), nil
		},
		BuildBlockResultsFunc: func(work contractframework.ResultBuildRequest) (
			contractframework.ResultBuildResult, contractframework.ExecutionResult, error) {
			var parentRoot [32]byte
			if store != nil {
				parentRoot = store.StateRoot()
			}
			result, err := tmplcontract.BuildBlockResultTxs(tmplcontract.BlockResultBuildRequest{
				Txs:            work.Txs,
				Store:          store,
				Registry:       registry,
				ContractPrefix: contractPrefixForRequest(work.Prefix, contractPrefix),
				GasConfig:      blockGasConfig,
				ContractUTXOs:  contractUTXOs,
				AssetPrecision: cfg.AssetPrecision,
				BlockHeight:    int64(req.Height),
				ResolveInvoker: resolveInvoker,
				ResolveScript:  resolveScript,
				ResolveOutput:  resolveOutput,
			})
			if err != nil {
				return contractframework.ResultBuildResult{}, contractframework.ExecutionResult{}, err
			}
			build, exec := contractframework.WrapModuleBlockResult(contractframework.ModuleBlockResultSpec{
				ModuleType: contractframework.ModuleTemplate,
				ParentRoot: parentRoot,
				Result: contractframework.BlockResultBuildResult{
					ResultTxs: result.ResultTxs,
					Execution: result.Execution,
				},
				Records: func(exec any) []contractframework.ExecutionRecord {
					return exec.(tmplcontract.BlockExecutionResult).Records
				},
				StateRoot: func(exec any) [32]byte {
					return exec.(tmplcontract.BlockExecutionResult).StateRoot
				},
			})
			return build, exec, nil
		},
		BuildResultTxsFunc: func(work contractframework.ResultBuildRequest,
			exec contractframework.ExecutionResult) (contractframework.ResultBuildResult, error) {
			result, err := tmplcontract.BuildBlockResultTxs(tmplcontract.BlockResultBuildRequest{
				Txs:            work.Txs,
				Store:          store,
				Registry:       registry,
				ContractPrefix: contractPrefixForRequest(work.Prefix, contractPrefix),
				GasConfig:      blockGasConfig,
				ContractUTXOs:  contractUTXOs,
				AssetPrecision: cfg.AssetPrecision,
				BlockHeight:    int64(req.Height),
				ResolveInvoker: resolveInvoker,
				ResolveScript:  resolveScript,
				ResolveOutput:  resolveOutput,
			})
			if err != nil {
				return contractframework.ResultBuildResult{}, err
			}
			return contractframework.ResultBuildResult{
				ResultTxs: result.ResultTxs,
				StateRoot: result.Execution.StateRoot,
			}, nil
		},
		VerifyResultTxsFunc: func(verifyReq contractframework.ResultVerifyRequest, exec contractframework.ExecutionResult) error {
			return contractframework.VerifySingleResultTx(contractframework.SingleResultTxVerifyRequest{
				Label:        "template",
				ResultTxs:    verifyReq.ResultTxs,
				Expectations: templateExecutionResultPlans(exec),
				Verify: func(tx *wire.MsgTx, plans []tmplcontract.ResultPlan) error {
					return contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
						Label:        "template",
						ResultTx:     tx,
						Status:       tmplcontract.ResultStatusSuccess,
						Plans:        plans,
						Resolve:      resolveOutput,
						PlanCount:    templateResultPlanCount,
						CheckPayload: true,
					})
				},
			})
		},
	}, nil
}

func newAgentMiningModule(cfg Config, req mining.ContractBuildRequest) (contractframework.Module, error) {
	gasConfig := agentGasConfigFromCommon(cfg.GasConfig)
	if gasConfig == (agentcontract.GasConfig{}) {
		gasConfig = agentcontract.DefaultGasConfig()
	}
	stateStore := NewAgentStateStore(cfg.DB)
	store, err := stateStore.RuntimeFactory()(buildParentBlock(req), nil)
	if err != nil {
		return nil, err
	}
	blockGasConfig := agentGasConfigForBlock(gasConfig, cfg.ChainParams)
	contractPrefix := agentContractPrefix(cfg.ChainParams)
	contractUTXOs := agentContractUTXOProvider(cfg.AgentContractUTXOs)
	resolveInvoker := agentcontract.LastInputPreviousOutputInvokerResolver(cfg.ChainParams,
		previousOutputScriptResolver(req.UtxoView))
	resolveScript := agentResultScriptResolver(cfg.ChainParams)
	resolveOutput := agentResultOutputResolver(cfg)
	blockHeight := int64(req.Height)
	blockTime := req.Timestamp.Unix()
	return contractframework.ModuleAdapter{
		ModuleDescriptor: agentModuleDescriptor(),
		ExecuteWorkBlockFunc: func(work contractframework.WorkExecutionRequest) (contractframework.ExecutionResult, error) {
			prefix := contractPrefixForRequest(work.Prefix, contractPrefix)
			overlay := contractframework.ContractUTXOProviderWithTxOutputs(
				contractUTXOs, work.Txs, prefix, agentcontract.ContractTypeAgent)
			executed, err := agentcontract.ExecuteBlock(agentcontract.BlockExecutionRequest{
				Txs:            work.Txs,
				Store:          store,
				ContractPrefix: prefix,
				RuntimeConfig:  cfg.AgentRuntime,
				GasConfig:      blockGasConfig,
				ContractUTXOs:  overlay,
				AssetPrecision: cfg.AssetPrecision,
				BlockHeight:    blockHeight,
				BlockTime:      blockTime,
				ResolveInvoker: resolveInvoker,
			})
			if err != nil {
				return contractframework.ExecutionResult{}, err
			}
			resultPlans, err := agentcontract.AugmentResultPlans(executed.ResultPlans, overlay, store, cfg.AssetPrecision,
				blockGasConfig.Normalize().GasAssetName, cfg.AgentRuntime.BootstrapAddress)
			if err != nil {
				return contractframework.ExecutionResult{}, err
			}
			executed.ResultPlans = resultPlans
			return contractframework.NewExecutionResult(
				contractframework.ModuleAgent,
				executed.Records,
				nil,
				executed.StateRoot,
				executed,
			), nil
		},
		BuildBlockResultsFunc: func(work contractframework.ResultBuildRequest) (
			contractframework.ResultBuildResult, contractframework.ExecutionResult, error) {
			var parentRoot [32]byte
			if store != nil {
				parentRoot = store.StateRoot()
			}
			result, err := agentcontract.BuildBlockResultTxs(agentcontract.BlockResultBuildRequest{
				Txs:            work.Txs,
				Store:          store,
				ContractPrefix: contractPrefixForRequest(work.Prefix, contractPrefix),
				RuntimeConfig:  cfg.AgentRuntime,
				GasConfig:      blockGasConfig,
				ContractUTXOs:  contractUTXOs,
				AssetPrecision: cfg.AssetPrecision,
				BlockHeight:    blockHeight,
				BlockTime:      blockTime,
				ResolveInvoker: resolveInvoker,
				ResolveScript:  resolveScript,
				ResolveOutput:  resolveOutput,
			})
			if err != nil {
				return contractframework.ResultBuildResult{}, contractframework.ExecutionResult{}, err
			}
			build, exec := contractframework.WrapModuleBlockResult(contractframework.ModuleBlockResultSpec{
				ModuleType: contractframework.ModuleAgent,
				ParentRoot: parentRoot,
				Result: contractframework.BlockResultBuildResult{
					ResultTxs: result.ResultTxs,
					Execution: result.Execution,
				},
				Records: func(exec any) []contractframework.ExecutionRecord {
					return exec.(agentcontract.BlockExecutionResult).Records
				},
				StateRoot: func(exec any) [32]byte {
					return exec.(agentcontract.BlockExecutionResult).StateRoot
				},
			})
			return build, exec, nil
		},
		BuildResultTxsFunc: func(work contractframework.ResultBuildRequest,
			exec contractframework.ExecutionResult) (contractframework.ResultBuildResult, error) {
			result, err := agentcontract.BuildBlockResultTxs(agentcontract.BlockResultBuildRequest{
				Txs:            work.Txs,
				Store:          store,
				ContractPrefix: contractPrefixForRequest(work.Prefix, contractPrefix),
				RuntimeConfig:  cfg.AgentRuntime,
				GasConfig:      blockGasConfig,
				ContractUTXOs:  contractUTXOs,
				AssetPrecision: cfg.AssetPrecision,
				BlockHeight:    blockHeight,
				BlockTime:      blockTime,
				ResolveInvoker: resolveInvoker,
				ResolveScript:  resolveScript,
				ResolveOutput:  resolveOutput,
			})
			if err != nil {
				return contractframework.ResultBuildResult{}, err
			}
			return contractframework.ResultBuildResult{
				ResultTxs: result.ResultTxs,
				StateRoot: result.Execution.StateRoot,
			}, nil
		},
		VerifyResultTxsFunc: func(verifyReq contractframework.ResultVerifyRequest, exec contractframework.ExecutionResult) error {
			return contractframework.VerifySingleResultTx(contractframework.SingleResultTxVerifyRequest{
				Label:        "agent",
				ResultTxs:    verifyReq.ResultTxs,
				Expectations: agentExecutionResultPlans(exec),
				Verify: func(tx *wire.MsgTx, plans []agentcontract.ResultPlan) error {
					return contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
						Label:        "agent",
						ResultTx:     tx,
						Status:       agentcontract.ResultStatusSuccess,
						Plans:        plans,
						Resolve:      resolveOutput,
						CheckPayload: true,
					})
				},
			})
		},
	}, nil
}

func NewEVMResultBuilder(cfg Config) (mining.ContractResultBuilder, error) {
	gasConfig := evmGasConfigFromCommon(cfg.GasConfig)
	if gasConfig == (evm.GasConfig{}) {
		gasConfig = evm.DefaultGasConfig()
	}
	if err := gasConfig.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewEVMStateStore(cfg.DB)
	contractPrefix := evmContractPrefix(cfg.ChainParams)
	resolveOutput := evmResultOutputResolver(cfg)
	return func(req mining.ContractBuildRequest) (mining.ContractBuildResult, error) {
		parentBlock := buildParentBlock(req)
		runtime, err := stateStore.RuntimeFactory()(parentBlock, nil)
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		parentRoot := runtime.State.StateRoot()
		txs := make([]*wire.MsgTx, 0, len(req.Txs))
		for _, tx := range req.Txs {
			msgTx := tx.MsgTx()
			class, found, err := contractengine.ClassifyTxForBlockOrder(msgTx, cfg.ChainParams)
			if err != nil {
				return mining.ContractBuildResult{}, err
			}
			if !found || class.ContractType != contractcommon.ContractTypeEVM {
				continue
			}
			txs = append(txs, msgTx)
		}
		blockGasConfig := gasConfig
		blockGasConfig.GasAssetName = contractGasAssetNameForParams(cfg.ChainParams)
		result, err := evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
			Txs:            txs,
			Runtime:        runtime,
			ContractPrefix: contractPrefix,
			GasConfig:      blockGasConfig,
			Block: evm.BlockContext{
				Number:        uint64(req.Height),
				Time:          uint64(req.Timestamp.Unix()),
				GasLimit:      blockGasConfig.MaxGasPerBlock,
				FixedGasPrice: blockGasConfig.FixedGasPrice,
			},
			ResolveCaller: evm.LastInputPreviousOutputCallerResolver(cfg.ChainParams,
				previousOutputScriptResolver(req.UtxoView)),
			ResolveGasRefundRecipient: evm.LastInputPreviousOutputGasRefundRecipientResolver(cfg.ChainParams,
				previousOutputScriptResolver(req.UtxoView)),
			ContractUTXOs: evmContractUTXOProvider(cfg.EVMContractUTXOs),
			ResolveScript: evmResultScriptResolver(cfg.ChainParams),
			ResolveOutput: resolveOutput,
		})
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		if len(result.Execution.Records) == 0 && result.Execution.StateRoot == parentRoot {
			return mining.ContractBuildResult{}, nil
		}
		return mining.ContractBuildResult{
			ResultTxs: result.ResultTxs,
			StateRoot: result.Execution.StateRoot,
		}, nil
	}, nil
}

func NewTemplateResultBuilder(cfg Config) (mining.ContractResultBuilder, error) {
	gasConfig := templateGasConfigFromCommon(cfg.GasConfig, cfg)
	if gasConfig == (tmplcontract.GasConfig{}) {
		gasConfig = tmplcontract.DefaultGasConfig()
	}
	if err := gasConfig.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewTemplateStateStore(cfg.DB)
	contractPrefix := templateContractPrefix(cfg.ChainParams)
	resolveOutput := templateResultOutputResolver(cfg)
	return func(req mining.ContractBuildRequest) (mining.ContractBuildResult, error) {
		store, err := stateStore.RuntimeFactory()(buildParentBlock(req), nil)
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		txs := msgTxs(req.Txs)
		blockGasConfig := templateGasConfigForBlock(gasConfig, cfg.ChainParams)
		result, err := tmplcontract.BuildBlockResultTxs(tmplcontract.BlockResultBuildRequest{
			Txs:            txs,
			Store:          store,
			Registry:       templateRegistry(),
			ContractPrefix: contractPrefix,
			GasConfig:      blockGasConfig,
			ContractUTXOs:  templateContractUTXOProvider(cfg.TemplateContractUTXOs),
			AssetPrecision: cfg.AssetPrecision,
			BlockHeight:    int64(req.Height),
			ResolveInvoker: tmplcontract.LastInputPreviousOutputInvokerResolver(cfg.ChainParams,
				previousOutputScriptResolver(req.UtxoView)),
			ResolveScript: templateResultScriptResolver(cfg.ChainParams),
			ResolveOutput: resolveOutput,
		})
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		return mining.ContractBuildResult{
			ResultTxs: result.ResultTxs,
			StateRoot: result.Execution.StateRoot,
		}, nil
	}, nil
}

func NewAgentResultBuilder(cfg Config) (mining.ContractResultBuilder, error) {
	gasConfig := agentGasConfigFromCommon(cfg.GasConfig)
	if gasConfig == (agentcontract.GasConfig{}) {
		gasConfig = agentcontract.DefaultGasConfig()
	}
	stateStore := NewAgentStateStore(cfg.DB)
	contractPrefix := agentContractPrefix(cfg.ChainParams)
	resolveOutput := agentResultOutputResolver(cfg)
	return func(req mining.ContractBuildRequest) (mining.ContractBuildResult, error) {
		store, err := stateStore.RuntimeFactory()(buildParentBlock(req), nil)
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		parentRoot := store.StateRoot()
		txs := msgTxs(req.Txs)
		blockGasConfig := agentGasConfigForBlock(gasConfig, cfg.ChainParams)
		result, err := agentcontract.BuildBlockResultTxs(agentcontract.BlockResultBuildRequest{
			Txs:            txs,
			Store:          store,
			ContractPrefix: contractPrefix,
			RuntimeConfig:  cfg.AgentRuntime,
			GasConfig:      blockGasConfig,
			ContractUTXOs:  agentContractUTXOProvider(cfg.AgentContractUTXOs),
			AssetPrecision: cfg.AssetPrecision,
			BlockHeight:    int64(req.Height),
			BlockTime:      req.Timestamp.Unix(),
			ResolveInvoker: agentcontract.LastInputPreviousOutputInvokerResolver(cfg.ChainParams,
				previousOutputScriptResolver(req.UtxoView)),
			ResolveScript: agentResultScriptResolver(cfg.ChainParams),
			ResolveOutput: resolveOutput,
		})
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		if len(result.Execution.Records) == 0 && len(result.ResultTxs) == 0 &&
			result.Execution.StateRoot == parentRoot {
			return mining.ContractBuildResult{}, nil
		}
		return mining.ContractBuildResult{
			ResultTxs: result.ResultTxs,
			StateRoot: result.Execution.StateRoot,
		}, nil
	}, nil
}

func buildParentBlock(req mining.ContractBuildRequest) *btcutil.Block {
	return btcutil.NewBlock(&wire.MsgBlock{
		Header: wire.BlockHeader{
			PrevBlock: req.PrevHash,
			Timestamp: req.Timestamp,
		},
	})
}

func msgTxs(txs []*btcutil.Tx) []*wire.MsgTx {
	out := make([]*wire.MsgTx, 0, len(txs))
	for _, tx := range txs {
		out = append(out, tx.MsgTx())
	}
	return out
}

func contractBitcoinNet(params *chaincfg.Params) wire.BitcoinNet {
	if params == nil {
		return wire.TestNet
	}
	return params.Net
}

func contractGasAssetNameForParams(params *chaincfg.Params) string {
	return contractcommon.GasAssetNameForNet(contractBitcoinNet(params))
}

func defaultGasConfig(base GasConfig) GasConfig {
	if base == (GasConfig{}) {
		base = contractframework.DefaultGasConfig()
	}
	return base
}

func evmGasConfigFromCommon(base GasConfig) evm.GasConfig {
	if base == (GasConfig{}) {
		return evm.GasConfig{}
	}
	return evm.GasConfig{
		GasAssetName:             base.GasAssetName,
		GasPriceDenominator:      base.GasPriceDenominator,
		InitialGasPriceNumerator: base.InitialGasPriceNumerator,
		GasPriceDecayInterval:    base.GasPriceDecayInterval,
		GasPriceDecayNumerator:   base.GasPriceDecayNumerator,
		GasPriceDecayDenominator: base.GasPriceDecayDenominator,
		GasPriceFloorNumerator:   base.GasPriceFloorNumerator,
		DeployBaseGas:            base.DeployBaseGas,
		InvokeBaseGas:            base.InvokeBaseGas,
		ResultBaseGas:            base.ResultBaseGas,
		TriggerBaseGas:           base.TriggerBaseGas,
		MaxGasPerInvoke:          base.MaxGasPerInvoke,
		MaxGasPerTrigger:         base.MaxGasPerTrigger,
		MaxGasPerBlock:           base.MaxGasPerBlock,
		FixedGasPrice:            base.FixedGasPrice,
		ResultPackingFee:         base.ResultPackingFee,
	}
}

func templateGasConfigFromCommon(base GasConfig, cfg Config) tmplcontract.GasConfig {
	out := defaultGasConfig(base)
	out.GasAssetName = contractGasAssetNameForParams(cfg.ChainParams)
	out.BootstrapAddress = cfg.BootstrapAddress
	return out
}

func templateGasConfigForBlock(base tmplcontract.GasConfig, params *chaincfg.Params) tmplcontract.GasConfig {
	cfg := base
	cfg.GasAssetName = contractGasAssetNameForParams(params)
	return cfg
}

func agentGasConfigFromCommon(base GasConfig) agentcontract.GasConfig {
	return defaultGasConfig(base)
}

func agentGasConfigForBlock(base agentcontract.GasConfig, params *chaincfg.Params) agentcontract.GasConfig {
	cfg := base
	cfg.GasAssetName = contractGasAssetNameForParams(params)
	return cfg
}

func contractPrefixForParams(params *chaincfg.Params) string {
	if params == nil {
		return contractcommon.TestnetContractPrefix
	}
	if params.Net == wire.MainNet {
		return contractcommon.MainnetContractPrefix
	}
	return contractcommon.TestnetContractPrefix
}

func templateRegistry() *tmplcontract.Registry {
	return tmplcontract.NewDefaultRegistry()
}

func evmContractPrefix(params *chaincfg.Params) string {
	if params == nil {
		return evm.TestnetContractPrefix
	}
	return evm.ContractPrefixForNet(params.Net)
}

func templateContractPrefix(params *chaincfg.Params) string {
	if params == nil {
		return tmplcontract.TestnetContractPrefix
	}
	return tmplcontract.ContractPrefixForNet(params.Net)
}

func agentContractPrefix(params *chaincfg.Params) string {
	if params == nil {
		return agentcontract.TestnetContractPrefix
	}
	return agentcontract.ContractPrefixForNet(params.Net)
}

func evmContractUTXOProvider(provider ContractUTXOProvider) evm.ContractUTXOProvider {
	if provider == nil {
		return nil
	}
	return func(contract evm.ContractAddress) ([]evm.UTXO, error) {
		utxos, err := provider(contract)
		if err != nil {
			return nil, err
		}
		out := make([]evm.UTXO, 0, len(utxos))
		for _, utxo := range utxos {
			if utxo.Value < 0 {
				return nil, fmt.Errorf("negative EVM contract output value")
			}
			out = append(out, evm.UTXO{
				OutPoint:       evm.WireOutPointToEVM(utxo.OutPoint),
				Contract:       contract,
				Value:          utxo.Value,
				Assets:         utxo.Assets.Clone(),
				Height:         utxo.Height,
				IsGasFunding:   utxo.IsGasFunding,
				SourceCallID:   utxo.SourceCallID,
				ReservedReason: utxo.ReservedReason,
			})
		}
		return out, nil
	}
}

func templateContractUTXOProvider(provider ContractUTXOProvider) tmplcontract.ContractUTXOProvider {
	if provider == nil {
		return nil
	}
	return func(contract tmplcontract.ContractAddress) ([]tmplcontract.UTXO, error) {
		utxos, err := provider(contract)
		if err != nil {
			return nil, err
		}
		out := make([]tmplcontract.UTXO, 0, len(utxos))
		for _, utxo := range utxos {
			out = append(out, tmplcontract.UTXO{
				OutPoint: tmplcontract.WireOutPointToTemplate(utxo.OutPoint),
				Contract: contract,
				Value:    utxo.Value,
				Assets:   utxo.Assets.Clone(),
				Height:   utxo.Height,
			})
		}
		return out, nil
	}
}

func agentContractUTXOProvider(provider ContractUTXOProvider) agentcontract.ContractUTXOProvider {
	if provider == nil {
		return nil
	}
	return func(contract agentcontract.ContractAddress) ([]agentcontract.UTXO, error) {
		utxos, err := provider(contract)
		if err != nil {
			return nil, err
		}
		out := make([]agentcontract.UTXO, 0, len(utxos))
		for _, utxo := range utxos {
			out = append(out, agentcontract.UTXO{
				OutPoint: agentcontract.WireOutPointToAgent(utxo.OutPoint),
				Contract: contract,
				Value:    utxo.Value,
				Assets:   utxo.Assets.Clone(),
				Height:   utxo.Height,
			})
		}
		return out, nil
	}
}

func evmResultScriptResolver(params *chaincfg.Params) evm.ResultRecipientScriptResolver {
	return func(output evm.ResultOutput) ([]byte, error) {
		if contract, err := evm.DecodeContractAddress(output.To); err == nil {
			return evm.ContractPkScript(contract)
		}
		addr, err := btcutil.DecodeAddress(output.To, params)
		if err != nil {
			return nil, err
		}
		return txscript.PayToAddrScript(addr)
	}
}

func evmResultOutputResolver(cfg Config) evm.ResultOutputResolver {
	contractPrefix := evmContractPrefix(cfg.ChainParams)
	return func(resultTx *wire.MsgTx) ([]evm.ResultOutput, error) {
		return contractframework.ResultOutputsFromTx(resultTx, contractPrefix,
			contractcommon.ParseContractPkScript,
			contractframework.ScriptRecipientResolver(cfg.EVMResolveRecipient))
	}
}

func templateResultScriptResolver(params *chaincfg.Params) tmplcontract.ResultRecipientScriptResolver {
	return func(output tmplcontract.ResultOutput) ([]byte, error) {
		if contract, err := tmplcontract.DecodeContractAddress(output.To); err == nil {
			return tmplcontract.ContractPkScript(contract)
		}
		addr, err := btcutil.DecodeAddress(output.To, params)
		if err != nil {
			return nil, err
		}
		return txscript.PayToAddrScript(addr)
	}
}

func templateResultOutputResolver(cfg Config) tmplcontract.ResultOutputResolver {
	contractPrefix := templateContractPrefix(cfg.ChainParams)
	return func(resultTx *wire.MsgTx) ([]tmplcontract.ResultOutput, error) {
		return contractframework.ResultOutputsFromTx(resultTx, contractPrefix,
			contractcommon.ParseContractPkScript,
			contractframework.ScriptRecipientResolver(cfg.TemplateResolveRecipient))
	}
}

func agentResultScriptResolver(params *chaincfg.Params) agentcontract.ResultRecipientScriptResolver {
	return func(output agentcontract.ResultOutput) ([]byte, error) {
		if contract, err := agentcontract.DecodeContractAddress(output.To); err == nil {
			return agentcontract.ContractPkScript(contract)
		}
		addr, err := btcutil.DecodeAddress(output.To, params)
		if err != nil {
			return nil, err
		}
		return txscript.PayToAddrScript(addr)
	}
}

func agentResultOutputResolver(cfg Config) agentcontract.ResultOutputResolver {
	contractPrefix := agentContractPrefix(cfg.ChainParams)
	return func(resultTx *wire.MsgTx) ([]agentcontract.ResultOutput, error) {
		return contractframework.ResultOutputsFromTx(resultTx, contractPrefix,
			contractcommon.ParseContractPkScript,
			contractframework.ScriptRecipientResolver(cfg.AgentResolveRecipient))
	}
}

func templateResultPlanCount(plan tmplcontract.ResultPlan) int {
	if len(plan.ItemIDs) != 0 {
		return len(plan.ItemIDs)
	}
	return 1
}

func templateExecutionResultPlans(exec contractframework.ExecutionResult) []tmplcontract.ResultPlan {
	result, ok := exec.PostState.(tmplcontract.BlockExecutionResult)
	if !ok {
		return nil
	}
	return result.ResultPlans
}

func agentExecutionResultPlans(exec contractframework.ExecutionResult) []agentcontract.ResultPlan {
	result, ok := exec.PostState.(agentcontract.BlockExecutionResult)
	if !ok {
		return nil
	}
	return result.ResultPlans
}

func previousOutputScriptResolver(view *blockchain.UtxoViewpoint) func(wire.OutPoint) ([]byte, bool) {
	return func(outpoint wire.OutPoint) ([]byte, bool) {
		if view == nil {
			return nil, false
		}
		entry := view.LookupEntry(outpoint)
		if entry == nil {
			return nil, false
		}
		return append([]byte(nil), entry.PkScript()...), true
	}
}

type contractNodeUTXOView struct {
	view *blockchain.UtxoViewpoint
}

func (v contractNodeUTXOView) LookupContractAddress(outpoint wire.OutPoint,
	prefix string) (contractcommon.ContractAddress, bool, error) {

	if v.view == nil {
		return contractcommon.ContractAddress{}, false, nil
	}
	entry := v.view.LookupEntry(outpoint)
	if entry == nil {
		return contractcommon.ContractAddress{}, false, nil
	}
	return contractcommon.ParseContractPkScript(entry.PkScript(), prefix)
}
