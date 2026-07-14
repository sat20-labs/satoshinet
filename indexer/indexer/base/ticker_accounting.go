package base

import (
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

func (b *BaseIndexer) applyAscendingTicker(ascend *common.AscendData, data []byte) error {
	if ascend == nil {
		return fmt.Errorf("ascending marker without anchor input")
	}
	if len(ascend.Assets) > 1 {
		return fmt.Errorf("anchor contains %d assets", len(ascend.Assets))
	}

	tickerInfo, err := common.GenTickerInfo(data)
	if err != nil {
		return err
	}

	expectedName := &indexer.ASSET_PLAIN_SAT
	ascendAmount := indexer.NewDecimal(ascend.Value, 0)
	if len(ascend.Assets) == 1 {
		expectedName = &ascend.Assets[0].Name
		ascendAmount = ascend.Assets[0].Amount.Clone()
	}

	markerName := normalizeTickerName(&tickerInfo.AssetName)
	expectedName = normalizeTickerName(expectedName)
	if *markerName != *expectedName {
		return fmt.Errorf("marker asset %s does not match anchor asset %s",
			markerName.String(), expectedName.String())
	}
	tickerInfo.AssetName = *markerName
	tickerInfo.TotalAscendAmt = ascendAmount
	tickerInfo.TotalDescendAmt = indexer.NewDecimal(0, tickerInfo.Divisibility)

	existingTicker := b.GetTickerInfo(markerName)
	if existingTicker == nil {
		b.tickInfoMap[markerName.String()] = tickerInfo
		return nil
	}
	if err := sameTickerMetadata(existingTicker, tickerInfo); err != nil {
		return err
	}
	existingTicker.TotalAscendAmt = existingTicker.TotalAscendAmt.Add(ascendAmount)
	if existingTicker.TotalDescendAmt == nil {
		existingTicker.TotalDescendAmt = indexer.NewDecimal(0, existingTicker.Divisibility)
	}
	return nil
}

func sameTickerMetadata(existing, incoming *common.TickerInfo) error {
	if existing.AssetName != incoming.AssetName || existing.N != incoming.N ||
		existing.Divisibility != incoming.Divisibility {
		return fmt.Errorf("ticker metadata mismatch for %s", incoming.AssetName.String())
	}
	return nil
}

func (b *BaseIndexer) applyDescendingTicker(descend *common.DescendData) error {
	if descend == nil {
		return fmt.Errorf("nil descend data")
	}

	bindingSats := descend.Assets.GetBindingSatAmout()
	for i := range descend.Assets {
		asset := &descend.Assets[i]
		ticker := b.GetTickerInfo(&asset.Name)
		if ticker == nil {
			return fmt.Errorf("ticker %s not found", asset.Name.String())
		}
		ticker.TotalDescendAmt = ticker.TotalDescendAmt.Add(&asset.Amount)
		if ticker.TotalDescendAmt.Cmp(ticker.TotalAscendAmt) > 0 {
			return fmt.Errorf("asset %s amount exceeds ascend: %s > %s",
				asset.Name.String(), ticker.TotalDescendAmt.String(), ticker.TotalAscendAmt.String())
		}
	}

	plainSats := descend.Value - bindingSats
	if plainSats <= 0 {
		return nil
	}
	ticker := b.GetTickerInfo(&indexer.ASSET_PLAIN_SAT)
	if ticker == nil {
		return fmt.Errorf("plain sats ticker not found")
	}
	ticker.TotalDescendAmt = ticker.TotalDescendAmt.Add(indexer.NewDefaultDecimal(plainSats))
	if ticker.TotalDescendAmt.Cmp(ticker.TotalAscendAmt) > 0 {
		return fmt.Errorf("plain sats amount exceeds ascend: %s > %s",
			ticker.TotalDescendAmt.String(), ticker.TotalAscendAmt.String())
	}
	return nil
}
