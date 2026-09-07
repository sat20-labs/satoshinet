package evm

import (
	"errors"

	gethcommon "github.com/ethereum/go-ethereum/common"
)

var ErrCrossContractCallUnsupported = errors.New("cross-contract EVM calls are unsupported; invoke contracts through transactions")

// GetCode prevents geth from executing another contract's bytecode through
// CALL, STATICCALL, DELEGATECALL or CALLCODE. The trace frame is entered before
// geth resolves call code. Code inspection (e.g. EXTCODECOPY) is not a call.
func (s *nativeStateView) GetCode(addr gethcommon.Address) []byte {
	code := s.MemoryStateDB.GetCode(addr)
	if len(code) == 0 || s.calls == nil || len(s.calls.frames) < 2 {
		return code
	}
	if _, precompile := s.precompiles[addr]; precompile {
		return code
	}
	frames := s.calls.frames
	if addr == frames[len(frames)-1].codeAddress && addr != frames[0].codeAddress {
		// Returning no code prevents even read-only execution of the target.
		// The sticky error fails and reverts the whole top-level execution, so
		// Solidity cannot hide the violation by catching a low-level CALL result.
		s.err = ErrCrossContractCallUnsupported
		return nil
	}
	return code
}
