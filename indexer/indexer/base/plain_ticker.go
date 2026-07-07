package base

import (
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
)

const plainSatMaxSupply = "21000000000000000"

func normalizeTickerName(ticker *wire.AssetName) *wire.AssetName {
	if ticker == nil || ticker.String() == indexer.ASSET_ALL_SAT.String() ||
		indexer.IsPlainAsset(ticker) {
		return &indexer.ASSET_PLAIN_SAT
	}
	return ticker
}

func isPlainTickerName(ticker *wire.AssetName) bool {
	return indexer.IsPlainAsset(normalizeTickerName(ticker))
}

func newPlainSatTickerInfo() *common.TickerInfo {
	maxSupply, err := indexer.NewDecimalFromString(plainSatMaxSupply, 0)
	if err != nil {
		common.Log.Panicf("invalid plain sat max supply %s: %v", plainSatMaxSupply, err)
	}
	return &common.TickerInfo{
		AssetName:       indexer.ASSET_PLAIN_SAT,
		N:               1,
		Divisibility:    0,
		MaxSupply:       maxSupply,
		TotalAscendAmt:  indexer.NewDefaultDecimal(0),
		TotalDescendAmt: indexer.NewDefaultDecimal(0),
	}
}
