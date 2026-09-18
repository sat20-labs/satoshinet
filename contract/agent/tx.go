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

type DeployTxBuildRequest = contractcommon.DeployTxBuildRequest
type InvokeTxBuildRequest = contractcommon.InvokeTxBuildRequest

func BuildDeployTx(req DeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	req.Type = ContractTypeAgent
	return contractcommon.BuildDeployTx(req)
}

func BuildInvokeTx(req InvokeTxBuildRequest) (*wire.MsgTx, error) {
	return contractcommon.BuildInvokeTx(req)
}

func agentParseSpec() contractframework.ParseSpec {
	return contractframework.ParseSpecFromPayloads(contractframework.PayloadParseSpec{
		ModuleName:   "agent",
		ContractType: ContractTypeAgent,
		DecodeDeploy: DecodeDeployPayload,
		DecodeInvoke: DecodeInvokePayload,
	})
}

func agentDeployPayloadFromFramework(payload *contractframework.DeployPayload) *DeployPayload {
	if payload == nil {
		return nil
	}
	out := *payload
	out.Type = ContractTypeAgent
	out.ContractContent = contractframework.CloneBytes(payload.ContractContent)
	return &out
}

func ValidateDeployTxBasic(tx *wire.MsgTx, prefix string, cfg RuntimeConfig, gasCfg GasConfig) (DeployValidation, error) {
	return ValidateDeployTxBasicWithActor(tx, prefix, cfg, gasCfg, "")
}

func ValidateDeployTxBasicWithActor(tx *wire.MsgTx, prefix string, cfg RuntimeConfig, gasCfg GasConfig, actor string) (DeployValidation, error) {
	return contractframework.ValidateDeployWithRuntime(contractframework.DeployValidationRequest{
		Tx:         tx,
		Prefix:     prefix,
		ModuleName: "agent",
		ParseSpec:  agentParseSpec(),
		GasConfig:  gasCfg,
		Actor:      actor,
		Resolver:   StandardContractScriptResolver,
		BuildRuntime: func(payload contractframework.DeployPayload, deployer string) (ContractAddress, any, error) {
			addr, _, err := DeriveContractAddress(prefix, payload.SubType,
				payload.ContractContent, deployer, payload.DeployNonce)
			if err != nil {
				return ContractAddress{}, nil, err
			}
			deployPayload := agentDeployPayloadFromFramework(&payload)
			runtime, err := NewRuntimeWithDeployer(addr, *deployPayload, cfg, deployer)
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
