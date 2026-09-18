package node

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type CompositeContractBlockValidatorConfig struct {
	ChainParams *chaincfg.Params
	Modules     []RegisteredContractValidator

	// Convenience wiring for callers that enable a subset of the built-ins.
	// The constructor converts these fields into the same module registry.
	TemplateValidator ContractModuleBlockValidator
	EVMValidator      ContractModuleBlockValidator
	AgentValidator    ContractModuleBlockValidator
}

type ContractModuleBlockValidator interface {
	ValidateContractModuleBlock(*btcutil.Block, *blockchain.UtxoViewpoint) error
}

type BlockStateProvider interface {
	BlockPostState(*chainhash.Hash) (contractframework.RuntimeStore, bool)
}

type BlockStateReleaser interface{ ReleaseBlockPostState(*chainhash.Hash) }

type ParentStateProvider interface {
	ParentState(*btcutil.Block, *blockchain.UtxoViewpoint) (contractframework.RuntimeStore, bool, error)
}

type CompositeContractBlockValidator struct {
	cfg                CompositeContractBlockValidatorConfig
	registrationError  error
	assetViewMu        sync.Mutex
	preparedAssetViews map[chainhash.Hash]blockchain.ContractAssetIndexView
}

type contractBlockActivity struct{ Template, EVM, Agent bool }

type ContractBlockActivityProvider interface {
	HasContractBlockActivity(*btcutil.Block, *blockchain.UtxoViewpoint) (bool, error)
}

func NewCompositeContractBlockValidator(cfg CompositeContractBlockValidatorConfig) *CompositeContractBlockValidator {
	if len(cfg.Modules) == 0 {
		cfg.Modules = []RegisteredContractValidator{
			{Descriptor: templateModuleDescriptor(), Validator: cfg.TemplateValidator},
			{Descriptor: evmModuleDescriptor(), Validator: cfg.EVMValidator},
			{Descriptor: agentModuleDescriptor(), Validator: cfg.AgentValidator},
		}
	} else {
		cfg.Modules = append([]RegisteredContractValidator(nil), cfg.Modules...)
		// Existing AIDX provider adapters access these built-in configuration
		// handles. Execution and state dispatch below use only the registry.
		for _, module := range cfg.Modules {
			if module.Descriptor.Type() == contractframework.ModuleTemplate {
				cfg.TemplateValidator = module.Validator
			}
			if module.Descriptor.Type() == contractframework.ModuleEVM {
				cfg.EVMValidator = module.Validator
			}
			if module.Descriptor.Type() == contractframework.ModuleAgent {
				cfg.AgentValidator = module.Validator
			}
		}
	}
	v := &CompositeContractBlockValidator{cfg: cfg, preparedAssetViews: make(map[chainhash.Hash]blockchain.ContractAssetIndexView)}
	seen := make(map[contractframework.ModuleType]bool)
	for _, module := range cfg.Modules {
		typ := module.Descriptor.Type()
		if typ == 0 || seen[typ] || module.Descriptor.ClassifyOrder == nil {
			v.registrationError = fmt.Errorf("invalid or duplicate contract validator registration %d", typ)
			break
		}
		seen[typ] = true
	}
	return v
}

func (v *CompositeContractBlockValidator) PrepareContractAssetIndexView(hash *chainhash.Hash,
	view blockchain.ContractAssetIndexView) error {

	if v == nil || hash == nil || view == nil {
		return fmt.Errorf("invalid prepared contract asset view")
	}
	v.assetViewMu.Lock()
	defer v.assetViewMu.Unlock()
	if _, exists := v.preparedAssetViews[*hash]; exists {
		return fmt.Errorf("contract asset view already prepared for block %s", hash)
	}
	v.preparedAssetViews[*hash] = view
	return nil
}

func (v *CompositeContractBlockValidator) ValidateContractBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	v.assetViewMu.Lock()
	defer v.assetViewMu.Unlock()
	if v.registrationError != nil {
		return v.registrationError
	}
	var assetView blockchain.ContractAssetIndexView
	if block != nil {
		assetView = v.preparedAssetViews[*block.Hash()]
		delete(v.preparedAssetViews, *block.Hash())
	}
	restore, err := v.applyContractAssetIndexView(assetView)
	if err != nil {
		return err
	}
	defer restore()
	return v.validateContractBlock(block, view)
}

func (v *CompositeContractBlockValidator) validateContractBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	if block == nil || len(block.Transactions()) == 0 {
		return contractBlockRuleError("missing block")
	}
	active, err := v.registeredActivity(block, view)
	if err != nil {
		return err
	}
	anyActive := false
	for _, yes := range active {
		anyActive = anyActive || yes
	}
	if !anyActive {
		return nil
	}
	modules := make([]contractframework.Module, 0, len(v.cfg.Modules))
	for _, registration := range v.cfg.Modules {
		module, err := v.blockModule(registration, block, view, active[registration.Descriptor.Type()])
		if err != nil {
			return err
		}
		modules = append(modules, module)
	}
	coordinator := contractframework.BlockCoordinator{
		Modules: modules, Prefix: contractValidationPrefixForParams(v.cfg.ChainParams),
	}
	validated, err := coordinator.ValidateBlock(contractframework.BlockValidationRequest{
		Txs: blockWorkAndResults(block), ParentView: contractNodeUTXOView{view: view},
	})
	if err != nil {
		return contractBlockRuleError("contract block: %v", err)
	}
	payload, found, err := contractcommon.FindCoinbaseStateRoot(block.Transactions()[0].MsgTx())
	if err != nil {
		return contractBlockRuleError("combined contract state root: %v", err)
	}
	if !found {
		return contractBlockRuleError("combined contract state root: missing state root commitment")
	}
	if payload.StateRoot != validated.CombinedRoot {
		return contractBlockRuleError("combined contract state root mismatch: committed=%x expected=%x",
			payload.StateRoot, validated.CombinedRoot)
	}
	// Cache candidate states only after every module Result and the combined
	// commitment have passed. State persistence happens in the chain DB tx.
	for _, registration := range v.cfg.Modules {
		exec := validated.Executions[registration.Descriptor.Type()]
		if !active[registration.Descriptor.Type()] && !exec.StateChanged {
			continue
		}
		if recorder, ok := registration.Validator.(ContractBlockStateRecorder); ok {
			if err := recorder.RecordContractBlockState(block, exec); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *CompositeContractBlockValidator) blockModule(reg RegisteredContractValidator,
	block *btcutil.Block, view *blockchain.UtxoViewpoint, active bool) (contractframework.Module, error) {

	if factory, ok := reg.Validator.(ContractBlockModuleFactory); ok {
		return factory.ContractBlockModule(block, view)
	}
	// A test/custom validator may provide only the existing validator interface.
	// It still runs inside the coordinator and supplies its verified state. All
	// built-in production validators implement ContractBlockModuleFactory.
	return contractframework.ModuleAdapter{
		ModuleDescriptor: reg.Descriptor,
		ExecuteWorkBlockFunc: func(req contractframework.WorkExecutionRequest) (contractframework.ExecutionResult, error) {
			if active {
				if reg.Validator == nil {
					return contractframework.ExecutionResult{}, fmt.Errorf("missing %s validator", reg.Descriptor.Name())
				}
				if err := reg.Validator.ValidateContractModuleBlock(block, view); err != nil {
					return contractframework.ExecutionResult{}, err
				}
				state, ok := moduleBlockPostState(reg.Validator, block.Hash())
				if !ok || state == nil {
					return contractframework.ExecutionResult{}, fmt.Errorf("missing %s post-state", reg.Descriptor.Name())
				}
				return contractframework.ExecutionResult{
					ModuleType: reg.Descriptor.Type(), StateRoot: state.Root(), PostState: state.Snapshot(), StateChanged: true,
				}, nil
			}
			state, found, err := moduleParentState(reg.Validator, block, view)
			if err != nil {
				return contractframework.ExecutionResult{}, err
			}
			exec := contractframework.ExecutionResult{ModuleType: reg.Descriptor.Type()}
			if found && state != nil {
				exec.StateRoot, exec.PostState = state.Root(), state.Snapshot()
			}
			return exec, nil
		},
		VerifyResultTxsFunc: func(req contractframework.ResultVerifyRequest, exec contractframework.ExecutionResult) error {
			return nil // The supplied custom validator verified its Result above.
		},
	}, nil
}

func (v *CompositeContractBlockValidator) ContractBlockPostState(module contractframework.ModuleType,
	hash *chainhash.Hash) (contractframework.EngineState, bool) {

	for _, registration := range v.cfg.Modules {
		if registration.Descriptor.Type() == module {
			return moduleBlockPostState(registration.Validator, hash)
		}
	}
	return nil, false
}

func (v *CompositeContractBlockValidator) ReleaseContractBlockPostState(module contractframework.ModuleType, hash *chainhash.Hash) {
	for _, registration := range v.cfg.Modules {
		if registration.Descriptor.Type() == module {
			releaseModuleBlockPostState(registration.Validator, hash)
			return
		}
	}
}

func releaseModuleBlockPostState(validator ContractModuleBlockValidator, hash *chainhash.Hash) {
	if releaser, ok := validator.(BlockStateReleaser); ok {
		releaser.ReleaseBlockPostState(hash)
	}
}

func moduleBlockPostState(validator ContractModuleBlockValidator, hash *chainhash.Hash) (contractframework.EngineState, bool) {
	provider, ok := validator.(BlockStateProvider)
	if !ok {
		return nil, false
	}
	return provider.BlockPostState(hash)
}

func moduleParentState(validator ContractModuleBlockValidator, block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (contractframework.EngineState, bool, error) {

	provider, ok := validator.(ParentStateProvider)
	if !ok {
		return nil, false, nil
	}
	return provider.ParentState(block, view)
}

func (v *CompositeContractBlockValidator) registeredActivity(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (map[contractframework.ModuleType]bool, error) {

	activity := make(map[contractframework.ModuleType]bool)
	if block == nil {
		return activity, nil
	}
	modules := make([]contractframework.Module, 0, len(v.cfg.Modules))
	for _, registration := range v.cfg.Modules {
		modules = append(modules, contractframework.ModuleAdapter{ModuleDescriptor: registration.Descriptor})
	}
	split, err := contractframework.SplitBlockContractTxs(contractframework.SplitRequest{
		Txs: blockWorkAndResults(block), Prefix: contractValidationPrefixForParams(v.cfg.ChainParams),
		Modules: modules, ParentView: contractNodeUTXOView{view: view},
	})
	if err != nil {
		return nil, contractBlockRuleError("contract block activity: %v", err)
	}
	for _, registration := range v.cfg.Modules {
		typ := registration.Descriptor.Type()
		activity[typ] = len(split.WorkTxs[typ]) != 0 || len(split.ResultTxs[typ]) != 0
		if !activity[typ] {
			if provider, ok := registration.Validator.(ContractBlockActivityProvider); ok {
				activity[typ], err = provider.HasContractBlockActivity(block, view)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return activity, nil
}

func (v *CompositeContractBlockValidator) blockActivity(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (contractBlockActivity, error) {

	activity, err := v.registeredActivity(block, view)
	return contractBlockActivity{
		Template: activity[contractframework.ModuleTemplate], EVM: activity[contractframework.ModuleEVM], Agent: activity[contractframework.ModuleAgent],
	}, err
}

func contractActivityModules() []contractframework.Module {
	return []contractframework.Module{
		contractframework.ModuleAdapter{ModuleDescriptor: templateModuleDescriptor()},
		contractframework.ModuleAdapter{ModuleDescriptor: evmModuleDescriptor()},
		contractframework.ModuleAdapter{ModuleDescriptor: agentModuleDescriptor()},
	}
}

func blockWorkAndResults(block *btcutil.Block) []*wire.MsgTx {
	if block == nil || len(block.Transactions()) == 0 {
		return nil
	}
	out := make([]*wire.MsgTx, 0, len(block.Transactions())-1)
	for _, tx := range block.Transactions()[1:] {
		out = append(out, tx.MsgTx())
	}
	return out
}

func splitBlockContractTxs(block *btcutil.Block, view *blockchain.UtxoViewpoint,
	params *chaincfg.Params, prefix string) (bool, contractframework.BlockContractSplit, error) {

	if block == nil || len(block.Transactions()) == 0 {
		return false, contractframework.BlockContractSplit{}, fmt.Errorf("missing block")
	}
	if prefix == "" {
		prefix = contractValidationPrefixForParams(params)
	}
	_, hasRoot, err := contractcommon.FindCoinbaseStateRoot(block.Transactions()[0].MsgTx())
	if err != nil {
		return false, contractframework.BlockContractSplit{}, err
	}
	split, err := contractframework.SplitBlockContractTxs(contractframework.SplitRequest{
		Txs: blockWorkAndResults(block), Prefix: prefix, Modules: contractActivityModules(),
		ParentView: contractNodeUTXOView{view: view},
	})
	return hasRoot, split, err
}

type contractActivityUTXOView = contractNodeUTXOView

func markContractActivity(activity *contractBlockActivity, typ byte) {
	activity.Template = activity.Template || typ == contractcommon.ContractTypeTemplate
	activity.EVM = activity.EVM || typ == contractcommon.ContractTypeEVM
	activity.Agent = activity.Agent || typ == contractcommon.ContractTypeAgent
}

func contractValidationPrefixForParams(params *chaincfg.Params) string {
	return contractPrefixForParams(params)
}

func contractBlockRuleError(format string, args ...interface{}) error {
	return blockchain.RuleError{ErrorCode: blockchain.ErrInvalidEVMBlock, Description: fmt.Sprintf(format, args...)}
}
