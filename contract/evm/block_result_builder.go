package evm

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type BlockResultBuildRequest struct {
	Txs                       []*wire.MsgTx
	Runtime                   *Runtime
	ContractPrefix            string
	GasConfig                 GasConfig
	Block                     BlockContext
	ResolveCaller             CallerResolver
	ResolveGasRefundRecipient GasRefundRecipientResolver
	ContractUTXOs             ContractUTXOProvider
	ResolveScript             ResultRecipientScriptResolver
	ResolveOutput             ResultOutputResolver
	ResolveTriggers           TriggerResolver
	Triggers                  []TriggerCall
}

type BlockResultBuildResult struct {
	ResultTxs []*wire.MsgTx
	Execution BlockExecutionResult
}

func BuildBlockResultTxs(req BlockResultBuildRequest) (BlockResultBuildResult, error) {
	runtime := req.Runtime
	if runtime == nil {
		runtime = NewRuntime(nil)
	}
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	runtime.ContractPrefix = prefix

	overlay := NewContractUTXOOverlay(prefix, req.ContractUTXOs)
	resolveOutput := req.ResolveOutput
	if resolveOutput == nil {
		resolveOutput = func(resultTx *wire.MsgTx) ([]ResultOutput, error) {
			return ResultOutputsFromTx(resultTx, prefix, nil)
		}
	}
	verifier := CanonicalResultVerifier{
		GasConfig:     req.GasConfig,
		UTXOs:         overlay.Provider,
		ResolveOutput: resolveOutput,
	}
	executor := NewBlockExecutor(BlockExecutionRequest{
		Runtime:                   runtime,
		ContractPrefix:            prefix,
		GasConfig:                 req.GasConfig,
		Block:                     req.Block,
		ResolveCaller:             req.ResolveCaller,
		ResolveGasRefundRecipient: req.ResolveGasRefundRecipient,
		VerifyResult:              verifier.Verify,
		ResolveTriggers:           req.ResolveTriggers,
		ContractUTXOs:             overlay.Provider,
	})

	for _, tx := range req.Txs {
		payloadType, foundPayload, payloadErr := contractcommon.ClassifyTxPayloadType(tx)
		if payloadErr == nil && foundPayload && payloadType == contractcommon.TxTypeResult {
			return BlockResultBuildResult{}, fmt.Errorf("EVM input already contains RESULT")
		}
		info, err := ClassifyTxForBlockOrder(tx, prefix)
		if err != nil || !info.IsEVM {
			continue
		}
		if info.Type == TxTypeResult {
			return BlockResultBuildResult{}, fmt.Errorf("EVM input already contains RESULT")
		}
		parsed, err := ParseTx(tx, StandardContractScriptResolver(prefix))
		if err != nil {
			continue
		}
		if err := executor.ExecuteParsedTx(tx, parsed); err != nil {
			return BlockResultBuildResult{}, err
		}
		if err := overlay.ApplyTx(tx, int64(req.Block.Number)); err != nil {
			return BlockResultBuildResult{}, err
		}
	}

	triggers := append([]TriggerCall(nil), req.Triggers...)
	triggers = append(triggers, executor.Runtime.DueTriggerCalls(executor.Block)...)
	if req.ResolveTriggers != nil {
		resolved, err := req.ResolveTriggers(TriggerResolutionContext{
			Block:          executor.Block,
			Runtime:        executor.Runtime,
			ContractPrefix: executor.ContractPrefix,
		})
		if err != nil {
			return BlockResultBuildResult{}, err
		}
		triggers = append(triggers, resolved...)
	}
	for _, trigger := range triggers {
		if err := executor.ExecuteTrigger(trigger); err != nil {
			return BlockResultBuildResult{}, err
		}
	}

	resultTxs := make([]*wire.MsgTx, 0)
	for {
		pending := executor.PendingRecords()
		if len(pending) == 0 {
			break
		}
		record := pending[0]
		resultTx, err := BuildCanonicalResultTx(CanonicalResultTxRequest{
			Status:        record.Status,
			Records:       []ExecutionRecord{record},
			GasConfig:     req.GasConfig,
			UTXOs:         overlay.Provider,
			ResolveScript: req.ResolveScript,
		})
		if err != nil {
			return BlockResultBuildResult{}, err
		}
		if err := executor.ExecuteTx(resultTx); err != nil {
			return BlockResultBuildResult{}, err
		}
		if err := overlay.ApplyTx(resultTx, int64(req.Block.Number)); err != nil {
			return BlockResultBuildResult{}, err
		}
		resultTxs = append(resultTxs, resultTx)
	}
	execution, err := executor.Finalize()
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	return BlockResultBuildResult{
		ResultTxs: resultTxs,
		Execution: execution,
	}, nil
}
