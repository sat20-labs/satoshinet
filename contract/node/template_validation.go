package node

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
)

type TemplateRuntimeFactory func(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*template.RuntimeStore, error)

type TemplateBlockExecutionConfig struct {
	ChainParams *chaincfg.Params

	ContractPrefix string
	GasConfig      template.GasConfig
	Registry       *template.Registry

	NewRuntime          TemplateRuntimeFactory
	ResolveInvoker      template.InvokerResolver
	ResolveOutput       template.ResultOutputResolver
	ResolveResultScript template.ResultRecipientScriptResolver
	ContractUTXOs       template.ContractUTXOProvider
	AssetPrecision      contractframework.AssetPrecisionResolver
	VerifyResult        func(resultTx *wire.MsgTx, expected []template.ResultPlan, status template.ResultStatus) error
}

type TemplateBlockExecutionValidator struct {
	cfg TemplateBlockExecutionConfig

	postStateMu sync.Mutex
	postStates  map[chainhash.Hash]*template.RuntimeStore
}

func NewTemplateBlockExecutionValidator(cfg TemplateBlockExecutionConfig) *TemplateBlockExecutionValidator {
	return &TemplateBlockExecutionValidator{
		cfg:        cfg,
		postStates: make(map[chainhash.Hash]*template.RuntimeStore),
	}
}

func (v *TemplateBlockExecutionValidator) ValidateTemplateBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	if block == nil {
		return templateBlockRuleError("missing block")
	}
	if view == nil {
		return templateBlockRuleError("missing UTXO view")
	}
	txs := block.Transactions()
	if len(txs) == 0 {
		return templateBlockRuleError("missing coinbase transaction")
	}

	prefix := v.contractPrefix()
	hasRoot, split, err := splitBlockContractTxs(block, view, v.cfg.ChainParams, prefix)
	if err != nil {
		return templateBlockRuleError("split template contract txs: %v", err)
	}
	blockTxs := split.WorkTxs[contractframework.ModuleTemplate]
	resultTxs := split.ResultTxs[contractframework.ModuleTemplate]
	hasExecution := len(blockTxs) != 0 || len(resultTxs) != 0
	if !hasRoot && !hasExecution {
		return nil
	}
	if hasExecution && !hasRoot {
		return templateBlockRuleError("missing template state root commitment")
	}

	store, err := v.runtime(block, view)
	if err != nil {
		return templateBlockRuleError("load template runtime: %v", err)
	}
	gasConfig := v.cfg.GasConfig
	gasConfig.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)
	contractUTXOs := contractframework.ContractUTXOProviderWithTxOutputs(
		v.cfg.ContractUTXOs, blockTxs, prefix, template.ContractTypeTemplate)

	executed, err := template.ExecuteBlock(template.BlockExecutionRequest{
		Txs:            blockTxs,
		Store:          store,
		Registry:       v.cfg.Registry,
		ContractPrefix: prefix,
		GasConfig:      gasConfig,
		ContractUTXOs:  contractUTXOs,
		AssetPrecision: v.cfg.AssetPrecision,
		BlockHeight:    int64(block.Height()),
		ResolveInvoker: template.LastInputPreviousOutputInvokerResolver(
			v.cfg.ChainParams, previousOutputScriptResolver(view)),
	})
	if err != nil {
		return templateBlockRuleError("validate template block: %v", err)
	}
	augmentStore := store.Clone()
	resultPlans, err := template.AugmentResultPlans(executed.ResultPlans, augmentStore, gasConfig, contractUTXOs, v.cfg.AssetPrecision)
	if err != nil {
		return templateBlockRuleError("template result plan: %v", err)
	}
	if len(resultPlans) == 0 {
		if len(resultTxs) != 0 {
			return templateBlockRuleError("unexpected template RESULT transaction")
		}
	} else if len(resultTxs) > 1 {
		return templateBlockRuleError("unexpected extra template RESULT transactions")
	}
	if err := v.verifyResults(resultTxs, resultPlans); err != nil {
		return templateBlockRuleError("template result: %v", err)
	}
	v.rememberPostState(block.Hash(), store.Clone())
	if store.StateRoot() != executed.StateRoot {
		return templateBlockRuleError("post-state root changed after validation")
	}
	return nil
}

func (v *TemplateBlockExecutionValidator) ValidateContractModuleBlock(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) error {

	return v.ValidateTemplateBlock(block, view)
}

func (v *TemplateBlockExecutionValidator) HasContractBlockActivity(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (bool, error) {

	if block == nil {
		return false, templateBlockRuleError("missing block")
	}
	if view == nil {
		return false, templateBlockRuleError("missing UTXO view")
	}
	store, err := v.runtime(block, view)
	if err != nil {
		return false, templateBlockRuleError("load template runtime: %v", err)
	}
	if store == nil {
		return false, nil
	}
	probe := store.Clone()
	before := probe.StateRoot()

	gasConfig := v.cfg.GasConfig
	gasConfig.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)
	if v.cfg.ContractUTXOs != nil {
		contractUTXOs := contractframework.ContractUTXOProviderWithTxOutputs(
			v.cfg.ContractUTXOs, nil, v.contractPrefix(), template.ContractTypeTemplate)
		if err := probe.ReconcileAssetCaches(contractUTXOs, gasConfig); err != nil {
			return false, templateBlockRuleError("template activity reconcile: %v", err)
		}
	}
	plans, err := probe.SettleBlockWithGasConfigAndPrecision(
		int64(block.Height()), gasConfig.Normalize(), v.cfg.AssetPrecision)
	if err != nil {
		return false, templateBlockRuleError("template activity settle: %v", err)
	}
	return probe.StateRoot() != before || len(plans) != 0, nil
}

func (v *TemplateBlockExecutionValidator) TemplateBlockPostState(hash *chainhash.Hash) (*template.RuntimeStore, bool) {
	if hash == nil {
		return nil, false
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	state, ok := v.postStates[*hash]
	if !ok {
		return nil, false
	}
	return state.Clone(), true
}

func (v *TemplateBlockExecutionValidator) BlockPostState(hash *chainhash.Hash) (contractframework.EngineState, bool) {
	state, ok := v.TemplateBlockPostState(hash)
	if !ok || state == nil {
		return nil, false
	}
	return contractframework.RootEngineState{StateRoot: state.StateRoot(), StateSnapshot: state}, true
}

func (v *TemplateBlockExecutionValidator) ParentState(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (contractframework.EngineState, bool, error) {

	store, err := v.runtime(block, view)
	if err != nil {
		return nil, false, err
	}
	if store == nil {
		return nil, false, nil
	}
	return contractframework.RootEngineState{StateRoot: store.StateRoot(), StateSnapshot: store.Clone()}, true, nil
}

func (v *TemplateBlockExecutionValidator) rememberPostState(hash *chainhash.Hash, state *template.RuntimeStore) {
	if hash == nil || state == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	v.postStates[*hash] = state.Clone()
}

func (v *TemplateBlockExecutionValidator) ReleaseBlockPostState(hash *chainhash.Hash) {
	if hash == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	delete(v.postStates, *hash)
}

func (v *TemplateBlockExecutionValidator) runtime(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*template.RuntimeStore, error) {
	if block != nil {
		prevHash := block.MsgBlock().Header.PrevBlock
		if state, ok := v.TemplateBlockPostState(&prevHash); ok {
			return state, nil
		}
	}
	if v.cfg.NewRuntime != nil {
		return v.cfg.NewRuntime(block, view)
	}
	return template.NewRuntimeStore(), nil
}

func (v *TemplateBlockExecutionValidator) verifyResults(resultTxs []*wire.MsgTx, plans []template.ResultPlan) error {
	if len(plans) == 0 {
		if len(resultTxs) != 0 {
			return fmt.Errorf("unexpected template RESULT transactions")
		}
		return nil
	}
	if len(resultTxs) != 1 {
		return fmt.Errorf("template result transaction count mismatch: got %d want 1", len(resultTxs))
	}
	if v.cfg.VerifyResult != nil {
		return v.cfg.VerifyResult(resultTxs[0], plans, template.ResultStatusSuccess)
	}
	if v.cfg.ResolveOutput == nil {
		return fmt.Errorf("missing template result output resolver")
	}
	if v.cfg.ResolveResultScript == nil {
		return fmt.Errorf("missing template result output script resolver")
	}
	return contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
		Label:         "template",
		ResultTx:      resultTxs[0],
		Status:        template.ResultStatusSuccess,
		Plans:         plans,
		GasAssetName:  contractGasAssetNameForParams(v.cfg.ChainParams),
		Resolve:       v.cfg.ResolveOutput,
		ResolveScript: v.cfg.ResolveResultScript,
		PlanCount:     templateResultPlanCount,
		CheckPayload:  true,
	})
}

func (v *TemplateBlockExecutionValidator) contractPrefix() string {
	if v.cfg.ContractPrefix != "" {
		return v.cfg.ContractPrefix
	}
	if v.cfg.ChainParams != nil {
		return template.ContractPrefixForNet(v.cfg.ChainParams.Net)
	}
	return template.TestnetContractPrefix
}

func templateBlockRuleError(format string, args ...interface{}) error {
	return blockchain.RuleError{
		ErrorCode:   blockchain.ErrInvalidEVMBlock,
		Description: fmt.Sprintf(format, args...),
	}
}
