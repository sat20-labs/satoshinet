package agent

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type BlockResultBuildRequest struct {
	Txs            []*wire.MsgTx
	Store          *RuntimeStore
	ContractPrefix string
	RuntimeConfig  RuntimeConfig
	GasConfig      GasConfig
	ContractUTXOs  ContractUTXOProvider
	BlockHeight    int64
	BlockTime      int64
	ResolveInvoker InvokerResolver
	ResolveScript  ResultRecipientScriptResolver
	ResolveOutput  ResultOutputResolver
}

type BlockResultBuildResult struct {
	ResultTxs []*wire.MsgTx
	Execution BlockExecutionResult
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
	executor := NewBlockExecutor(BlockExecutionRequest{
		Store:          store,
		ContractPrefix: prefix,
		RuntimeConfig:  req.RuntimeConfig,
		GasConfig:      req.GasConfig,
		BlockHeight:    req.BlockHeight,
		BlockTime:      req.BlockTime,
		ResolveInvoker: req.ResolveInvoker,
	})
	for _, tx := range req.Txs {
		info, err := ClassifyTxForBlockOrder(tx, prefix)
		if err != nil || !info.IsAgent {
			continue
		}
		if info.Type == TxTypeResult {
			return BlockResultBuildResult{}, fmt.Errorf("agent input already contains RESULT")
		}
		if err := executor.ExecuteTx(tx); err != nil {
			return BlockResultBuildResult{}, err
		}
	}
	execution, err := executor.Finalize()
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	contractUTXOs := ContractUTXOProviderWithTxOutputs(req.ContractUTXOs, req.Txs, prefix)
	resultPlans, err := AugmentResultPlans(execution.ResultPlans, contractUTXOs)
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	execution.ResultPlans = resultPlans
	resultTxs := make([]*wire.MsgTx, 0, 1)
	if len(resultPlans) != 0 {
		if resultPlansHaveOutputs(resultPlans) && req.ResolveScript == nil {
			return BlockResultBuildResult{}, fmt.Errorf("missing result output script resolver")
		}
		tx, err := BuildResultTx(ResultTxBuildRequest{
			Status:        ResultStatusSuccess,
			Plans:         resultPlans,
			ResolveScript: req.ResolveScript,
		})
		if err != nil {
			return BlockResultBuildResult{}, err
		}
		verifier := CanonicalResultVerifier{ResolveOutput: req.ResolveOutput}
		if err := verifier.Verify(tx, resultPlans, ResultStatusSuccess); err != nil {
			return BlockResultBuildResult{}, err
		}
		resultTxs = append(resultTxs, tx)
	}
	return BlockResultBuildResult{
		ResultTxs: resultTxs,
		Execution: execution,
	}, nil
}

func resultPlansHaveOutputs(plans []ResultPlan) bool {
	for _, plan := range plans {
		if len(plan.Outputs) != 0 {
			return true
		}
	}
	return false
}
