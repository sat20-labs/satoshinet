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
	_, found, err := ClassifyTxPayloadType(tx)
	return found, err
}

// FindDefaultInvokeOutputs returns one call per funding output, in vout order.
// contractType zero selects all types so the framework can route registered
// modules without a hard-coded runtime list. No historical UTXOs are scanned.
func FindDefaultInvokeOutputs(tx *wire.MsgTx, prefix string, contractType byte) ([]DefaultInvokeOutput, error) {
	return FindDefaultInvokeOutputsWithTxID(tx, prefix, contractType, "")
}

// FindDefaultInvokeOutputsWithTxID reuses a txid already computed by the
// transaction execution entry point. An empty txid falls back to computing it.
func FindDefaultInvokeOutputsWithTxID(tx *wire.MsgTx, prefix string, contractType byte,
	txid string) ([]DefaultInvokeOutput, error) {

	if tx == nil {
		return nil, fmt.Errorf("missing transaction")
	}
	// Explicit DEPLOY/INVOKE and RESULT settlements never trigger default
	// calls. A malformed contract envelope is an error, not an absent payload.
	hasPayload, err := HasContractPayload(tx)
	if err != nil || hasPayload {
		return nil, err
	}
	for i, input := range tx.TxIn {
		if input == nil {
			return nil, fmt.Errorf("nil input %d", i)
		}
	}
	if txid == "" {
		txid = tx.TxID()
	}
	outputs := make([]DefaultInvokeOutput, 0)
	for i, txOut := range tx.TxOut {
		addr, ok, err := ParseContractPkScript(txOut.PkScript, prefix)
		if err != nil {
			return nil, err
		}
		if !ok || (contractType != 0 && addr.ContractType() != contractType) {
			continue
		}
		vout := uint32(i)
		outputs = append(outputs, DefaultInvokeOutput{
			TxID: txid, Vout: vout, Contract: addr, Value: txOut.Value,
			Assets: txOut.Assets.Clone(), PkScript: cloneBytes(txOut.PkScript),
		})
	}
	return outputs, nil
}
