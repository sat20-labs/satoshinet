package p2p

import (
	"encoding/hex"
	"sort"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

const dataPayloadBudget = 3500 * 1000

func ReceivePriority(record *wire.DKVSRecord) int {
	if record == nil {
		return 1
	}
	parsed, err := dkvs.ParseKey(record.Key)
	if err != nil || parsed.Namespace != "blob" || len(parsed.Segments) < 3 {
		return 1
	}
	if parsed.Segments[2] == "manifest" {
		return 0
	}
	if parsed.Segments[2] == "chunk" {
		return 2
	}
	return 1
}

func OrderRecords(records []*wire.DKVSRecord) []*wire.DKVSRecord {
	ordered := append([]*wire.DKVSRecord{}, records...)
	sort.SliceStable(ordered, func(a, b int) bool {
		return ReceivePriority(ordered[a]) < ReceivePriority(ordered[b])
	})
	return ordered
}

func RecordIsBlob(record *wire.DKVSRecord) bool {
	if record == nil || dkvs.IsTombstone(record.Flags) {
		return false
	}
	parsed, err := dkvs.ParseKey(record.Key)
	return err == nil && parsed.Namespace == "blob"
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
			if count > 0 && size+recordSize > dataPayloadBudget {
				break
			}
			size += recordSize
			count++
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

func NotifyForRecord(record *wire.DKVSRecord) *wire.MsgDKVSNotify {
	if record == nil {
		return nil
	}
	return &wire.MsgDKVSNotify{
		EventType:    dkvs.EventRecordUpdate,
		Key:          record.Key,
		KeyHash:      dkvs.KeyHash(record.Key),
		RecordHash:   dkvs.RecordHash(record),
		Seq:          record.Seq,
		ExpiryHeight: record.ExpiryHeight,
		Size:         uint32(dkvs.RecordSize(record)),
		Flags:        record.Flags,
	}
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
