package common

import (
	"fmt"

	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

const (
	ContentTypeContractDeploy    = sindexer.CONTENT_TYPE_CONTRACT_DEPLOY
	ContentTypeContractInvoke    = sindexer.CONTENT_TYPE_CONTRACT_INVOKE
	ContentTypeContractResult    = sindexer.CONTENT_TYPE_CONTRACT_RESULT
	ContentTypeContractStateRoot = sindexer.CONTENT_TYPE_CONTRACT_STATE_ROOT

	ContentTypeEVMDeploy    = ContentTypeContractDeploy
	ContentTypeEVMInvoke    = ContentTypeContractInvoke
	ContentTypeEVMResult    = ContentTypeContractResult
	ContentTypeEVMStateRoot = ContentTypeContractStateRoot

	MaxNullDataPayloadLen = sindexer.MAX_PAYLOAD_LEN
)

func NullDataScript(txType TxType, content []byte) ([]byte, error) {
	switch txType {
	case TxTypeDeploy:
		return sindexer.NullDataScript(ContentTypeContractDeploy, content)
	case TxTypeInvoke:
		return sindexer.NullDataScript(ContentTypeContractInvoke, content)
	case TxTypeResult:
		return sindexer.NullDataScript(ContentTypeContractResult, content)
	case TxTypeCoinbaseStateRoot:
		return sindexer.NullDataScript(ContentTypeContractStateRoot, content)
	default:
		return nil, fmt.Errorf("unsupported evm tx type %d", txType)
	}
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
	contentType, content, err := sindexer.ReadDataFromNullDataScript(script)
	if err != nil {
		return 0, nil, err
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
		return 0, nil, fmt.Errorf("not an evm content type %d", contentType)
	}
}

func ReadDeployNullDataScript(script []byte) (DeployPayload, error) {
	txType, content, err := ReadNullDataScript(script)
	if err != nil {
		return DeployPayload{}, err
	}
	if txType != TxTypeDeploy {
		return DeployPayload{}, fmt.Errorf("unexpected evm tx type %d", txType)
	}
	return DecodeDeployPayload(content)
}

func ReadInvokeNullDataScript(script []byte) (InvokePayload, error) {
	txType, content, err := ReadNullDataScript(script)
	if err != nil {
		return InvokePayload{}, err
	}
	if txType != TxTypeInvoke {
		return InvokePayload{}, fmt.Errorf("unexpected evm tx type %d", txType)
	}
	return DecodeInvokePayload(content)
}

func ReadResultNullDataScript(script []byte) (ResultPayload, error) {
	txType, content, err := ReadNullDataScript(script)
	if err != nil {
		return ResultPayload{}, err
	}
	if txType != TxTypeResult {
		return ResultPayload{}, fmt.Errorf("unexpected evm tx type %d", txType)
	}
	return DecodeResultPayload(content)
}

func ReadStateRootNullDataScript(script []byte) (StateRootPayload, error) {
	txType, content, err := ReadNullDataScript(script)
	if err != nil {
		return StateRootPayload{}, err
	}
	if txType != TxTypeCoinbaseStateRoot {
		return StateRootPayload{}, fmt.Errorf("unexpected evm tx type %d", txType)
	}
	return DecodeStateRootPayload(content)
}
