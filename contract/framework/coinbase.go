package framework

import (
	"errors"
	"fmt"

	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func FindCoinbaseStateRoot(tx *wire.MsgTx, label string) (contract.StateRootPayload, bool, error) {
	if tx == nil {
		return contract.StateRootPayload{}, false, errors.New("missing coinbase transaction")
	}
	var found bool
	var payload contract.StateRootPayload
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		txType, content, err := contract.ReadNullDataScript(txOut.PkScript)
		if err != nil || txType != contract.TxTypeCoinbaseStateRoot {
			continue
		}
		if found {
			return contract.StateRootPayload{}, false, fmt.Errorf("multiple %s state roots in coinbase", label)
		}
		decoded, err := contract.DecodeStateRootPayload(content)
		if err != nil {
			return contract.StateRootPayload{}, false, err
		}
		payload = decoded
		found = true
	}
	return payload, found, nil
}

func VerifyCoinbaseStateRoot(tx *wire.MsgTx, expected [32]byte, label string) error {
	payload, found, err := FindCoinbaseStateRoot(tx, label)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("missing %s state root in coinbase", label)
	}
	if payload.StateRoot != expected {
		return fmt.Errorf("%s state root mismatch", label)
	}
	return nil
}

func UpsertCoinbaseStateRoot(tx *wire.MsgTx, root [32]byte, label string) error {
	if tx == nil {
		return errors.New("missing coinbase transaction")
	}
	script, err := contract.StateRootNullDataScript(contract.StateRootPayload{StateRoot: root})
	if err != nil {
		return err
	}
	foundIndex := -1
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		txType, _, err := contract.ReadNullDataScript(txOut.PkScript)
		if err != nil || txType != contract.TxTypeCoinbaseStateRoot {
			continue
		}
		if foundIndex >= 0 {
			return fmt.Errorf("multiple %s state roots in coinbase", label)
		}
		foundIndex = i
	}
	if foundIndex >= 0 {
		tx.TxOut[foundIndex].PkScript = script
		return nil
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return nil
}
