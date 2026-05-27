package contract

import (
	"errors"
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

type ResultSpendValidation struct {
	Payload        contractcommon.ResultPayload
	ContractInputs []ContractInput
	Contracts      []contractcommon.ContractAddress
}

type ContractInput struct {
	OutPoint wire.OutPoint
	Contract contractcommon.ContractAddress
	PkScript []byte
}

func IsContractPkScript(pkScript []byte) bool {
	return contractcommon.IsContractPkScript(pkScript)
}

func ValidateResultContractSpend(tx *wire.MsgTx, inputScripts map[wire.OutPoint][]byte,
	prefix string) (ResultSpendValidation, error) {

	result, err := validateResultTxBasic(tx)
	if err != nil {
		return ResultSpendValidation{}, err
	}
	if prefix == "" {
		prefix = contractcommon.TestnetContractPrefix
	}
	if inputScripts == nil {
		return ResultSpendValidation{}, errors.New("missing input script map")
	}

	contractInputs := make([]ContractInput, 0)
	contracts := make([]contractcommon.ContractAddress, 0)
	seenContracts := make(map[string]struct{})
	for i, txIn := range tx.TxIn {
		if txIn == nil {
			return ResultSpendValidation{}, fmt.Errorf("nil input %d", i)
		}
		pkScript, ok := inputScripts[txIn.PreviousOutPoint]
		if !ok {
			return ResultSpendValidation{}, fmt.Errorf("missing input script for %s", txIn.PreviousOutPoint)
		}
		contract, ok, err := contractcommon.ParseContractPkScript(pkScript, prefix)
		if err != nil {
			return ResultSpendValidation{}, err
		}
		if !ok {
			continue
		}

		contractInputs = append(contractInputs, ContractInput{
			OutPoint: txIn.PreviousOutPoint,
			Contract: contract,
			PkScript: append([]byte(nil), pkScript...),
		})
		key := resultSpendContractKey(contract)
		if _, ok := seenContracts[key]; !ok {
			seenContracts[key] = struct{}{}
			contracts = append(contracts, contract)
		}
	}
	if len(contractInputs) == 0 {
		return ResultSpendValidation{}, errors.New("CONTRACT_RESULT spends no contract UTXO")
	}

	return ResultSpendValidation{
		Payload:        result,
		ContractInputs: contractInputs,
		Contracts:      contracts,
	}, nil
}

func validateResultTxBasic(tx *wire.MsgTx) (contractcommon.ResultPayload, error) {
	if tx == nil {
		return contractcommon.ResultPayload{}, errors.New("missing transaction")
	}
	txType, found, err := ClassifyTxPayloadType(tx)
	if err != nil {
		return contractcommon.ResultPayload{}, err
	}
	if !found || txType != contractcommon.TxTypeResult {
		return contractcommon.ResultPayload{}, errors.New("not a CONTRACT_RESULT transaction")
	}
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		payload, err := contractcommon.ReadResultNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if payload.ResultCount == 0 {
			return contractcommon.ResultPayload{}, errors.New("result count is zero")
		}
		return payload, nil
	}
	return contractcommon.ResultPayload{}, errors.New("missing CONTRACT_RESULT payload")
}

func resultSpendContractKey(contract contractcommon.ContractAddress) string {
	hash := contractcommon.ContractAddressHash(contract)
	return fmt.Sprintf("%d:%d:%x", contract.Version(), contract.ContractType(), hash[:])
}
