package common

import "crypto/sha256"

func CombineStateRoots(templateRoot, evmRoot, agentRoot [32]byte) [32]byte {
	h := sha256.New()
	h.Write(templateRoot[:])
	h.Write(evmRoot[:])
	h.Write(agentRoot[:])
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
