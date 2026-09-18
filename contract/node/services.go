package node

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
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

type ContractUTXOProvider func(contractcommon.ContractAddress) ([]ContractUTXO, error)

func DefaultGasConfig() GasConfig { return contractframework.DefaultGasConfig() }

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

	AdditionalModules []ModuleRegistration

	BlockValidator       blockchain.ContractBlockValidator
	ContractStateManager blockchain.ContractStateManager
	ResultBuilder        mining.ContractResultBuilder
	MempoolPolicy        contractframework.MempoolPolicy
	QueryService         contractframework.QueryService
}

func NewServices(cfg Config) (*Services, error) {
	cfg, err := normalizedServiceConfig(cfg)
	if err != nil {
		return nil, err
	}
	registrations, err := contractModuleRegistrations(cfg)
	if err != nil {
		return nil, err
	}
	validator := cfg.BlockValidator
	if validator == nil {
		validator, err = newBlockValidator(cfg)
		if err != nil {
			return nil, err
		}
	}
	builder := cfg.ResultBuilder
	if builder == nil {
		builder, err = NewResultBuilder(cfg)
		if err != nil {
			return nil, err
		}
	}
	manager := cfg.ContractStateManager
	if manager == nil {
		registered := &ContractStateManager{codecs: make(map[contractframework.ModuleType]ModuleStateCodec)}
		for _, registration := range registrations {
			if err := registered.RegisterStateCodec(registration.StateCodec); err != nil {
				return nil, err
			}
		}
		manager = registered
	}
	return &Services{
		BlockValidator: validator, ContractStateManager: manager, ResultBuilder: builder,
		MempoolPolicy: cfg.MempoolPolicy, QueryService: cfg.QueryService,
	}, nil
}

func newBlockValidator(cfg Config) (blockchain.ContractBlockValidator, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("missing contract service DB")
	}
	registrations, err := contractModuleRegistrations(cfg)
	if err != nil {
		return nil, err
	}
	validators := make([]RegisteredContractValidator, 0, len(registrations))
	for _, registration := range registrations {
		validator, err := registration.NewValidator(cfg)
		if err != nil {
			return nil, err
		}
		validators = append(validators, RegisteredContractValidator{Descriptor: registration.Descriptor, Validator: validator})
	}
	return NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams: cfg.ChainParams, Modules: validators,
	}), nil
}

func NewEVMBlockValidator(cfg Config) (ContractModuleBlockValidator, error) {
	cfg, err := normalizedServiceConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.EVMContractUTXOs == nil || cfg.EVMResolveRecipient == nil || cfg.AssetPrecision == nil {
		return nil, fmt.Errorf("missing EVM contract UTXO provider, result recipient resolver or asset precision resolver")
	}
	gas := evmGasConfigForBlock(defaultGasConfig(cfg.GasConfig), cfg)
	if err := gas.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewEVMStateStore(cfg.DB)
	validator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{
		ChainParams: cfg.ChainParams, GasConfig: gas, NewRuntime: stateStore.RuntimeFactory(), HistoryDB: cfg.DB,
		ContractUTXOs: evmContractUTXOProvider(cfg.EVMContractUTXOs), ResolveRecipient: cfg.EVMResolveRecipient,
		ResolveResultOutput: evmResultOutputResolver(cfg), ResolveResultScript: evmResultScriptResolver(cfg.ChainParams),
		AssetPrecision: cfg.AssetPrecision,
	})
	validator.cfg.NewRuntime = func(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*evm.Runtime, error) {
		validator.postStateMu.Lock()
		hasTransient := len(validator.postStates) != 0
		validator.postStateMu.Unlock()
		if !hasTransient {
			return stateStore.RuntimeFactory()(block, view)
		}
		return stateStore.runtimeFactory(validator.EVMBlockPostState)(block, view)
	}
	return validator, nil
}

func NewTemplateBlockValidator(cfg Config) (ContractModuleBlockValidator, error) {
	cfg, err := normalizedServiceConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.TemplateContractUTXOs == nil || cfg.TemplateResolveRecipient == nil || cfg.AssetPrecision == nil {
		return nil, fmt.Errorf("missing template contract UTXO provider, result recipient resolver or asset precision resolver")
	}
	gas := templateGasConfigFromCommon(cfg.GasConfig, cfg)
	if err := gas.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewTemplateStateStore(cfg.DB)
	validator := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{
		ChainParams: cfg.ChainParams, ContractPrefix: templateContractPrefix(cfg.ChainParams), GasConfig: gas,
		Registry: templateRegistry(), NewRuntime: stateStore.RuntimeFactory(), ResolveOutput: templateResultOutputResolver(cfg),
		ResolveResultScript: templateResultScriptResolver(cfg.ChainParams),
		ContractUTXOs:       templateContractUTXOProvider(cfg.TemplateContractUTXOs), AssetPrecision: cfg.AssetPrecision,
	})
	validator.cfg.NewRuntime = func(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*tmplcontract.RuntimeStore, error) {
		validator.postStateMu.Lock()
		hasTransient := len(validator.postStates) != 0
		validator.postStateMu.Unlock()
		if !hasTransient {
			return stateStore.RuntimeFactory()(block, view)
		}
		return stateStore.runtimeFactory(validator.TemplateBlockPostState)(block, view)
	}
	return validator, nil
}

func NewAgentBlockValidator(cfg Config) (ContractModuleBlockValidator, error) {
	cfg, err := normalizedServiceConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.AgentContractUTXOs == nil || cfg.AgentResolveRecipient == nil || cfg.AssetPrecision == nil {
		return nil, fmt.Errorf("missing agent contract UTXO provider, result recipient resolver or asset precision resolver")
	}
	gas := agentGasConfigForBlock(defaultGasConfig(cfg.GasConfig), cfg.ChainParams)
	if err := gas.Validate(); err != nil {
		return nil, err
	}
	stateStore := NewAgentStateStore(cfg.DB)
	return NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
		ChainParams: cfg.ChainParams, ContractPrefix: agentContractPrefix(cfg.ChainParams), RuntimeConfig: cfg.AgentRuntime,
		GasConfig: gas, NewRuntime: stateStore.RuntimeFactory(), ResolveResultOutput: agentResultOutputResolver(cfg),
		ResolveResultScript: agentResultScriptResolver(cfg.ChainParams),
		ContractUTXOs:       agentContractUTXOProvider(cfg.AgentContractUTXOs), AssetPrecision: cfg.AssetPrecision,
	}), nil
}

func NewResultBuilder(cfg Config) (mining.ContractResultBuilder, error) {
	var err error
	cfg, err = normalizedServiceConfig(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.DB == nil {
		return nil, fmt.Errorf("missing contract service DB")
	}
	if _, err := contractModuleRegistrations(cfg); err != nil {
		return nil, err
	}
	return func(req mining.ContractBuildRequest) (mining.ContractBuildResult, error) {
		modules, err := miningModulesForRequest(cfg, req)
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		coordinator := contractframework.BlockCoordinator{Modules: modules, Prefix: contractPrefixForParams(cfg.ChainParams)}
		result, err := coordinator.BuildResults(contractframework.ResultCoordinatorBuildRequest{
			Txs: msgTxs(req.Txs), ParentView: contractNodeUTXOView{view: req.UtxoView},
		})
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		return mining.ContractBuildResult{ResultTxs: result.ResultTxs, StateRoot: result.CombinedRoot}, nil
	}, nil
}

func miningModulesForRequest(cfg Config, req mining.ContractBuildRequest) ([]contractframework.Module, error) {
	registrations, err := contractModuleRegistrations(cfg)
	if err != nil {
		return nil, err
	}
	modules := make([]contractframework.Module, 0, len(registrations))
	for _, registration := range registrations {
		module, err := registration.NewMiningModule(cfg, req)
		if err != nil {
			return nil, err
		}
		if module == nil || module.Type() != registration.Descriptor.Type() {
			return nil, fmt.Errorf("contract module factory returned the wrong module")
		}
		modules = append(modules, module)
	}
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
		NameValue: "evm", TypeValue: contractframework.ModuleEVM, PriorityValue: 2,
		DefaultPrefix: contractcommon.TestnetContractPrefix, ClassifyOrder: evm.ClassifyTxForBlockOrder,
		MatchesOrder: func(info contractframework.TxOrderInfo) bool { return info.IsEVM },
	}
}

func templateModuleDescriptor() contractframework.ModuleDescriptor {
	return contractframework.ModuleDescriptor{
		NameValue: "template", TypeValue: contractframework.ModuleTemplate, PriorityValue: 1,
		DefaultPrefix: contractcommon.TestnetContractPrefix, ClassifyOrder: tmplcontract.ClassifyTxForBlockOrder,
		MatchesOrder: func(info contractframework.TxOrderInfo) bool { return info.IsTemplate },
	}
}

func agentModuleDescriptor() contractframework.ModuleDescriptor {
	return contractframework.ModuleDescriptor{
		NameValue: "agent", TypeValue: contractframework.ModuleAgent, PriorityValue: 3,
		DefaultPrefix: contractcommon.TestnetContractPrefix, ClassifyOrder: agentcontract.ClassifyTxForBlockOrder,
		MatchesOrder: func(info contractframework.TxOrderInfo) bool { return info.IsAgent },
	}
}

func newEVMMiningModule(cfg Config, req mining.ContractBuildRequest) (contractframework.Module, error) {
	gas := evmGasConfigForBlock(defaultGasConfig(cfg.GasConfig), cfg)
	if err := gas.Validate(); err != nil {
		return nil, err
	}
	parent, err := NewEVMStateStore(cfg.DB).RuntimeFactory()(buildParentBlock(req), nil)
	if err != nil {
		return nil, err
	}
	block, err := evmContextWithHistory(cfg.DB, evm.BlockContext{
		ChainID: evmChainID(cfg.ChainParams), Number: uint64(req.Height), Time: uint64(req.Timestamp.Unix()),
		GasLimit: gas.Normalize().MaxGasPerBlock, FixedGasPrice: gas.Normalize().FixedGasPrice, ParentHash: [32]byte(req.PrevHash),
	})
	if err != nil {
		return nil, err
	}
	resolvePrevious := previousOutputScriptResolver(req.UtxoView)
	resolveScript := evmResultScriptResolver(cfg.ChainParams)
	return contractframework.NewSettlementModule(contractframework.SettlementModuleConfig{
		Descriptor: evmModuleDescriptor(), ParentRoot: parent.State.StateRoot(), GasConfig: gas,
		ResolveScript: resolveScript, ResolveOutput: evmResultOutputResolver(cfg), CountRecords: true, UseRecordStatus: true,
		Execute: func(work contractframework.WorkExecutionRequest) (contractframework.BackendBlockExecutionResult, any, error) {
			// Execution clones mutable state and only replaces this wrapper on success.
			candidate := *parent
			exec, err := evm.ExecuteWorkBlock(evm.BlockExecutionRequest{
				Txs: work.Txs, Runtime: &candidate, ContractPrefix: contractPrefixForRequest(work.Prefix, evmContractPrefix(cfg.ChainParams)),
				GasConfig: gas, Block: block,
				ResolveCaller:             evm.LastInputPreviousOutputCallerResolver(cfg.ChainParams, resolvePrevious),
				ResolveGasRefundRecipient: evm.LastInputPreviousOutputGasRefundRecipientResolver(cfg.ChainParams, resolvePrevious),
				ResolveResultScript:       resolveScript, ContractUTXOs: evmContractUTXOProvider(cfg.EVMContractUTXOs),
				AssetPrecision: cfg.AssetPrecision,
			})
			return exec, candidate.State, err
		},
	})
}

func newTemplateMiningModule(cfg Config, req mining.ContractBuildRequest) (contractframework.Module, error) {
	gas := templateGasConfigFromCommon(cfg.GasConfig, cfg)
	if err := gas.Validate(); err != nil {
		return nil, err
	}
	parent, err := NewTemplateStateStore(cfg.DB).RuntimeFactory()(buildParentBlock(req), nil)
	if err != nil {
		return nil, err
	}
	return contractframework.NewSettlementModule(contractframework.SettlementModuleConfig{
		Descriptor: templateModuleDescriptor(), ParentRoot: parent.StateRoot(), GasConfig: gas,
		ResolveScript: templateResultScriptResolver(cfg.ChainParams), ResolveOutput: templateResultOutputResolver(cfg),
		PlanCount: templateResultPlanCount,
		Execute: func(work contractframework.WorkExecutionRequest) (contractframework.BackendBlockExecutionResult, any, error) {
			// Execution clones mutable state and only replaces this wrapper on success.
			candidate := *parent
			exec, err := tmplcontract.ExecuteBlock(tmplcontract.BlockExecutionRequest{
				Txs: work.Txs, Store: &candidate, Registry: templateRegistry(),
				ContractPrefix: contractPrefixForRequest(work.Prefix, templateContractPrefix(cfg.ChainParams)),
				GasConfig:      gas, ContractUTXOs: templateContractUTXOProvider(cfg.TemplateContractUTXOs),
				AssetPrecision: cfg.AssetPrecision, BlockHeight: int64(req.Height),
				ResolveInvoker: tmplcontract.LastInputPreviousOutputInvokerResolver(cfg.ChainParams, previousOutputScriptResolver(req.UtxoView)),
			})
			return exec, &candidate, err
		},
	})
}

func newAgentMiningModule(cfg Config, req mining.ContractBuildRequest) (contractframework.Module, error) {
	gas := agentGasConfigForBlock(defaultGasConfig(cfg.GasConfig), cfg.ChainParams)
	if err := gas.Validate(); err != nil {
		return nil, err
	}
	parent, err := NewAgentStateStore(cfg.DB).RuntimeFactory()(buildParentBlock(req), nil)
	if err != nil {
		return nil, err
	}
	return contractframework.NewSettlementModule(contractframework.SettlementModuleConfig{
		Descriptor: agentModuleDescriptor(), ParentRoot: parent.StateRoot(), GasConfig: gas,
		ResolveScript: agentResultScriptResolver(cfg.ChainParams), ResolveOutput: agentResultOutputResolver(cfg),
		Execute: func(work contractframework.WorkExecutionRequest) (contractframework.BackendBlockExecutionResult, any, error) {
			// Execution clones mutable state and only replaces this wrapper on success.
			candidate := *parent
			exec, err := agentcontract.ExecuteBlock(agentcontract.BlockExecutionRequest{
				Txs: work.Txs, Store: &candidate,
				ContractPrefix: contractPrefixForRequest(work.Prefix, agentContractPrefix(cfg.ChainParams)),
				RuntimeConfig:  cfg.AgentRuntime, GasConfig: gas, ContractUTXOs: agentContractUTXOProvider(cfg.AgentContractUTXOs),
				AssetPrecision: cfg.AssetPrecision, BlockHeight: int64(req.Height), BlockTime: req.Timestamp.Unix(),
				ResolveInvoker: agentcontract.LastInputPreviousOutputInvokerResolver(cfg.ChainParams, previousOutputScriptResolver(req.UtxoView)),
			})
			return exec, &candidate, err
		},
	})
}

// Single-engine entry points retain their API but use the same registered
// execute/build/verify adapter; none replays work during Result construction.
func NewEVMResultBuilder(cfg Config) (mining.ContractResultBuilder, error) {
	return newSingleModuleResultBuilder(cfg, newEVMMiningModule)
}
func NewTemplateResultBuilder(cfg Config) (mining.ContractResultBuilder, error) {
	return newSingleModuleResultBuilder(cfg, newTemplateMiningModule)
}
func NewAgentResultBuilder(cfg Config) (mining.ContractResultBuilder, error) {
	return newSingleModuleResultBuilder(cfg, newAgentMiningModule)
}

func newSingleModuleResultBuilder(cfg Config,
	factory func(Config, mining.ContractBuildRequest) (contractframework.Module, error)) (mining.ContractResultBuilder, error) {

	var err error
	cfg, err = normalizedServiceConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := defaultGasConfig(cfg.GasConfig).Validate(); err != nil {
		return nil, err
	}
	return func(req mining.ContractBuildRequest) (mining.ContractBuildResult, error) {
		module, err := factory(cfg, req)
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		prefix := contractPrefixForParams(cfg.ChainParams)
		var work []*wire.MsgTx
		for _, tx := range msgTxs(req.Txs) {
			class, belongs, err := module.ClassifyTx(tx, prefix)
			if err != nil {
				return mining.ContractBuildResult{}, err
			}
			if belongs {
				if class.IsResult() {
					return mining.ContractBuildResult{}, fmt.Errorf("external contract RESULT is not work")
				}
				work = append(work, tx)
			}
		}
		exec, err := module.ExecuteWorkBlock(contractframework.WorkExecutionRequest{Txs: work, Prefix: prefix})
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		result, err := module.BuildResultTxs(contractframework.ResultBuildRequest{Txs: work, Prefix: prefix}, exec)
		if err != nil {
			return mining.ContractBuildResult{}, err
		}
		if err := module.VerifyResultTxs(contractframework.ResultVerifyRequest{ResultTxs: result.ResultTxs, Prefix: prefix}, exec); err != nil {
			return mining.ContractBuildResult{}, err
		}
		return mining.ContractBuildResult{ResultTxs: result.ResultTxs, StateRoot: result.StateRoot}, nil
	}, nil
}

func buildParentBlock(req mining.ContractBuildRequest) *btcutil.Block {
	return btcutil.NewBlock(&wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: req.PrevHash, Timestamp: req.Timestamp}})
}

func msgTxs(txs []*btcutil.Tx) []*wire.MsgTx {
	out := make([]*wire.MsgTx, 0, len(txs))
	for _, tx := range txs {
		if tx == nil {
			out = append(out, nil)
		} else {
			out = append(out, tx.MsgTx())
		}
	}
	return out
}

func contractBitcoinNet(params *chaincfg.Params) wire.BitcoinNet {
	if params == nil {
		return wire.TestNet
	}
	return params.Net
}
func evmChainID(params *chaincfg.Params) uint64 { return uint64(contractBitcoinNet(params)) }
func contractGasAssetNameForParams(params *chaincfg.Params) string {
	return contractcommon.GasAssetNameForNet(contractBitcoinNet(params))
}
func defaultGasConfig(base GasConfig) GasConfig {
	if base == (GasConfig{}) {
		return contractframework.DefaultGasConfig()
	}
	return base
}
func evmGasConfigFromCommon(base GasConfig) evm.GasConfig { return base }
func evmGasConfigForBlock(base evm.GasConfig, cfg Config) evm.GasConfig {
	base.GasAssetName = contractGasAssetNameForParams(cfg.ChainParams)
	base.BootstrapAddress = cfg.BootstrapAddress
	return base
}
func templateGasConfigFromCommon(base GasConfig, cfg Config) tmplcontract.GasConfig {
	base = defaultGasConfig(base)
	base.GasAssetName = contractGasAssetNameForParams(cfg.ChainParams)
	base.BootstrapAddress = cfg.BootstrapAddress
	return base
}
func templateGasConfigForBlock(base tmplcontract.GasConfig, params *chaincfg.Params) tmplcontract.GasConfig {
	base.GasAssetName = contractGasAssetNameForParams(params)
	return base
}
func agentGasConfigFromCommon(base GasConfig) agentcontract.GasConfig { return defaultGasConfig(base) }
func agentGasConfigForBlock(base agentcontract.GasConfig, params *chaincfg.Params) agentcontract.GasConfig {
	base.GasAssetName = contractGasAssetNameForParams(params)
	return base
}

func contractPrefixForParams(params *chaincfg.Params) string {
	if contractBitcoinNet(params) == wire.MainNet {
		return contractcommon.MainnetContractPrefix
	}
	return contractcommon.TestnetContractPrefix
}
func templateRegistry() *tmplcontract.Registry              { return tmplcontract.NewDefaultRegistry() }
func evmContractPrefix(params *chaincfg.Params) string      { return contractPrefixForParams(params) }
func templateContractPrefix(params *chaincfg.Params) string { return contractPrefixForParams(params) }
func agentContractPrefix(params *chaincfg.Params) string    { return contractPrefixForParams(params) }

func frameworkContractUTXOProvider(provider ContractUTXOProvider) contractframework.ContractUTXOProvider {
	if provider == nil {
		return nil
	}
	return func(addr contractcommon.ContractAddress) ([]contractframework.UTXO, error) {
		utxos, err := provider(addr)
		if err != nil {
			return nil, err
		}
		out := make([]contractframework.UTXO, 0, len(utxos))
		for _, utxo := range utxos {
			if utxo.Value < 0 {
				return nil, fmt.Errorf("negative contract output value")
			}
			next := contractframework.UTXOFromTxOutput(contractframework.OutPoint{
				TxID: utxo.OutPoint.Hash.String(), Vout: utxo.OutPoint.Index,
			}, addr, utxo.Height, &wire.TxOut{Value: utxo.Value, Assets: utxo.Assets.Clone()})
			next.IsGasFunding, next.SourceCallID, next.ReservedReason = utxo.IsGasFunding, utxo.SourceCallID, utxo.ReservedReason
			out = append(out, next)
		}
		return out, nil
	}
}
func evmContractUTXOProvider(provider ContractUTXOProvider) evm.ContractUTXOProvider {
	return frameworkContractUTXOProvider(provider)
}
func templateContractUTXOProvider(provider ContractUTXOProvider) tmplcontract.ContractUTXOProvider {
	return frameworkContractUTXOProvider(provider)
}
func agentContractUTXOProvider(provider ContractUTXOProvider) agentcontract.ContractUTXOProvider {
	return frameworkContractUTXOProvider(provider)
}

func contractResultScriptResolver(params *chaincfg.Params) contractframework.ResultRecipientScriptResolver {
	return func(output contractframework.ResultOutput) ([]byte, error) {
		if addr, err := contractcommon.DecodeContractAddress(output.To); err == nil {
			return contractcommon.ContractPkScript(addr)
		}
		address, err := btcutil.DecodeAddress(output.To, params)
		if err != nil {
			return nil, err
		}
		return txscript.PayToAddrScript(address)
	}
}
func evmResultScriptResolver(params *chaincfg.Params) evm.ResultRecipientScriptResolver {
	return contractResultScriptResolver(params)
}
func templateResultScriptResolver(params *chaincfg.Params) tmplcontract.ResultRecipientScriptResolver {
	return contractResultScriptResolver(params)
}
func agentResultScriptResolver(params *chaincfg.Params) agentcontract.ResultRecipientScriptResolver {
	return contractResultScriptResolver(params)
}
func contractResultOutputResolver(params *chaincfg.Params, recipient ScriptRecipientResolver) contractframework.ResultOutputResolver {
	return func(tx *wire.MsgTx) ([]contractframework.ResultOutput, error) {
		return contractframework.ResultOutputsFromTx(tx, contractPrefixForParams(params), contractcommon.ParseContractPkScript, recipient)
	}
}
func evmResultOutputResolver(cfg Config) evm.ResultOutputResolver {
	return contractResultOutputResolver(cfg.ChainParams, cfg.EVMResolveRecipient)
}
func templateResultOutputResolver(cfg Config) tmplcontract.ResultOutputResolver {
	return contractResultOutputResolver(cfg.ChainParams, cfg.TemplateResolveRecipient)
}
func agentResultOutputResolver(cfg Config) agentcontract.ResultOutputResolver {
	return contractResultOutputResolver(cfg.ChainParams, cfg.AgentResolveRecipient)
}

func templateResultPlanCount(plan tmplcontract.ResultPlan) int {
	if len(plan.ItemIDs) != 0 {
		return len(plan.ItemIDs)
	}
	return 1
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

type contractNodeUTXOView struct{ view *blockchain.UtxoViewpoint }

func (v contractNodeUTXOView) LookupContractAddress(outpoint wire.OutPoint, prefix string) (contractcommon.ContractAddress, bool, error) {
	if v.view == nil {
		return contractcommon.ContractAddress{}, false, nil
	}
	entry := v.view.LookupEntry(outpoint)
	if entry == nil {
		return contractcommon.ContractAddress{}, false, nil
	}
	return contractcommon.ParseContractPkScript(entry.PkScript(), prefix)
}
