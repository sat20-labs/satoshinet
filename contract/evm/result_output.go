package evm

import (
	"fmt"

	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type ScriptRecipientResolver func(pkScript []byte) (recipient string, ok bool, err error)

func ResultOutputsFromTx(tx *wire.MsgTx, contractPrefix string, resolve ScriptRecipientResolver) ([]ResultOutput, error) {
	if tx == nil {
		return nil, fmt.Errorf("missing result transaction")
	}
	if contractPrefix == "" {
		contractPrefix = TestnetContractPrefix
	}
	outputs := make([]ResultOutput, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, fmt.Errorf("nil output %d", i)
		}
		if txscript.IsUnspendable(txOut.PkScript) {
			txType, _, err := evmcommon.ReadNullDataScript(txOut.PkScript)
			if err == nil && txType == TxTypeResult {
				continue
			}
			continue
		}

		recipient, err := resultOutputRecipient(txOut.PkScript, contractPrefix, resolve)
		if err != nil {
			return nil, err
		}
		if txOut.Value < 0 {
			return nil, fmt.Errorf("output %d has negative value", i)
		}
		output := ResultOutput{
			To:     recipient,
			Value:  uint64(txOut.Value),
			Assets: txOut.Assets.Clone(),
		}
		for _, asset := range txOut.Assets {
			if err := validateAssetDecimal(asset.Amount); err != nil {
				return nil, fmt.Errorf("output %d asset %s amount: %w", i, asset.Name.String(), err)
			}
		}
		outputs = append(outputs, normalizeResultOutput(output))
	}
	return outputs, nil
}

func resultOutputRecipient(pkScript []byte, contractPrefix string, resolve ScriptRecipientResolver) (string, error) {
	if contract, ok, err := ParseContractPkScript(pkScript, contractPrefix); err != nil {
		return "", err
	} else if ok {
		return contract.MustEncode(), nil
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

func decimalToUint64(amount scommon.Decimal) (uint64, error) {
	if err := validateAssetDecimal(amount); err != nil {
		return 0, err
	}
	if amount.Precision != 0 {
		return 0, fmt.Errorf("fractional asset amounts are not supported")
	}
	if !amount.Value.IsUint64() {
		return 0, fmt.Errorf("asset amount overflows uint64")
	}
	return amount.Value.Uint64(), nil
}

func validateAssetDecimal(amount scommon.Decimal) error {
	if amount.Value == nil {
		return fmt.Errorf("nil decimal value")
	}
	if amount.Sign() < 0 {
		return fmt.Errorf("negative amount")
	}
	return nil
}
