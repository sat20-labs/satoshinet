package template

import contractcommon "github.com/sat20-labs/satoshinet/contract/common"

func DeployNullDataScripts(payload DeployPayload) ([][]byte, error) {
	encoded, err := EncodeDeployPayload(payload)
	if err != nil {
		return nil, err
	}
	return contractcommon.NullDataScripts(TxTypeDeploy, encoded)
}

func InvokeNullDataScripts(payload InvokePayload) ([][]byte, error) {
	encoded, err := EncodeInvokePayload(payload)
	if err != nil {
		return nil, err
	}
	return contractcommon.NullDataScripts(TxTypeInvoke, encoded)
}

func DeployNullDataScript(payload DeployPayload) ([]byte, error) {
	encoded, err := EncodeDeployPayload(payload)
	if err != nil {
		return nil, err
	}
	return contractcommon.NullDataScript(TxTypeDeploy, encoded)
}

func InvokeNullDataScript(payload InvokePayload) ([]byte, error) {
	encoded, err := EncodeInvokePayload(payload)
	if err != nil {
		return nil, err
	}
	return contractcommon.NullDataScript(TxTypeInvoke, encoded)
}

func ReadDeployNullDataScript(script []byte) (DeployPayload, error) {
	txType, content, err := contractcommon.ReadNullDataScript(script)
	if err != nil {
		return DeployPayload{}, err
	}
	if txType != TxTypeDeploy {
		return DeployPayload{}, errUnexpectedTemplateTxType(txType)
	}
	return DecodeDeployPayload(content)
}

func ReadInvokeNullDataScript(script []byte) (InvokePayload, error) {
	txType, content, err := contractcommon.ReadNullDataScript(script)
	if err != nil {
		return InvokePayload{}, err
	}
	if txType != TxTypeInvoke {
		return InvokePayload{}, errUnexpectedTemplateTxType(txType)
	}
	return DecodeInvokePayload(content)
}
