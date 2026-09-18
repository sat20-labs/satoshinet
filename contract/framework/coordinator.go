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
	modules, err := checkedModules(c.Modules)
	if err != nil {
		return BlockValidationResult{}, err
	}
	split, err := SplitBlockContractTxs(SplitRequest{
		Txs: req.Txs, Prefix: prefix, Modules: modules, ParentView: req.ParentView,
	})
	if err != nil {
		return BlockValidationResult{}, err
	}
	executions := make(map[ModuleType]ExecutionResult, len(modules))
	for _, module := range modules {
		exec, err := module.ExecuteWorkBlock(WorkExecutionRequest{Txs: split.WorkTxs[module.Type()], Prefix: prefix})
		if err != nil {
			return BlockValidationResult{}, fmt.Errorf("%s work execution: %w", module.Name(), err)
		}
		if exec.ModuleType != module.Type() {
			return BlockValidationResult{}, fmt.Errorf("%w: execution module mismatch", ErrAccountingInvariant)
		}
		if err := module.VerifyResultTxs(ResultVerifyRequest{ResultTxs: split.ResultTxs[module.Type()], Prefix: prefix}, exec); err != nil {
			return BlockValidationResult{}, fmt.Errorf("%s result verification: %w", module.Name(), err)
		}
		exec.StateRoot = module.StateRoot(exec)
		executions[module.Type()] = exec
	}
	root, err := settleExecutionRoots(executions)
	if err != nil {
		return BlockValidationResult{}, err
	}
	return BlockValidationResult{Executions: executions, CombinedRoot: root, ContractSplit: split}, nil
}

func (c *BlockCoordinator) BuildResults(req ResultCoordinatorBuildRequest) (ResultCoordinatorBuildResult, error) {
	prefix := c.contractPrefix()
	modules, err := checkedModules(c.Modules)
	if err != nil {
		return ResultCoordinatorBuildResult{}, err
	}
	split, err := SplitBlockContractTxs(SplitRequest{
		Txs: req.Txs, Prefix: prefix, Modules: modules, ParentView: req.ParentView,
	})
	if err != nil {
		return ResultCoordinatorBuildResult{}, err
	}
	for _, results := range split.ResultTxs {
		if len(results) != 0 {
			return ResultCoordinatorBuildResult{}, fmt.Errorf("%w: external RESULT transactions are not work", ErrCallAdmission)
		}
	}
	executions := make(map[ModuleType]ExecutionResult, len(modules))
	var resultTxs []*wire.MsgTx
	for _, module := range modules {
		work := ResultBuildRequest{Txs: split.WorkTxs[module.Type()], Prefix: prefix}
		var exec ExecutionResult
		var result ResultBuildResult
		if builder, ok := module.(BlockResultModule); ok {
			result, exec, err = builder.BuildBlockResults(work)
		} else {
			exec, err = module.ExecuteWorkBlock(WorkExecutionRequest{Txs: work.Txs, Prefix: prefix})
			if err == nil {
				result, err = module.BuildResultTxs(work, exec)
			}
		}
		if err != nil {
			return ResultCoordinatorBuildResult{}, fmt.Errorf("%s result build: %w", module.Name(), err)
		}
		if exec.ModuleType != module.Type() {
			return ResultCoordinatorBuildResult{}, fmt.Errorf("%w: execution module mismatch", ErrAccountingInvariant)
		}
		if err := module.VerifyResultTxs(ResultVerifyRequest{ResultTxs: result.ResultTxs, Prefix: prefix}, exec); err != nil {
			return ResultCoordinatorBuildResult{}, fmt.Errorf("%s built result verification: %w", module.Name(), err)
		}
		exec.StateRoot = module.StateRoot(exec)
		if result.StateRoot != ([32]byte{}) && result.StateRoot != exec.StateRoot {
			return ResultCoordinatorBuildResult{}, fmt.Errorf("%w: Result construction changed the execution root", ErrAccountingInvariant)
		}
		executions[module.Type()] = exec
		resultTxs = append(resultTxs, result.ResultTxs...)
	}
	root, err := settleExecutionRoots(executions)
	if err != nil {
		return ResultCoordinatorBuildResult{}, err
	}
	return ResultCoordinatorBuildResult{
		ResultTxs: resultTxs, Executions: executions, CombinedRoot: root, ContractSplit: split,
	}, nil
}

func settleExecutionRoots(executions map[ModuleType]ExecutionResult) ([32]byte, error) {
	if err := applyCrossModuleResultBalances(executions); err != nil {
		return [32]byte{}, err
	}
	state := NewStateSet()
	for _, typ := range sortedExecutionTypes(executions) {
		exec := executions[typ]
		state.SetEngine(typ, RootEngineState{StateRoot: exec.StateRoot, StateSnapshot: exec.PostState})
	}
	return state.CombinedRoot(), nil
}

func sortedExecutionTypes(executions map[ModuleType]ExecutionResult) []ModuleType {
	types := make([]ModuleType, 0, len(executions))
	for typ := range executions {
		types = append(types, typ)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	return types
}

func checkedModules(modules []Module) ([]Module, error) {
	ordered := orderedModules(modules)
	seen := make(map[ModuleType]bool, len(ordered))
	for _, module := range ordered {
		if module.Type() == 0 || module.Name() == "" || seen[module.Type()] {
			return nil, fmt.Errorf("invalid or duplicate contract module %d", module.Type())
		}
		seen[module.Type()] = true
	}
	return ordered, nil
}

func moduleParticipated(split BlockContractSplit, typ ModuleType) bool {
	return len(split.WorkTxs[typ]) != 0 || len(split.ResultTxs[typ]) != 0
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
