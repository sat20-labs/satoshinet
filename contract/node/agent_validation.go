package node

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/agent"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type AgentBlockExecutionConfig struct {
	ChainParams         *chaincfg.Params
	ContractPrefix      string
	RuntimeConfig       agent.RuntimeConfig
	GasConfig           agent.GasConfig
	NewRuntime          AgentRuntimeFactory
	ResolveInvoker      agent.InvokerResolver
	ContractUTXOs       agent.ContractUTXOProvider
	AssetPrecision      contractframework.AssetPrecisionResolver
	ResolveResultScript agent.ResultRecipientScriptResolver
	ResolveResultOutput agent.ResultOutputResolver
}

type AgentBlockExecutionValidator struct {
	cfg         AgentBlockExecutionConfig
	postStateMu sync.Mutex
	postStates  map[chainhash.Hash]*agent.RuntimeStore
}

func NewAgentBlockExecutionValidator(cfg AgentBlockExecutionConfig) *AgentBlockExecutionValidator {
	return &AgentBlockExecutionValidator{cfg: cfg, postStates: make(map[chainhash.Hash]*agent.RuntimeStore)}
}

func (v *AgentBlockExecutionValidator) ValidateAgentBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	return validateStandaloneModule(block, view, v.cfg.ChainParams, v.contractPrefix(), contractframework.ModuleAgent, v, v)
}

func (v *AgentBlockExecutionValidator) ValidateContractModuleBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	return v.ValidateAgentBlock(block, view)
}

func (v *AgentBlockExecutionValidator) ContractBlockModule(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (contractframework.Module, error) {

	if block == nil {
		return nil, agentBlockRuleError("missing block")
	}
	parent, err := v.runtime(block, view)
	if err != nil {
		return nil, agentBlockRuleError("load agent runtime: %v", err)
	}
	if parent == nil {
		return nil, agentBlockRuleError("missing agent runtime")
	}
	gas := v.cfg.GasConfig
	gas.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)
	return contractframework.NewSettlementModule(contractframework.SettlementModuleConfig{
		Descriptor: agentModuleDescriptor(), ParentRoot: parent.StateRoot(), GasConfig: gas,
		ResolveScript: v.cfg.ResolveResultScript, ResolveOutput: v.cfg.ResolveResultOutput,
		Execute: func(work contractframework.WorkExecutionRequest) (contractframework.BackendBlockExecutionResult, any, error) {
			// Execution clones mutable state and only replaces this wrapper on success.
			candidate := *parent
			exec, err := agent.ExecuteBlock(agent.BlockExecutionRequest{
				Txs: work.Txs, Store: &candidate, ContractPrefix: v.contractPrefix(),
				RuntimeConfig: v.cfg.RuntimeConfig, GasConfig: gas, ContractUTXOs: v.cfg.ContractUTXOs,
				AssetPrecision: v.cfg.AssetPrecision, BlockHeight: int64(block.Height()),
				BlockTime:      block.MsgBlock().Header.Timestamp.Unix(),
				ResolveInvoker: agent.LastInputPreviousOutputInvokerResolver(v.cfg.ChainParams, previousOutputScriptResolver(view)),
			})
			return exec, &candidate, err
		},
	})
}

func (v *AgentBlockExecutionValidator) RecordContractBlockState(block *btcutil.Block, exec contractframework.ExecutionResult) error {
	state, ok := exec.PostState.(*agent.RuntimeStore)
	if block == nil || !ok || state == nil || exec.ModuleType != contractframework.ModuleAgent {
		return agentBlockRuleError("invalid agent post-state snapshot")
	}
	if state.StateRoot() != exec.StateRoot {
		return agentBlockRuleError("agent post-state root changed after validation")
	}
	v.rememberPostState(block.Hash(), state)
	return nil
}

func (v *AgentBlockExecutionValidator) HasContractBlockActivity(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) (bool, error) {

	if block == nil {
		return false, agentBlockRuleError("missing block")
	}
	if view == nil {
		return false, agentBlockRuleError("missing UTXO view")
	}
	store, err := v.runtime(block, view)
	if err != nil {
		return false, agentBlockRuleError("load agent runtime: %v", err)
	}
	if store == nil {
		return false, agentBlockRuleError("missing agent runtime")
	}
	return store.HasDuePredictionStatusAdvance(int64(block.Height()), block.MsgBlock().Header.Timestamp.Unix()), nil
}

func (v *AgentBlockExecutionValidator) AgentBlockPostState(hash *chainhash.Hash) (*agent.RuntimeStore, bool) {
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

func (v *AgentBlockExecutionValidator) BlockPostState(hash *chainhash.Hash) (contractframework.EngineState, bool) {
	state, ok := v.AgentBlockPostState(hash)
	if !ok || state == nil {
		return nil, false
	}
	return contractframework.RootEngineState{StateRoot: state.StateRoot(), StateSnapshot: state}, true
}

func (v *AgentBlockExecutionValidator) ParentState(block *btcutil.Block,
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

func (v *AgentBlockExecutionValidator) rememberPostState(hash *chainhash.Hash, state *agent.RuntimeStore) {
	if hash == nil || state == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	v.postStates[*hash] = state.Clone()
}

func (v *AgentBlockExecutionValidator) ReleaseBlockPostState(hash *chainhash.Hash) {
	if hash == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	delete(v.postStates, *hash)
}

func (v *AgentBlockExecutionValidator) runtime(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error) {
	if block != nil {
		prev := block.MsgBlock().Header.PrevBlock
		if state, ok := v.AgentBlockPostState(&prev); ok {
			return state, nil
		}
	}
	if v.cfg.NewRuntime != nil {
		return v.cfg.NewRuntime(block, view)
	}
	return agent.NewRuntimeStore(), nil
}

func (v *AgentBlockExecutionValidator) verifyResults(txs []*wire.MsgTx, plans []agent.ResultPlan) error {
	if len(plans) == 0 {
		if len(txs) != 0 {
			return fmt.Errorf("unexpected agent RESULT transactions")
		}
		return nil
	}
	if len(txs) != 1 {
		return fmt.Errorf("agent result transaction count mismatch: got %d want 1", len(txs))
	}
	if v.cfg.ResolveResultOutput == nil || v.cfg.ResolveResultScript == nil {
		return fmt.Errorf("missing agent result output resolver or script resolver")
	}
	return contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
		Label: "agent", ResultTx: txs[0], Status: agent.ResultStatusSuccess, Plans: plans,
		GasAssetName: contractGasAssetNameForParams(v.cfg.ChainParams), Resolve: v.cfg.ResolveResultOutput,
		ResolveScript: v.cfg.ResolveResultScript, CheckPayload: true, UseInputUTXO: true,
	})
}

func (v *AgentBlockExecutionValidator) contractPrefix() string {
	if v.cfg.ContractPrefix != "" {
		return v.cfg.ContractPrefix
	}
	return contractPrefixForParams(v.cfg.ChainParams)
}

func agentBlockRuleError(format string, args ...interface{}) error {
	return contractBlockRuleError(format, args...)
}
