package evm

import (
	"errors"
	"fmt"
	"math/big"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	scommon "github.com/sat20-labs/indexer/common"
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
	CallerAddress string
	TargetAddress string
	CallID        string
	Input         []byte
	Gas           int64
	Value         int64
	FundingOutput *contractframework.ContractOutput
	GasAssetName  string
	GasFeeReserve *scommon.Decimal
	Block         BlockContext
}

type CallResult struct {
	ReturnData         []byte
	GasUsed            int64
	GasLeft            int64
	Status             ResultStatus
	Err                error
	RetainedGasFunding *scommon.Decimal
}

type DeployRequest struct {
	CallerAddress string
	CallID        string
	InitCode      []byte
	Gas           int64
	Value         int64
	DeployNonce   uint64
	Block         BlockContext
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
	caller := EVMAddressFromAddressString(req.CallerAddress)
	gasLimit, err := contractframework.GasUnitsUint64(req.Gas)
	if err != nil {
		return DeployResult{Status: ResultStatusInvalid, Err: err}
	}
	if _, err := contractframework.SatoshiAmountUint64(req.Value); err != nil {
		return DeployResult{Status: ResultStatusInvalid, Err: err}
	}
	stateSnapshot := r.State.Snapshot()
	intentSnapshot := len(r.AssetIntents)
	r.State.SetNonce(GethAddress(caller), req.DeployNonce, 0)
	capturedIntents := make([]AssetIntent, 0)
	capturedTriggers := make([]Trigger, 0)
	config := r.configWithSatoshiNetTrace(req.CallID, &capturedIntents, &capturedTriggers, nil)
	evm := vm.NewEVM(r.blockContext(req.Block), r.State, r.ChainConfig, config)
	rules := r.ChainConfig.Rules(new(big.Int).SetUint64(req.Block.Number), false, req.Block.Time)
	precompiles := SatoshiNetPrecompiles(r.AssetBalances, nil, "", vm.ActivePrecompiledContracts(rules))
	r.State.Prepare(rules, GethAddress(caller), GethAddress(req.Block.Coinbase), nil, precompileAddresses(precompiles), nil)
	evm.SetPrecompiles(precompiles)
	evm.SetTxContext(vm.TxContext{
		Origin:   GethAddress(caller),
		GasPrice: uint256.NewInt(req.Block.FixedGasPrice),
	})
	_, contractAddr, left, err := evm.Create(
		GethAddress(caller),
		contractframework.CloneBytes(req.InitCode),
		gasLimit,
		uint256.NewInt(0),
	)
	contract := r.contractAddressFromGeth(contractAddr)
	if err == nil {
		for i := range capturedTriggers {
			capturedTriggers[i].Contract = contract
		}
		err = r.commitCapturedEffects(capturedIntents, capturedTriggers, r.AssetBalances)
	}
	gasLeft, gasLeftErr := contractframework.GasUnitsInt64(left)
	if gasLeftErr != nil && err == nil {
		err = gasLeftErr
	}
	if err != nil {
		r.State.RevertToSnapshot(stateSnapshot)
		r.AssetIntents = r.AssetIntents[:intentSnapshot]
	} else {
		r.State.Finalise(true)
		r.State.DiscardSnapshot(stateSnapshot)
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
	caller := EVMAddressFromAddressString(req.CallerAddress)
	target := EVMAddressFromAddressString(req.TargetAddress)
	gasLimit, err := contractframework.GasUnitsUint64(req.Gas)
	if err != nil {
		return CallResult{Status: ResultStatusInvalid, Err: err}
	}
	if _, err := contractframework.SatoshiAmountUint64(req.Value); err != nil {
		return CallResult{Status: ResultStatusInvalid, Err: err}
	}
	capturedIntents := make([]AssetIntent, 0)
	capturedTriggers := make([]Trigger, 0)
	funding := NewFundingAssetView(contractframework.OptionalContractOutputSlice(req.FundingOutput),
		req.GasAssetName, req.GasFeeReserve)
	stateSnapshot := r.State.Snapshot()
	intentSnapshot := len(r.AssetIntents)
	config := r.configWithSatoshiNetTrace(req.CallID, &capturedIntents, &capturedTriggers, funding)
	evm := vm.NewEVM(r.blockContext(req.Block), r.State, r.ChainConfig, config)
	balances := AssetBalanceReader(r.AssetBalances)
	if req.FundingOutput != nil {
		balances = NewFundingOverlayAssetBalanceView(r.AssetBalances, funding, target)
	}
	pendingBalances := pendingIntentAssetBalanceView{
		Base:    balances,
		Prior:   r.AssetIntents,
		Intents: &capturedIntents,
	}
	rules := r.ChainConfig.Rules(new(big.Int).SetUint64(req.Block.Number), false, req.Block.Time)
	precompiles := SatoshiNetPrecompiles(pendingBalances, funding, req.CallerAddress, vm.ActivePrecompiledContracts(rules))
	targetAddress := GethAddress(target)
	r.State.Prepare(rules, GethAddress(caller), GethAddress(req.Block.Coinbase), &targetAddress, precompileAddresses(precompiles), nil)
	evm.SetPrecompiles(precompiles)
	evm.SetTxContext(vm.TxContext{
		Origin:   GethAddress(caller),
		GasPrice: uint256.NewInt(req.Block.FixedGasPrice),
	})
	ret, left, err := evm.Call(
		GethAddress(caller),
		GethAddress(target),
		contractframework.CloneBytes(req.Input),
		gasLimit,
		uint256.NewInt(0),
	)
	if err == nil {
		for i := range capturedTriggers {
			if ContractAddressHash(capturedTriggers[i].Contract) == (EVMAddress{}) {
				capturedTriggers[i].Contract = r.contractAddressFromGeth(GethAddress(target))
			}
		}
		err = r.commitCapturedEffects(capturedIntents, capturedTriggers, balances)
	}
	gasLeft, gasLeftErr := contractframework.GasUnitsInt64(left)
	if gasLeftErr != nil && err == nil {
		err = gasLeftErr
	}
	if err != nil {
		r.State.RevertToSnapshot(stateSnapshot)
		r.AssetIntents = r.AssetIntents[:intentSnapshot]
		funding.RevertTo(nil)
	} else {
		r.State.Finalise(true)
		r.State.DiscardSnapshot(stateSnapshot)
	}
	var retainedGas *scommon.Decimal
	if err == nil && req.GasAssetName != "" {
		retainedGas = funding.ClaimedAssetAmount(req.GasAssetName)
	}
	return CallResult{
		ReturnData:         ret,
		GasUsed:            gasUsed(req.Gas, gasLeft),
		GasLeft:            gasLeft,
		Status:             ResultStatusFromError(err),
		Err:                err,
		RetainedGasFunding: retainedGas,
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

func (r *Runtime) commitCapturedEffects(capturedIntents []AssetIntent, capturedTriggers []Trigger,
	balances AssetBalanceReader) error {

	allIntents := append(contractframework.CloneAssetIntents(r.AssetIntents), capturedIntents...)
	if err := validateCapturedAssetIntents(allIntents, balances); err != nil {
		return err
	}
	seenTriggers := make(map[triggerKey]struct{}, len(capturedTriggers))
	for _, trigger := range capturedTriggers {
		if err := trigger.Validate(); err != nil {
			return err
		}
		if err := contractframework.ValidateTriggerGasLimit(trigger.GasLimit, r.GasConfig); err != nil {
			return err
		}
		key := newTriggerKey(trigger.Contract, trigger.ID)
		if _, exists := seenTriggers[key]; exists {
			return fmt.Errorf("duplicate captured trigger %s", trigger.ID)
		}
		if _, exists := r.State.Trigger(trigger.Contract, trigger.ID); exists {
			return fmt.Errorf("captured trigger %s already exists", trigger.ID)
		}
		seenTriggers[key] = struct{}{}
	}
	if uint64(len(r.AssetIntents))+uint64(len(capturedIntents)) > uint64(^uint32(0)) {
		return fmt.Errorf("too many EVM asset intents")
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

func validateCapturedAssetIntents(intents []AssetIntent, balances AssetBalanceReader) error {
	if balances == nil || len(intents) == 0 {
		return nil
	}
	totals := make(map[string]*scommon.Decimal)
	owners := make(map[string]EVMAddress)
	assets := make(map[string]string)
	for _, intent := range intents {
		if intent.AssetName == "" || intent.Amount == nil || intent.Amount.Sign() <= 0 {
			return fmt.Errorf("invalid captured asset intent")
		}
		owner := ContractAddressHash(intent.From)
		key := fmt.Sprintf("%x\x00%s", owner[:], intent.AssetName)
		owners[key] = owner
		assets[key] = intent.AssetName
		totals[key] = contractframework.DecimalAddAllowNil(totals[key], intent.Amount)
	}
	for key, total := range totals {
		available, err := balances.AssetBalance(owners[key], assets[key])
		if err != nil {
			return err
		}
		if available == nil || available.Cmp(total) < 0 {
			return fmt.Errorf("captured asset intents spend %s %s but only %s is available",
				total.String(), assets[key], contractframework.CloneDecimal(available).String())
		}
	}
	return nil
}

type pendingIntentAssetBalanceView struct {
	Base    AssetBalanceReader
	Prior   []AssetIntent
	Intents *[]AssetIntent
}

func (v pendingIntentAssetBalanceView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	available := zeroDecimal()
	if v.Base != nil {
		base, err := v.Base.AssetBalance(owner, assetName)
		if err != nil {
			return nil, err
		}
		if base != nil {
			available = base.Clone()
		}
	}
	for _, intent := range v.Prior {
		if ContractAddressHash(intent.From) != owner || intent.AssetName != assetName || intent.Amount == nil {
			continue
		}
		available = available.SubAlignPrecision(intent.Amount)
	}
	if v.Intents != nil {
		for _, intent := range *v.Intents {
			if ContractAddressHash(intent.From) != owner || intent.AssetName != assetName || intent.Amount == nil {
				continue
			}
			available = available.SubAlignPrecision(intent.Amount)
		}
	}
	if available.Sign() < 0 {
		return nil, fmt.Errorf("pending asset transfers exceed %s balance", assetName)
	}
	return available, nil
}

type assetTraceFrame struct {
	from            gethcommon.Address
	to              gethcommon.Address
	input           []byte
	intentLen       int
	triggerLen      int
	fundingSnapshot fundingAssetSnapshot
}

func (r *Runtime) configWithSatoshiNetTrace(callID string, capturedIntents *[]AssetIntent,
	capturedTriggers *[]Trigger, funding *FundingAssetView) vm.Config {
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
			from:            from,
			to:              to,
			input:           contractframework.CloneBytes(input),
			intentLen:       len(*capturedIntents),
			triggerLen:      len(*capturedTriggers),
			fundingSnapshot: funding.Snapshot(),
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
			*capturedIntents = (*capturedIntents)[:frame.intentLen]
			*capturedTriggers = (*capturedTriggers)[:frame.triggerLen]
			funding.RevertTo(frame.fundingSnapshot)
			return
		}
		switch frame.to {
		case AssetPrecompileAddress:
			transfers, decodeErr := DecodeAssetTransferIntents(frame.input)
			if decodeErr != nil {
				return
			}
			from := r.contractAddressFromGeth(frame.from)
			for _, transfer := range transfers {
				intent := AssetIntent{
					CallID:    callID,
					From:      from,
					To:        transfer.To,
					AssetName: transfer.AssetName,
					Amount:    cloneDecimal(transfer.Amount),
					ExtraData: contractframework.CloneBytes(transfer.ExtraData),
				}
				*capturedIntents = append(*capturedIntents, intent)
			}
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

func precompileAddresses(precompiles vm.PrecompiledContracts) []gethcommon.Address {
	out := make([]gethcommon.Address, 0, len(precompiles))
	for addr := range precompiles {
		out = append(out, addr)
	}
	return out
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
