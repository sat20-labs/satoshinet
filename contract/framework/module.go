package framework

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type ModuleType byte

const (
	ModuleTemplate ModuleType = ModuleType(contract.ContractTypeTemplate)
	ModuleEVM      ModuleType = ModuleType(contract.ContractTypeEVM)
	ModuleAgent    ModuleType = ModuleType(contract.ContractTypeAgent)
)

type TxClass struct {
	ContractType  ModuleType
	TxType        contract.TxType
	GasLimit      int64
	Priority      int
	DefaultInvoke bool
}

func (c TxClass) IsWork() bool {
	return c.TxType == contract.TxTypeDeploy || c.TxType == contract.TxTypeInvoke
}

func (c TxClass) IsResult() bool {
	return c.TxType == contract.TxTypeResult
}

func NewTxClass(moduleType ModuleType, priority int, info TxOrderInfo) TxClass {
	return TxClass{
		ContractType: moduleType,
		TxType:       info.Type,
		GasLimit:     info.GasLimit,
		Priority:     priority,
	}
}

func ModulePrefix(reqPrefix, cfgPrefix, defaultPrefix string) string {
	if reqPrefix != "" {
		return reqPrefix
	}
	if cfgPrefix != "" {
		return cfgPrefix
	}
	return defaultPrefix
}

type Module interface {
	Name() string
	Type() ModuleType
	Priority() int

	ClassifyTx(tx *wire.MsgTx, prefix string) (TxClass, bool, error)

	ExecuteWorkBlock(req WorkExecutionRequest) (ExecutionResult, error)
	BuildResultTxs(req ResultBuildRequest, exec ExecutionResult) (ResultBuildResult, error)
	VerifyResultTxs(req ResultVerifyRequest, exec ExecutionResult) error

	StateRoot(exec ExecutionResult) [32]byte
}

type ModuleDescriptor struct {
	NameValue     string
	TypeValue     ModuleType
	PriorityValue int
	DefaultPrefix string
	ClassifyOrder func(*wire.MsgTx, string) (TxOrderInfo, error)
	MatchesOrder  func(TxOrderInfo) bool
}

type ModuleAdapter struct {
	ModuleDescriptor
	ExecuteWorkBlockFunc  func(WorkExecutionRequest) (ExecutionResult, error)
	BuildResultTxsFunc    func(ResultBuildRequest, ExecutionResult) (ResultBuildResult, error)
	BuildBlockResultsFunc func(ResultBuildRequest) (ResultBuildResult, ExecutionResult, error)
	VerifyResultTxsFunc   func(ResultVerifyRequest, ExecutionResult) error
}

func (d ModuleDescriptor) Name() string {
	return d.NameValue
}

func (d ModuleDescriptor) Type() ModuleType {
	return d.TypeValue
}

func (d ModuleDescriptor) Priority() int {
	return d.PriorityValue
}

func (d ModuleDescriptor) Prefix(reqPrefix, cfgPrefix string) string {
	return ModulePrefix(reqPrefix, cfgPrefix, d.DefaultPrefix)
}

func (d ModuleDescriptor) ClassifyTx(tx *wire.MsgTx, prefix string) (TxClass, bool, error) {
	if d.ClassifyOrder == nil {
		return TxClass{}, false, fmt.Errorf("missing %s tx classifier", d.NameValue)
	}
	info, err := d.ClassifyOrder(tx, prefix)
	if err != nil || (d.MatchesOrder != nil && !d.MatchesOrder(info)) {
		return TxClass{}, false, err
	}
	return NewTxClass(d.TypeValue, d.PriorityValue, info), true, nil
}

func (d ModuleDescriptor) StateRoot(exec ExecutionResult) [32]byte {
	return exec.StateRoot
}

func (m ModuleAdapter) ExecuteWorkBlock(req WorkExecutionRequest) (ExecutionResult, error) {
	if m.ExecuteWorkBlockFunc == nil {
		return ExecutionResult{}, fmt.Errorf("missing %s work execution adapter", m.Name())
	}
	return m.ExecuteWorkBlockFunc(req)
}

func (m ModuleAdapter) BuildResultTxs(req ResultBuildRequest, exec ExecutionResult) (ResultBuildResult, error) {
	if m.BuildResultTxsFunc == nil {
		return ResultBuildResult{}, fmt.Errorf("missing %s result build adapter", m.Name())
	}
	return m.BuildResultTxsFunc(req, exec)
}

func (m ModuleAdapter) BuildBlockResults(req ResultBuildRequest) (ResultBuildResult, ExecutionResult, error) {
	if m.BuildBlockResultsFunc == nil {
		exec, err := m.ExecuteWorkBlock(WorkExecutionRequest{
			Txs:    req.Txs,
			Prefix: req.Prefix,
		})
		if err != nil {
			return ResultBuildResult{}, ExecutionResult{}, err
		}
		result, err := m.BuildResultTxs(req, exec)
		if err != nil {
			return ResultBuildResult{}, ExecutionResult{}, err
		}
		return result, exec, nil
	}
	return m.BuildBlockResultsFunc(req)
}

func (m ModuleAdapter) VerifyResultTxs(req ResultVerifyRequest, exec ExecutionResult) error {
	if m.VerifyResultTxsFunc == nil {
		return fmt.Errorf("missing %s result verify adapter", m.Name())
	}
	return m.VerifyResultTxsFunc(req, exec)
}
