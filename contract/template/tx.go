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
	Random         []byte
	GasLimit       int64
	Funding        wire.TxOut
	Inputs         []wire.OutPoint
	ChangeOutputs  []*wire.TxOut
}

type InvokeTxBuildRequest = contractcommon.TemplateInvokeTxBuildRequest

func BuildDeployTx(req DeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	if req.Contract == nil {
		return nil, ContractAddress{}, fmt.Errorf("missing template contract")
	}
	encoded, err := req.Contract.Encode()
	if err != nil {
		return nil, ContractAddress{}, err
	}
	return contractcommon.BuildTemplateDeployTx(contractcommon.TemplateDeployTxBuildRequest{
		ContractPrefix:  req.ContractPrefix,
		TemplateName:    req.Contract.TemplateName(),
		TemplateVersion: req.Contract.Version(),
		Deployer:        req.Deployer,
		Random:          req.Random,
		ContractContent: encoded,
		GasLimit:        req.GasLimit,
		Funding:         req.Funding,
		Inputs:          req.Inputs,
		ChangeOutputs:   req.ChangeOutputs,
	})
}

func BuildInvokeTx(req InvokeTxBuildRequest) (*wire.MsgTx, error) {
	return contractcommon.BuildTemplateInvokeTx(req)
}

func templateParseSpec() contractframework.ParseSpec {
	return contractframework.ParseSpecFromPayloads(contractframework.PayloadParseSpec{
		ModuleName:   "template",
		ContractType: ContractTypeTemplate,
		DecodeDeploy: func(data []byte) (contractframework.DeployPayload, error) {
			payload, err := DecodeDeployPayload(data)
			if err != nil {
				return contractframework.DeployPayload{}, err
			}
			return contractframework.DeployPayload{
				GasLimit: payload.GasLimit,
				Name:     payload.TemplateName,
				Version:  payload.TemplateVersion,
				Deployer: payload.Deployer,
				Random:   contractframework.CloneBytes(payload.Random),
				Code:     contractframework.CloneBytes(payload.ContractContent),
			}, nil
		},
		DecodeInvoke: func(data []byte) (contractframework.InvokePayload, error) {
			payload, err := DecodeInvokePayload(data)
			if err != nil {
				return contractframework.InvokePayload{}, err
			}
			return contractframework.InvokePayload{
				GasLimit:  payload.GasLimit,
				CallNonce: payload.CallNonce,
				Action:    payload.Action,
				Data:      contractframework.CloneBytes(payload.Param),
			}, nil
		},
	})
}

func templateDeployPayloadFromFramework(payload *contractframework.DeployPayload) *DeployPayload {
	if payload == nil {
		return nil
	}
	return &DeployPayload{
		GasLimit:        payload.GasLimit,
		TemplateName:    payload.Name,
		TemplateVersion: payload.Version,
		Deployer:        payload.Deployer,
		Random:          contractframework.CloneBytes(payload.Random),
		ContractContent: contractframework.CloneBytes(payload.Code),
	}
}

func ValidateDeployTxBasic(tx *wire.MsgTx, prefix string, registry *Registry, cfg GasConfig) (DeployValidation, error) {
	return contractframework.ValidateDeployWithRuntime(contractframework.DeployValidationRequest{
		Tx:         tx,
		Prefix:     prefix,
		ModuleName: "template",
		ParseSpec:  templateParseSpec(),
		GasConfig:  cfg,
		Resolver:   StandardContractScriptResolver,
		BuildRuntime: func(payload contractframework.DeployPayload) (ContractAddress, any, error) {
			addr, _, err := DeriveContractAddress(
				prefix,
				payload.Code,
				payload.Deployer,
				payload.Random,
			)
			if err != nil {
				return ContractAddress{}, nil, err
			}
			deployPayload := templateDeployPayloadFromFramework(&payload)
			runtime, err := NewRuntime(addr, *deployPayload, registry)
			if err != nil {
				return ContractAddress{}, nil, err
			}
			return addr, runtime, nil
		},
	})
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
