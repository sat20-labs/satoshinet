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

const (
	syncCursorVersion = byte(1)
	syncPhaseRecords  = byte(0)
	syncPhaseDeletes  = byte(1)
)

type syncRange struct {
	target string
	exact  bool
}

type decodedSyncCursor struct {
	rangeIndex int
	phase      byte
	seek       []byte
}

func encodeSyncCursor(cursor decodedSyncCursor) []byte {
	if cursor.rangeIndex < 0 || cursor.rangeIndex > int(^uint16(0)) || len(cursor.seek) > int(^uint16(0)) {
		return nil
	}
	encoded := make([]byte, 1+2+1+2+len(cursor.seek))
	encoded[0] = syncCursorVersion
	binary.LittleEndian.PutUint16(encoded[1:3], uint16(cursor.rangeIndex))
	encoded[3] = cursor.phase
	binary.LittleEndian.PutUint16(encoded[4:6], uint16(len(cursor.seek)))
	copy(encoded[6:], cursor.seek)
	return encoded
}

func decodeSyncCursor(encoded []byte) (decodedSyncCursor, error) {
	if len(encoded) == 0 {
		return decodedSyncCursor{phase: syncPhaseRecords}, nil
	}
	// Accept the pre-path-sync cursor format for an in-flight session. It is a
	// raw record DB key and can only resume the first record range.
	if encoded[0] != syncCursorVersion {
		return decodedSyncCursor{phase: syncPhaseRecords, seek: append([]byte{}, encoded...)}, nil
	}
	if len(encoded) < 6 {
		return decodedSyncCursor{}, ErrInvalidRecord
	}
	seekSize := int(binary.LittleEndian.Uint16(encoded[4:6]))
	if seekSize != len(encoded)-6 {
		return decodedSyncCursor{}, ErrInvalidRecord
	}
	phase := encoded[3]
	if phase != syncPhaseRecords && phase != syncPhaseDeletes {
		return decodedSyncCursor{}, ErrInvalidRecord
	}
	return decodedSyncCursor{
		rangeIndex: int(binary.LittleEndian.Uint16(encoded[1:3])),
		phase:      phase,
		seek:       append([]byte{}, encoded[6:]...),
	}, nil
}

func syncRangesForFilters(filters []Subscription) ([]syncRange, error) {
	if len(filters) == 0 {
		return []syncRange{{target: ""}}, nil
	}
	ranges := make([]syncRange, 0, len(filters)+1)
	for _, filter := range filters {
		sub, err := validateSubscription(filter)
		if err != nil {
			return nil, err
		}
		switch sub.Type {
		case SubscriptionKey:
			ranges = append(ranges, syncRange{target: sub.Target, exact: true})
		case SubscriptionMailbox:
			ranges = append(ranges,
				syncRange{target: sub.Target + "/msg"},
				syncRange{target: sub.Target + "/share"},
			)
		case SubscriptionPrefix, SubscriptionService:
			ranges = append(ranges, syncRange{target: sub.Target})
		default:
			return nil, ErrInvalidRecord
		}
	}
	return compactSyncRanges(ranges), nil
}

func compactSyncRanges(ranges []syncRange) []syncRange {
	sort.Slice(ranges, func(a, b int) bool {
		if ranges[a].target == ranges[b].target {
			return !ranges[a].exact && ranges[b].exact
		}
		return ranges[a].target < ranges[b].target
	})
	out := make([]syncRange, 0, len(ranges))
	for _, candidate := range ranges {
		covered := false
		for _, existing := range out {
			if existing.exact {
				covered = candidate.exact && candidate.target == existing.target
			} else {
				covered = candidate.target == existing.target || strings.HasPrefix(candidate.target, existing.target+"/")
			}
			if covered {
				break
			}
		}
		if !covered {
			out = append(out, candidate)
		}
	}
	return out
}

func (i *Indexer) scanActiveSyncRangeLocked(r syncRange, seek []byte, limit, byteLimit int,
	height, now uint64, relayOnly, allowFirst bool) ([]*wire.DKVSRecord, []byte, bool, int, error) {
	if r.exact {
		if len(seek) != 0 {
			return nil, nil, true, 0, nil
		}
		record, err := i.getRaw(r.target)
		if errors.Is(err, ErrRecordNotFound) {
			return nil, nil, true, 0, nil
		}
		if err != nil {
			return nil, nil, false, 0, err
		}
		if i.activeError(record, height, now) != nil {
			return nil, nil, true, 0, nil
		}
		if relayOnly && i.isLocalOnlyRecord(record) {
			return nil, nil, true, 0, nil
		}
		size := wire.DKVSRecordSerializeSize(record)
		if byteLimit > 0 && size > byteLimit && !allowFirst {
			return nil, nil, false, 0, nil
		}
		return []*wire.DKVSRecord{record}, nil, true, size, nil
	}
	return i.scanSyncRangeLocked(r.target, seek, limit, byteLimit, height, now, relayOnly, allowFirst)
}

func (i *Indexer) scanSyncRangeLocked(prefix string, cursor []byte, limit, byteLimit int,
	height, now uint64, relayOnly, allowFirst bool) ([]*wire.DKVSRecord, []byte, bool, int, error) {
	scanPrefix := recordKeyPrefix
	normalizedPrefix := strings.TrimSuffix(prefix, "/")
	if prefix != "" {
		scanPrefix = recordDBKey(normalizedPrefix)
	}
	seek := cursor
	if len(seek) == 0 {
		seek = scanPrefix
	}
	records := make([]*wire.DKVSRecord, 0)
	var next, lastIncluded []byte
	used := 0
	done := true
	err := i.db.BatchReadV2(scanPrefix, seek, false, func(key, value []byte) error {
		if len(cursor) != 0 && bytes.Equal(key, cursor) {
			return nil
		}
		record, err := UnmarshalRecord(value)
		if err != nil {
			return err
		}
		if normalizedPrefix != "" && record.Key != normalizedPrefix && !strings.HasPrefix(record.Key, normalizedPrefix+"/") {
			return nil
		}
		if i.activeError(record, height, now) != nil ||
			(relayOnly && i.isLocalOnlyRecord(record)) {
			return nil
		}
		size := wire.DKVSRecordSerializeSize(record)
		if byteLimit > 0 && used+size > byteLimit && !(allowFirst && len(records) == 0) {
			done = false
			next = append([]byte{}, lastIncluded...)
			return errStopScan
		}
		records = append(records, record)
		used += size
		lastIncluded = append(lastIncluded[:0], key...)
		if limit > 0 && len(records) >= limit {
			done = false
			next = append([]byte{}, key...)
			return errStopScan
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}
	return records, next, done, used, err
}

func (i *Indexer) scanDeleteSyncRangeLocked(r syncRange, seek []byte, limit, byteLimit int,
	now uint64, relayOnly, allowFirst bool) ([]*wire.DKVSRecord, []byte, bool, int, error) {
	if r.exact {
		if len(seek) != 0 {
			return nil, nil, true, 0, nil
		}
		state, err := i.getDeleteStateLocked(r.target)
		if errors.Is(err, ErrRecordNotFound) || (err == nil && deleteRecordForRelay(state, now) == nil) {
			return nil, nil, true, 0, nil
		}
		if err != nil {
			return nil, nil, false, 0, err
		}
		if relayOnly && state.LocalOnly {
			return nil, nil, true, 0, nil
		}
		size := wire.DKVSRecordSerializeSize(state.Record)
		if byteLimit > 0 && size > byteLimit && !allowFirst {
			return nil, nil, false, 0, nil
		}
		return []*wire.DKVSRecord{state.Record}, nil, true, size, nil
	}
	scanPrefix := deleteKeyPrefix
	normalized := strings.TrimSuffix(r.target, "/")
	if normalized != "" {
		scanPrefix = deleteDBKey(normalized)
	}
	if len(seek) == 0 {
		seek = scanPrefix
	}
	records := make([]*wire.DKVSRecord, 0)
	var next, lastIncluded []byte
	used := 0
	done := true
	err := i.db.BatchReadV2(scanPrefix, seek, false, func(key, value []byte) error {
		if len(seek) != 0 && bytes.Equal(key, seek) && !bytes.Equal(seek, scanPrefix) {
			return nil
		}
		if len(key) < len(deleteKeyPrefix) {
			return nil
		}
		recordKey := string(key[len(deleteKeyPrefix):])
		if normalized != "" && recordKey != normalized && !strings.HasPrefix(recordKey, normalized+"/") {
			return nil
		}
		state, err := unmarshalDeleteState(value)
		if err != nil {
			return err
		}
		if state.Record != nil && (state.Record.Key != recordKey || state.Record.Seq > state.FloorSeq ||
			!bytesEqual(state.Record.PubKey, state.PubKey)) {
			return ErrInvalidRecord
		}
		if (relayOnly && state.LocalOnly) || deleteRecordForRelay(state, now) == nil {
			return nil
		}
		size := wire.DKVSRecordSerializeSize(state.Record)
		if byteLimit > 0 && used+size > byteLimit && !(allowFirst && len(records) == 0) {
			done = false
			next = append([]byte{}, lastIncluded...)
			return errStopScan
		}
		records = append(records, state.Record)
		used += size
		lastIncluded = append(lastIncluded[:0], key...)
		if limit > 0 && len(records) >= limit {
			done = false
			next = append([]byte{}, key...)
			return errStopScan
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}
	return records, next, done, used, err
}

func (i *Indexer) syncRanges(cursor []byte, limit uint32, ranges []syncRange,
	relayOnly, includeDeletesInRoot bool) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if limit == 0 || limit > wire.MaxDKVSRecordsPerMsg {
		limit = 100
	}
	decoded, err := decodeSyncCursor(cursor)
	if err != nil || decoded.rangeIndex > len(ranges) {
		return nil, nil, false, chainhash.Hash{}, ErrInvalidRecord
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	remaining := int(limit)
	// Reserve the sync response session ID and maximum varint overhead. The wire
	// encoder independently validates the same upper bound.
	remainingBytes := wire.MaxDKVSRecordsPayloadSize - 8 - wire.MaxVarIntPayload
	records := make([]*wire.DKVSRecord, 0, remaining)
	i.mutex.RLock()
	for decoded.rangeIndex < len(ranges) && remaining > 0 && remainingBytes > 0 {
		currentRange := ranges[decoded.rangeIndex]
		var page []*wire.DKVSRecord
		var next []byte
		var phaseDone bool
		var used int
		allowFirst := len(records) == 0
		if decoded.phase == syncPhaseRecords {
			page, next, phaseDone, used, err = i.scanActiveSyncRangeLocked(
				currentRange, decoded.seek, remaining, remainingBytes, height, now, relayOnly, allowFirst)
		} else {
			page, next, phaseDone, used, err = i.scanDeleteSyncRangeLocked(
				currentRange, decoded.seek, remaining, remainingBytes, now, relayOnly, allowFirst)
		}
		if err != nil {
			i.mutex.RUnlock()
			return nil, nil, false, chainhash.Hash{}, err
		}
		records = append(records, page...)
		remaining -= len(page)
		remainingBytes -= used
		if !phaseDone {
			if len(next) != 0 {
				decoded.seek = next
			}
			break
		}
		decoded.seek = nil
		if decoded.phase == syncPhaseRecords {
			decoded.phase = syncPhaseDeletes
		} else {
			decoded.phase = syncPhaseRecords
			decoded.rangeIndex++
		}
	}
	done := decoded.rangeIndex >= len(ranges)
	var nextCursor []byte
	if !done {
		nextCursor = encodeSyncCursor(decoded)
		if len(nextCursor) == 0 || len(nextCursor) > wire.MaxDKVSCursorSize || bytes.Equal(nextCursor, cursor) {
			i.mutex.RUnlock()
			return nil, nil, false, chainhash.Hash{}, ErrInvalidRecord
		}
	}
	rootRecords, err := i.syncViewRecordsForRangesLocked(ranges, height, now, relayOnly, includeDeletesInRoot)
	if err != nil {
		i.mutex.RUnlock()
		return nil, nil, false, chainhash.Hash{}, err
	}
	checkpoint, err := checkpointFromRecords(rootRecords, height)
	if err != nil {
		i.mutex.RUnlock()
		return nil, nil, false, chainhash.Hash{}, err
	}
	rootBytes, err := hexDecodeRoot(checkpoint.ActiveRecordRoot)
	if err != nil {
		i.mutex.RUnlock()
		return nil, nil, false, chainhash.Hash{}, err
	}
	var root chainhash.Hash
	copy(root[:], rootBytes)
	i.mutex.RUnlock()
	return records, nextCursor, done, root, nil
}

func (i *Indexer) filteredRoot(filters []Subscription, relayOnly, includeDeletes bool) (chainhash.Hash, error) {
	ranges, err := syncRangesForFilters(filters)
	if err != nil {
		return chainhash.Hash{}, err
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	records, err := i.syncViewRecordsForRangesLocked(ranges, height, now, relayOnly, includeDeletes)
	if err != nil {
		return chainhash.Hash{}, err
	}
	return recordsRoot(records, height)
}

// WaitFilteredForClient blocks until the client-visible active root covered by
// filters changes or the context ends.
func (i *Indexer) WaitFilteredForClient(ctx context.Context, filters []Subscription,
	knownRoot chainhash.Hash) (chainhash.Hash, bool, error) {

	if len(filters) == 0 {
		return chainhash.Hash{}, false, ErrInvalidRecord
	}
	root, err := i.filteredRoot(filters, false, true)
	if err != nil || root != knownRoot {
		return root, root != knownRoot, err
	}
	// Recompute the root on every tick rather than only when the mutation
	// generation changes. TTL expiry changes the active directory view without
	// writing to the database, and application watches must observe it.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			root, err = i.filteredRoot(filters, false, true)
			if err != nil || root != knownRoot {
				return root, root != knownRoot, err
			}
		case <-ctx.Done():
			root, err = i.filteredRoot(filters, false, true)
			if err != nil || root != knownRoot {
				return root, root != knownRoot, err
			}
			return root, false, ctx.Err()
		}
	}
}

func (i *Indexer) syncViewRecordsForRangesLocked(ranges []syncRange, height, now uint64,
	relayOnly, includeDeletes bool) ([]*wire.DKVSRecord, error) {
	records, err := i.activeRecordsForRangesLocked(ranges, height, now, relayOnly)
	if err != nil || !includeDeletes {
		return records, err
	}
	byKey := make(map[string]*wire.DKVSRecord, len(records))
	for _, record := range records {
		if record != nil {
			byKey[record.Key] = record
		}
	}
	for _, currentRange := range ranges {
		deletes, _, _, _, err := i.scanDeleteSyncRangeLocked(currentRange, nil, 0, 0, now, relayOnly, true)
		if err != nil {
			return nil, err
		}
		for _, record := range deletes {
			if record != nil {
				byKey[record.Key] = record
			}
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*wire.DKVSRecord, 0, len(keys))
	for _, key := range keys {
		out = append(out, byKey[key])
	}
	return out, nil
}

func (i *Indexer) activeRecordsForRangesLocked(ranges []syncRange, height, now uint64,
	relayOnly bool) ([]*wire.DKVSRecord, error) {
	records, err := i.recordsForRangesLocked(ranges, true, height, now)
	if err != nil {
		return nil, err
	}
	if relayOnly {
		return i.relayableRecords(records), nil
	}
	return records, nil
}

func (i *Indexer) recordsForRangesLocked(ranges []syncRange, activeOnly bool, height, now uint64) ([]*wire.DKVSRecord, error) {
	seen := make(map[string]*wire.DKVSRecord)
	for _, currentRange := range ranges {
		if currentRange.exact {
			record, err := i.getRaw(currentRange.target)
			if errors.Is(err, ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !activeOnly || i.activeError(record, height, now) == nil {
				seen[record.Key] = record
			}
			continue
		}
		records, _, _, err := i.scanLocked(currentRange.target, nil, 0, activeOnly, height, now)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			seen[record.Key] = record
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	records := make([]*wire.DKVSRecord, 0, len(keys))
	for _, key := range keys {
		records = append(records, seen[key])
	}
	return records, nil
}

func recordsRoot(records []*wire.DKVSRecord, height uint64) (chainhash.Hash, error) {
	ordered := append([]*wire.DKVSRecord{}, records...)
	sort.Slice(ordered, func(a, b int) bool {
		if ordered[a] == nil {
			return true
		}
		if ordered[b] == nil {
			return false
		}
		return ordered[a].Key < ordered[b].Key
	})
	checkpoint, err := checkpointFromRecords(ordered, height)
	if err != nil {
		return chainhash.Hash{}, err
	}
	rootBytes, err := hexDecodeRoot(checkpoint.ActiveRecordRoot)
	if err != nil {
		return chainhash.Hash{}, err
	}
	var root chainhash.Hash
	copy(root[:], rootBytes)
	return root, nil
}

func hexDecodeRoot(value string) ([]byte, error) {
	decoded := make([]byte, chainhash.HashSize)
	if len(value) != chainhash.HashSize*2 {
		return nil, ErrInvalidCheckpoint
	}
	for n := 0; n < len(decoded); n++ {
		hi, ok := fromHex(value[n*2])
		if !ok {
			return nil, ErrInvalidCheckpoint
		}
		lo, ok := fromHex(value[n*2+1])
		if !ok {
			return nil, ErrInvalidCheckpoint
		}
		decoded[n] = hi<<4 | lo
	}
	return decoded, nil
}

func fromHex(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

// ListActiveKeys returns the local active key set covered by the supplied
// subscriptions. It is used by authoritative mirror sync to remove local keys
// omitted by the source after the base scan completes.
func (i *Indexer) ListActiveKeys(filters []Subscription) ([]string, error) {
	ranges, err := syncRangesForFilters(filters)
	if err != nil {
		return nil, err
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	seen := make(map[string]struct{})
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	for _, currentRange := range ranges {
		if currentRange.exact {
			record, err := i.getRaw(currentRange.target)
			if errors.Is(err, ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if i.activeError(record, height, now) == nil {
				seen[record.Key] = struct{}{}
			}
			continue
		}
		records, _, _, err := i.scanLocked(currentRange.target, nil, 0, true, height, now)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			seen[record.Key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}
