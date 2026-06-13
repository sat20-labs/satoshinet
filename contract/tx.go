package contract

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

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
	if txType == 0 {
		return 0, false, nil
	}
	return txType, true, nil
}
