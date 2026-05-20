package evm

import (
	"errors"

	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
)

func FindCoinbaseStateRoot(tx *wire.MsgTx) (StateRootPayload, bool, error) {
	if tx == nil {
		return StateRootPayload{}, false, errors.New("missing coinbase transaction")
	}
	var found bool
	var payload StateRootPayload
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		txType, content, err := evmcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil || txType != TxTypeCoinbaseStateRoot {
			continue
		}
		if found {
			return StateRootPayload{}, false, errors.New("multiple EVM state roots in coinbase")
		}
		decoded, err := evmcommon.DecodeStateRootPayload(content)
		if err != nil {
			return StateRootPayload{}, false, err
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
		return errors.New("missing EVM state root in coinbase")
	}
	if payload.StateRoot != expected {
		return errors.New("EVM state root mismatch")
	}
	return nil
}

func UpsertCoinbaseStateRoot(tx *wire.MsgTx, root [32]byte) error {
	if tx == nil {
		return errors.New("missing coinbase transaction")
	}
	script, err := evmcommon.StateRootNullDataScript(StateRootPayload{StateRoot: root})
	if err != nil {
		return err
	}

	foundIndex := -1
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		txType, _, err := evmcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil || txType != TxTypeCoinbaseStateRoot {
			continue
		}
		if foundIndex >= 0 {
			return errors.New("multiple EVM state roots in coinbase")
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
