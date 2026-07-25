package p2p

import (
	"encoding/hex"
	"sort"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func OrderRecords(records []*wire.DKVSRecord) []*wire.DKVSRecord {
	ordered := append([]*wire.DKVSRecord(nil), records...)
	sort.SliceStable(ordered, func(a, b int) bool {
		if ordered[a] == nil {
			return ordered[b] != nil
		}
		if ordered[b] == nil {
			return false
		}
		return ordered[a].Key < ordered[b].Key
	})
	return ordered
}

func DataMessages(records []*wire.DKVSRecord, notFound []chainhash.Hash) []*wire.MsgDKVSData {
	if len(records) == 0 {
		return []*wire.MsgDKVSData{{NotFound: notFound}}
	}
	messages := make([]*wire.MsgDKVSData, 0, 1)
	for len(records) > 0 {
		count := 0
		size := 16
		for count < len(records) && count < wire.MaxDKVSRecordsPerMsg {
			recordSize := wire.DKVSRecordSerializeSize(records[count])
			if count > 0 && size+recordSize > wire.MaxDKVSRecordsPayloadSize {
				break
			}
			size += recordSize
			count++
		}
		if count == 0 {
			return nil
		}
		msg := &wire.MsgDKVSData{Records: records[:count]}
		records = records[count:]
		if len(records) == 0 {
			msg.NotFound = notFound
		}
		messages = append(messages, msg)
	}
	return messages
}

func notifyEventTypeForRelay(record *wire.DKVSRecord) uint8 {
	if record == nil {
		return 0
	}
	if dkvs.IsTombstone(record.Flags) {
		return dkvs.EventRecordTombstone
	}
	parsed, err := dkvs.ParseKey(record.Key)
	if err != nil {
		return 0
	}
	if parsed.Namespace == "mail" && len(parsed.Segments) >= 2 && parsed.Segments[1] == "msg" {
		return dkvs.EventMailboxMessage
	}
	if parsed.Namespace == "sys" && len(parsed.Segments) >= 2 {
		switch parsed.Segments[0] {
		case "checkpoint":
			return dkvs.EventCheckpointReady
		case "snapshot":
			return dkvs.EventSnapshotReady
		}
	}
	return dkvs.EventRecordUpdate
}

func NotifyForRecord(record *wire.DKVSRecord) *wire.MsgDKVSNotify {
	if record == nil {
		return nil
	}
	event, err := dkvs.NewNotifyEvent(notifyEventTypeForRelay(record), record)
	if err != nil {
		return nil
	}
	return &wire.MsgDKVSNotify{
		EventType: event.EventType,
		Data:      event.Data,
	}
}

func RecordFromNotify(msg *wire.MsgDKVSNotify) (*wire.DKVSRecord, error) {
	if msg == nil {
		return nil, dkvs.ErrInvalidRecord
	}
	return dkvs.RecordFromNotifyEvent(&dkvs.NotifyEvent{EventType: msg.EventType, Data: msg.Data})
}

func FiltersFromSubscriptions(subs []dkvs.Subscription) []wire.DKVSSyncFilter {
	if len(subs) == 0 {
		return nil
	}
	filters := make([]wire.DKVSSyncFilter, 0, len(subs))
	for _, sub := range subs {
		filters = append(filters, wire.DKVSSyncFilter{Type: string(sub.Type), Target: sub.Target})
	}
	return filters
}

func SubscriptionsFromFilters(filters []wire.DKVSSyncFilter) []dkvs.Subscription {
	if len(filters) == 0 {
		return nil
	}
	subs := make([]dkvs.Subscription, 0, len(filters))
	for _, filter := range filters {
		subs = append(subs, dkvs.Subscription{Type: dkvs.SubscriptionType(filter.Type), Target: filter.Target})
	}
	return subs
}

func SyncRequestPayloadLen(cursor []byte, filters []wire.DKVSSyncFilter) int {
	size := 8 + wire.VarIntSerializeSize(uint64(len(cursor))) + len(cursor) + 4
	if len(filters) == 0 {
		return size
	}
	size += wire.VarIntSerializeSize(uint64(len(filters)))
	for _, filter := range filters {
		size += wire.VarIntSerializeSize(uint64(len(filter.Type))) + len(filter.Type)
		size += wire.VarIntSerializeSize(uint64(len(filter.Target))) + len(filter.Target)
	}
	return size
}

func CheckpointRootMismatch(localRootHex string, remoteRoot chainhash.Hash) bool {
	localRoot, err := hex.DecodeString(localRootHex)
	if err != nil || len(localRoot) != chainhash.HashSize {
		return true
	}
	var localHash chainhash.Hash
	copy(localHash[:], localRoot)
	return localHash != remoteRoot
}
