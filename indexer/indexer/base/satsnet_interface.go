package base

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

/*
 提供一些数据接口，供聪网节点实时查询数据。
 被访问的数据需要加锁
*/

func (b *BaseIndexer) IsCoreNode(pubkey string) bool {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	_, ok := b.coreNodeMap[pubkey]
	return ok
}

func (b *BaseIndexer) IsMinerNode(pubkey string) bool {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	_, ok := b.coreNodeMap[pubkey]
	if ok {
		return true
	}

	for _, v := range b.coreNodeMap {
		_, ok := v.ChildMiners[pubkey]
		if ok {
			return true
		}
	}

	return false
}

func (b *BaseIndexer) GetNodeType(pubkey string) int {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.seqMgr.GetNodeType(pubkey)
}

func (b *BaseIndexer) GetSequenceMgr() *common.MiningSequenceMgr {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.seqMgr
}

// block的高度必须设置
func (b *BaseIndexer) CheckBlockMiningInfo(block *btcutil.Block) error {
	miningAddr := common.GetMiningAddress(block.MsgBlock(), b.chaincfgParam)
	txs := block.Transactions()
	if len(txs) == 0 {
		return fmt.Errorf("empty block %d %s", block.Height(), block.Hash().String())
	}

	return b.seqMgr.CheckMiningAddr(txs[0].MsgTx(), int(block.Height()), miningAddr)
}
