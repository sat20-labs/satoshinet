package dkvs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const syncCursorVersion = byte(2)

type syncRange struct {
	target string
	exact bool
}

type decodedSyncCursor struct {
	rangeIndex int
	seek []byte
}

func encodeSyncCursor(cursor decodedSyncCursor) []byte {
	if cursor.rangeIndex < 0 || cursor.rangeIndex > int(^uint16(0)) || len(cursor.seek) > int(^uint16(0)) { return nil }
	encoded := make([]byte, 1+2+2+len(cursor.seek))
	encoded[0] = syncCursorVersion
	binary.LittleEndian.PutUint16(encoded[1:3], uint16(cursor.rangeIndex))
	binary.LittleEndian.PutUint16(encoded[3:5], uint16(len(cursor.seek)))
	copy(encoded[5:], cursor.seek)
	return encoded
}

func decodeSyncCursor(encoded []byte) (decodedSyncCursor, error) {
	if len(encoded) == 0 { return decodedSyncCursor{}, nil }
	if len(encoded) < 5 || encoded[0] != syncCursorVersion || len(encoded) > wire.MaxDKVSCursorSize { return decodedSyncCursor{}, ErrInvalidRecord }
	if int(binary.LittleEndian.Uint16(encoded[3:5])) != len(encoded)-5 { return decodedSyncCursor{}, ErrInvalidRecord }
	return decodedSyncCursor{rangeIndex: int(binary.LittleEndian.Uint16(encoded[1:3])), seek: append([]byte(nil), encoded[5:]...)}, nil
}

func syncRangesForFilters(filters []Subscription) ([]syncRange, error) {
	if len(filters) == 0 { return []syncRange{{target: ""}}, nil }
	ranges := make([]syncRange, 0, len(filters))
	for _, filter := range filters {
		sub, err := validateSubscription(filter)
		if err != nil { return nil, err }
		switch sub.Type {
		case SubscriptionKey:
			ranges = append(ranges, syncRange{target: sub.Target, exact: true})
		case SubscriptionMailbox, SubscriptionPrefix, SubscriptionService:
			ranges = append(ranges, syncRange{target: sub.Target})
		default:
			return nil, ErrInvalidRecord
		}
	}
	return compactSyncRanges(ranges), nil
}

func compactSyncRanges(ranges []syncRange) []syncRange {
	sort.Slice(ranges, func(a, b int) bool {
		if ranges[a].target == ranges[b].target { return !ranges[a].exact && ranges[b].exact }
		return ranges[a].target < ranges[b].target
	})
	out := make([]syncRange, 0, len(ranges))
	for _, candidate := range ranges {
		covered := false
		for _, existing := range out {
			if existing.exact { covered = candidate.exact && candidate.target == existing.target
			} else { covered = existing.target == "" || candidate.target == existing.target || strings.HasPrefix(candidate.target, existing.target+"/") }
			if covered { break }
		}
		if !covered { out = append(out, candidate) }
	}
	return out
}

func (i *Indexer) scanActiveSyncRangeLocked(r syncRange, seek []byte, limit, byteLimit int,
	height, now uint64, relayOnly, allowFirst bool) ([]*wire.DKVSRecord, []byte, bool, int, error) {
	if r.exact {
		if len(seek) != 0 { return nil, nil, false, 0, ErrInvalidRecord }
		record, err := i.getRaw(r.target)
		if errors.Is(err, ErrRecordNotFound) { return nil, nil, true, 0, nil }
		if err != nil { return nil, nil, false, 0, err }
		if i.activeError(record, height, now) != nil || (relayOnly && i.isLocalOnlyRecord(record)) { return nil, nil, true, 0, nil }
		size := wire.DKVSRecordSerializeSize(record)
		if byteLimit > 0 && size > byteLimit {
			if allowFirst { return nil, nil, false, 0, ErrRecordTooLarge }
			return nil, nil, false, 0, nil
		}
		return []*wire.DKVSRecord{record}, nil, true, size, nil
	}
	return i.scanSyncRangeLocked(r.target, seek, limit, byteLimit, height, now, relayOnly, allowFirst)
}

func (i *Indexer) scanSyncRangeLocked(prefix string, cursor []byte, limit, byteLimit int,
	height, now uint64, relayOnly, allowFirst bool) ([]*wire.DKVSRecord, []byte, bool, int, error) {
	scanPrefix := recordKeyPrefix
	normalized := strings.TrimSuffix(prefix, "/")
	if prefix != "" { scanPrefix = recordDBKey(normalized) }
	if len(cursor) != 0 && !bytes.HasPrefix(cursor, scanPrefix) { return nil, nil, false, 0, ErrInvalidRecord }
	seek := cursor
	if len(seek) == 0 { seek = scanPrefix }
	records := make([]*wire.DKVSRecord, 0)
	var next, lastIncluded []byte
	used, done := 0, true
	err := i.db.BatchReadV2(scanPrefix, seek, false, func(key, value []byte) error {
		if len(cursor) != 0 && bytes.Equal(key, cursor) { return nil }
		record, err := UnmarshalRecord(value)
		if err != nil { return err }
		if normalized != "" && record.Key != normalized && !strings.HasPrefix(record.Key, normalized+"/") { return nil }
		if i.activeError(record, height, now) != nil || (relayOnly && i.isLocalOnlyRecord(record)) { return nil }
		size := wire.DKVSRecordSerializeSize(record)
		if byteLimit > 0 && size > byteLimit-used {
			if allowFirst && len(records) == 0 { return ErrRecordTooLarge }
			done = false
			next = append([]byte(nil), lastIncluded...)
			return errStopScan
		}
		records = append(records, record)
		used += size
		lastIncluded = append(lastIncluded[:0], key...)
		if limit > 0 && len(records) >= limit { done = false; next = append([]byte(nil), key...); return errStopScan }
		return nil
	})
	if errors.Is(err, errStopScan) { err = nil }
	return records, next, done, used, err
}

// syncRanges enumerates a complete current view, used by node discovery and
// read-only filtered queries. There is exactly one scan phase: active records.
// Live incremental replication uses the per-key generation ActivePage API.
func (i *Indexer) syncRanges(cursor []byte, limit uint32, ranges []syncRange,
	relayOnly, endpointView bool) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if relayOnly && endpointView { return nil, nil, false, chainhash.Hash{}, ErrInvalidRecord }
	if limit == 0 || limit > wire.MaxDKVSRecordsPerMsg { limit = 100 }
	decoded, err := decodeSyncCursor(cursor)
	if err != nil || decoded.rangeIndex > len(ranges) { return nil, nil, false, chainhash.Hash{}, ErrInvalidRecord }
	height, now := i.currentHeight(), currentUnixMilli()
	remaining := int(limit)
	remainingBytes := wire.MaxDKVSRecordsPayloadSize - 8 - wire.MaxVarIntPayload
	records := make([]*wire.DKVSRecord, 0, remaining)
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	for decoded.rangeIndex < len(ranges) && remaining > 0 && remainingBytes > 0 {
		page, next, rangeDone, used, err := i.scanActiveSyncRangeLocked(ranges[decoded.rangeIndex], decoded.seek,
			remaining, remainingBytes, height, now, relayOnly, len(records) == 0)
		if err != nil { return nil, nil, false, chainhash.Hash{}, err }
		records = append(records, page...)
		remaining -= len(page)
		remainingBytes -= used
		if !rangeDone {
			if len(next) != 0 { decoded.seek = next }
			break
		}
		decoded.seek = nil
		decoded.rangeIndex++
	}
	done := decoded.rangeIndex >= len(ranges)
	var nextCursor []byte
	if !done {
		nextCursor = encodeSyncCursor(decoded)
		if len(nextCursor) == 0 || len(nextCursor) > wire.MaxDKVSCursorSize || bytes.Equal(nextCursor, cursor) { return nil, nil, false, chainhash.Hash{}, ErrInvalidRecord }
	}
	rootRecords, err := i.activeRecordsForRangesLocked(ranges, height, now, relayOnly)
	if err != nil { return nil, nil, false, chainhash.Hash{}, err }
	root, err := recordsRoot(rootRecords, height)
	if err != nil { return nil, nil, false, chainhash.Hash{}, err }
	return records, nextCursor, done, root, nil
}

func (i *Indexer) filteredRoot(filters []Subscription, relayOnly, endpointView bool) (chainhash.Hash, error) {
	if relayOnly && endpointView { return chainhash.Hash{}, ErrInvalidRecord }
	ranges, err := syncRangesForFilters(filters)
	if err != nil { return chainhash.Hash{}, err }
	height, now := i.currentHeight(), currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	records, err := i.activeRecordsForRangesLocked(ranges, height, now, relayOnly)
	if err != nil { return chainhash.Hash{}, err }
	return recordsRoot(records, height)
}

// WaitFilteredForClient is the aggregate read-query wait. The selective
// managed endpoint path uses ActiveWatch's shared event-driven subscriptions.
func (i *Indexer) WaitFilteredForClient(ctx context.Context, filters []Subscription, knownRoot chainhash.Hash) (chainhash.Hash, bool, error) {
	if len(filters) == 0 { return chainhash.Hash{}, false, ErrInvalidRecord }
	root, err := i.filteredRoot(filters, false, true)
	if err != nil || root != knownRoot { return root, root != knownRoot, err }
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			root, err = i.filteredRoot(filters, false, true)
			if err != nil || root != knownRoot { return root, root != knownRoot, err }
		case <-ctx.Done():
			root, err = i.filteredRoot(filters, false, true)
			if err != nil || root != knownRoot { return root, root != knownRoot, err }
			return root, false, ctx.Err()
		}
	}
}

func (i *Indexer) activeRecordsForRangesLocked(ranges []syncRange, height, now uint64, relayOnly bool) ([]*wire.DKVSRecord, error) {
	records, err := i.recordsForRangesLocked(ranges, true, height, now)
	if err != nil { return nil, err }
	if relayOnly { return i.relayableRecords(records), nil }
	return records, nil
}

func (i *Indexer) recordsForRangesLocked(ranges []syncRange, activeOnly bool, height, now uint64) ([]*wire.DKVSRecord, error) {
	seen := make(map[string]*wire.DKVSRecord)
	for _, r := range ranges {
		if r.exact {
			record, err := i.getRaw(r.target)
			if errors.Is(err, ErrRecordNotFound) { continue }
			if err != nil { return nil, err }
			if !activeOnly || i.activeError(record, height, now) == nil { seen[record.Key] = record }
			continue
		}
		records, _, _, err := i.scanLocked(r.target, nil, 0, activeOnly, height, now)
		if err != nil { return nil, err }
		for _, record := range records { seen[record.Key] = record }
	}
	keys := make([]string, 0, len(seen))
	for key := range seen { keys = append(keys, key) }
	sort.Strings(keys)
	records := make([]*wire.DKVSRecord, 0, len(keys))
	for _, key := range keys { records = append(records, seen[key]) }
	return records, nil
}

func recordsRoot(records []*wire.DKVSRecord, height uint64) (chainhash.Hash, error) {
	ordered := append([]*wire.DKVSRecord(nil), records...)
	sort.Slice(ordered, func(a, b int) bool {
		if ordered[a] == nil { return ordered[b] != nil }
		if ordered[b] == nil { return false }
		return ordered[a].Key < ordered[b].Key
	})
	checkpoint, err := checkpointFromRecords(ordered, height)
	if err != nil { return chainhash.Hash{}, err }
	rootBytes, err := hexDecodeRoot(checkpoint.ActiveRecordRoot)
	if err != nil { return chainhash.Hash{}, err }
	var root chainhash.Hash
	copy(root[:], rootBytes)
	return root, nil
}

func hexDecodeRoot(value string) ([]byte, error) {
	if len(value) != chainhash.HashSize*2 { return nil, ErrInvalidCheckpoint }
	decoded := make([]byte, chainhash.HashSize)
	for n := range decoded {
		hi, ok := fromHex(value[n*2])
		if !ok { return nil, ErrInvalidCheckpoint }
		lo, ok := fromHex(value[n*2+1])
		if !ok { return nil, ErrInvalidCheckpoint }
		decoded[n] = hi<<4 | lo
	}
	return decoded, nil
}

func fromHex(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9': return value - '0', true
	case value >= 'a' && value <= 'f': return value - 'a' + 10, true
	case value >= 'A' && value <= 'F': return value - 'A' + 10, true
	default: return 0, false
	}
}

func (i *Indexer) ListActiveKeys(filters []Subscription) ([]string, error) {
	ranges, err := syncRangesForFilters(filters)
	if err != nil { return nil, err }
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	records, err := i.recordsForRangesLocked(ranges, true, i.currentHeight(), currentUnixMilli())
	if err != nil { return nil, err }
	keys := make([]string, 0, len(records))
	for _, record := range records { keys = append(keys, record.Key) }
	return keys, nil
}
