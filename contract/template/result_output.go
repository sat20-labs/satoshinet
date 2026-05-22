package template

import (
	"fmt"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
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
			txType, _, err := contractcommon.ReadNullDataScript(txOut.PkScript)
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
		outputs = append(outputs, normalizeResultOutput(ResultOutput{
			To:     recipient,
			Value:  txOut.Value,
			Assets: txOut.Assets.Clone(),
		}))
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
