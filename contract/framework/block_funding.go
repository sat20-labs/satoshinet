package framework

import (
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

// WithoutBlockOutputs makes a supplied physical view a parent view. The
// existing sequential UTXO overlay adds work outputs only after their own
// transaction executes, even when the supplied view includes the whole block.
func WithoutBlockOutputs(base ContractUTXOProvider, txs []*wire.MsgTx) ContractUTXOProvider {
	if base == nil || len(txs) == 0 {
		return base
	}
	excluded := make(map[OutPoint]bool)
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		txID := tx.TxID()
		for vout := range tx.TxOut {
			excluded[OutPoint{TxID: txID, Vout: uint32(vout)}] = true
		}
	}
	return func(addr contract.ContractAddress) ([]UTXO, error) {
		utxos, err := base(addr)
		if err != nil {
			return nil, err
		}
		out := make([]UTXO, 0, len(utxos))
		for _, utxo := range utxos {
			if !excluded[utxo.OutPoint] {
				out = append(out, utxo.Clone())
			}
		}
		return out, nil
	}
}
