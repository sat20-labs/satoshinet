package node

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractapi "github.com/sat20-labs/satoshinet/contract"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractengine "github.com/sat20-labs/satoshinet/contract/engine"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type CompositeContractBlockValidatorConfig struct {
	ChainParams *chaincfg.Params

	TemplateValidator ContractModuleBlockValidator
	EVMValidator      ContractModuleBlockValidator
	AgentValidator    ContractModuleBlockValidator
}

type ContractModuleBlockValidator interface {
	ValidateContractModuleBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error
}

type BlockStateProvider interface {
	BlockPostState(hash *chainhash.Hash) (contractframework.RuntimeStore, bool)
}

type BlockStateReleaser interface {
	ReleaseBlockPostState(hash *chainhash.Hash)
}

type ParentStateProvider interface {
	ParentState(block *btcutil.Block, view *blockchain.UtxoViewpoint) (contractframework.RuntimeStore, bool, error)
}

type CompositeContractBlockValidator struct {
	cfg CompositeContractBlockValidatorConfig
}

type contractBlockActivity struct {
	Template bool
	EVM      bool
	Agent    bool
}

type ContractBlockActivityProvider interface {
	HasContractBlockActivity(block *btcutil.Block, view *blockchain.UtxoViewpoint) (bool, error)
}

func NewCompositeContractBlockValidator(cfg CompositeContractBlockValidatorConfig) *CompositeContractBlockValidator {
	return &CompositeContractBlockValidator{cfg: cfg}
}

func (v *CompositeContractBlockValidator) ValidateContractBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	activity, err := v.blockActivity(block, view)
	if err != nil {
		return err
	}

	if v.cfg.TemplateValidator != nil && activity.Template {
		if err := v.cfg.TemplateValidator.ValidateContractModuleBlock(block, view); err != nil {
			return err
		}
	}
	if v.cfg.EVMValidator != nil && activity.EVM {
		if err := v.cfg.EVMValidator.ValidateContractModuleBlock(block, view); err != nil {
			return err
		}
	}
	if v.cfg.AgentValidator != nil && activity.Agent {
		if err := v.cfg.AgentValidator.ValidateContractModuleBlock(block, view); err != nil {
			return err
		}
	}
	if activity.Template || activity.EVM || activity.Agent {
		return v.verifyCombinedStateRoot(block, view, activity.Template, activity.EVM, activity.Agent)
	}
	return nil
}

func (v *CompositeContractBlockValidator) ContractBlockPostState(
	module contractframework.ModuleType, hash *chainhash.Hash) (contractframework.EngineState, bool) {

	switch module {
	case contractframework.ModuleTemplate:
		return moduleBlockPostState(v.cfg.TemplateValidator, hash)
	case contractframework.ModuleEVM:
		return moduleBlockPostState(v.cfg.EVMValidator, hash)
	case contractframework.ModuleAgent:
		return moduleBlockPostState(v.cfg.AgentValidator, hash)
	default:
		return nil, false
	}
}

// ReleaseContractBlockPostState drops the transient validation snapshot after
// the state manager has durably stored it. Persisted block states remain
// available through the module state stores for reorg handling.
func (v *CompositeContractBlockValidator) ReleaseContractBlockPostState(
	module contractframework.ModuleType, hash *chainhash.Hash) {

	switch module {
	case contractframework.ModuleTemplate:
		releaseModuleBlockPostState(v.cfg.TemplateValidator, hash)
	case contractframework.ModuleEVM:
		releaseModuleBlockPostState(v.cfg.EVMValidator, hash)
	case contractframework.ModuleAgent:
		releaseModuleBlockPostState(v.cfg.AgentValidator, hash)
	}
}

func releaseModuleBlockPostState(validator ContractModuleBlockValidator, hash *chainhash.Hash) {
	releaser, ok := validator.(BlockStateReleaser)
	if ok {
		releaser.ReleaseBlockPostState(hash)
	}
}

func moduleBlockPostState(validator ContractModuleBlockValidator,
	hash *chainhash.Hash) (contractframework.EngineState, bool) {

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

func moduleCombinedRoot(validator ContractModuleBlockValidator, label string,
	block *btcutil.Block, view *blockchain.UtxoViewpoint, hasWork bool) ([32]byte, error) {

	if hasWork {
		postState, ok := moduleBlockPostState(validator, block.Hash())
		if !ok || postState == nil {
			return [32]byte{}, contractBlockRuleError("missing %s post-state", label)
		}
		return postState.Root(), nil
	}
	parentState, ok, err := moduleParentState(validator, block, view)
	if err != nil {
		return [32]byte{}, contractBlockRuleError("load %s parent-state: %v", label, err)
	}
	if !ok || parentState == nil {
		return [32]byte{}, nil
	}
	return parentState.Root(), nil
}

func (v *CompositeContractBlockValidator) verifyCombinedStateRoot(block *btcutil.Block,
	view *blockchain.UtxoViewpoint, hasTemplateWork, hasEVMWork, hasAgentWork bool) error {

	templateRoot, err := moduleCombinedRoot(v.cfg.TemplateValidator, "template", block, view, hasTemplateWork)
	if err != nil {
		return err
	}
	evmRoot, err := moduleCombinedRoot(v.cfg.EVMValidator, "EVM", block, view, hasEVMWork)
	if err != nil {
		return err
	}
	agentRoot, err := moduleCombinedRoot(v.cfg.AgentValidator, "agent", block, view, hasAgentWork)
	if err != nil {
		return err
	}
	expected := contractcommon.CombineStateRoots(templateRoot, evmRoot, agentRoot)
	payload, found, err := contractapi.FindCoinbaseStateRoot(block.Transactions()[0].MsgTx())
	if err != nil {
		return contractBlockRuleError("combined contract state root: %v", err)
	}
	if !found {
		return contractBlockRuleError("combined contract state root: missing state root commitment")
	}
	if payload.StateRoot != expected {
		return contractBlockRuleError(
			"combined contract state root mismatch: committed=%x expected=%x template=%x evm=%x agent=%x",
			payload.StateRoot, expected, templateRoot, evmRoot, agentRoot)
	}
	return nil
}

func (v *CompositeContractBlockValidator) blockActivity(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (contractBlockActivity, error) {

	var activity contractBlockActivity
	if block == nil {
		return activity, nil
	}
	_, split, err := splitBlockContractTxs(block, view, v.cfg.ChainParams,
		contractValidationPrefixForParams(v.cfg.ChainParams))
	if err != nil {
		return activity, contractBlockRuleError("contract block activity: %v", err)
	}
	for contractType, workTxs := range split.WorkTxs {
		if len(workTxs) != 0 {
			markContractActivity(&activity, byte(contractType))
		}
	}
	for contractType, resultTxs := range split.ResultTxs {
		if len(resultTxs) != 0 {
			markContractActivity(&activity, byte(contractType))
		}
	}
	if !activity.Template {
		if provider, ok := v.cfg.TemplateValidator.(ContractBlockActivityProvider); ok {
			hasActivity, err := provider.HasContractBlockActivity(block, view)
			if err != nil {
				return activity, err
			}
			activity.Template = hasActivity
		}
	}
	if !activity.EVM {
		if provider, ok := v.cfg.EVMValidator.(ContractBlockActivityProvider); ok {
			hasActivity, err := provider.HasContractBlockActivity(block, view)
			if err != nil {
				return activity, err
			}
			activity.EVM = hasActivity
		}
	}
	if !activity.Agent {
		if provider, ok := v.cfg.AgentValidator.(ContractBlockActivityProvider); ok {
			hasActivity, err := provider.HasContractBlockActivity(block, view)
			if err != nil {
				return activity, err
			}
			activity.Agent = hasActivity
		}
	}
	return activity, nil
}

type contractActivityModule struct {
	contractType contractframework.ModuleType
	priority     int
}

func contractActivityModules() []contractframework.Module {
	return []contractframework.Module{
		contractActivityModule{contractType: contractframework.ModuleTemplate, priority: 1},
		contractActivityModule{contractType: contractframework.ModuleEVM, priority: 2},
		contractActivityModule{contractType: contractframework.ModuleAgent, priority: 3},
	}
}

func (m contractActivityModule) Name() string {
	switch m.contractType {
	case contractframework.ModuleTemplate:
		return "template"
	case contractframework.ModuleEVM:
		return "evm"
	case contractframework.ModuleAgent:
		return "agent"
	default:
		return "unknown"
	}
}

func (m contractActivityModule) Type() contractframework.ModuleType { return m.contractType }
func (m contractActivityModule) Priority() int                      { return m.priority }

func (m contractActivityModule) ClassifyTx(tx *wire.MsgTx,
	prefix string) (contractframework.TxClass, bool, error) {

	class, found, err := contractengine.ClassifyTxForBlockOrderWithPrefix(tx, prefix)
	if err != nil || !found || class.ContractType != byte(m.contractType) {
		return contractframework.TxClass{}, false, err
	}
	return contractframework.TxClass{
		ContractType: m.contractType,
		TxType:       class.TxType,
		GasLimit:     class.GasLimit,
		Priority:     m.priority,
	}, true, nil
}

func (m contractActivityModule) ExecuteWorkBlock(req contractframework.WorkExecutionRequest) (contractframework.ExecutionResult, error) {
	return contractframework.ExecutionResult{ModuleType: m.contractType}, nil
}

func (m contractActivityModule) BuildResultTxs(req contractframework.ResultBuildRequest,
	exec contractframework.ExecutionResult) (contractframework.ResultBuildResult, error) {

	return contractframework.ResultBuildResult{}, nil
}

func (m contractActivityModule) VerifyResultTxs(req contractframework.ResultVerifyRequest,
	exec contractframework.ExecutionResult) error {

	return nil
}

func (m contractActivityModule) StateRoot(exec contractframework.ExecutionResult) [32]byte {
	return exec.StateRoot
}

func splitBlockContractTxs(block *btcutil.Block, view *blockchain.UtxoViewpoint,
	params *chaincfg.Params, prefix string) (bool, contractframework.BlockContractSplit, error) {

	if block == nil || len(block.Transactions()) == 0 {
		return false, contractframework.BlockContractSplit{}, fmt.Errorf("missing block")
	}
	if prefix == "" {
		prefix = contractValidationPrefixForParams(params)
	}
	txs := block.Transactions()
	_, hasRoot, err := contractapi.FindCoinbaseStateRoot(txs[0].MsgTx())
	if err != nil {
		return false, contractframework.BlockContractSplit{}, err
	}
	msgTxs := make([]*wire.MsgTx, 0, len(txs)-1)
	for _, tx := range txs[1:] {
		msgTxs = append(msgTxs, tx.MsgTx())
	}
	split, err := contractframework.SplitBlockContractTxs(contractframework.SplitRequest{
		Txs:        msgTxs,
		Prefix:     prefix,
		Modules:    contractActivityModules(),
		ParentView: contractActivityUTXOView{view: view},
	})
	if err != nil {
		return false, contractframework.BlockContractSplit{}, err
	}
	return hasRoot, split, nil
}

type contractActivityUTXOView struct {
	view *blockchain.UtxoViewpoint
}

func (v contractActivityUTXOView) LookupContractAddress(outpoint wire.OutPoint,
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

func markContractActivity(activity *contractBlockActivity, contractType byte) {
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		activity.Template = true
	case contractcommon.ContractTypeEVM:
		activity.EVM = true
	case contractcommon.ContractTypeAgent:
		activity.Agent = true
	}
}

func contractValidationPrefixForParams(params *chaincfg.Params) string {
	if params == nil {
		return contractcommon.TestnetContractPrefix
	}
	if params.Net == wire.MainNet {
		return contractcommon.MainnetContractPrefix
	}
	return contractcommon.TestnetContractPrefix
}

func contractBlockRuleError(format string, args ...interface{}) error {
	return blockchain.RuleError{
		ErrorCode:   blockchain.ErrInvalidEVMBlock,
		Description: fmt.Sprintf(format, args...),
	}
}
