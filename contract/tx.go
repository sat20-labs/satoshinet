package contract

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

// hasContractPayloadHeader recognizes the contract envelope independently of
// whether its payload can be decoded. A damaged explicit call must never be
// reinterpreted as an implicit call. Other SAT20 messages and ordinary memos
// are not contract envelopes.
func hasContractPayloadHeader(script []byte) bool {
	tokens := txscript.MakeScriptTokenizer(0, script)
	if !tokens.Next() || tokens.Opcode() != txscript.OP_RETURN ||
		!tokens.Next() || tokens.Opcode() != sat20MagicNumber || !tokens.Next() {
		return false
	}
	switch tokens.ExtractInt64() {
	case int64(ContentTypeContractDeploy), int64(ContentTypeContractInvoke),
		int64(ContentTypeContractResult), int64(ContentTypeContractStateRoot):
		return true
	default:
		return false
	}
}

func ClassifyTxPayloadType(tx *wire.MsgTx) (TxType, bool, error) {
	if tx == nil {
		return 0, false, fmt.Errorf("missing transaction")
	}
	var txType TxType
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return 0, false, fmt.Errorf("nil output %d", i)
		}
		nextType, _, err := ReadNullDataScript(txOut.PkScript)
		if err != nil {
			if hasContractPayloadHeader(txOut.PkScript) {
				return 0, true, fmt.Errorf("malformed contract payload at output %d: %w", i, err)
			}
			continue
		}
		if txType == 0 {
			txType = nextType
			continue
		}
		if txType != nextType {
			return 0, false, fmt.Errorf("transaction contains mixed contract OP_RETURN output types")
		}
		if txType != TxTypeDeploy && txType != TxTypeInvoke {
			return 0, false, fmt.Errorf("transaction contains multiple singleton contract OP_RETURN outputs")
		}
	}
	return txType, txType != 0, nil
}
