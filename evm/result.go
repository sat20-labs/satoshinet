package evm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type ResultMinimalCommitment struct {
	Status       ResultStatus
	ResultCount  uint16
	ErrorDigest  [32]byte
	HasErrorInfo bool
}

type ResultBinding struct {
	InvokeCalls  []InvokeCallBinding
	DeployCount  int
	TriggerCount int
}

type InvokeCallBinding struct {
	CallID       string
	InvokeTxID   string
	FundingInput OutPoint
	Contract     ContractAddress
}

func DeriveInvokeCallID(invokeTxID string, vout uint32, contract ContractAddress) string {
	encoded := contract.MustEncode()
	buf := []byte(fmt.Sprintf("invoke:%s:%d:%s", invokeTxID, vout, encoded))
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func DeriveDeployCallID(deployTxID string, contract ContractAddress) string {
	encoded := contract.MustEncode()
	buf := []byte(fmt.Sprintf("deploy:%s:%s", deployTxID, encoded))
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func DeriveTriggerCallID(contract ContractAddress, triggerID string, height int64) string {
	encoded := contract.MustEncode()
	buf := []byte(fmt.Sprintf("trigger:%s:%s:%d", encoded, triggerID, height))
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func BindResultInvokes(resultInputs []OutPoint, fundingByOutPoint map[OutPoint]InvokeCallBinding) []InvokeCallBinding {
	bindings := make([]InvokeCallBinding, 0)
	for _, in := range resultInputs {
		if binding, ok := fundingByOutPoint[in]; ok {
			bindings = append(bindings, binding)
		}
	}
	return bindings
}

func BindResultTxInvokes(resultTx *wire.MsgTx, fundingByOutPoint map[OutPoint]InvokeCallBinding) ([]InvokeCallBinding, error) {
	if resultTx == nil {
		return nil, fmt.Errorf("missing result transaction")
	}
	inputs := make([]OutPoint, 0, len(resultTx.TxIn))
	for i, txIn := range resultTx.TxIn {
		if txIn == nil {
			return nil, fmt.Errorf("nil result input %d", i)
		}
		inputs = append(inputs, WireOutPointToEVM(txIn.PreviousOutPoint))
	}
	return BindResultInvokes(inputs, fundingByOutPoint), nil
}

func RequireResultTxInvokeBindings(resultTx *wire.MsgTx, fundingByOutPoint map[OutPoint]InvokeCallBinding, minBindings int) ([]InvokeCallBinding, error) {
	bindings, err := BindResultTxInvokes(resultTx, fundingByOutPoint)
	if err != nil {
		return nil, err
	}
	if len(bindings) < minBindings {
		return nil, fmt.Errorf("result transaction binds %d invokes, require at least %d", len(bindings), minBindings)
	}
	return bindings, nil
}
