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
		// A genuine foreign-module transaction is a no-op for this classifier,
		// not a parsing error. Malformed invocations of this module still fail.
		hasOutput, err := HasContractOutput(tx, spec.Resolver(contractPrefix))
		if err != nil {
			return TxOrderInfo{}, err
		}
		if !hasOutput {
			hasForeign, err := HasContractOutput(tx, ContractScriptResolverForType(contractPrefix, 0))
			if err != nil {
				return TxOrderInfo{}, err
			}
			if hasForeign {
				return TxOrderInfo{}, nil
			}
			return TxOrderInfo{}, fmt.Errorf("%s INVOKE has no contract output", parseSpec.ModuleName)
		}
		if _, err := FindInvokeContractOutputs(tx, spec.Resolver(contractPrefix), parseSpec); err != nil {
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
	// Use the same strict envelope classification as admission and block
	// splitting, including malformed headers and singleton/mixed payloads.
	txType, found, err := contract.ClassifyTxPayloadType(tx)
	if err != nil || !found {
		return 0, nil, false, err
	}
	payload := make([]byte, 0)
	for _, txOut := range tx.TxOut {
		nextType, content, err := contract.ReadNullDataScript(txOut.PkScript)
		if err == nil && nextType == txType {
			payload = append(payload, content...)
		}
	}
	return txType, payload, true, nil
}
