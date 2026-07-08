package framework

import (
	"fmt"
	"sort"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type BlockCoordinator struct {
	Modules []Module
	Prefix  string
}

type BlockValidationRequest struct {
	Txs        []*wire.MsgTx
	ParentView UTXOView
}

type BlockValidationResult struct {
	Executions    map[ModuleType]ExecutionResult
	CombinedRoot  [32]byte
	ContractSplit BlockContractSplit
}

type ResultCoordinatorBuildRequest struct {
	Txs        []*wire.MsgTx
	ParentView UTXOView
}

type ResultCoordinatorBuildResult struct {
	ResultTxs     []*wire.MsgTx
	Executions    map[ModuleType]ExecutionResult
	CombinedRoot  [32]byte
	ContractSplit BlockContractSplit
}

func (c *BlockCoordinator) ValidateBlock(req BlockValidationRequest) (BlockValidationResult, error) {
	prefix := c.contractPrefix()
	modules := orderedModules(c.Modules)
	split, err := SplitBlockContractTxs(SplitRequest{
		Txs:        req.Txs,
		Prefix:     prefix,
		Modules:    modules,
		ParentView: req.ParentView,
	})
	if err != nil {
		return BlockValidationResult{}, err
	}
	executions := make(map[ModuleType]ExecutionResult, len(modules))
	state := NewStateSet()
	for _, module := range modules {
		exec, err := module.ExecuteWorkBlock(WorkExecutionRequest{
			Txs:    split.WorkTxs[module.Type()],
			Prefix: prefix,
		})
		if err != nil {
			return BlockValidationResult{}, fmt.Errorf("%s work execution: %w", module.Name(), err)
		}
		if err := module.VerifyResultTxs(ResultVerifyRequest{
			ResultTxs: split.ResultTxs[module.Type()],
			Prefix:    prefix,
		}, exec); err != nil {
			return BlockValidationResult{}, fmt.Errorf("%s result verification: %w", module.Name(), err)
		}
		executions[module.Type()] = exec
		state.SetEngine(module.Type(), RootEngineState{
			StateRoot:     module.StateRoot(exec),
			StateSnapshot: exec.PostState,
		})
	}
	return BlockValidationResult{
		Executions:    executions,
		CombinedRoot:  state.CombinedRoot(),
		ContractSplit: split,
	}, nil
}

func (c *BlockCoordinator) BuildResults(req ResultCoordinatorBuildRequest) (ResultCoordinatorBuildResult, error) {
	prefix := c.contractPrefix()
	modules := orderedModules(c.Modules)
	split, err := SplitBlockContractTxs(SplitRequest{
		Txs:        req.Txs,
		Prefix:     prefix,
		Modules:    modules,
		ParentView: req.ParentView,
	})
	if err != nil {
		return ResultCoordinatorBuildResult{}, err
	}
	executions := make(map[ModuleType]ExecutionResult, len(modules))
	state := NewStateSet()
	resultTxs := make([]*wire.MsgTx, 0)
	for _, module := range modules {
		if builder, ok := module.(BlockResultModule); ok {
			result, exec, err := builder.BuildBlockResults(ResultBuildRequest{
				Txs:    split.WorkTxs[module.Type()],
				Prefix: prefix,
			})
			if err != nil {
				return ResultCoordinatorBuildResult{}, fmt.Errorf("%s result build: %w", module.Name(), err)
			}
			resultTxs = append(resultTxs, result.ResultTxs...)
			executions[module.Type()] = exec
			state.SetEngine(module.Type(), RootEngineState{
				StateRoot:     module.StateRoot(exec),
				StateSnapshot: exec.PostState,
			})
			continue
		}
		exec, err := module.ExecuteWorkBlock(WorkExecutionRequest{
			Txs:    split.WorkTxs[module.Type()],
			Prefix: prefix,
		})
		if err != nil {
			return ResultCoordinatorBuildResult{}, fmt.Errorf("%s work execution: %w", module.Name(), err)
		}
		result, err := module.BuildResultTxs(ResultBuildRequest{
			Txs:    split.WorkTxs[module.Type()],
			Prefix: prefix,
		}, exec)
		if err != nil {
			return ResultCoordinatorBuildResult{}, fmt.Errorf("%s result build: %w", module.Name(), err)
		}
		resultTxs = append(resultTxs, result.ResultTxs...)
		executions[module.Type()] = exec
		if result.StateRoot != ([32]byte{}) {
			state.SetRoot(module.Type(), result.StateRoot)
		} else {
			state.SetEngine(module.Type(), RootEngineState{
				StateRoot:     module.StateRoot(exec),
				StateSnapshot: exec.PostState,
			})
		}
	}
	return ResultCoordinatorBuildResult{
		ResultTxs:     resultTxs,
		Executions:    executions,
		CombinedRoot:  state.CombinedRoot(),
		ContractSplit: split,
	}, nil
}

func moduleParticipated(split BlockContractSplit, moduleType ModuleType) bool {
	return len(split.WorkTxs[moduleType]) != 0 || len(split.ResultTxs[moduleType]) != 0
}

func (c *BlockCoordinator) contractPrefix() string {
	if c != nil && c.Prefix != "" {
		return c.Prefix
	}
	return contract.TestnetContractPrefix
}

func orderedModules(modules []Module) []Module {
	ordered := make([]Module, 0, len(modules))
	for _, module := range modules {
		if module != nil {
			ordered = append(ordered, module)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Priority() == ordered[j].Priority() {
			return ordered[i].Type() < ordered[j].Type()
		}
		return ordered[i].Priority() < ordered[j].Priority()
	})
	return ordered
}
