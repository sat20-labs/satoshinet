package template

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractOutput = contractframework.ContractOutput

var ParseTx = contractframework.ParseTxFunc(templateParseSpec)
var FindInvokeContractOutputs = contractframework.FindInvokeContractOutputsFunc(templateParseSpec)
var FindContractOutputsForContract = contractframework.FindContractOutputsForContractFunc()

type DeployTxBuildRequest struct {
	ContractPrefix string
	Contract       Contract
	Deployer       string
	DeployNonce    uint64
	Flags          contractcommon.ContractFlags
	GasLimit       int64
	Funding        wire.TxOut
	Inputs         []wire.OutPoint
	ExtraOutputs   []*wire.TxOut
}

type InvokeTxBuildRequest = contractcommon.InvokeTxBuildRequest

func BuildDeployTx(req DeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	if req.Contract == nil {
		return nil, ContractAddress{}, fmt.Errorf("missing template contract")
	}
	encoded, err := req.Contract.Encode()
	if err != nil {
		return nil, ContractAddress{}, err
	}
	return contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  req.ContractPrefix,
		Type:            ContractTypeTemplate,
		SubType:         req.Contract.TemplateName(),
		Version:         req.Contract.Version(),
		Deployer:        req.Deployer,
		DeployNonce:     req.DeployNonce,
		Flags:           req.Flags,
		ContractContent: encoded,
		GasLimit:        req.GasLimit,
		Funding:         req.Funding,
		Inputs:          req.Inputs,
		ExtraOutputs:    req.ExtraOutputs,
	})
}

func BuildInvokeTx(req InvokeTxBuildRequest) (*wire.MsgTx, error) {
	return contractcommon.BuildInvokeTx(req)
}

func templateParseSpec() contractframework.ParseSpec {
	return contractframework.ParseSpecFromPayloads(contractframework.PayloadParseSpec{
		ModuleName:   "template",
		ContractType: ContractTypeTemplate,
		DecodeDeploy: DecodeDeployPayload,
		DecodeInvoke: DecodeInvokePayload,
	})
}

func templateDeployPayloadFromFramework(payload *contractframework.DeployPayload) *DeployPayload {
	if payload == nil {
		return nil
	}
	out := *payload
	out.Type = ContractTypeTemplate
	out.ContractContent = contractframework.CloneBytes(payload.ContractContent)
	return &out
}

func ValidateDeployTxBasic(tx *wire.MsgTx, prefix string, registry *Registry, cfg GasConfig) (DeployValidation, error) {
	return ValidateDeployTxBasicWithActor(tx, prefix, registry, cfg, "")
}

func ValidateDeployTxBasicWithActor(tx *wire.MsgTx, prefix string, registry *Registry, cfg GasConfig, actor string) (DeployValidation, error) {
	parsed, err := ParseTx(tx, nil)
	if err != nil {
		return DeployValidation{}, err
	}
	if parsed.Type != TxTypeDeploy || parsed.Deploy == nil {
		return DeployValidation{}, fmt.Errorf("not a template DEPLOY transaction")
	}
	addr, _, err := DeriveContractAddress(prefix, parsed.Deploy.ContractContent, actor, parsed.Deploy.DeployNonce)
	if err != nil {
		return DeployValidation{}, err
	}
	deployPayload := templateDeployPayloadFromFramework(parsed.Deploy)
	runtime, err := NewRuntimeWithDeployer(addr, *deployPayload, registry, actor)
	if err != nil {
		return DeployValidation{}, err
	}
	gasConfig := GasConfigForRuntime(cfg, runtime)
	if err := contractframework.ValidateDeployGasLimit(parsed.Deploy.GasLimit, gasConfig); err != nil {
		return DeployValidation{}, err
	}
	fundingOutputs, err := FindContractOutputsForContract(tx, StandardContractScriptResolver(prefix), addr)
	if err != nil {
		return DeployValidation{}, err
	}
	if len(fundingOutputs) > 1 {
		return DeployValidation{}, fmt.Errorf("template DEPLOY must use at most one contract output")
	}
	validated := DeployValidation{
		Payload: *parsed.Deploy,
		Address: addr,
		Runtime: runtime,
	}
	if len(fundingOutputs) == 1 {
		validated.FundingOutput = &fundingOutputs[0]
	}
	return validated, nil
}

func ValidateInvokeTxBasic(tx *wire.MsgTx, resolver ContractScriptResolver, exists ContractExistsFunc, cfg GasConfig) (InvokeValidation, error) {
	parsed, err := ParseTx(tx, resolver)
	if err != nil {
		return InvokeValidation{}, err
	}
	return ValidateParsedInvokeTxBasic(parsed, exists, cfg)
}

func ValidateParsedInvokeTxBasic(parsed ParsedTx, exists ContractExistsFunc, cfg GasConfig) (InvokeValidation, error) {
	return contractframework.ValidateParsedInvokeBasic(
		parsed, "template", ContractTypeTemplate,
		contractframework.ContractExistsFunc(exists), cfg)
}
