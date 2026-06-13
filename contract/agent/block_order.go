package agent

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type TxOrderInfo struct {
	IsAgent  bool
	Type     TxType
	GasLimit uint64
}

func ContractPrefixForNet(net wire.BitcoinNet) string {
	if net == wire.MainNet {
		return MainnetContractPrefix
	}
	return TestnetContractPrefix
}

func ClassifyTxForBlockOrder(tx *wire.MsgTx, contractPrefix string) (TxOrderInfo, error) {
	parsedType, payload, found, err := classifyAgentPayload(tx)
	if err != nil {
		return TxOrderInfo{}, err
	}
	if !found {
		outputs, err := contractcommon.FindDefaultInvokeOutputs(tx, contractPrefix, ContractTypeAgent)
		if err != nil || len(outputs) == 0 {
			return TxOrderInfo{}, err
		}
		return TxOrderInfo{IsAgent: true, Type: TxTypeInvoke, GasLimit: DefaultGasConfig().InvokeBaseGas}, nil
	}
	info := TxOrderInfo{
		IsAgent: true,
		Type:    parsedType,
	}
	switch parsedType {
	case TxTypeDeploy:
		deploy, err := DecodeDeployPayload(payload)
		if err != nil {
			return TxOrderInfo{}, err
		}
		hasOutput, err := hasAgentContractOutput(tx, contractPrefix)
		if err != nil || !hasOutput {
			return TxOrderInfo{}, err
		}
		info.GasLimit = deploy.GasLimit
	case TxTypeInvoke:
		invoke, err := DecodeInvokePayload(payload)
		if err != nil {
			return TxOrderInfo{}, err
		}
		hasOutput, err := hasAgentContractOutput(tx, contractPrefix)
		if err != nil || !hasOutput {
			return TxOrderInfo{}, err
		}
		info.GasLimit = invoke.GasLimit
	case TxTypeResult, TxTypeCoinbaseStateRoot:
	default:
		return TxOrderInfo{}, fmt.Errorf("unsupported agent tx type %d", parsedType)
	}
	return info, nil
}

func classifyAgentPayload(tx *wire.MsgTx) (TxType, []byte, bool, error) {
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
			return 0, nil, false, fmt.Errorf("transaction contains mixed agent OP_RETURN output types")
		} else if txType != TxTypeDeploy && txType != TxTypeInvoke {
			return 0, nil, false, fmt.Errorf("transaction contains multiple singleton agent OP_RETURN outputs")
		}
		payload = append(payload, content...)
	}
	if txType == 0 {
		return 0, nil, false, nil
	}
	return txType, payload, true, nil
}

func hasAgentContractOutput(tx *wire.MsgTx, contractPrefix string) (bool, error) {
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
