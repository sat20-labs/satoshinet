package evm

import (
	"errors"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

var ErrNativeTransferUnsupported = errors.New("native EVM balance transfers are unsupported; use the asset precompile")

// Native balances are an execution-only view of spendable UTXOs and pending
// asset intents. They must never become a second persistent asset ledger.
type nativeStateView struct {
	*MemoryStateDB
	balances    AssetBalanceReader
	precompiles vm.PrecompiledContracts
	calls       *precompileCallContext
	err         error
}

func (s *nativeStateView) GetBalance(addr gethcommon.Address) *uint256.Int {
	if s.err != nil || s.balances == nil {
		return new(uint256.Int)
	}
	amount, err := s.balances.AssetBalance(EVMAddress(addr), SatoshiAssetName)
	if err != nil {
		s.err = err
		return new(uint256.Int)
	}
	if amount == nil {
		return new(uint256.Int)
	}
	integral := amount.NewPrecision(0)
	if integral.Cmp(amount) != 0 {
		s.err = errors.New("fractional native satoshi balance")
		return new(uint256.Int)
	}
	value, err := contractframework.DecimalToInt64(*integral)
	if err != nil {
		s.err = err
		return new(uint256.Int)
	}
	return uint256.NewInt(uint64(value))
}

func (s *nativeStateView) AddBalance(addr gethcommon.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	previous := *s.GetBalance(addr)
	if !amount.IsZero() {
		s.err = ErrNativeTransferUnsupported
	}
	return previous
}

func (s *nativeStateView) SubBalance(addr gethcommon.Address, amount *uint256.Int, reason tracing.BalanceChangeReason) uint256.Int {
	previous := *s.GetBalance(addr)
	if !amount.IsZero() {
		s.err = ErrNativeTransferUnsupported
	}
	return previous
}

func (s *nativeStateView) SelfDestruct(gethcommon.Address) {
	// Even a contract with zero plain sats may own other assets. Deleting its
	// code outside the normal close/Result path could strand those UTXOs.
	s.err = ErrNativeTransferUnsupported
}

func (s *nativeStateView) Empty(addr gethcommon.Address) bool {
	return s.MemoryStateDB.GetNonce(addr) == 0 && len(s.MemoryStateDB.GetCode(addr)) == 0 && s.GetBalance(addr).IsZero()
}

func (s *nativeStateView) Exist(addr gethcommon.Address) bool {
	if s.MemoryStateDB.ContractClosed(addr) {
		return false
	}
	if _, ok := s.precompiles[addr]; ok {
		return true
	}
	return s.MemoryStateDB.Exist(addr) || !s.GetBalance(addr).IsZero()
}

func (r *Runtime) nativeBlockContext(ctx BlockContext, calls *precompileCallContext, hasFunding bool) vm.BlockContext {
	block := r.blockContext(ctx)
	block.CanTransfer = func(_ vm.StateDB, _ gethcommon.Address, amount *uint256.Int) bool {
		// The root funding output is already in the target's UTXO balance. Nested
		// value transfers require a Result protocol extension and fail closed.
		return amount.IsZero() || (hasFunding && len(calls.frames) == 1)
	}
	block.Transfer = func(_ vm.StateDB, _, _ gethcommon.Address, _ *uint256.Int, _ *params.Rules) {}
	return block
}

func nativeFundingValue(value int64, funding *contractframework.ContractOutput) (uint64, error) {
	if funding != nil {
		return contractframework.SatoshiAmountUint64(funding.PhysicalValue())
	}
	if value != 0 {
		return 0, errors.New("EVM msg.value requires a funding output")
	}
	return 0, nil
}
