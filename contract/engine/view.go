package engine

import (
	"encoding/hex"
	"errors"
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

type TxView struct {
	IsContract    bool           `json:"isContract"`
	TxID          string         `json:"txid,omitempty"`
	Ops           []TxOpView     `json:"ops,omitempty"`
	Outputs       []OutputView   `json:"outputs,omitempty"`
	StateRoot     *StateRootView `json:"stateRoot,omitempty"`
	PayloadErrors []string       `json:"payloadErrors,omitempty"`
}

type TxOpView struct {
	Kind           string                 `json:"kind"`
	ContractType   string                 `json:"contractType,omitempty"`
	ContractTypeID byte                   `json:"contractTypeId,omitempty"`
	Subtype        string                 `json:"subtype,omitempty"`
	Action         string                 `json:"action,omitempty"`
	GasLimit       int64                  `json:"gasLimit,omitempty"`
	Nonce          uint64                 `json:"nonce,omitempty"`
	Contract       string                 `json:"contract,omitempty"`
	Deployer       string                 `json:"deployer,omitempty"`
	TemplateName   string                 `json:"templateName,omitempty"`
	Version        uint32                 `json:"version,omitempty"`
	Status         string                 `json:"status,omitempty"`
	ResultCount    uint16                 `json:"resultCount,omitempty"`
	PayloadHex     string                 `json:"payloadHex,omitempty"`
	Details        map[string]interface{} `json:"details,omitempty"`
}

type OutputView struct {
	Vout           uint32 `json:"vout"`
	Contract       string `json:"contract"`
	ContractType   string `json:"contractType"`
	ContractTypeID byte   `json:"contractTypeId"`
	Version        byte   `json:"version"`
	Hash           string `json:"hash"`
	Value          int64  `json:"value"`
	Role           string `json:"role,omitempty"`
}

type StateRootView struct {
	Combined string `json:"combined"`
}

func BuildTxView(tx *wire.MsgTx, prefix string) (TxView, error) {
	if tx == nil {
		return TxView{}, errors.New("missing transaction")
	}
	if prefix == "" {
		prefix = contractcommon.TestnetContractPrefix
	}

	view := TxView{TxID: tx.TxID()}
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return TxView{}, fmt.Errorf("nil output %d", i)
		}
		if output, ok, err := decodeContractOutput(uint32(i), txOut, prefix); err != nil {
			return TxView{}, err
		} else if ok {
			view.Outputs = append(view.Outputs, output)
			view.IsContract = true
		}
	}

	ops, stateRoot, payloadErrors := decodeContractOps(tx, prefix)
	if len(ops) > 0 {
		view.Ops = ops
		view.IsContract = true
	}
	if stateRoot != nil {
		view.StateRoot = stateRoot
		view.IsContract = true
	}
	view.PayloadErrors = payloadErrors
	return view, nil
}

func DecodeContractAddressView(address string) (OutputView, error) {
	contractAddr, err := contractcommon.DecodeContractAddress(address)
	if err != nil {
		return OutputView{}, err
	}
	return OutputView{
		Contract:       contractAddr.EncodeAddress(),
		ContractType:   contractTypeName(contractAddr.ContractType()),
		ContractTypeID: contractAddr.ContractType(),
		Version:        contractAddr.Version(),
		Hash:           hex.EncodeToString(contractcommon.ContractAddressHashBytes(contractAddr)),
	}, nil
}

func decodeContractOutput(vout uint32, txOut *wire.TxOut, prefix string) (OutputView, bool, error) {
	contractAddr, ok, err := contractcommon.ParseContractPkScript(txOut.PkScript, prefix)
	if err != nil || !ok {
		return OutputView{}, ok, err
	}
	return OutputView{
		Vout:           vout,
		Contract:       contractAddr.EncodeAddress(),
		ContractType:   contractTypeName(contractAddr.ContractType()),
		ContractTypeID: contractAddr.ContractType(),
		Version:        contractAddr.Version(),
		Hash:           hex.EncodeToString(contractcommon.ContractAddressHashBytes(contractAddr)),
		Value:          txOut.Value,
		Role:           "pool",
	}, true, nil
}

func decodeContractOps(tx *wire.MsgTx, prefix string) ([]TxOpView, *StateRootView, []string) {
	payload, txType, found, err := collectContractPayload(tx)
	if err != nil {
		return nil, nil, []string{err.Error()}
	}
	if !found {
		return nil, nil, nil
	}

	if txType == contractcommon.TxTypeCoinbaseStateRoot {
		root, err := contractcommon.DecodeStateRootPayload(payload)
		if err != nil {
			return nil, nil, []string{err.Error()}
		}
		return []TxOpView{{
			Kind:   txTypeName(txType),
			Status: "committed",
		}}, &StateRootView{Combined: hex.EncodeToString(root.StateRoot[:])}, nil
	}

	class, foundClass, classErr := ClassifyTxForBlockOrderWithPrefix(tx, prefix)
	var contractType byte
	if foundClass {
		contractType = class.ContractType
	} else {
		contractType = inferContractTypeFromOutputs(tx, prefix)
	}
	if contractType == 0 {
		contractType = contractcommon.ContractTypeEVM
	}

	op := TxOpView{
		Kind:           txTypeName(txType),
		ContractType:   contractTypeName(contractType),
		ContractTypeID: contractType,
		PayloadHex:     hex.EncodeToString(payload),
	}
	var payloadErrs []string
	if classErr != nil {
		payloadErrs = append(payloadErrs, classErr.Error())
	}

	switch txType {
	case contractcommon.TxTypeDeploy:
		fillDeployOp(&op, contractType, payload, prefix)
	case contractcommon.TxTypeInvoke:
		fillInvokeOp(&op, contractType, payload)
	case contractcommon.TxTypeResult:
		result, err := contractcommon.DecodeResultPayload(payload)
		if err != nil {
			payloadErrs = append(payloadErrs, err.Error())
		} else {
			op.Status = resultStatusName(result.Status)
			op.ResultCount = result.ResultCount
		}
	}
	return []TxOpView{op}, nil, payloadErrs
}

func collectContractPayload(tx *wire.MsgTx) ([]byte, contractcommon.TxType, bool, error) {
	var txType contractcommon.TxType
	var payload []byte
	found := false
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, 0, false, fmt.Errorf("nil output %d", i)
		}
		nextType, part, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if !found {
			txType = nextType
			found = true
		} else if txType != nextType {
			return nil, 0, false, errors.New("transaction contains mixed contract OP_RETURN output types")
		}
		payload = append(payload, part...)
	}
	return payload, txType, found, nil
}

func fillDeployOp(op *TxOpView, contractType byte, payload []byte, prefix string) {
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		p, err := contractcommon.DecodeTemplateDeployPayload(payload)
		if err != nil {
			op.Details = errorDetails(err)
			return
		}
		op.GasLimit = p.GasLimit
		op.Subtype = p.TemplateName
		op.TemplateName = p.TemplateName
		op.Version = p.TemplateVersion
		op.Deployer = p.Deployer
		if addr, _, err := contractcommon.DeriveTemplateContractAddress(prefix, p.ContractContent, p.Deployer, p.Random); err == nil {
			op.Contract = addr.EncodeAddress()
		}
		op.Details = map[string]interface{}{
			"random":       hex.EncodeToString(p.Random),
			"content_size": len(p.ContractContent),
		}
	case contractcommon.ContractTypeAgent:
		p, err := contractcommon.DecodeAgentDeployPayload(payload)
		if err != nil {
			op.Details = errorDetails(err)
			return
		}
		op.GasLimit = p.GasLimit
		op.Subtype = p.Subtype
		op.Version = p.AgentVersion
		op.Deployer = p.Deployer
		if addr, _, err := contractcommon.DeriveAgentContractAddress(prefix, p.Subtype, p.ContractContent, p.Deployer, p.Random); err == nil {
			op.Contract = addr.EncodeAddress()
		}
		op.Details = map[string]interface{}{
			"random":       hex.EncodeToString(p.Random),
			"content_size": len(p.ContractContent),
		}
	default:
		p, err := contractcommon.DecodeDeployPayload(payload)
		if err != nil {
			op.Details = errorDetails(err)
			return
		}
		op.GasLimit = p.GasLimit
		op.Nonce = p.DeployNonce
		op.Details = map[string]interface{}{"init_code_size": len(p.InitCode)}
	}
}

func fillInvokeOp(op *TxOpView, contractType byte, payload []byte) {
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		p, err := contractcommon.DecodeTemplateInvokePayload(payload)
		if err != nil {
			op.Details = errorDetails(err)
			return
		}
		op.GasLimit = p.GasLimit
		op.Nonce = p.CallNonce
		op.Action = p.Action
		op.Details = map[string]interface{}{"param_size": len(p.Param)}
	case contractcommon.ContractTypeAgent:
		p, err := contractcommon.DecodeAgentInvokePayload(payload)
		if err != nil {
			op.Details = errorDetails(err)
			return
		}
		op.GasLimit = p.GasLimit
		op.Nonce = p.CallNonce
		op.Action = p.Action
		op.Details = map[string]interface{}{"param_size": len(p.Param)}
	default:
		p, err := contractcommon.DecodeInvokePayload(payload)
		if err != nil {
			op.Details = errorDetails(err)
			return
		}
		op.GasLimit = p.GasLimit
		op.Nonce = p.CallNonce
		op.Details = map[string]interface{}{"calldata_size": len(p.Calldata)}
	}
}

func inferContractTypeFromOutputs(tx *wire.MsgTx, prefix string) byte {
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		contractAddr, ok, err := contractcommon.ParseContractPkScript(txOut.PkScript, prefix)
		if err == nil && ok {
			return contractAddr.ContractType()
		}
	}
	return 0
}

func errorDetails(err error) map[string]interface{} {
	return map[string]interface{}{"decode_error": err.Error()}
}

func txTypeName(t contractcommon.TxType) string {
	switch t {
	case contractcommon.TxTypeDeploy:
		return "deploy"
	case contractcommon.TxTypeInvoke:
		return "invoke"
	case contractcommon.TxTypeResult:
		return "result"
	case contractcommon.TxTypeCoinbaseStateRoot:
		return "state_root"
	default:
		return fmt.Sprintf("unknown:%d", t)
	}
}

func contractTypeName(t byte) string {
	switch t {
	case contractcommon.ContractTypeTemplate:
		return "template"
	case contractcommon.ContractTypeEVM:
		return "evm"
	case contractcommon.ContractTypeAgent:
		return "agent"
	default:
		return fmt.Sprintf("unknown:%d", t)
	}
}

func resultStatusName(s contractcommon.ResultStatus) string {
	switch s {
	case contractcommon.ResultStatusSuccess:
		return "success"
	case contractcommon.ResultStatusRevert:
		return "revert"
	case contractcommon.ResultStatusOutOfGas:
		return "out_of_gas"
	case contractcommon.ResultStatusInvalid:
		return "invalid"
	default:
		return fmt.Sprintf("unknown:%d", s)
	}
}

func GetContractTypeName(t byte) string {
	return contractTypeName(t)
}
