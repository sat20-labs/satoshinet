package base

import (
	"fmt"
	"time"

	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"

	db "github.com/sat20-labs/indexer/indexer/db"
)

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
	if event.Status == "" {
		event.Status = common.CHANNEL_EVENT_STATUS_OBSERVED
	}
	if event.CreatedAt == 0 {
		event.CreatedAt = time.Now().Unix()
	}
	if event.L2Height == 0 {
		event.L2Height = b.GetSyncHeight()
	}

	key := stp.GetChannelStateEventDBKey(event)
	b.utxoIndex.ChannelStateEventMap[string(key)] = event

	wb := b.db.NewWriteBatch()
	defer wb.Close()
	if err := db.SetDB(key, event, wb); err != nil {
		return err
	}
	return wb.Flush()
}
