package base

import (
	"github.com/sat20-labs/satoshinet/wire"

	indexer "github.com/sat20-labs/indexer/common"
)

func normalizeTickerName(ticker *wire.AssetName) *wire.AssetName {
	if ticker == nil || ticker.String() == indexer.ASSET_ALL_SAT.String() ||
		indexer.IsPlainAsset(ticker) {
		return &indexer.ASSET_PLAIN_SAT
	}
	return ticker
}
