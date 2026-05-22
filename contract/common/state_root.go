package common

import "crypto/sha256"

func CombineStateRoots(templateRoot, evmRoot [32]byte) [32]byte {
	var zero [32]byte
	if templateRoot == zero {
		return evmRoot
	}
	if evmRoot == zero {
		return templateRoot
	}
	h := sha256.New()
	h.Write([]byte("satoshinet-contract-state-root-v1"))
	h.Write([]byte("template"))
	h.Write(templateRoot[:])
	h.Write([]byte("evm"))
	h.Write(evmRoot[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
