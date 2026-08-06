package dkvs

import (
	"container/heap"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type feeUsageEntry struct {
	usageKey     string
	recordHash   chainhash.Hash
	expiryHeight uint64
	tombstone    bool
}

type feeExpiryItem struct {
	recordKey  string
	recordHash chainhash.Hash
	expires    uint64
}

type feeExpiryHeap []feeExpiryItem

func (h feeExpiryHeap) Len() int           { return len(h) }
func (h feeExpiryHeap) Less(a, b int) bool { return h[a].expires < h[b].expires }
func (h feeExpiryHeap) Swap(a, b int)      { h[a], h[b] = h[b], h[a] }
func (h *feeExpiryHeap) Push(value interface{}) {
	*h = append(*h, value.(feeExpiryItem))
}
func (h *feeExpiryHeap) Pop() interface{} {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

func (i *Indexer) resetFeeUsageLocked() {
	i.feeUsageInitialized = false
	i.feeUsageCounts = make(map[string]uint64)
	i.feeUsageEntries = make(map[string]feeUsageEntry)
	i.feeExpiryHeights = nil
}

func (i *Indexer) ensureFeeUsageLocked(verifier IndexedFeeCapacityVerifier, height, now uint64) error {
	if i.feeUsageInitialized {
		i.expireFeeUsageLocked(height)
		return nil
	}
	i.resetFeeUsageLocked()
	records, _, _, err := i.scanLocked("", nil, 0, false, height, now)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record == nil || IsTombstone(record.Flags) || IsExpired(record, height) {
			continue
		}
		usageKey, err := verifier.FeeUsageKey(record)
		if err != nil || usageKey == "" {
			continue
		}
		i.addFeeUsageLocked(record, usageKey)
	}
	i.feeUsageInitialized = true
	return nil
}

func (i *Indexer) addFeeUsageLocked(record *wire.DKVSRecord, usageKey string) {
	if record == nil || usageKey == "" {
		return
	}
	hash := RecordHash(record)
	entry := feeUsageEntry{
		usageKey:     usageKey,
		recordHash:   hash,
		expiryHeight: RecordExpiryHeight(record),
		tombstone:    IsTombstone(record.Flags),
	}
	i.feeUsageEntries[record.Key] = entry
	i.feeUsageCounts[usageKey]++
	if !entry.tombstone && entry.expiryHeight != 0 {
		heap.Push(&i.feeExpiryHeights, feeExpiryItem{recordKey: record.Key, recordHash: hash, expires: entry.expiryHeight})
	}
}

func (i *Indexer) removeFeeUsageLocked(recordKey string) {
	entry, ok := i.feeUsageEntries[recordKey]
	if !ok {
		return
	}
	delete(i.feeUsageEntries, recordKey)
	if count := i.feeUsageCounts[entry.usageKey]; count <= 1 {
		delete(i.feeUsageCounts, entry.usageKey)
	} else {
		i.feeUsageCounts[entry.usageKey] = count - 1
	}
}

func (i *Indexer) expireFeeUsageLocked(height uint64) {
	i.expireFeeHeapLocked(&i.feeExpiryHeights, height)
}

func (i *Indexer) expireFeeHeapLocked(items *feeExpiryHeap, current uint64) {
	if current == 0 {
		return
	}
	for items.Len() > 0 && (*items)[0].expires <= current {
		item := heap.Pop(items).(feeExpiryItem)
		entry, ok := i.feeUsageEntries[item.recordKey]
		if !ok || entry.recordHash != item.recordHash || entry.tombstone {
			continue
		}
		i.removeFeeUsageLocked(item.recordKey)
	}
}

func (i *Indexer) replaceFeeUsageLocked(verifier IndexedFeeCapacityVerifier, record *wire.DKVSRecord) {
	i.removeFeeUsageLocked(record.Key)
	usageKey, err := verifier.FeeUsageKey(record)
	if err == nil && usageKey != "" {
		i.addFeeUsageLocked(record, usageKey)
	}
}
