package evm

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/wire"
)

type ContractUTXOOverlay struct {
	ContractPrefix string
	Base           ContractUTXOProvider

	utxos map[string][]UTXO
	spent map[OutPoint]struct{}
}

func NewContractUTXOOverlay(prefix string, base ContractUTXOProvider) *ContractUTXOOverlay {
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	return &ContractUTXOOverlay{
		ContractPrefix: prefix,
		Base:           base,
		utxos:          make(map[string][]UTXO),
		spent:          make(map[OutPoint]struct{}),
	}
}

func (o *ContractUTXOOverlay) Provider(contract ContractAddress) ([]UTXO, error) {
	if o == nil {
		return nil, fmt.Errorf("missing contract UTXO overlay")
	}
	out := make([]UTXO, 0)
	if o.Base != nil {
		base, err := o.Base(contract)
		if err != nil {
			return nil, err
		}
		out = append(out, base...)
	}
	for _, utxo := range o.utxos[resultSpendContractKey(contract)] {
		out = append(out, utxo)
	}
	filtered := out[:0]
	seen := make(map[OutPoint]struct{}, len(out))
	for _, utxo := range out {
		if _, spent := o.spent[utxo.OutPoint]; spent {
			continue
		}
		if _, ok := seen[utxo.OutPoint]; ok {
			continue
		}
		seen[utxo.OutPoint] = struct{}{}
		filtered = append(filtered, utxo)
	}
	return filtered, nil
}

func (o *ContractUTXOOverlay) ApplyTx(tx *wire.MsgTx, height int64) error {
	if o == nil {
		return fmt.Errorf("missing contract UTXO overlay")
	}
	for _, txIn := range tx.TxIn {
		if txIn == nil {
			continue
		}
		o.spent[WireOutPointToEVM(txIn.PreviousOutPoint)] = struct{}{}
	}
	return o.AddTxOutputs(tx, height)
}

func (o *ContractUTXOOverlay) AddTxOutputs(tx *wire.MsgTx, height int64) error {
	if o == nil {
		return fmt.Errorf("missing contract UTXO overlay")
	}
	utxos, err := ContractUTXOsFromTx(tx, o.ContractPrefix, height)
	if err != nil {
		return err
	}
	for _, utxo := range utxos {
		key := resultSpendContractKey(utxo.Contract)
		o.utxos[key] = append(o.utxos[key], utxo)
	}
	return nil
}

func ContractUTXOsFromTx(tx *wire.MsgTx, prefix string, height int64) ([]UTXO, error) {
	if tx == nil {
		return nil, fmt.Errorf("missing transaction")
	}
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	txid := tx.TxID()
	utxos := make([]UTXO, 0)
	for vout, txOut := range tx.TxOut {
		if txOut == nil {
			return nil, fmt.Errorf("nil output %d", vout)
		}
		contract, ok, err := ParseContractPkScript(txOut.PkScript, prefix)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if txOut.Value < 0 {
			return nil, fmt.Errorf("output %d has negative value", vout)
		}
		for _, asset := range txOut.Assets {
			if err := validateAssetDecimal(asset.Amount); err != nil {
				return nil, fmt.Errorf("output %d asset %s amount: %w",
					vout, asset.Name.String(), err)
			}
		}
		utxos = append(utxos, UTXO{
			OutPoint: OutPoint{TxID: txid, Vout: uint32(vout)},
			Contract: contract,
			Value:    uint64(txOut.Value),
			Assets:   txOut.Assets.Clone(),
			Height:   height,
		})
	}
	return utxos, nil
}
