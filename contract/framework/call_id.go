package framework

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
)

func DeriveDeployCallID(kind, deployTxID string, contractAddr contract.ContractAddress) string {
	encoded := contractAddr.MustEncode()
	return hashCallID(fmt.Sprintf("%s:%s:%s", kind, deployTxID, encoded))
}

func DeriveInvokeCallID(kind, invokeTxID string, vout uint32, contractAddr contract.ContractAddress) string {
	encoded := contractAddr.MustEncode()
	return hashCallID(fmt.Sprintf("%s:%s:%d:%s", kind, invokeTxID, vout, encoded))
}

func DeriveTriggerCallID(kind string, contractAddr contract.ContractAddress, triggerID string, height int64) string {
	encoded := contractAddr.MustEncode()
	return hashCallID(fmt.Sprintf("%s:%s:%s:%d", kind, encoded, triggerID, height))
}

func hashCallID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
