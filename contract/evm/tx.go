package evm

import (
	"errors"

	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractScriptResolver = contractframework.ContractScriptResolver

type ContractOutput = contractframework.ContractOutput

type ContractExistsFunc func(ContractAddress) bool

type DeployValidation = contractframework.DeployValidation

type InvokeValidation = contractframework.InvokeValidation

type ResultValidation struct {
	Payload ResultPayload
	Inputs  []OutPoint
}

var ParseTx = contractframework.ParseTxFunc(evmParseSpec)

var FindInvokeContractOutputs = contractframework.FindInvokeContractOutputsFunc(evmParseSpec)

var FindContractOutputsForContract = contractframework.FindContractOutputsForContractFunc()

type DeployTxBuildRequest = evmcommon.EVMDeployTxBuildRequest

type InvokeTxBuildRequest = evmcommon.EVMInvokeTxBuildRequest

func BuildDeployTx(req DeployTxBuildRequest) (*wire.MsgTx, ContractAddress, error) {
	return evmcommon.BuildEVMDeployTx(req)
}

func BuildInvokeTx(req InvokeTxBuildRequest) (*wire.MsgTx, error) {
	return evmcommon.BuildEVMInvokeTx(req)
}

func evmParseSpec() contractframework.ParseSpec {
	return contractframework.ParseSpecFromPayloads(contractframework.PayloadParseSpec{
		ModuleName:   "EVM",
		ContractType: ContractTypeEVM,
		DecodeDeploy: func(data []byte) (contractframework.DeployPayload, error) {
			payload, err := evmcommon.DecodeDeployPayload(data)
			if err != nil {
				return contractframework.DeployPayload{}, err
			}
			return contractframework.DeployPayload{
				GasLimit: payload.GasLimit,
				Nonce:    payload.DeployNonce,
				Code:     contractframework.CloneBytes(payload.InitCode),
			}, nil
		},
		DecodeInvoke: func(data []byte) (contractframework.InvokePayload, error) {
			payload, err := evmcommon.DecodeInvokePayload(data)
			if err != nil {
				return contractframework.InvokePayload{}, err
			}
			return contractframework.InvokePayload{
				GasLimit:  payload.GasLimit,
				CallNonce: payload.CallNonce,
				Data:      contractframework.CloneBytes(payload.Calldata),
			}, nil
		},
		AcceptResult:         true,
		RequireResultLastOut: true,
	})
}

func InvokeCallBindings(tx *wire.MsgTx, resolver ContractScriptResolver) ([]InvokeCallBinding, error) {
	outputs, err := FindInvokeContractOutputs(tx, resolver)
	if err != nil {
		return nil, err
	}
	bindings := make([]InvokeCallBinding, 0, len(outputs))
	txid := tx.TxID()
	for _, output := range outputs {
		bindings = append(bindings, InvokeCallBinding{
			CallID:       DeriveInvokeCallID(txid, output.Vout, output.Contract),
			InvokeTxID:   txid,
			FundingInput: output.OutPoint,
			Contract:     output.Contract,
		})
	}
	return bindings, nil
}

func ValidateDeployTxBasic(tx *wire.MsgTx, cfg GasConfig) (DeployValidation, error) {
	parsed, err := contractframework.ParseTx(tx, nil, evmParseSpec())
	if err != nil {
		return DeployValidation{}, err
	}
	validated, err := contractframework.ValidateParsedDeployBasic(parsed, "EVM", cfg)
	if err != nil {
		return DeployValidation{}, err
	}
	if len(validated.Payload.Code) == 0 {
		return DeployValidation{}, errors.New("deploy init code is empty")
	}
	return validated, nil
}

func ValidateInvokeTxBasic(tx *wire.MsgTx, resolver ContractScriptResolver, exists ContractExistsFunc, cfg GasConfig) (InvokeValidation, error) {
	parsed, err := ParseTx(tx, resolver)
	if err != nil {
		return InvokeValidation{}, err
	}
	return contractframework.ValidateParsedInvokeBasic(parsed, "EVM", ContractTypeEVM,
		contractframework.ContractExistsFunc(exists), cfg)
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
