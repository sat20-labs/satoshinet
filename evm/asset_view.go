package evm

import scommon "github.com/sat20-labs/indexer/common"

type UTXOAssetView struct {
	UTXOs []UTXO
}

func NewUTXOAssetView(utxos []UTXO) UTXOAssetView {
	cp := make([]UTXO, len(utxos))
	copy(cp, utxos)
	return UTXOAssetView{UTXOs: cp}
}

func (v UTXOAssetView) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	if assetName == "" {
		return nil, ErrInvalidAsset
	}
	total := zeroDecimal()
	for _, u := range v.UTXOs {
		if u.Contract.Hash != owner {
			continue
		}
		amount, err := u.AssetAmount(assetName)
		if err != nil {
			return nil, err
		}
		total = total.AddAlignPrecision(amount)
	}
	return total, nil
}
