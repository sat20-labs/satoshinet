package indexer

import (
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

// GetInternalSyncHeight returns the in-memory compiling height for internal
// node logic. External RPC queries should keep using GetSyncHeight.
func (b *IndexerMgr) GetInternalSyncHeight() int {
	if b == nil || b.compiling == nil {
		return 0
	}
	return b.compiling.GetHeight()
}

// GetInternalAssetUTXOsInAddress returns the current compiling view for
// consensus and block-building code. External RPC queries should keep using
// GetAssetUTXOsInAddress, which reads the stable rpcService snapshot.
func (b *IndexerMgr) GetInternalAssetUTXOsInAddress(address string) map[wire.AssetName][]*common.TxOutput {
	if b == nil || b.compiling == nil {
		return nil
	}
	utxos, err := b.compiling.GetUTXOs(address)
	if err != nil {
		return nil
	}

	result := make(map[wire.AssetName][]*common.TxOutput)
	for utxoId := range utxos {
		utxo, err := b.compiling.GetUtxoByID(utxoId)
		if err != nil {
			continue
		}
		info := b.GetInternalTxOutputWithUtxo(utxo)
		if info == nil {
			continue
		}
		for _, asset := range info.OutValue.Assets {
			result[asset.Name] = append(result[asset.Name], info)
		}
		if len(info.OutValue.Assets) == 0 {
			result[common.ASSET_PLAIN_SAT] = append(result[common.ASSET_PLAIN_SAT], info)
		}
	}
	return result
}

// GetInternalTxOutputWithUtxo returns a UTXO from the compiling view.
func (b *IndexerMgr) GetInternalTxOutputWithUtxo(utxo string) *common.TxOutput {
	if b == nil || b.compiling == nil {
		return nil
	}
	info, err := b.compiling.GetUtxoInfo(utxo)
	if err != nil {
		return nil
	}
	return &common.TxOutput{
		UtxoId:      info.UtxoId,
		OutPointStr: utxo,
		OutValue: wire.TxOut{
			Value:    info.Value,
			Assets:   info.Assets,
			PkScript: info.PkScript,
		},
	}
}
