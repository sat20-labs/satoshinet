package blockchain

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
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

func (v *AgentBlockExecutionValidator) ValidateAgentBlock(block *btcutil.Block, view *UtxoViewpoint) error {
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
	hasRoot, hasExecution, err := v.scanAgentWork(block, prefix)
	if err != nil {
		return err
	}
	if !hasRoot && !hasExecution {
		return nil
	}
	if hasExecution && !hasRoot {
		return agentBlockRuleError("missing agent state root commitment")
	}

	store, err := v.runtime(block, view)
	if err != nil {
		return agentBlockRuleError("load agent runtime: %v", err)
	}
	gasConfig := v.cfg.GasConfig
	gasConfig.GasAssetName = contractGasAssetNameForParams(v.cfg.ChainParams)
	blockTxs := make([]*wire.MsgTx, 0, len(txs)-1)
	resultTxs := make([]*wire.MsgTx, 0)
	for _, tx := range txs[1:] {
		info, err := agent.ClassifyTxForBlockOrder(tx.MsgTx(), prefix)
		if err != nil || !info.IsAgent {
			continue
		}
		if info.Type == agent.TxTypeResult {
			activity, err := contractResultActivity(tx.MsgTx(), view, v.cfg.ChainParams)
			if err != nil {
				return agentBlockRuleError("agent result activity: %v", err)
			}
			if !activity.Agent {
				continue
			}
			resultTxs = append(resultTxs, tx.MsgTx())
			continue
		}
		blockTxs = append(blockTxs, tx.MsgTx())
	}

	executed, err := agent.ExecuteBlock(agent.BlockExecutionRequest{
		Txs:            blockTxs,
		Store:          store,
		ContractPrefix: prefix,
		RuntimeConfig:  v.cfg.RuntimeConfig,
		GasConfig:      gasConfig,
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
	contractUTXOs := agent.ContractUTXOProviderWithTxOutputs(v.cfg.ContractUTXOs, blockTxs, prefix)
	resultPlans, err := agent.AugmentResultPlans(executed.ResultPlans, contractUTXOs)
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

func (v *AgentBlockExecutionValidator) rememberPostState(hash *chainhash.Hash, state *agent.RuntimeStore) {
	if hash == nil || state == nil {
		return
	}
	v.postStateMu.Lock()
	defer v.postStateMu.Unlock()
	v.postStates[*hash] = state.Clone()
}

func (v *AgentBlockExecutionValidator) scanAgentWork(block *btcutil.Block, prefix string) (bool, bool, error) {
	txs := block.Transactions()
	_, hasRoot, err := contractengine.FindCoinbaseStateRoot(txs[0].MsgTx())
	if err != nil {
		return false, false, agentBlockRuleError("invalid agent state root: %v", err)
	}
	hasExecution := false
	for _, tx := range txs[1:] {
		info, err := agent.ClassifyTxForBlockOrder(tx.MsgTx(), prefix)
		if err != nil {
			continue
		}
		if info.IsAgent && (info.Type == agent.TxTypeDeploy ||
			info.Type == agent.TxTypeInvoke || info.Type == agent.TxTypeResult) {
			hasExecution = true
		}
	}
	return hasRoot, hasExecution, nil
}

func (v *AgentBlockExecutionValidator) runtime(block *btcutil.Block, view *UtxoViewpoint) (*agent.RuntimeStore, error) {
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
	verifier := agent.CanonicalResultVerifier{ResolveOutput: v.cfg.ResolveResultOutput}
	return verifier.Verify(resultTxs[0], plans, agent.ResultStatusSuccess)
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
	return ruleError(ErrInvalidEVMBlock, fmt.Sprintf(format, args...))
}
