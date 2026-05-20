package evm

import "github.com/sat20-labs/satoshinet/wire"

func NewContractTxOut(value int64, assets wire.TxAssets, contract ContractAddress) (*wire.TxOut, error) {
	pkScript, err := ContractPkScript(contract)
	if err != nil {
		return nil, err
	}
	return wire.NewTxOut(value, assets, pkScript), nil
}

func ContractFromTxOut(txOut *wire.TxOut, prefix string) (ContractAddress, bool, error) {
	if txOut == nil {
		return ContractAddress{}, false, nil
	}
	return ParseContractPkScript(txOut.PkScript, prefix)
}
