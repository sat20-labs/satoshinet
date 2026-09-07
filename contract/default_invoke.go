package contract

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

const ContractInvokeAPIDefault = "default"
const ContractInvokeAPICall = "call"
const ContractInvokeAPIClose = "close"

type DefaultInvokeOutput struct {
	TxID     string
	Vout     uint32
	Contract ContractAddress
	Value    int64
	Assets   wire.TxAssets
	PkScript []byte
}

func HasContractPayload(tx *wire.MsgTx) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("missing transaction")
	}
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return false, fmt.Errorf("nil output %d", i)
		}
		if _, _, err := ReadNullDataScript(txOut.PkScript); err == nil {
			return true, nil
		}
	}
	return false, nil
}

func FindDefaultInvokeOutputs(tx *wire.MsgTx, prefix string, contractType byte) ([]DefaultInvokeOutput, error) {
	if tx == nil {
		return nil, fmt.Errorf("missing transaction")
	}
	// Contract payloads, including Result settlements, are not default calls.
	// Paying a contract from a Result must not start an automatic invocation chain.
	hasPayload, err := HasContractPayload(tx)
	if err != nil || hasPayload {
		return nil, err
	}
	txid := tx.TxID()
	outputs := make([]DefaultInvokeOutput, 0)
	for i, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, fmt.Errorf("nil output %d", i)
		}
		addr, ok, err := ParseContractPkScript(txOut.PkScript, prefix)
		if err != nil {
			return nil, err
		}
		if !ok || addr.ContractType() != contractType {
			continue
		}
		vout := uint32(i)
		outputs = append(outputs, DefaultInvokeOutput{
			TxID:     txid,
			Vout:     vout,
			Contract: addr,
			Value:    txOut.Value,
			Assets:   txOut.Assets.Clone(),
			PkScript: cloneBytes(txOut.PkScript),
		})
	}
	return outputs, nil
}
