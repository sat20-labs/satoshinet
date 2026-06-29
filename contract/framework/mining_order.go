package framework

import (
	"bytes"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

func CompareMiningOrder(left, right TxClass, leftHash, rightHash chainhash.Hash) (bool, bool) {
	leftContract := ClassIsContract(left)
	rightContract := ClassIsContract(right)
	if leftContract != rightContract {
		return !leftContract, true
	}
	if !leftContract {
		return false, false
	}
	if left.Priority != right.Priority {
		return left.Priority < right.Priority, true
	}
	if left.GasLimit != right.GasLimit {
		return left.GasLimit > right.GasLimit, true
	}
	if left.TxType != right.TxType {
		return left.TxType < right.TxType, true
	}
	return bytes.Compare(leftHash[:], rightHash[:]) < 0, true
}
