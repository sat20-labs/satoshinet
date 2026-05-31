package template

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type TxOrderInfo struct {
	IsTemplate bool
	Type       TxType
	GasLimit   uint64
}

func ContractPrefixForNet(net wire.BitcoinNet) string {
	if net == wire.MainNet {
		return MainnetContractPrefix
	}
	return TestnetContractPrefix
}

func ClassifyTxForBlockOrder(tx *wire.MsgTx, contractPrefix string) (TxOrderInfo, error) {
	parsedType, payload, found, err := classifyTemplatePayload(tx)
	if err != nil {
		return TxOrderInfo{}, err
	}
	if !found {
		outputs, err := contractcommon.FindDefaultInvokeOutputs(tx, contractPrefix, ContractTypeTemplate)
		if err != nil || len(outputs) == 0 {
			return TxOrderInfo{}, err
		}
		return TxOrderInfo{IsTemplate: true, Type: TxTypeInvoke, GasLimit: DefaultGasConfig().InvokeBaseGas}, nil
	}
	info := TxOrderInfo{
		IsTemplate: true,
		Type:       parsedType,
	}
	switch parsedType {
	case TxTypeDeploy:
		deploy, err := DecodeDeployPayload(payload)
		if err != nil {
			return TxOrderInfo{}, err
		}
		hasOutput, err := hasTemplateContractOutput(tx, contractPrefix)
		if err != nil || !hasOutput {
			return TxOrderInfo{}, err
		}
		info.GasLimit = deploy.GasLimit
	case TxTypeInvoke:
		invoke, err := DecodeInvokePayload(payload)
		if err != nil {
			return TxOrderInfo{}, err
		}
		hasOutput, err := hasTemplateContractOutput(tx, contractPrefix)
		if err != nil || !hasOutput {
			return TxOrderInfo{}, err
		}
		info.GasLimit = invoke.GasLimit
	case TxTypeResult, TxTypeCoinbaseStateRoot:
	default:
		return TxOrderInfo{}, fmt.Errorf("unsupported template tx type %d", parsedType)
	}
	return info, nil
}

func classifyTemplatePayload(tx *wire.MsgTx) (TxType, []byte, bool, error) {
	if tx == nil {
		return 0, nil, false, fmt.Errorf("missing transaction")
	}
	var txType TxType
	payload := make([]byte, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return 0, nil, false, fmt.Errorf("nil output %d", i)
		}
		nextType, content, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if txType == 0 {
			txType = nextType
		} else if txType != nextType {
			return 0, nil, false, fmt.Errorf("transaction contains mixed template OP_RETURN output types")
		} else if txType != TxTypeDeploy && txType != TxTypeInvoke {
			return 0, nil, false, fmt.Errorf("transaction contains multiple singleton template OP_RETURN outputs")
		}
		payload = append(payload, content...)
	}
	if txType == 0 {
		return 0, nil, false, nil
	}
	return txType, payload, true, nil
}

func hasTemplateContractOutput(tx *wire.MsgTx, contractPrefix string) (bool, error) {
	resolver := StandardContractScriptResolver(contractPrefix)
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		_, ok, err := resolver(txOut.PkScript)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}
