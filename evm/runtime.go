package evm

import (
	"errors"
	"math/big"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

type Runtime struct {
	State       *MemoryStateDB
	ChainConfig *params.ChainConfig
	Config      vm.Config

	ContractPrefix string
	AssetBalances  AssetBalanceReader
	AssetIntents   []AssetIntent
}

type BlockContext struct {
	Coinbase      EVMAddress
	Number        uint64
	Time          uint64
	GasLimit      uint64
	FixedGasPrice uint64
}

type CallRequest struct {
	Caller EVMAddress
	Target EVMAddress
	CallID string
	Input  []byte
	Gas    uint64
	Value  uint64
	Block  BlockContext
}

type CallResult struct {
	ReturnData []byte
	GasUsed    uint64
	GasLeft    uint64
	Status     ResultStatus
	Err        error
}

type DeployRequest struct {
	Caller      EVMAddress
	CallID      string
	InitCode    []byte
	Gas         uint64
	Value       uint64
	DeployNonce uint64
	Block       BlockContext
}

type DeployResult struct {
	Contract    ContractAddress
	RuntimeCode []byte
	GasUsed     uint64
	GasLeft     uint64
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
	cloned.AssetIntents = cloneAssetIntents(r.AssetIntents)
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
		cloneBytes(req.InitCode),
		req.Gas,
		uint256.NewInt(req.Value),
	)
	contract := r.contractAddressFromGeth(contractAddr)
	if err == nil {
		for i := range capturedIntents {
			capturedIntents[i].IntentIndex = uint32(len(r.AssetIntents) + i)
		}
		r.AssetIntents = append(r.AssetIntents, capturedIntents...)
		for _, trigger := range capturedTriggers {
			trigger.Contract = contract
			_ = r.State.RegisterTrigger(trigger)
		}
	}
	return DeployResult{
		Contract:    contract,
		RuntimeCode: r.State.GetCode(contractAddr),
		GasUsed:     gasUsed(req.Gas, left),
		GasLeft:     left,
		Status:      ResultStatusFromError(err),
		Err:         err,
	}
}

func (r *Runtime) Call(req CallRequest) CallResult {
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
		cloneBytes(req.Input),
		req.Gas,
		uint256.NewInt(req.Value),
	)
	if err == nil {
		for i := range capturedIntents {
			capturedIntents[i].IntentIndex = uint32(len(r.AssetIntents) + i)
		}
		r.AssetIntents = append(r.AssetIntents, capturedIntents...)
		for _, trigger := range capturedTriggers {
			if trigger.Contract.Hash == (EVMAddress{}) {
				trigger.Contract = r.contractAddressFromGeth(GethAddress(req.Target))
			}
			_ = r.State.RegisterTrigger(trigger)
		}
	}
	return CallResult{
		ReturnData: ret,
		GasUsed:    gasUsed(req.Gas, left),
		GasLeft:    left,
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

func gasUsed(initial, left uint64) uint64 {
	if left > initial {
		return 0
	}
	return initial - left
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
			input: cloneBytes(input),
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
	return ContractAddress{
		Prefix:  prefix,
		Version: AddressVersionV1,
		Type:    ContractTypeEVM,
		Hash:    hash,
	}
}

func (r *Runtime) blockContext(ctx BlockContext) vm.BlockContext {
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
		GasLimit:    ctx.GasLimit,
		BlockNumber: new(big.Int).SetUint64(ctx.Number),
		Time:        ctx.Time,
		Difficulty:  big.NewInt(0),
		BaseFee:     big.NewInt(0),
		BlobBaseFee: big.NewInt(0),
	}
}
