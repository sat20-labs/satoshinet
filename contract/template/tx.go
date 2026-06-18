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
		DecodeDeploy: func(data []byte) (contractframework.DeployPayload, error) {
			payload, err := DecodeDeployPayload(data)
			if err != nil {
				return contractframework.DeployPayload{}, err
			}
			return contractframework.DeployPayload{
				Type:            payload.Type,
				SubType:         payload.SubType,
				Version:         payload.Version,
				GasLimit:        payload.GasLimit,
				DeployNonce:     payload.DeployNonce,
				ContractContent: contractframework.CloneBytes(payload.ContractContent),
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
				Param:     contractframework.CloneBytes(payload.Param),
			}, nil
		},
	})
}

func templateDeployPayloadFromFramework(payload *contractframework.DeployPayload) *DeployPayload {
	if payload == nil {
		return nil
	}
	return &DeployPayload{
		Type:            ContractTypeTemplate,
		SubType:         payload.SubType,
		Version:         payload.Version,
		GasLimit:        payload.GasLimit,
		DeployNonce:     payload.DeployNonce,
		ContractContent: contractframework.CloneBytes(payload.ContractContent),
	}
}

func ValidateDeployTxBasic(tx *wire.MsgTx, prefix string, registry *Registry, cfg GasConfig) (DeployValidation, error) {
	return ValidateDeployTxBasicWithActor(tx, prefix, registry, cfg, "")
}

func ValidateDeployTxBasicWithActor(tx *wire.MsgTx, prefix string, registry *Registry, cfg GasConfig, actor string) (DeployValidation, error) {
	return contractframework.ValidateDeployWithRuntime(contractframework.DeployValidationRequest{
		Tx:         tx,
		Prefix:     prefix,
		ModuleName: "template",
		ParseSpec:  templateParseSpec(),
		GasConfig:  cfg,
		Actor:      actor,
		Resolver:   StandardContractScriptResolver,
		BuildRuntime: func(payload contractframework.DeployPayload, deployer string) (ContractAddress, any, error) {
			addr, _, err := DeriveContractAddress(
				prefix,
				payload.ContractContent,
				deployer,
				payload.DeployNonce,
			)
			if err != nil {
				return ContractAddress{}, nil, err
			}
			deployPayload := templateDeployPayloadFromFramework(&payload)
			runtime, err := NewRuntimeWithDeployer(addr, *deployPayload, registry, deployer)
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
