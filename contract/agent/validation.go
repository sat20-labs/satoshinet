package agent

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type GasConfig struct {
	MaxGasPerInvoke uint64
}

type ContractExistsFunc func(ContractAddress) bool

type DeployValidation struct {
	Payload        DeployPayload
	Address        ContractAddress
	Runtime        *Runtime
	FundingOutputs []ContractOutput
}

type InvokeValidation struct {
	Contract       ContractAddress
	FundingOutputs []ContractOutput
	Payload        InvokePayload
}

func ValidateDeployTxBasic(tx *wire.MsgTx, prefix string, cfg RuntimeConfig, gasCfg GasConfig) (DeployValidation, error) {
	parsed, err := ParseTx(tx, nil)
	if err != nil {
		return DeployValidation{}, err
	}
	if parsed.Type != TxTypeDeploy || parsed.Deploy == nil {
		return DeployValidation{}, errors.New("not an agent DEPLOY transaction")
	}
	if parsed.Deploy.GasLimit == 0 {
		return DeployValidation{}, errors.New("deploy gas limit is zero")
	}
	if gasCfg.MaxGasPerInvoke > 0 && parsed.Deploy.GasLimit > gasCfg.MaxGasPerInvoke {
		return DeployValidation{}, errors.New("deploy gas limit exceeds maximum")
	}
	addr, _, err := DeriveContractAddress(
		prefix,
		parsed.Deploy.Subtype,
		parsed.Deploy.ContractContent,
		parsed.Deploy.Deployer,
		parsed.Deploy.Random,
	)
	if err != nil {
		return DeployValidation{}, err
	}
	runtime, err := NewRuntime(addr, *parsed.Deploy, cfg)
	if err != nil {
		return DeployValidation{}, err
	}
	fundingOutputs, err := FindContractOutputsForContract(tx, StandardContractScriptResolver(prefix), addr)
	if err != nil {
		return DeployValidation{}, err
	}
	return DeployValidation{
		Payload:        *parsed.Deploy,
		Address:        addr,
		Runtime:        runtime,
		FundingOutputs: fundingOutputs,
	}, nil
}

func ValidateInvokeTxBasic(tx *wire.MsgTx, resolver ContractScriptResolver, exists ContractExistsFunc, gasCfg GasConfig) (InvokeValidation, error) {
	parsed, err := ParseTx(tx, resolver)
	if err != nil {
		return InvokeValidation{}, err
	}
	return ValidateParsedInvokeTxBasic(parsed, exists, gasCfg)
}

func ValidateParsedInvokeTxBasic(parsed ParsedTx, exists ContractExistsFunc, gasCfg GasConfig) (InvokeValidation, error) {
	if parsed.Type != TxTypeInvoke || parsed.Invoke == nil {
		return InvokeValidation{}, errors.New("not an agent INVOKE transaction")
	}
	if parsed.Invoke.GasLimit == 0 {
		return InvokeValidation{}, errors.New("invoke gas limit is zero")
	}
	if gasCfg.MaxGasPerInvoke > 0 && parsed.Invoke.GasLimit > gasCfg.MaxGasPerInvoke {
		return InvokeValidation{}, errors.New("invoke gas limit exceeds maximum")
	}
	contract := parsed.ContractOutputs[0].Contract
	if contract.ContractType() != ContractTypeAgent {
		return InvokeValidation{}, fmt.Errorf("invoke target is not an agent contract")
	}
	if exists != nil && !exists(contract) {
		return InvokeValidation{}, errors.New("invoke target contract does not exist")
	}
	return InvokeValidation{
		Contract:       contract,
		FundingOutputs: parsed.ContractOutputs,
		Payload:        *parsed.Invoke,
	}, nil
}
