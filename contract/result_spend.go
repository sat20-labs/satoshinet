package contract

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type ResultSpendValidation struct {
	Payload        ResultPayload
	ContractInputs []ContractInput
	Contracts      []ContractAddress
}

type ContractInput struct {
	OutPoint wire.OutPoint
	Contract ContractAddress
	PkScript []byte
}

func ValidateResultContractSpend(tx *wire.MsgTx, inputScripts map[wire.OutPoint][]byte,
	prefix string) (ResultSpendValidation, error) {

	result, err := validateResultTxBasic(tx)
	if err != nil {
		return ResultSpendValidation{}, err
	}
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	if inputScripts == nil {
		return ResultSpendValidation{}, errors.New("missing input script map")
	}

	contractInputs := make([]ContractInput, 0)
	contracts := make([]ContractAddress, 0)
	seenContracts := make(map[string]struct{})
	for i, txIn := range tx.TxIn {
		if txIn == nil {
			return ResultSpendValidation{}, fmt.Errorf("nil input %d", i)
		}
		pkScript, ok := inputScripts[txIn.PreviousOutPoint]
		if !ok {
			return ResultSpendValidation{}, fmt.Errorf("missing input script for %s", txIn.PreviousOutPoint)
		}
		contract, ok, err := ParseContractPkScript(pkScript, prefix)
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

func validateResultTxBasic(tx *wire.MsgTx) (ResultPayload, error) {
	if tx == nil {
		return ResultPayload{}, errors.New("missing transaction")
	}
	txType, found, err := ClassifyTxPayloadType(tx)
	if err != nil {
		return ResultPayload{}, err
	}
	if !found || txType != TxTypeResult {
		return ResultPayload{}, errors.New("not a CONTRACT_RESULT transaction")
	}
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		payload, err := ReadResultNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if payload.ResultCount == 0 {
			return ResultPayload{}, errors.New("result count is zero")
		}
		return payload, nil
	}
	return ResultPayload{}, errors.New("missing CONTRACT_RESULT payload")
}

func resultSpendContractKey(contract ContractAddress) string {
	hash := ContractAddressHash(contract)
	return fmt.Sprintf("%d:%d:%x", contract.Version(), contract.ContractType(), hash[:])
}
