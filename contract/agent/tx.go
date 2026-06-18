package agent

import (
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractOutput = contractframework.ContractOutput

type OutPoint = contractframework.OutPoint

var ParseTx = contractframework.ParseTxFunc(agentParseSpec)

var FindInvokeContractOutputs = contractframework.FindInvokeContractOutputsFunc(agentParseSpec)

var FindContractOutputsForContract = contractframework.FindContractOutputsForContractFunc()

type DeployTxBuildRequest = contractcommon.AgentDeployTxBuildRequest

type InvokeTxBuildRequest = contractcommon.AgentInvokeTxBuildRequest

func BuildDeployTx(req DeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	return contractcommon.BuildAgentDeployTx(req)
}

func BuildInvokeTx(req InvokeTxBuildRequest) (*wire.MsgTx, error) {
	return contractcommon.BuildAgentInvokeTx(req)
}

func agentParseSpec() contractframework.ParseSpec {
	return contractframework.ParseSpecFromPayloads(contractframework.PayloadParseSpec{
		ModuleName:   "agent",
		ContractType: ContractTypeAgent,
		DecodeDeploy: func(data []byte) (contractframework.DeployPayload, error) {
			payload, err := DecodeDeployPayload(data)
			if err != nil {
				return contractframework.DeployPayload{}, err
			}
			return contractframework.DeployPayload{
				GasLimit: payload.GasLimit,
				Name:     payload.Subtype,
				Version:  payload.AgentVersion,
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

func agentDeployPayloadFromFramework(payload *contractframework.DeployPayload) *DeployPayload {
	if payload == nil {
		return nil
	}
	return &DeployPayload{
		GasLimit:        payload.GasLimit,
		Subtype:         payload.Name,
		AgentVersion:    payload.Version,
		Deployer:        payload.Deployer,
		Random:          contractframework.CloneBytes(payload.Random),
		ContractContent: contractframework.CloneBytes(payload.Code),
	}
}

func ValidateDeployTxBasic(tx *wire.MsgTx, prefix string, cfg RuntimeConfig, gasCfg GasConfig) (DeployValidation, error) {
	return contractframework.ValidateDeployWithRuntime(contractframework.DeployValidationRequest{
		Tx:         tx,
		Prefix:     prefix,
		ModuleName: "agent",
		ParseSpec:  agentParseSpec(),
		GasConfig:  gasCfg,
		Resolver:   StandardContractScriptResolver,
		BuildRuntime: func(payload contractframework.DeployPayload) (ContractAddress, any, error) {
			addr, _, err := DeriveContractAddress(
				prefix,
				payload.Name,
				payload.Code,
				payload.Deployer,
				payload.Random,
			)
			if err != nil {
				return ContractAddress{}, nil, err
			}
			deployPayload := agentDeployPayloadFromFramework(&payload)
			runtime, err := NewRuntime(addr, *deployPayload, cfg)
			if err != nil {
				return ContractAddress{}, nil, err
			}
			return addr, runtime, nil
		},
	})
}

func ValidateInvokeTxBasic(tx *wire.MsgTx, resolver ContractScriptResolver, exists ContractExistsFunc, gasCfg GasConfig) (InvokeValidation, error) {
	parsed, err := ParseTx(tx, resolver)
	if err != nil {
		return InvokeValidation{}, err
	}
	return ValidateParsedInvokeTxBasic(parsed, exists, gasCfg)
}

func ValidateParsedInvokeTxBasic(parsed ParsedTx, exists ContractExistsFunc, gasCfg GasConfig) (InvokeValidation, error) {
	return contractframework.ValidateParsedInvokeBasic(
		parsed, "agent", ContractTypeAgent,
		contractframework.ContractExistsFunc(exists), gasCfg)
}
