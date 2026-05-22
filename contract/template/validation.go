package template

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type GasConfig struct {
	GasAssetName    string
	MaxGasPerInvoke uint64
}

func DefaultGasConfig() GasConfig {
	return GasConfig{
		GasAssetName:    "ordx:f:gas",
		MaxGasPerInvoke: 30000000,
	}
}

func (c GasConfig) Validate() error {
	if c.GasAssetName == "" {
		return fmt.Errorf("missing gas asset name")
	}
	return nil
}

type ContractExistsFunc func(ContractAddress) bool

type DeployValidation struct {
	Payload        DeployPayload
	Address        ContractAddress
	Runtime        *ContractRuntime
	FundingOutputs []ContractOutput
}

type InvokeValidation struct {
	Contract       ContractAddress
	FundingOutputs []ContractOutput
	Payload        InvokePayload
}

func ValidateDeployTxBasic(tx *wire.MsgTx, prefix string, registry *Registry, cfg GasConfig) (DeployValidation, error) {
	parsed, err := ParseTx(tx, nil)
	if err != nil {
		return DeployValidation{}, err
	}
	if parsed.Type != TxTypeDeploy || parsed.Deploy == nil {
		return DeployValidation{}, errors.New("not a template DEPLOY transaction")
	}
	if parsed.Deploy.GasLimit == 0 {
		return DeployValidation{}, errors.New("deploy gas limit is zero")
	}
	if cfg.MaxGasPerInvoke > 0 && parsed.Deploy.GasLimit > cfg.MaxGasPerInvoke {
		return DeployValidation{}, errors.New("deploy gas limit exceeds maximum")
	}
	addr, _, err := DeriveContractAddress(
		prefix,
		parsed.Deploy.ContractContent,
		parsed.Deploy.Deployer,
		parsed.Deploy.Random,
	)
	if err != nil {
		return DeployValidation{}, err
	}
	runtime, err := NewRuntime(addr, *parsed.Deploy, registry)
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

func ValidateInvokeTxBasic(tx *wire.MsgTx, resolver ContractScriptResolver, exists ContractExistsFunc, cfg GasConfig) (InvokeValidation, error) {
	parsed, err := ParseTx(tx, resolver)
	if err != nil {
		return InvokeValidation{}, err
	}
	return ValidateParsedInvokeTxBasic(parsed, exists, cfg)
}

func ValidateParsedInvokeTxBasic(parsed ParsedTx, exists ContractExistsFunc, cfg GasConfig) (InvokeValidation, error) {
	if parsed.Type != TxTypeInvoke || parsed.Invoke == nil {
		return InvokeValidation{}, errors.New("not a template INVOKE transaction")
	}
	if parsed.Invoke.GasLimit == 0 {
		return InvokeValidation{}, errors.New("invoke gas limit is zero")
	}
	if cfg.MaxGasPerInvoke > 0 && parsed.Invoke.GasLimit > cfg.MaxGasPerInvoke {
		return InvokeValidation{}, errors.New("invoke gas limit exceeds maximum")
	}
	contract := parsed.ContractOutputs[0].Contract
	if contract.ContractType() != ContractTypeTemplate {
		return InvokeValidation{}, fmt.Errorf("invoke target is not a template contract")
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

var ErrInvalidAsset = errors.New("invalid asset")
