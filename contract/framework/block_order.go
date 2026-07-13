package framework

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type TxOrderInfo struct {
	IsEVM      bool
	IsTemplate bool
	IsAgent    bool
	Type       contract.TxType
	GasLimit   int64
}

func ClassIsContract(class TxClass) bool {
	return class.Priority != 0 || class.TxType != 0 || class.GasLimit != 0 || class.ContractType != 0
}

type TxOrderSpec struct {
	ParseSpec        func() ParseSpec
	Resolver         func(prefix string) ContractScriptResolver
	ContractType     byte
	DefaultInvokeGas int64
	SetModuleFlag    func(*TxOrderInfo)
}

func ContractPrefixForNet(net wire.BitcoinNet, mainnetPrefix, testnetPrefix string) string {
	if net == wire.MainNet {
		return mainnetPrefix
	}
	return testnetPrefix
}

func ClassifyTxForBlockOrder(tx *wire.MsgTx, contractPrefix string, spec TxOrderSpec) (TxOrderInfo, error) {
	parsedType, payload, found, err := classifyOrderPayload(tx, spec.ParseSpec().ModuleName)
	if err != nil {
		return TxOrderInfo{}, err
	}
	if !found {
		outputs, err := contract.FindDefaultInvokeOutputs(tx, contractPrefix, spec.ContractType)
		if err != nil || len(outputs) == 0 {
			return TxOrderInfo{}, err
		}
		defaultGas := spec.DefaultInvokeGas
		if defaultGas == 0 {
			defaultGas = contract.DefaultInvokeGasForType(spec.ContractType)
		}
		info := TxOrderInfo{Type: contract.TxTypeInvoke, GasLimit: defaultGas}
		if spec.SetModuleFlag != nil {
			spec.SetModuleFlag(&info)
		}
		return info, nil
	}

	info := TxOrderInfo{Type: parsedType}
	if spec.SetModuleFlag != nil {
		spec.SetModuleFlag(&info)
	}
	parseSpec := spec.ParseSpec()
	switch parsedType {
	case contract.TxTypeDeploy:
		hasOutput, err := HasContractOutput(tx, spec.Resolver(contractPrefix))
		if err != nil || !hasOutput {
			return TxOrderInfo{}, err
		}
		deploy, err := parseSpec.DecodeDeploy(payload)
		if err != nil {
			return TxOrderInfo{}, err
		}
		info.GasLimit = deploy.GasLimit
	case contract.TxTypeInvoke:
		_, err := FindInvokeContractOutputs(tx, spec.Resolver(contractPrefix), parseSpec)
		if err != nil {
			return TxOrderInfo{}, err
		}
		invoke, err := parseSpec.DecodeInvoke(payload)
		if err != nil {
			return TxOrderInfo{}, err
		}
		info.GasLimit = invoke.GasLimit
	case contract.TxTypeResult, contract.TxTypeCoinbaseStateRoot:
	default:
		return TxOrderInfo{}, fmt.Errorf("unsupported %s tx type %d", parseSpec.ModuleName, parsedType)
	}
	return info, nil
}

func classifyOrderPayload(tx *wire.MsgTx, moduleName string) (contract.TxType, []byte, bool, error) {
	if tx == nil {
		return 0, nil, false, fmt.Errorf("missing transaction")
	}
	var txType contract.TxType
	payload := make([]byte, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return 0, nil, false, fmt.Errorf("nil output %d", i)
		}
		nextType, content, err := contract.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if txType == 0 {
			txType = nextType
		} else if txType != nextType {
			return 0, nil, false, fmt.Errorf("transaction contains mixed %s OP_RETURN output types", moduleName)
		} else if txType != contract.TxTypeDeploy && txType != contract.TxTypeInvoke {
			return 0, nil, false, fmt.Errorf("transaction contains multiple singleton %s OP_RETURN outputs", moduleName)
		}
		payload = append(payload, content...)
	}
	if txType == 0 {
		return 0, nil, false, nil
	}
	return txType, payload, true, nil
}
