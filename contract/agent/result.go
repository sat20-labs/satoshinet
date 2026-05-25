package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func DeriveDeployCallID(deployTxID string, contract ContractAddress) string {
	encoded := contract.MustEncode()
	buf := []byte(fmt.Sprintf("agent-deploy:%s:%s", deployTxID, encoded))
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func DeriveInvokeCallID(invokeTxID string, vout uint32, contract ContractAddress) string {
	encoded := contract.MustEncode()
	buf := []byte(fmt.Sprintf("agent-invoke:%s:%d:%s", invokeTxID, vout, encoded))
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}
