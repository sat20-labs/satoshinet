package evm

import (
	"errors"
	"math/big"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

type Runtime struct {
	State       *MemoryStateDB
	ChainConfig *params.ChainConfig
	Config      vm.Config
	GasConfig   GasConfig

	ContractPrefix string
	AssetBalances  AssetBalanceReader
	AssetIntents   []AssetIntent
}

type BlockContext struct {
	Coinbase      EVMAddress
	Number        uint64
	Time          uint64
	GasLimit      int64
	FixedGasPrice uint64
}

type CallRequest struct {
	Caller EVMAddress
	Target EVMAddress
	CallID string
	Input  []byte
	Gas    int64
	Value  int64
	Block  BlockContext
}

type CallResult struct {
	ReturnData []byte
	GasUsed    int64
	GasLeft    int64
	Status     ResultStatus
	Err        error
}

type DeployRequest struct {
	Caller      EVMAddress
	CallID      string
	InitCode    []byte
	Gas         int64
	Value       int64
	DeployNonce uint64
	Block       BlockContext
}

type DeployResult struct {
	Contract    ContractAddress
	RuntimeCode []byte
	GasUsed     int64
	GasLeft     int64
	Status      ResultStatus
	Err         error
}

func NewRuntime(state *MemoryStateDB) *Runtime {
	if state == nil {
		state = NewMemoryStateDB()
	}
	return &Runtime{
		State:          state,
		ChainConfig:    params.AllEthashProtocolChanges,
		ContractPrefix: TestnetContractPrefix,
	}
}

func (r *Runtime) Clone() *Runtime {
	if r == nil {
		return NewRuntime(nil)
	}
	cloned := *r
	cloned.State = r.State.Clone()
	cloned.AssetIntents = contractframework.CloneAssetIntents(r.AssetIntents)
	return &cloned
}

func (r *Runtime) SetCode(addr EVMAddress, code []byte) {
	r.State.SetCode(GethAddress(addr), code, 0)
}

func (r *Runtime) AddBalance(addr EVMAddress, amount uint64) {
	r.State.AddBalance(GethAddress(addr), uint256.NewInt(amount), 0)
}

func (r *Runtime) DueTriggerCalls(block BlockContext) []TriggerCall {
	if r == nil || r.State == nil {
		return nil
	}
	return r.State.DueTriggerCalls(BlockEnvironment{
		Height: int64(block.Number),
	})
}

func (r *Runtime) Deploy(req DeployRequest) DeployResult {
	r.State.SetNonce(GethAddress(req.Caller), req.DeployNonce, 0)
	gasLimit, err := contractframework.GasUnitsUint64(req.Gas)
	if err != nil {
		return DeployResult{Status: ResultStatusInvalid, Err: err}
	}
	value, err := contractframework.SatoshiAmountUint64(req.Value)
	if err != nil {
		return DeployResult{Status: ResultStatusInvalid, Err: err}
	}
	capturedIntents := make([]AssetIntent, 0)
	capturedTriggers := make([]Trigger, 0)
	config := r.configWithSatoshiNetTrace(req.CallID, &capturedIntents, &capturedTriggers)
	evm := vm.NewEVM(r.blockContext(req.Block), r.State, r.ChainConfig, config)
	evm.SetPrecompiles(SatoshiNetPrecompiles(r.AssetBalances, vm.ActivePrecompiledContracts(r.ChainConfig.Rules(
		new(big.Int).SetUint64(req.Block.Number),
		false,
		req.Block.Time,
	))))
	evm.SetTxContext(vm.TxContext{
		Origin:   GethAddress(req.Caller),
		GasPrice: uint256.NewInt(req.Block.FixedGasPrice),
	})
	_, contractAddr, left, err := evm.Create(
		GethAddress(req.Caller),
		contractframework.CloneBytes(req.InitCode),
		gasLimit,
		uint256.NewInt(value),
	)
	contract := r.contractAddressFromGeth(contractAddr)
	if err == nil {
		for i := range capturedTriggers {
			capturedTriggers[i].Contract = contract
		}
		err = r.commitCapturedEffects(capturedIntents, capturedTriggers)
	}
	gasLeft, gasLeftErr := contractframework.GasUnitsInt64(left)
	if gasLeftErr != nil && err == nil {
		err = gasLeftErr
	}
	return DeployResult{
		Contract:    contract,
		RuntimeCode: r.State.GetCode(contractAddr),
		GasUsed:     gasUsed(req.Gas, gasLeft),
		GasLeft:     gasLeft,
		Status:      ResultStatusFromError(err),
		Err:         err,
	}
}

func (r *Runtime) Call(req CallRequest) CallResult {
	gasLimit, err := contractframework.GasUnitsUint64(req.Gas)
	if err != nil {
		return CallResult{Status: ResultStatusInvalid, Err: err}
	}
	value, err := contractframework.SatoshiAmountUint64(req.Value)
	if err != nil {
		return CallResult{Status: ResultStatusInvalid, Err: err}
	}
	capturedIntents := make([]AssetIntent, 0)
	capturedTriggers := make([]Trigger, 0)
	config := r.configWithSatoshiNetTrace(req.CallID, &capturedIntents, &capturedTriggers)
	evm := vm.NewEVM(r.blockContext(req.Block), r.State, r.ChainConfig, config)
	evm.SetPrecompiles(SatoshiNetPrecompiles(r.AssetBalances, vm.ActivePrecompiledContracts(r.ChainConfig.Rules(
		new(big.Int).SetUint64(req.Block.Number),
		false,
		req.Block.Time,
	))))
	evm.SetTxContext(vm.TxContext{
		Origin:   GethAddress(req.Caller),
		GasPrice: uint256.NewInt(req.Block.FixedGasPrice),
	})
	ret, left, err := evm.Call(
		GethAddress(req.Caller),
		GethAddress(req.Target),
		contractframework.CloneBytes(req.Input),
		gasLimit,
		uint256.NewInt(value),
	)
	if err == nil {
		for i := range capturedTriggers {
			if ContractAddressHash(capturedTriggers[i].Contract) == (EVMAddress{}) {
				capturedTriggers[i].Contract = r.contractAddressFromGeth(GethAddress(req.Target))
			}
		}
		err = r.commitCapturedEffects(capturedIntents, capturedTriggers)
	}
	gasLeft, gasLeftErr := contractframework.GasUnitsInt64(left)
	if gasLeftErr != nil && err == nil {
		err = gasLeftErr
	}
	return CallResult{
		ReturnData: ret,
		GasUsed:    gasUsed(req.Gas, gasLeft),
		GasLeft:    gasLeft,
		Status:     ResultStatusFromError(err),
		Err:        err,
	}
}

func ResultStatusFromError(err error) ResultStatus {
	switch {
	case err == nil:
		return ResultStatusSuccess
	case errors.Is(err, vm.ErrExecutionReverted):
		return ResultStatusRevert
	case errors.Is(err, vm.ErrOutOfGas):
		return ResultStatusOutOfGas
	default:
		return ResultStatusInvalid
	}
}

func gasUsed(initial, left int64) int64 {
	if left > initial {
		return 0
	}
	return initial - left
}

func (r *Runtime) commitCapturedEffects(capturedIntents []AssetIntent, capturedTriggers []Trigger) error {
	for _, trigger := range capturedTriggers {
		if err := contractframework.ValidateTriggerGasLimit(trigger.GasLimit, r.GasConfig); err != nil {
			return err
		}
	}
	for i := range capturedIntents {
		capturedIntents[i].IntentIndex = uint32(len(r.AssetIntents) + i)
	}
	r.AssetIntents = append(r.AssetIntents, capturedIntents...)
	for _, trigger := range capturedTriggers {
		if err := r.State.RegisterTrigger(trigger); err != nil {
			return err
		}
	}
	return nil
}

type assetTraceFrame struct {
	from  gethcommon.Address
	to    gethcommon.Address
	input []byte
}

func (r *Runtime) configWithSatoshiNetTrace(callID string, capturedIntents *[]AssetIntent,
	capturedTriggers *[]Trigger) vm.Config {
	config := r.Config
	base := config.Tracer
	tracer := &tracing.Hooks{}
	if base != nil {
		*tracer = *base
	}
	baseOnEnter := tracer.OnEnter
	baseOnExit := tracer.OnExit
	frames := make([]assetTraceFrame, 0, 4)
	tracer.OnEnter = func(depth int, typ byte, from gethcommon.Address,
		to gethcommon.Address, input []byte, gas uint64, value *big.Int) {
		if baseOnEnter != nil {
			baseOnEnter(depth, typ, from, to, input, gas, value)
		}
		frames = append(frames, assetTraceFrame{
			from:  from,
			to:    to,
			input: contractframework.CloneBytes(input),
		})
	}
	tracer.OnExit = func(depth int, output []byte, gasUsed uint64, err error, reverted bool) {
		var frame assetTraceFrame
		if len(frames) > 0 {
			frame = frames[len(frames)-1]
			frames = frames[:len(frames)-1]
		}
		if baseOnExit != nil {
			baseOnExit(depth, output, gasUsed, err, reverted)
		}
		if err != nil || reverted {
			return
		}
		switch frame.to {
		case AssetPrecompileAddress:
			assetName, to, amount, extraData, decodeErr := DecodeTransferAssetCall(frame.input)
			if decodeErr != nil {
				return
			}
			intent := AssetIntent{
				CallID:    callID,
				From:      r.contractAddressFromGeth(frame.from),
				To:        to,
				AssetName: assetName,
				Amount:    cloneDecimal(amount),
				ExtraData: extraData,
			}
			*capturedIntents = append(*capturedIntents, intent)
		case TriggerPrecompileAddress:
			if r.State.GetCodeSize(frame.from) == 0 && !r.State.IsNewContract(frame.from) {
				return
			}
			trigger, decodeErr := DecodeTriggerRegistrationCall(frame.input)
			if decodeErr != nil {
				return
			}
			trigger.Contract = r.contractAddressFromGeth(frame.from)
			*capturedTriggers = append(*capturedTriggers, trigger)
		}
	}
	config.Tracer = tracer
	return config
}

func (r *Runtime) contractAddressFromGeth(addr gethcommon.Address) ContractAddress {
	var hash EVMAddress
	copy(hash[:], addr.Bytes())
	prefix := r.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	contract, err := NewContractAddress(prefix, AddressVersionV1, ContractTypeEVM, hash)
	if err != nil {
		return ContractAddress{}
	}
	return contract
}

func (r *Runtime) blockContext(ctx BlockContext) vm.BlockContext {
	gasLimit, err := contractframework.GasUnitsUint64(ctx.GasLimit)
	if err != nil {
		gasLimit = 0
	}
	return vm.BlockContext{
		CanTransfer: func(db vm.StateDB, addr gethcommon.Address, amount *uint256.Int) bool {
			return db.GetBalance(addr).Cmp(amount) >= 0
		},
		Transfer: func(db vm.StateDB, from, to gethcommon.Address, amount *uint256.Int, rules *params.Rules) {
			db.SubBalance(from, amount, 0)
			db.AddBalance(to, amount, 0)
		},
		GetHash:     func(uint64) gethcommon.Hash { return gethcommon.Hash{} },
		Coinbase:    GethAddress(ctx.Coinbase),
		GasLimit:    gasLimit,
		BlockNumber: new(big.Int).SetUint64(ctx.Number),
		Time:        ctx.Time,
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		BlobBaseFee: big.NewInt(0),
	}
}
