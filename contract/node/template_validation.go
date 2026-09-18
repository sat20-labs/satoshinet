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

type TemplateRuntimeFactory func(*btcutil.Block, *blockchain.UtxoViewpoint) (*template.RuntimeStore, error)

type TemplateBlockExecutionConfig struct {
	ChainParams         *chaincfg.Params
	ContractPrefix      string
	GasConfig           template.GasConfig
	Registry            *template.Registry
	NewRuntime          TemplateRuntimeFactory
	ResolveInvoker      template.InvokerResolver
	ResolveOutput       template.ResultOutputResolver
	ResolveResultScript template.ResultRecipientScriptResolver
	ContractUTXOs       template.ContractUTXOProvider
	AssetPrecision      contractframework.AssetPrecisionResolver
	VerifyResult        func(*wire.MsgTx, []template.ResultPlan, template.ResultStatus) error
}

type TemplateBlockExecutionValidator struct {
	cfg         TemplateBlockExecutionConfig
	postStateMu sync.Mutex
	postStates  map[chainhash.Hash]*template.RuntimeStore
}

func NewTemplateBlockExecutionValidator(cfg TemplateBlockExecutionConfig) *TemplateBlockExecutionValidator {
	return &TemplateBlockExecutionValidator{cfg: cfg, postStates: make(map[chainhash.Hash]*template.RuntimeStore)}
}

func (v *TemplateBlockExecutionValidator) ValidateTemplateBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	return validateStandaloneModule(block, view, v.cfg.ChainParams, v.contractPrefix(), contractframework.ModuleTemplate, v, v)
}

func (v *TemplateBlockExecutionValidator) ValidateContractModuleBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	return v.ValidateTemplateBlock(block, view)
}

func (v *TemplateBlockExecutionValidator) ContractBlockModule(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (contractframework.Module, error) {

	if block == nil {
		return nil, templateBlockRuleError("missing block")
	}
	parent, err := v.runtime(block, view)
	if err != nil {
		return nil, templateBlockRuleError("load template runtime: %v", err)
	}
	if parent == nil {
		return nil, templateBlockRuleError("missing template runtime")
	}
	gas := v.cfg.GasConfig
	gas.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)
	module, err := contractframework.NewSettlementModule(contractframework.SettlementModuleConfig{
		Descriptor: templateModuleDescriptor(), ParentRoot: parent.StateRoot(), GasConfig: gas,
		ResolveScript: v.cfg.ResolveResultScript, ResolveOutput: v.cfg.ResolveOutput, PlanCount: templateResultPlanCount,
		Execute: func(work contractframework.WorkExecutionRequest) (contractframework.BackendBlockExecutionResult, any, error) {
			// Execution clones mutable state and only replaces this wrapper on success.
			candidate := *parent
			exec, err := template.ExecuteBlock(template.BlockExecutionRequest{
				Txs: work.Txs, Store: &candidate, Registry: v.cfg.Registry, ContractPrefix: v.contractPrefix(),
				GasConfig: gas, ContractUTXOs: v.cfg.ContractUTXOs, AssetPrecision: v.cfg.AssetPrecision,
				BlockHeight:    int64(block.Height()),
				ResolveInvoker: template.LastInputPreviousOutputInvokerResolver(v.cfg.ChainParams, previousOutputScriptResolver(view)),
			})
			return exec, &candidate, err
		},
	})
	if err != nil {
		return nil, err
	}
	if v.cfg.VerifyResult != nil {
		// Existing trusted embedding/test hook. Production services use the
		// canonical verifier installed by NewSettlementModule.
		module.VerifyResultTxsFunc = func(req contractframework.ResultVerifyRequest, exec contractframework.ExecutionResult) error {
			return v.verifyResults(req.ResultTxs, exec.ResultPlans)
		}
	}
	return module, nil
}

func (v *TemplateBlockExecutionValidator) RecordContractBlockState(block *btcutil.Block, exec contractframework.ExecutionResult) error {
	state, ok := exec.PostState.(*template.RuntimeStore)
	if block == nil || !ok || state == nil || exec.ModuleType != contractframework.ModuleTemplate {
		return templateBlockRuleError("invalid template post-state snapshot")
	}
	if state.StateRoot() != exec.StateRoot {
		return templateBlockRuleError("template post-state root changed after validation")
	}
	v.rememberPostState(block.Hash(), state)
	return nil
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
	gas := v.cfg.GasConfig
	gas.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)
	// The activity probe uses business scheduling only. Physical quantities
	// are checked during actual settlement, not adopted into business caches.
	plans, err := probe.SettleBlockWithGasConfigAndPrecision(int64(block.Height()), gas.Normalize(), v.cfg.AssetPrecision)
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
		prev := block.MsgBlock().Header.PrevBlock
		if state, ok := v.TemplateBlockPostState(&prev); ok {
			return state, nil
		}
	}
	if v.cfg.NewRuntime != nil {
		return v.cfg.NewRuntime(block, view)
	}
	return template.NewRuntimeStore(), nil
}

func (v *TemplateBlockExecutionValidator) verifyResults(txs []*wire.MsgTx, plans []template.ResultPlan) error {
	if len(plans) == 0 {
		if len(txs) != 0 {
			return fmt.Errorf("unexpected template RESULT transactions")
		}
		return nil
	}
	if len(txs) != 1 {
		return fmt.Errorf("template result transaction count mismatch: got %d want 1", len(txs))
	}
	if v.cfg.VerifyResult != nil {
		return v.cfg.VerifyResult(txs[0], plans, template.ResultStatusSuccess)
	}
	if v.cfg.ResolveOutput == nil || v.cfg.ResolveResultScript == nil {
		return fmt.Errorf("missing template result output resolver or script resolver")
	}
	return contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
		Label: "template", ResultTx: txs[0], Status: template.ResultStatusSuccess, Plans: plans,
		GasAssetName: contractGasAssetNameForParams(v.cfg.ChainParams), Resolve: v.cfg.ResolveOutput,
		ResolveScript: v.cfg.ResolveResultScript, PlanCount: templateResultPlanCount, CheckPayload: true, UseInputUTXO: true,
	})
}

func (v *TemplateBlockExecutionValidator) contractPrefix() string {
	if v.cfg.ContractPrefix != "" {
		return v.cfg.ContractPrefix
	}
	return contractPrefixForParams(v.cfg.ChainParams)
}

func templateBlockRuleError(format string, args ...interface{}) error {
	return contractBlockRuleError(format, args...)
}
