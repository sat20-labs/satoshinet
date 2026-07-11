package framework

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type SingleResultBlockRequest[T any] struct {
	ModuleName string
	Txs        []*wire.MsgTx
	Prefix     string

	Classify  func(*wire.MsgTx, string) (TxOrderInfo, error)
	IsModule  func(TxOrderInfo) bool
	IsResult  func(TxOrderInfo) bool
	ExecuteTx func(*wire.MsgTx) error
	Finalize  func() (T, error)
	Plans     func(T) []ResultPlan
	SetPlans  func(T, []ResultPlan) T
	Policy    SingleResultTxPolicy
}

type SingleResultTxPolicy struct {
	Label         string
	Status        contract.ResultStatus
	GasAssetName  string
	PlanCount     func(ResultPlan) int
	ResolveScript ResultRecipientScriptResolver
	ResolveOutput ResultOutputResolver
	Augment       func([]ResultPlan) ([]ResultPlan, error)
}

func (p SingleResultTxPolicy) AugmentPlans(plans []ResultPlan) ([]ResultPlan, error) {
	if p.Augment != nil {
		return p.Augment(plans)
	}
	return CloneResultPlans(plans), nil
}

func (p SingleResultTxPolicy) BuildTx(plans []ResultPlan) (*wire.MsgTx, error) {
	if ResultPlansHaveOutputs(plans) && p.ResolveScript == nil {
		return nil, fmt.Errorf("missing result output script resolver")
	}
	return BuildResultTx(ResultTxBuildRequest{
		Status:        p.Status,
		Plans:         plans,
		ResolveScript: p.ResolveScript,
	}, ResultTxBuildOptions{PlanCount: p.PlanCount})
}

func (p SingleResultTxPolicy) VerifyTx(tx *wire.MsgTx, plans []ResultPlan) error {
	if p.ResolveOutput == nil {
		return fmt.Errorf("missing result output resolver")
	}
	if p.ResolveScript == nil {
		return fmt.Errorf("missing result output script resolver")
	}
	return VerifyCanonicalResultTx(CanonicalResultVerifyRequest{
		Label:         p.Label,
		ResultTx:      tx,
		Status:        p.Status,
		Plans:         plans,
		GasAssetName:  p.GasAssetName,
		Resolve:       p.ResolveOutput,
		ResolveScript: p.ResolveScript,
		PlanCount:     p.PlanCount,
		CheckPayload:  true,
	})
}

type BlockResultBuildResult struct {
	ResultTxs []*wire.MsgTx
	Execution any
}

type ModuleBlockResultSpec struct {
	ModuleType ModuleType
	ParentRoot [32]byte
	Result     BlockResultBuildResult
	Records    func(any) []ExecutionRecord
	Pending    func(any) []ExecutionRecord
	StateRoot  func(any) [32]byte
}

func WrapModuleBlockResult(spec ModuleBlockResultSpec) (ResultBuildResult, ExecutionResult) {
	records := spec.Records(spec.Result.Execution)
	stateRoot := spec.StateRoot(spec.Result.Execution)
	resultRoot := ResultStateRoot(spec.ParentRoot, stateRoot, len(records), len(spec.Result.ResultTxs))
	exec := NewExecutionResult(
		spec.ModuleType,
		records,
		moduleBlockPending(spec, spec.Result.Execution),
		stateRoot,
		spec.Result.Execution,
	)
	return ResultBuildResult{
		ResultTxs: spec.Result.ResultTxs,
		StateRoot: resultRoot,
	}, exec
}

func moduleBlockPending(spec ModuleBlockResultSpec, exec any) []ExecutionRecord {
	if spec.Pending == nil {
		return nil
	}
	return spec.Pending(exec)
}

func BuildSingleResultTxBlock[T any](req SingleResultBlockRequest[T]) (BlockResultBuildResult, error) {
	for _, tx := range req.Txs {
		payloadType, foundPayload, payloadErr := contract.ClassifyTxPayloadType(tx)
		if payloadErr == nil && foundPayload && payloadType == contract.TxTypeResult {
			return BlockResultBuildResult{}, fmt.Errorf("%s input already contains RESULT", req.ModuleName)
		}
		info, err := req.Classify(tx, req.Prefix)
		if err != nil || !req.IsModule(info) {
			continue
		}
		if req.IsResult(info) {
			return BlockResultBuildResult{}, fmt.Errorf("%s input already contains RESULT", req.ModuleName)
		}
		if err := req.ExecuteTx(tx); err != nil {
			return BlockResultBuildResult{}, err
		}
	}
	execution, err := req.Finalize()
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	plans, err := req.Policy.AugmentPlans(req.Plans(execution))
	if err != nil {
		return BlockResultBuildResult{}, err
	}
	if req.SetPlans != nil {
		execution = req.SetPlans(execution, plans)
	}
	resultTxs := make([]*wire.MsgTx, 0, 1)
	if len(plans) != 0 {
		tx, err := req.Policy.BuildTx(plans)
		if err != nil {
			return BlockResultBuildResult{}, err
		}
		if err := req.Policy.VerifyTx(tx, plans); err != nil {
			return BlockResultBuildResult{}, err
		}
		resultTxs = append(resultTxs, tx)
	}
	return BlockResultBuildResult{
		ResultTxs: resultTxs,
		Execution: any(execution),
	}, nil
}
