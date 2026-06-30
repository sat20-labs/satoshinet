package framework

import (
	"errors"
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractExistsFunc func(contract.ContractAddress) bool

type DeployValidation struct {
	Payload       DeployPayload
	Address       contract.ContractAddress
	Runtime       any
	FundingOutput *ContractOutput
}

type InvokeValidation struct {
	Contract      contract.ContractAddress
	FundingOutput ContractOutput
	MsgValue      int64
	Payload       InvokePayload
}

func ValidateParsedDeployBasic(parsed ParsedTx, moduleName string, cfg GasConfig) (DeployValidation, error) {
	if parsed.Type != contract.TxTypeDeploy || parsed.Deploy == nil {
		return DeployValidation{}, fmt.Errorf("not a %s DEPLOY transaction", moduleName)
	}
	if err := ValidateDeployGasLimit(parsed.Deploy.GasLimit, cfg); err != nil {
		return DeployValidation{}, err
	}
	return DeployValidation{Payload: *parsed.Deploy}, nil
}

type DeployRuntimeBuilder func(DeployPayload, string) (contract.ContractAddress, any, error)

type DeployValidationRequest struct {
	Tx           *wire.MsgTx
	Prefix       string
	ModuleName   string
	ParseSpec    ParseSpec
	GasConfig    GasConfig
	Actor        string
	Resolver     func(string) ContractScriptResolver
	BuildRuntime DeployRuntimeBuilder
}

func ValidateDeployWithRuntime(req DeployValidationRequest) (DeployValidation, error) {
	if req.Resolver == nil {
		return DeployValidation{}, errors.New("missing contract script resolver")
	}
	if req.BuildRuntime == nil {
		return DeployValidation{}, errors.New("missing deploy runtime builder")
	}
	parsed, err := ParseTx(req.Tx, nil, req.ParseSpec)
	if err != nil {
		return DeployValidation{}, err
	}
	validated, err := ValidateParsedDeployBasic(parsed, req.ModuleName, req.GasConfig)
	if err != nil {
		return DeployValidation{}, err
	}
	addr, runtime, err := req.BuildRuntime(validated.Payload, req.Actor)
	if err != nil {
		return DeployValidation{}, err
	}
	fundingOutputs, err := FindContractOutputsForContract(req.Tx, req.Resolver(req.Prefix), addr)
	if err != nil {
		return DeployValidation{}, err
	}
	if len(fundingOutputs) > 1 {
		return DeployValidation{}, fmt.Errorf("%s DEPLOY must use at most one contract output", req.ModuleName)
	}
	validated.Address = addr
	validated.Runtime = runtime
	if len(fundingOutputs) == 1 {
		validated.FundingOutput = &fundingOutputs[0]
	}
	return validated, nil
}

func ValidateParsedInvokeBasic(parsed ParsedTx, moduleName string, contractType byte,
	exists ContractExistsFunc, cfg GasConfig) (InvokeValidation, error) {

	if parsed.Type != contract.TxTypeInvoke || parsed.Invoke == nil {
		return InvokeValidation{}, fmt.Errorf("not a %s INVOKE transaction", moduleName)
	}
	if err := ValidateInvokeGasLimit(parsed.Invoke.GasLimit, cfg); err != nil {
		return InvokeValidation{}, err
	}
	if len(parsed.ContractOutputs) == 0 {
		return InvokeValidation{}, fmt.Errorf("%s INVOKE has no contract output", moduleName)
	}
	if len(parsed.ContractOutputs) != 1 {
		return InvokeValidation{}, fmt.Errorf("%s INVOKE must use exactly one contract output", moduleName)
	}
	contractAddr := parsed.ContractOutputs[0].Contract
	if contractType != 0 && contractAddr.ContractType() != contractType {
		return InvokeValidation{}, fmt.Errorf("invoke target is not a %s contract", moduleName)
	}
	if exists != nil && !exists(contractAddr) {
		return InvokeValidation{}, errors.New("invoke target contract does not exist")
	}
	msgValue, err := SumContractOutputValue(parsed.ContractOutputs)
	if err != nil {
		return InvokeValidation{}, err
	}
	return InvokeValidation{
		Contract:      contractAddr,
		FundingOutput: parsed.ContractOutputs[0],
		MsgValue:      msgValue,
		Payload:       *parsed.Invoke,
	}, nil
}

func SumContractOutputValue(outputs []ContractOutput) (int64, error) {
	var total int64
	for _, output := range outputs {
		value := output.PhysicalValue()
		if value < 0 {
			return 0, fmt.Errorf("contract output %s has negative value", output.OutPoint)
		}
		next, overflow := addInt64(total, value)
		if overflow {
			return 0, errors.New("contract output value overflows int64")
		}
		total = next
	}
	return total, nil
}

func addInt64(a, b int64) (int64, bool) {
	c := a + b
	return c, c < a
}
