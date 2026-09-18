package framework

import (
	"fmt"
	"math"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

// Engines supply work execution and an existing state snapshot. Assembly and
// canonical verification consume the plans from that execution, never a replay.
type SettlementModuleConfig struct {
	Descriptor      ModuleDescriptor
	ParentRoot      [32]byte
	Execute         func(WorkExecutionRequest) (BackendBlockExecutionResult, any, error)
	GasConfig       GasConfig
	ResolveScript   ResultRecipientScriptResolver
	ResolveOutput   ResultOutputResolver
	PlanCount       func(ResultPlan) int
	CountRecords    bool
	UseRecordStatus bool
}

func NewSettlementModule(cfg SettlementModuleConfig) (ModuleAdapter, error) {
	if cfg.Descriptor.Type() == 0 || cfg.Descriptor.Name() == "" || cfg.Execute == nil {
		return ModuleAdapter{}, fmt.Errorf("incomplete settlement module registration")
	}
	status := func(exec ExecutionResult) contract.ResultStatus {
		if cfg.UseRecordStatus {
			return AggregateResultStatus(exec.Records)
		}
		return contract.ResultStatusSuccess
	}
	count := func(exec ExecutionResult) (int, error) {
		if !cfg.CountRecords {
			return 0, nil
		}
		n := 0
		for _, record := range exec.Records {
			if record.RequiresResult {
				n++
			}
		}
		if n > math.MaxUint16 {
			return 0, fmt.Errorf("too many %s result executions", cfg.Descriptor.Name())
		}
		return n, nil
	}
	return ModuleAdapter{
		ModuleDescriptor: cfg.Descriptor,
		ExecuteWorkBlockFunc: func(req WorkExecutionRequest) (ExecutionResult, error) {
			result, snapshot, err := cfg.Execute(req)
			if err != nil {
				return ExecutionResult{}, err
			}
			exec := NewExecutionResult(cfg.Descriptor.Type(), result.Records, result.PendingRecords, result.StateRoot, snapshot)
			exec.ResultPlans = CloneResultPlans(result.ResultPlans)
			exec.StateChanged = result.StateRoot != cfg.ParentRoot || len(result.Records) != 0 || len(result.ResultPlans) != 0
			return exec, nil
		},
		BuildResultTxsFunc: func(_ ResultBuildRequest, exec ExecutionResult) (ResultBuildResult, error) {
			if exec.ModuleType != cfg.Descriptor.Type() {
				return ResultBuildResult{}, fmt.Errorf("settlement execution module mismatch")
			}
			out := ResultBuildResult{StateRoot: exec.StateRoot}
			if len(exec.ResultPlans) == 0 {
				return out, nil
			}
			n, err := count(exec)
			if err != nil {
				return ResultBuildResult{}, err
			}
			tx, err := BuildResultTx(ResultTxBuildRequest{
				Status: status(exec), ResultCount: uint16(n), Plans: CloneResultPlans(exec.ResultPlans), ResolveScript: cfg.ResolveScript,
			}, ResultTxBuildOptions{UseInputUTXOs: true, PlanCount: cfg.PlanCount})
			if err != nil {
				return ResultBuildResult{}, err
			}
			out.ResultTxs = []*wire.MsgTx{tx}
			return out, nil
		},
		VerifyResultTxsFunc: func(req ResultVerifyRequest, exec ExecutionResult) error {
			if exec.ModuleType != cfg.Descriptor.Type() {
				return fmt.Errorf("settlement execution module mismatch")
			}
			n, err := count(exec)
			if err != nil {
				return err
			}
			return VerifySingleResultTx(SingleResultTxVerifyRequest{
				Label: cfg.Descriptor.Name(), ResultTxs: req.ResultTxs, Expectations: exec.ResultPlans,
				Verify: func(tx *wire.MsgTx, plans []ResultPlan) error {
					return VerifyCanonicalResultTx(CanonicalResultVerifyRequest{
						Label: cfg.Descriptor.Name(), ResultTx: tx, Status: status(exec), Plans: plans,
						GasAssetName: cfg.GasConfig.Normalize().GasAssetName, Resolve: cfg.ResolveOutput, ResolveScript: cfg.ResolveScript,
						ResultCount: n, PlanCount: cfg.PlanCount, UseInputUTXO: true, CheckPayload: true,
					})
				},
			})
		},
	}, nil
}
