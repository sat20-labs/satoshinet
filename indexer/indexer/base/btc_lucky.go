package base

import (
	"encoding/hex"
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	"sort"

	dbcodec "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"
)

// GetBTCLuckyBlocks projects the existing durable ascends plus the current RPC
// snapshot. No independent counter/history is written. Funding outpoints are
// deduplicated before filtering, so the live snapshot overrides persisted data.
func (b *RpcIndexer) GetBTCLuckyBlocks(pubkey string, start, limit int) (*common.BTCLuckyBlocks, error) {
	if start < 0 || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid BTC lucky pagination")
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	ascends := make(map[string]*common.AscendData)
	err := b.db.BatchRead([]byte(stp.DB_KEY_ASCEND), false, func(_, v []byte) error {
		var ascend common.AscendData
		if err := dbcodec.DecodeBytes(v, &ascend); err != nil {
			return err
		}
		if ascend.BTCLucky != nil {
			ascends[ascend.FundingUtxo] = &ascend
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read BTC lucky ascends: %w", err)
	}
	for key, ascend := range b.utxoIndex.AscendMap {
		ascends[key] = ascend
	}
	result := &common.BTCLuckyBlocks{IndexedHeight: b.lastHeight, Start: start, Limit: limit, Items: []common.BTCLuckyBlock{}}
	records := []common.BTCLuckyBlock{}
	blocks := make(map[string]struct{})
	for _, ascend := range ascends {
		if ascend == nil || ascend.BTCLucky == nil || ascend.Height > result.IndexedHeight {
			continue
		}
		miner := hex.EncodeToString(ascend.PubB)
		if pubkey != "" && miner != pubkey {
			continue
		}
		blockHash := ascend.BTCLucky.BlockHash.String()
		blocks[blockHash] = struct{}{}
		result.TotalRewardSats += ascend.Value
		service := ascend.PubA
		if hex.EncodeToString(ascend.PubA) == indexer.GetBootstrapPubKey() {
			service = ascend.PubB
		}
		records = append(records, common.BTCLuckyBlock{BTCHeight: ascend.BTCLucky.BlockHeight, BTCBlockHash: blockHash,
			FundingUtxo: ascend.FundingUtxo, RewardSats: ascend.Value, MinerPubKey: miner, ServicePubKey: hex.EncodeToString(service),
			RewardAddress: ascend.Address, L2Height: ascend.Height, AnchorTxID: ascend.AnchorTxId})
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].L2Height != records[j].L2Height {
			return records[i].L2Height > records[j].L2Height
		}
		return records[i].FundingUtxo < records[j].FundingUtxo
	})
	result.Total = len(records)
	result.MinedBlockCount = len(blocks)
	if start < len(records) {
		end := len(records)
		if limit < end-start {
			end = start + limit
		}
		result.Items = records[start:end]
	}
	return result, nil
}
