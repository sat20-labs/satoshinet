package dkvs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"strings"

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

func (i *Indexer) scanActiveSyncRangeLocked(r syncRange, seek []byte, limit int, height, now uint64) ([]*wire.DKVSRecord, []byte, bool, error) {
	if r.exact {
		if len(seek) != 0 {
			return nil, nil, true, nil
		}
		record, err := i.getRaw(r.target)
		if errors.Is(err, ErrRecordNotFound) {
			return nil, nil, true, nil
		}
		if err != nil {
			return nil, nil, false, err
		}
		if i.activeError(record, height, now) != nil {
			return nil, nil, true, nil
		}
		return []*wire.DKVSRecord{record}, nil, true, nil
	}
	return i.scanLocked(r.target, seek, limit, true, height, now)
}

func (i *Indexer) scanDeleteSyncRangeLocked(r syncRange, seek []byte, limit int, now uint64) ([]*wire.DKVSRecord, []byte, bool, error) {
	if r.exact {
		if len(seek) != 0 {
			return nil, nil, true, nil
		}
		state, err := i.getDeleteStateLocked(r.target)
		if errors.Is(err, ErrRecordNotFound) || (err == nil && deleteRecordForRelay(state, now) == nil) {
			return nil, nil, true, nil
		}
		if err != nil {
			return nil, nil, false, err
		}
		return []*wire.DKVSRecord{state.Record}, nil, true, nil
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
	var next []byte
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
		if deleteRecordForRelay(state, now) == nil {
			return nil
		}
		records = append(records, state.Record)
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
	return records, next, done, err
}

func (i *Indexer) syncRanges(cursor []byte, limit uint32, ranges []syncRange) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
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
	records := make([]*wire.DKVSRecord, 0, remaining)
	i.mutex.RLock()
	for decoded.rangeIndex < len(ranges) && remaining > 0 {
		currentRange := ranges[decoded.rangeIndex]
		var page []*wire.DKVSRecord
		var next []byte
		var phaseDone bool
		if decoded.phase == syncPhaseRecords {
			page, next, phaseDone, err = i.scanActiveSyncRangeLocked(currentRange, decoded.seek, remaining, height, now)
		} else {
			page, next, phaseDone, err = i.scanDeleteSyncRangeLocked(currentRange, decoded.seek, remaining, now)
		}
		if err != nil {
			i.mutex.RUnlock()
			return nil, nil, false, chainhash.Hash{}, err
		}
		records = append(records, page...)
		remaining -= len(page)
		if !phaseDone {
			decoded.seek = next
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
		if len(nextCursor) == 0 || len(nextCursor) > wire.MaxDKVSCursorSize {
			i.mutex.RUnlock()
			return nil, nil, false, chainhash.Hash{}, ErrInvalidRecord
		}
	}
	i.mutex.RUnlock()
	checkpoint, err := i.Checkpoint()
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	rootBytes, err := hexDecodeRoot(checkpoint.ActiveRecordRoot)
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	var root chainhash.Hash
	copy(root[:], rootBytes)
	return records, nextCursor, done, root, nil
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
