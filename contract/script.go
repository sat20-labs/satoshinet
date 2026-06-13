package contract

import (
	"bytes"
	"fmt"

	"github.com/sat20-labs/satoshinet/txscript"
)

const (
	sat20MagicNumber = txscript.OP_16

	ContentTypeContractDeploy    = txscript.OP_DATA_31
	ContentTypeContractInvoke    = txscript.OP_DATA_32
	ContentTypeContractResult    = txscript.OP_DATA_33
	ContentTypeContractStateRoot = txscript.OP_DATA_34

	MaxNullDataPayloadLen = txscript.MaxDataCarrierSize - 8
)

var contractScriptMagic = []byte("CT")

func ContractPkScript(contract ContractAddress) ([]byte, error) {
	payload := contract.ScriptAddress()
	return txscript.NewScriptBuilder().
		AddOp(txscript.OP_FALSE).
		AddOp(txscript.OP_IF).
		AddData(contractScriptMagic).
		AddData(payload).
		AddOp(txscript.OP_ENDIF).
		AddOp(txscript.OP_FALSE).
		Script()
}

func ParseContractPkScript(pkScript []byte, prefix string) (ContractAddress, bool, error) {
	if len(pkScript) < 11 {
		return ContractAddress{}, false, nil
	}
	if pkScript[0] != txscript.OP_FALSE ||
		pkScript[1] != txscript.OP_IF ||
		pkScript[2] != byte(len(contractScriptMagic)) ||
		!bytes.Equal(pkScript[3:5], contractScriptMagic) {
		return ContractAddress{}, false, nil
	}
	payloadLen := int(pkScript[5])
	payloadStart := 6
	payloadEnd := payloadStart + payloadLen
	if payloadLen < 3 || len(pkScript) != payloadEnd+2 ||
		pkScript[payloadEnd] != txscript.OP_ENDIF ||
		pkScript[payloadEnd+1] != txscript.OP_FALSE {
		return ContractAddress{}, false, nil
	}

	payload := pkScript[payloadStart:payloadEnd]
	contract, err := NewContractAddressFromHash(prefix, payload[0], payload[1], payload[2:])
	if err != nil {
		return ContractAddress{}, false, fmt.Errorf("invalid contract script payload: %w", err)
	}
	return contract, true, nil
}

func IsContractPkScript(pkScript []byte) bool {
	_, ok, _ := ParseContractPkScript(pkScript, TestnetContractPrefix)
	return ok
}

func NullDataScript(txType TxType, content []byte) ([]byte, error) {
	if len(content) > MaxNullDataPayloadLen {
		return nil, fmt.Errorf("data size %d is larger than max allowed size %d", len(content), MaxNullDataPayloadLen)
	}

	var contentType uint8
	switch txType {
	case TxTypeDeploy:
		contentType = ContentTypeContractDeploy
	case TxTypeInvoke:
		contentType = ContentTypeContractInvoke
	case TxTypeResult:
		contentType = ContentTypeContractResult
	case TxTypeCoinbaseStateRoot:
		contentType = ContentTypeContractStateRoot
	default:
		return nil, fmt.Errorf("unsupported contract tx type %d", txType)
	}

	return txscript.NewScriptBuilder().
		AddOp(txscript.OP_RETURN).
		AddOp(sat20MagicNumber).
		AddInt64(int64(contentType)).
		AddData(content).Script()
}

func NullDataScripts(txType TxType, content []byte) ([][]byte, error) {
	if len(content) <= MaxNullDataPayloadLen {
		script, err := NullDataScript(txType, content)
		if err != nil {
			return nil, err
		}
		return [][]byte{script}, nil
	}
	scripts := make([][]byte, 0, (len(content)+MaxNullDataPayloadLen-1)/MaxNullDataPayloadLen)
	for start := 0; start < len(content); start += MaxNullDataPayloadLen {
		end := start + MaxNullDataPayloadLen
		if end > len(content) {
			end = len(content)
		}
		script, err := NullDataScript(txType, content[start:end])
		if err != nil {
			return nil, err
		}
		scripts = append(scripts, script)
	}
	return scripts, nil
}

func DeployNullDataScripts(payload DeployPayload) ([][]byte, error) {
	return NullDataScripts(TxTypeDeploy, EncodeDeployPayload(payload))
}

func InvokeNullDataScripts(payload InvokePayload) ([][]byte, error) {
	return NullDataScripts(TxTypeInvoke, EncodeInvokePayload(payload))
}

func DeployNullDataScript(payload DeployPayload) ([]byte, error) {
	return NullDataScript(TxTypeDeploy, EncodeDeployPayload(payload))
}

func InvokeNullDataScript(payload InvokePayload) ([]byte, error) {
	return NullDataScript(TxTypeInvoke, EncodeInvokePayload(payload))
}

func ResultNullDataScript(payload ResultPayload) ([]byte, error) {
	return NullDataScript(TxTypeResult, EncodeResultPayload(payload))
}

func StateRootNullDataScript(payload StateRootPayload) ([]byte, error) {
	return NullDataScript(TxTypeCoinbaseStateRoot, EncodeStateRootPayload(payload))
}

func ReadNullDataScript(script []byte) (TxType, []byte, error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	if !tokenizer.Next() || tokenizer.Opcode() != txscript.OP_RETURN {
		return 0, nil, fmt.Errorf("script is not OP_RETURN")
	}
	if !tokenizer.Next() || tokenizer.Opcode() != sat20MagicNumber {
		return 0, nil, fmt.Errorf("script is not SAT20 script")
	}
	if !tokenizer.Next() || tokenizer.Err() != nil {
		return 0, nil, fmt.Errorf("script is missing contract content type")
	}
	contentType := uint8(tokenizer.ExtractInt64())
	if !tokenizer.Next() || tokenizer.Data() == nil {
		return 0, nil, fmt.Errorf("script is missing contract payload")
	}
	content := tokenizer.Data()
	if len(content) > MaxNullDataPayloadLen {
		return 0, nil, fmt.Errorf("data size %d is larger than max allowed size %d", len(content), MaxNullDataPayloadLen)
	}
	switch contentType {
	case ContentTypeContractDeploy:
		return TxTypeDeploy, content, nil
	case ContentTypeContractInvoke:
		return TxTypeInvoke, content, nil
	case ContentTypeContractResult:
		return TxTypeResult, content, nil
	case ContentTypeContractStateRoot:
		return TxTypeCoinbaseStateRoot, content, nil
	default:
		return 0, nil, fmt.Errorf("not a contract content type %d", contentType)
	}
}

func ReadDeployNullDataScript(script []byte) (DeployPayload, error) {
	txType, content, err := ReadNullDataScript(script)
	if err != nil {
		return DeployPayload{}, err
	}
	if txType != TxTypeDeploy {
		return DeployPayload{}, fmt.Errorf("unexpected contract tx type %d", txType)
	}
	return DecodeDeployPayload(content)
}

func ReadInvokeNullDataScript(script []byte) (InvokePayload, error) {
	txType, content, err := ReadNullDataScript(script)
	if err != nil {
		return InvokePayload{}, err
	}
	if txType != TxTypeInvoke {
		return InvokePayload{}, fmt.Errorf("unexpected contract tx type %d", txType)
	}
	return DecodeInvokePayload(content)
}

func ReadResultNullDataScript(script []byte) (ResultPayload, error) {
	txType, content, err := ReadNullDataScript(script)
	if err != nil {
		return ResultPayload{}, err
	}
	if txType != TxTypeResult {
		return ResultPayload{}, fmt.Errorf("unexpected contract tx type %d", txType)
	}
	return DecodeResultPayload(content)
}

func ReadStateRootNullDataScript(script []byte) (StateRootPayload, error) {
	txType, content, err := ReadNullDataScript(script)
	if err != nil {
		return StateRootPayload{}, err
	}
	if txType != TxTypeCoinbaseStateRoot {
		return StateRootPayload{}, fmt.Errorf("unexpected contract tx type %d", txType)
	}
	return DecodeStateRootPayload(content)
}
