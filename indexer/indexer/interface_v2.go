package indexer

import (
	"github.com/sat20-labs/indexer/common"

	"github.com/sat20-labs/satoshinet/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

// return: utxoId->asset
func (b *IndexerMgr) GetAssetUTXOsInAddressWithTickV3(address string, ticker *swire.AssetName) (map[uint64]*common.AssetsInUtxo, error) {
	utxos, err := b.rpcService.GetUTXOs(address)
	if err != nil {
		return nil, err
	}

	result := make(map[uint64]*common.AssetsInUtxo)
	for utxoId := range utxos {
		utxo, err := b.rpcService.GetUtxoByID(utxoId)
		if err != nil {
			continue
		}

		output := b.GetTxOutputWithUtxo(utxo)
		if output == nil {
			continue
		}

		var assetsInUtxo common.AssetsInUtxo
		assetsInUtxo.UtxoId = output.UtxoId
		assetsInUtxo.OutPoint = utxo
		assetsInUtxo.Value = output.OutValue.Value
		assetsInUtxo.PkScript = output.OutValue.PkScript
		
		for _, v := range output.OutValue.Assets {
			asset := common.DisplayAsset{
				AssetName:  v.Name,
				Amount:     v.Amount.String(),
				Precision:  v.Amount.Precision,
				BindingSat: int(v.BindingSat),
			}
			assetsInUtxo.Assets = append(assetsInUtxo.Assets, &asset)
		}
		
		if ticker == nil {
			result[utxoId] = &assetsInUtxo
		} else if common.IsPlainAsset(ticker) {
			// 即使包含其他资产，只要有白聪存在，就可以放进来
			if output.HasPlainSat() {
				result[utxoId] = &assetsInUtxo
			}
		} else {
			for _, asset := range assetsInUtxo.Assets {
				if asset.AssetName == *ticker {
					result[utxoId] = &assetsInUtxo
				}
			}
		}
	}

	return result, nil
}


func (b *IndexerMgr) GetTxOutputWithUtxoV3(utxo string) *common.AssetsInUtxo {
	output := b.GetTxOutputWithUtxo(utxo)
	if output == nil {
		return nil
	}

	var assetsInUtxo common.AssetsInUtxo
	assetsInUtxo.UtxoId = output.UtxoId
	assetsInUtxo.OutPoint = utxo
	assetsInUtxo.Value = output.OutValue.Value
	assetsInUtxo.PkScript = output.OutValue.PkScript
	
	for _, v := range output.OutValue.Assets {
		asset := common.DisplayAsset{
			AssetName:  v.Name,
			Amount:     v.Amount.String(),
			Precision:  v.Amount.Precision,
			BindingSat: int(v.BindingSat),
		}

		assetsInUtxo.Assets = append(assetsInUtxo.Assets, &asset)
	}

	return &assetsInUtxo
}


func (b *IndexerMgr) GetAssetSummaryInAddressV3(address string) map[common.TickerName]*common.Decimal {
	utxos, err := b.rpcService.GetUTXOs(address)
	if err != nil {
		return nil
	}

	totalSats := int64(0)
	result := make(map[wire.AssetName]*common.Decimal)
	for utxoId := range utxos {
		utxo, err := b.rpcService.GetUtxoByID(utxoId)
		if err != nil {
			continue
		}
		info, err := b.rpcService.GetUtxoInfo(utxo)
		if err != nil {
			continue
		}
		totalSats += info.Value

		convertAssets(info, result)
	}
	result[common.ASSET_ALL_SAT] = common.NewDefaultDecimal(totalSats)
	
	return result
}

func convertAssets(info *sindexer.UtxoInfo, assetMap map[common.TickerName]*common.Decimal)  {

	// 白聪资产去除绑定资产的聪
	bindingSats := int64(0)
	if len(info.Assets) != 0 {
		for _, asset := range info.Assets {
			total, ok := assetMap[asset.Name]
			if ok {
				total = total.Add(&asset.Amount)
			} else {
				total = &asset.Amount
			}
			assetMap[asset.Name] = total
		}
		// 如果存在没绑定聪的ordx资产，需要为其预留空白聪
		bindingSats = info.Assets.GetBindingSatAmout() + int64(info.Assets.GetUnboundAssetCount())
	}
	value := (info.Value - bindingSats)
	if value > 0 {
		plainSats := assetMap[common.ASSET_PLAIN_SAT]
		assetMap[common.ASSET_PLAIN_SAT] = common.DecimalAdd(plainSats, common.NewDefaultDecimal(value))
	}
}


// return: ticker -> asset info (inscriptinId -> asset ranges)
func (b *IndexerMgr) GetAssetsWithUtxoV3(utxo string) map[common.TickerName]*common.Decimal {

	info, err := b.rpcService.GetUtxoInfo(utxo)
	if err != nil {
		return nil
	}

	result := make(map[wire.AssetName]*common.Decimal)
	convertAssets(info, result)
	return result
}
