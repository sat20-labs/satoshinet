package indexer

import (
	"github.com/sat20-labs/indexer/common"
	shareIndexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
)

func IsExistingInMemPool(utxo string) bool {
	isExist, err := satsnet_rpc.IsExistUtxoInMemPool(utxo)
	if err != nil {
		common.Log.Errorf("GetUnspendTxOutput %s failed. %v", utxo, err)
		return false
	}
	return isExist
}

func IsAvailableUtxoId(utxoId uint64) bool {
	return IsAvailableUtxo(shareIndexer.ShareIndexer.GetUtxoById(utxoId))
}

func IsAvailableUtxo(utxo string) bool {

	//Find common utxo (that is, utxo with non-ordinal attributes)
	if shareIndexer.ShareIndexer.HasAssetInUtxo(utxo) {
		return false
	}

	return !IsExistingInMemPool(utxo)
}
