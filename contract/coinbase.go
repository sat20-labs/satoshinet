package contract

import (
	"errors"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

func FindCoinbaseStateRoot(tx *wire.MsgTx) (contractcommon.StateRootPayload, bool, error) {
	if tx == nil {
		return contractcommon.StateRootPayload{}, false, errors.New("missing coinbase transaction")
	}
	var payload contractcommon.StateRootPayload
	found := false
	for _, txOut := range tx.TxOut {
		txType, content, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil || txType != contractcommon.TxTypeCoinbaseStateRoot {
			continue
		}
		if found {
			return contractcommon.StateRootPayload{}, false, errors.New("multiple contract state roots in coinbase")
		}
		decoded, err := contractcommon.DecodeStateRootPayload(content)
		if err != nil {
			return contractcommon.StateRootPayload{}, false, err
		}
		payload = decoded
		found = true
	}
	return payload, found, nil
}

func VerifyCoinbaseStateRoot(tx *wire.MsgTx, expected [32]byte) error {
	payload, found, err := FindCoinbaseStateRoot(tx)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("missing contract state root in coinbase")
	}
	if payload.StateRoot != expected {
		return errors.New("contract state root mismatch")
	}
	return nil
}

func UpsertCoinbaseStateRoot(tx *wire.MsgTx, root [32]byte) error {
	if tx == nil {
		return errors.New("missing coinbase transaction")
	}
	script, err := contractcommon.StateRootNullDataScript(contractcommon.StateRootPayload{StateRoot: root})
	if err != nil {
		return err
	}
	found := false
	for _, txOut := range tx.TxOut {
		txType, _, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil || txType != contractcommon.TxTypeCoinbaseStateRoot {
			continue
		}
		if found {
			return errors.New("multiple contract state roots in coinbase")
		}
		txOut.PkScript = script
		found = true
	}
	if !found {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}
	return nil
}
