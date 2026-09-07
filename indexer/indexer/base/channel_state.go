package base

import (
	"fmt"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"

	db "github.com/sat20-labs/indexer/indexer/db"
)

// RecordChannelStateEvent 用于测试网络的通道状态事件上报，不属于常规区块索引路径。
// 主网禁用由 IndexerMgr 控制，RPC 入口还要求显式的测试用途确认。
// 当前测试实现持有全局锁直到 Flush 完成，且失败时不会回滚已更新的内存事件。
// 若扩展到常规运行路径，需先处理锁粒度和失败一致性。
func (b *BaseIndexer) RecordChannelStateEvent(event *common.ChannelStateEvent) error {
	if event == nil {
		return fmt.Errorf("channel state event is nil")
	}
	if event.ChannelId == "" {
		return fmt.Errorf("channel is required")
	}
	if event.EventType == "" {
		return fmt.Errorf("eventType is required")
	}
	if event.ObservedL1TxId == "" {
		return fmt.Errorf("observedL1TxId is required")
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()
	stored := cloneChannelStateEvent(event)
	if stored.Status == "" {
		stored.Status = common.CHANNEL_EVENT_STATUS_OBSERVED
	}
	if stored.CreatedAt == 0 {
		stored.CreatedAt = time.Now().Unix()
	}
	if stored.L2Height == 0 {
		stored.L2Height = b.stats.SyncHeight
	}

	key := stp.GetChannelStateEventDBKey(stored)
	b.utxoIndex.ChannelStateEventMap[string(key)] = stored

	wb := b.db.NewWriteBatch()
	defer wb.Close()
	if err := db.SetDB(key, stored, wb); err != nil {
		return err
	}
	return wb.Flush()
}
