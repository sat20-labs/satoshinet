package framework

import (
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type ScriptRecipientResolver func(pkScript []byte) (recipient string, ok bool, err error)

type ResultContractParser func(pkScript []byte, contractPrefix string) (contract.ContractAddress, bool, error)

func ResultOutputsFromTx(tx *wire.MsgTx, contractPrefix string,
	parseContract ResultContractParser, resolve ScriptRecipientResolver) ([]ResultOutput, error) {

	if tx == nil {
		return nil, fmt.Errorf("missing result transaction")
	}
	outputs := make([]ResultOutput, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, fmt.Errorf("nil output %d", i)
		}
		if txscript.IsUnspendable(txOut.PkScript) {
			txType, _, err := contract.ReadNullDataScript(txOut.PkScript)
			if err == nil && txType == contract.TxTypeResult {
				continue
			}
			continue
		}

		recipient, err := resultOutputRecipient(
			txOut.PkScript, contractPrefix, parseContract, resolve)
		if err != nil {
			return nil, err
		}
		if txOut.Value < 0 {
			return nil, fmt.Errorf("output %d has negative value", i)
		}
		output := NormalizeResultOutput(ResultOutput{
			To:     recipient,
			Value:  txOut.Value,
			Assets: txOut.Assets.Clone(),
		})
		for _, asset := range output.Assets {
			if err := ValidateAssetDecimal(asset.Amount); err != nil {
				return nil, fmt.Errorf("output %d asset %s amount: %w",
					i, asset.Name.String(), err)
			}
		}
		outputs = append(outputs, output)
	}
	return outputs, nil
}

func resultOutputRecipient(pkScript []byte, contractPrefix string,
	parseContract ResultContractParser, resolve ScriptRecipientResolver) (string, error) {

	contractAddr, ok, err := parseContract(pkScript, contractPrefix)
	if err != nil {
		return "", err
	}
	if ok {
		return contractAddr.MustEncode(), nil
	}
	if resolve == nil {
		return "", fmt.Errorf("missing recipient resolver")
	}
	recipient, ok, err := resolve(pkScript)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("unrecognized result output script")
	}
	return recipient, nil
}
