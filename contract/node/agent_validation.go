package node

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type AgentBlockExecutionConfig struct {
	ChainParams *chaincfg.Params

	ContractPrefix string
	RuntimeConfig  agent.RuntimeConfig
	GasConfig      agent.GasConfig

	NewRuntime          AgentRuntimeFactory
	ResolveInvoker      agent.InvokerResolver
	ContractUTXOs       agent.ContractUTXOProvider
	AssetPrecision      contractframework.AssetPrecisionResolver
	ResolveRecipient    agent.ResultRecipientScriptResolver
	ResolveResultOutput agent.ResultOutputResolver
	SkipStateRootVerify bool
}

type AgentBlockExecutionValidator struct {
	cfg AgentBlockExecutionConfig

	postStateMu sync.Mutex
	postStates  map[chainhash.Hash]*agent.RuntimeStore
}

func NewAgentBlockExecutionValidator(cfg AgentBlockExecutionConfig) *AgentBlockExecutionValidator {
	return &AgentBlockExecutionValidator{
		cfg:        cfg,
		postStates: make(map[chainhash.Hash]*agent.RuntimeStore),
	}
}

func (v *AgentBlockExecutionValidator) ValidateAgentBlock(block *btcutil.Block, view *blockchain.UtxoViewpoint) error {
	if block == nil {
		return agentBlockRuleError("missing block")
	}
	if view == nil {
		return agentBlockRuleError("missing UTXO view")
	}
	txs := block.Transactions()
	if len(txs) == 0 {
		return agentBlockRuleError("missing coinbase transaction")
	}

	prefix := v.contractPrefix()
	hasRoot, split, err := splitBlockContractTxs(block, view, v.cfg.ChainParams, prefix)
	if err != nil {
		return agentBlockRuleError("split agent contract txs: %v", err)
	}
	blockTxs := split.WorkTxs[contractframework.ModuleAgent]
	resultTxs := split.ResultTxs[contractframework.ModuleAgent]
	hasExecution := len(blockTxs) != 0 || len(resultTxs) != 0

	store, err := v.runtime(block, view)
	if err != nil {
		return agentBlockRuleError("load agent runtime: %v", err)
	}
	hasDueTrigger := store.HasDuePredictionStatusAdvance(
		int64(block.Height()), block.MsgBlock().Header.Timestamp.Unix())
	if !hasRoot && !hasExecution && !hasDueTrigger {
		return nil
	}
	if (hasExecution || hasDueTrigger) && !hasRoot {
		return agentBlockRuleError("missing agent state root commitment")
	}
	gasConfig := v.cfg.GasConfig
	gasConfig.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)
	contractUTXOs := contractframework.ContractUTXOProviderWithTxOutputs(
		v.cfg.ContractUTXOs, blockTxs, prefix, agent.ContractTypeAgent)

	executed, err := agent.ExecuteBlock(agent.BlockExecutionRequest{
		Txs:            blockTxs,
		Store:          store,
		ContractPrefix: prefix,
		RuntimeConfig:  v.cfg.RuntimeConfig,
		GasConfig:      gasConfig,
		ContractUTXOs:  contractUTXOs,
		AssetPrecision: v.cfg.AssetPrecision,
		BlockHeight:    int64(block.Height()),
		BlockTime:      block.MsgBlock().Header.Timestamp.Unix(),
		ResolveInvoker: agent.LastInputPreviousOutputInvokerResolver(
			v.cfg.ChainParams, previousOutputScriptResolver(view)),
	})
	if err != nil {
		return agentBlockRuleError("validate agent block: %v", err)
	}
	if hasRoot && !v.cfg.SkipStateRootVerify {
		if err := contractengine.VerifyCoinbaseStateRoot(txs[0].MsgTx(), executed.StateRoot); err != nil {
			return agentBlockRuleError("agent state root: %v", err)
		}
	}
	resultPlans, err := agent.AugmentResultPlans(executed.ResultPlans, contractUTXOs, store, v.cfg.AssetPrecision,
		gasConfig.Normalize().GasAssetName, v.cfg.RuntimeConfig.BootstrapAddress)
	if err != nil {
		return agentBlockRuleError("agent result plan: %v", err)
	}
	if len(resultPlans) == 0 {
		if len(resultTxs) != 0 {
			return agentBlockRuleError("unexpected agent RESULT transaction")
		}
	} else if len(resultTxs) > 1 {
		return agentBlockRuleError("unexpected extra agent RESULT transactions")
	}
	if err := v.verifyResults(resultTxs, resultPlans); err != nil {
		return agentBlockRuleError("agent result: %v", err)
	}
	v.rememberPostState(block.Hash(), store.Clone())
	return nil
}

func (v *AgentBlockExecutionValidator) ValidateContractModuleBlock(block *btcutil.Block,
	view *blockchain.UtxoViewpoint) error {

	return v.ValidateAgentBlock(block, view)
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
	return store.HasDuePredictionStatusAdvance(
		int64(block.Height()), block.MsgBlock().Header.Timestamp.Unix()), nil
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

func (v *AgentBlockExecutionValidator) rememberPostState(hash *chainhash.Hash, state *agent.RuntimeStore) {
	if hash == nil || state == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	v.postStates[*hash] = state.Clone()
}

func (v *AgentBlockExecutionValidator) runtime(block *btcutil.Block, view *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error) {
	if v.cfg.NewRuntime != nil {
		return v.cfg.NewRuntime(block, view)
	}
	return agent.NewRuntimeStore(), nil
}

func (v *AgentBlockExecutionValidator) verifyResults(resultTxs []*wire.MsgTx, plans []agent.ResultPlan) error {
	if len(plans) == 0 {
		if len(resultTxs) != 0 {
			return fmt.Errorf("unexpected agent RESULT transactions")
		}
		return nil
	}
	if len(resultTxs) != 1 {
		return fmt.Errorf("agent result transaction count mismatch: got %d want 1", len(resultTxs))
	}
	return contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
		Label:        "agent",
		ResultTx:     resultTxs[0],
		Status:       agent.ResultStatusSuccess,
		Plans:        plans,
		GasAssetName: contractGasAssetNameForParams(v.cfg.ChainParams),
		Resolve:      v.cfg.ResolveResultOutput,
		CheckPayload: true,
	})
}

func (v *AgentBlockExecutionValidator) contractPrefix() string {
	if v.cfg.ContractPrefix != "" {
		return v.cfg.ContractPrefix
	}
	if v.cfg.ChainParams != nil {
		return agent.ContractPrefixForNet(v.cfg.ChainParams.Net)
	}
	return agent.TestnetContractPrefix
}

func agentBlockRuleError(format string, args ...interface{}) error {
	return blockchain.RuleError{
		ErrorCode:   blockchain.ErrInvalidEVMBlock,
		Description: fmt.Sprintf(format, args...),
	}
}
