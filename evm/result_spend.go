package evm

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
	OutPoint OutPoint
	Contract ContractAddress
	PkScript []byte
}

func ValidateResultContractSpend(tx *wire.MsgTx, inputScripts map[OutPoint][]byte, prefix string) (ResultSpendValidation, error) {
	result, err := ValidateResultTxBasic(tx)
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
		outpoint := WireOutPointToEVM(txIn.PreviousOutPoint)
		pkScript, ok := inputScripts[outpoint]
		if !ok {
			return ResultSpendValidation{}, fmt.Errorf("missing input script for %s", outpoint)
		}
		contract, ok, err := ParseContractPkScript(pkScript, prefix)
		if err != nil {
			return ResultSpendValidation{}, err
		}
		if !ok {
			continue
		}

		contractInputs = append(contractInputs, ContractInput{
			OutPoint: outpoint,
			Contract: contract,
			PkScript: cloneBytes(pkScript),
		})
		key := resultSpendContractKey(contract)
		if _, ok := seenContracts[key]; !ok {
			seenContracts[key] = struct{}{}
			contracts = append(contracts, contract)
		}
	}
	if len(contractInputs) == 0 {
		return ResultSpendValidation{}, errors.New("EVM_RESULT spends no contract UTXO")
	}

	return ResultSpendValidation{
		Payload:        result.Payload,
		ContractInputs: contractInputs,
		Contracts:      contracts,
	}, nil
}

func resultSpendContractKey(contract ContractAddress) string {
	hash := ContractAddressHash(contract)
	return fmt.Sprintf("%d:%d:%x", contract.Version(), contract.ContractType(), hash[:])
}
