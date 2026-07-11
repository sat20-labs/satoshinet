package dkvs

import (
	"container/heap"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type recordExpiryEntry struct {
	recordHash   chainhash.Hash
	expiryHeight uint64
	expiryTime   uint64
}

func (i *Indexer) resetRecordExpiryLocked() {
	i.recordExpiryInitialized = false
	i.recordExpiryEntries = make(map[string]recordExpiryEntry)
	i.recordExpiryHeights = nil
	i.recordExpiryTimes = nil
}

func (i *Indexer) ensureRecordExpiryLocked(height, now uint64) error {
	if i.recordExpiryInitialized {
		return nil
	}
	i.resetRecordExpiryLocked()
	records, _, _, err := i.scanLocked("", nil, 0, false, height, now)
	if err != nil {
		return err
	}
	for _, record := range records {
		i.addRecordExpiryLocked(record)
	}
	i.recordExpiryInitialized = true
	return nil
}

func (i *Indexer) addRecordExpiryLocked(record *wire.DKVSRecord) {
	if record == nil || IsTombstone(record.Flags) {
		return
	}
	entry := recordExpiryEntry{recordHash: RecordHash(record), expiryHeight: record.ExpiryHeight}
	if record.TTL != 0 && record.IssueTime != 0 && record.IssueTime <= ^uint64(0)-record.TTL {
		entry.expiryTime = record.IssueTime + record.TTL
	}
	if entry.expiryHeight == 0 && entry.expiryTime == 0 {
		return
	}
	i.recordExpiryEntries[record.Key] = entry
	if entry.expiryHeight != 0 {
		heap.Push(&i.recordExpiryHeights, feeExpiryItem{recordKey: record.Key, recordHash: entry.recordHash, expires: entry.expiryHeight})
	}
	if entry.expiryTime != 0 {
		heap.Push(&i.recordExpiryTimes, feeExpiryItem{recordKey: record.Key, recordHash: entry.recordHash, expires: entry.expiryTime})
	}
}

func (i *Indexer) replaceRecordExpiryLocked(record *wire.DKVSRecord) {
	delete(i.recordExpiryEntries, record.Key)
	i.addRecordExpiryLocked(record)
}

func (i *Indexer) expiredRecordKeysLocked(height, now uint64) []string {
	seen := make(map[string]struct{})
	i.popExpiredRecordHeapLocked(&i.recordExpiryHeights, height, seen)
	i.popExpiredRecordHeapLocked(&i.recordExpiryTimes, now, seen)
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	return keys
}

func (i *Indexer) popExpiredRecordHeapLocked(items *feeExpiryHeap, current uint64, expired map[string]struct{}) {
	if current == 0 {
		return
	}
	for items.Len() > 0 && (*items)[0].expires <= current {
		item := heap.Pop(items).(feeExpiryItem)
		entry, ok := i.recordExpiryEntries[item.recordKey]
		if !ok || entry.recordHash != item.recordHash {
			continue
		}
		delete(i.recordExpiryEntries, item.recordKey)
		expired[item.recordKey] = struct{}{}
	}
}
