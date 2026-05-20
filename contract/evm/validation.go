package evm

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type ContractExistsFunc func(ContractAddress) bool

type DeployValidation struct {
	Payload DeployPayload
}

type InvokeValidation struct {
	Contract       ContractAddress
	FundingOutputs []ContractOutput
	MsgValue       uint64
	Payload        InvokePayload
}

type ResultValidation struct {
	Payload ResultPayload
	Inputs  []OutPoint
}

func ValidateDeployTxBasic(tx *wire.MsgTx, cfg GasConfig) (DeployValidation, error) {
	parsed, err := ParseTx(tx, nil)
	if err != nil {
		return DeployValidation{}, err
	}
	if parsed.Type != TxTypeDeploy || parsed.Deploy == nil {
		return DeployValidation{}, errors.New("not an EVM_DEPLOY transaction")
	}
	if parsed.Deploy.GasLimit == 0 {
		return DeployValidation{}, errors.New("deploy gas limit is zero")
	}
	if cfg.MaxGasPerInvoke > 0 && parsed.Deploy.GasLimit > cfg.MaxGasPerInvoke {
		return DeployValidation{}, errors.New("deploy gas limit exceeds maximum")
	}
	if len(parsed.Deploy.InitCode) == 0 {
		return DeployValidation{}, errors.New("deploy init code is empty")
	}
	return DeployValidation{Payload: *parsed.Deploy}, nil
}

func ValidateInvokeTxBasic(tx *wire.MsgTx, resolver ContractScriptResolver, exists ContractExistsFunc, cfg GasConfig) (InvokeValidation, error) {
	parsed, err := ParseTx(tx, resolver)
	if err != nil {
		return InvokeValidation{}, err
	}
	if parsed.Type != TxTypeInvoke || parsed.Invoke == nil {
		return InvokeValidation{}, errors.New("not an EVM_INVOKE transaction")
	}
	if parsed.Invoke.GasLimit == 0 {
		return InvokeValidation{}, errors.New("invoke gas limit is zero")
	}
	if cfg.MaxGasPerInvoke > 0 && parsed.Invoke.GasLimit > cfg.MaxGasPerInvoke {
		return InvokeValidation{}, errors.New("invoke gas limit exceeds maximum")
	}
	contract := parsed.ContractOutputs[0].Contract
	if exists != nil && !exists(contract) {
		return InvokeValidation{}, errors.New("invoke target contract does not exist")
	}
	msgValue, err := sumContractOutputValue(parsed.ContractOutputs)
	if err != nil {
		return InvokeValidation{}, err
	}
	return InvokeValidation{
		Contract:       contract,
		FundingOutputs: parsed.ContractOutputs,
		MsgValue:       msgValue,
		Payload:        *parsed.Invoke,
	}, nil
}

func ValidateResultTxBasic(tx *wire.MsgTx) (ResultValidation, error) {
	parsed, err := ParseTx(tx, nil)
	if err != nil {
		return ResultValidation{}, err
	}
	if parsed.Type != TxTypeResult || parsed.Result == nil {
		return ResultValidation{}, errors.New("not an EVM_RESULT transaction")
	}
	if parsed.Result.ResultCount == 0 {
		return ResultValidation{}, errors.New("result count is zero")
	}
	return ResultValidation{
		Payload: *parsed.Result,
		Inputs:  parsed.Inputs,
	}, nil
}

func sumContractOutputValue(outputs []ContractOutput) (uint64, error) {
	var total uint64
	for _, output := range outputs {
		if output.Value < 0 {
			return 0, fmt.Errorf("contract output %s has negative value", output.OutPoint)
		}
		next, overflow := addUint64(total, uint64(output.Value))
		if overflow {
			return 0, errors.New("contract output value overflows uint64")
		}
		total = next
	}
	return total, nil
}
