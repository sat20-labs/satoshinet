package dkvs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
	"strings"

	"github.com/sat20-labs/satoshinet/wire"
)

// PrefixDelta reads current values and explicit deleted key states indexed
// after the caller's endpoint-local generation. Both share the PathMeta lock.
func (i *Indexer) PrefixDelta(prefix, endpointID string, after uint64) (*PrefixDeltaResult, error) {
	if i == nil {
		return nil, ErrInvalidRecord
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if !isCanonicalCollectionPath(prefix) {
		return nil, ErrInvalidKey
	}
	parsed, err := ParsePrefix(prefix)
	if err != nil || pathMode(parsed) == PathLocalOnly {
		return nil, ErrInvalidKey
	}
	if endpointID == "" || i.endpointID() == "" {
		return nil, ErrStaleEndpoint
	}
	if endpointID != i.endpointID() {
		return nil, ErrEndpointMismatch
	}
	height, now := i.currentHeight(), currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	meta, err := i.ensurePathMetaLocked(prefix, height, now)
	if err != nil {
		return nil, err
	}
	if after > meta.EndpointGeneration {
		return nil, ErrStaleGeneration
	}
	baseline, baselineFound, err := i.prefixChangeBaselineLocked(prefix)
	if err != nil {
		return nil, err
	}
	if !baselineFound {
		if _, err := i.ensurePrefixChangeBaselineLocked(prefix, meta.EndpointGeneration); err != nil {
			return nil, err
		}
		return nil, ErrStaleGeneration
	}
	if after < baseline {
		return nil, ErrStaleGeneration
	}
	result := &PrefixDeltaResult{EndpointID: endpointID, Prefix: prefix,
		Generation: meta.EndpointGeneration, ViewHeight: height,
		Records: make([]*wire.DKVSRecord, 0)}
	if after == meta.EndpointGeneration {
		return result, nil
	}
	base := prefixChangeDBPrefix(prefix)
	seek := append([]byte{}, base...)
	var next [8]byte
	binary.BigEndian.PutUint64(next[:], after+1)
	seek = append(seek, next[:]...)
	totalBytes := 0
	err = i.db.BatchReadV2(base, seek, false, func(indexKey, _ []byte) error {
		if !bytes.HasPrefix(indexKey, base) || len(indexKey) < len(base)+8 {
			return ErrInvalidRecord
		}
		generation := binary.BigEndian.Uint64(indexKey[len(base) : len(base)+8])
		if generation <= after {
			return nil
		}
		if generation > meta.EndpointGeneration {
			return errStopScan
		}
		key := string(indexKey[len(base)+8:])
		if key == "" {
			return ErrInvalidRecord
		}
		current, found, readErr := i.changedGenerationLocked(key)
		if readErr != nil {
			return readErr
		}
		if !found || current != generation {
			return nil
		}
		// A tombstone is stored as a delete floor, not a live record. Looking
		// up getRaw first would silently skip it while advancing the cursor.
		state, stateErr := i.keyStateLocked(key, false, height, now)
		if stateErr != nil {
			return stateErr
		}
		if state.Status == KeyStateNeverSeen {
			return nil
		}
		if state.Status == KeyStateDeleted {
			return appendPrefixKeyState(&result.KeyStates, &totalBytes, state)
		}
		record, readErr := i.getRaw(key)
		if readErr != nil {
			return readErr
		}
		size := RecordSize(record)
		if size > MaxPrefixReadBytes-totalBytes {
			return ErrBatchTooLarge
		}
		totalBytes += size
		if err := appendPrefixKeyState(&result.KeyStates, &totalBytes, state); err != nil {
			return err
		}
		result.Records = append(result.Records, cloneRecord(record))
		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(result.Records, func(a, b int) bool { return result.Records[a].Key < result.Records[b].Key })
	sort.Slice(result.KeyStates, func(a, b int) bool { return result.KeyStates[a].Key < result.KeyStates[b].Key })
	return result, nil
}

func normalizePrefixGenerations(values []PrefixGeneration) ([]PrefixGeneration, error) {
	if len(values) == 0 || len(values) > MaxPrefixesPerTerminal {
		return nil, ErrTooManySubscriptions
	}
	byPrefix := make(map[string]uint64, len(values))
	for _, value := range values {
		prefix := strings.TrimSuffix(strings.TrimSpace(value.Prefix), "/")
		if prefix == "" || len(prefix) > MaxPrefixLength || !isCanonicalCollectionPath(prefix) {
			return nil, ErrInvalidKey
		}
		parsed, err := ParsePrefix(prefix)
		if err != nil || pathMode(parsed) == PathLocalOnly {
			return nil, ErrInvalidKey
		}
		if _, duplicate := byPrefix[prefix]; duplicate {
			return nil, ErrInvalidKey
		}
		byPrefix[prefix] = value.Generation
	}
	result := make([]PrefixGeneration, 0, len(byPrefix))
	for prefix, generation := range byPrefix {
		result = append(result, PrefixGeneration{Prefix: prefix, Generation: generation})
	}
	sort.Slice(result, func(a, b int) bool { return result[a].Prefix < result[b].Prefix })
	return result, nil
}

// PrefixStatus compares client generations with PathMeta.EndpointGeneration.
// It performs no hashing and keeps no client session, cursor, waiter or change
// log. FREE_LOCAL changes are included on their accepting endpoint.
func (i *Indexer) PrefixStatus(endpointID string, known []PrefixGeneration) (*PrefixStatusResult, error) {
	if i == nil {
		return nil, ErrInvalidRecord
	}
	endpointID = strings.TrimSpace(endpointID)
	currentEndpoint := i.endpointID()
	if endpointID == "" || currentEndpoint == "" {
		return nil, ErrStaleEndpoint
	}
	if endpointID != currentEndpoint {
		return nil, ErrEndpointMismatch
	}
	values, err := normalizePrefixGenerations(known)
	if err != nil {
		return nil, err
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	result := &PrefixStatusResult{EndpointID: currentEndpoint, ViewHeight: height}
	for _, value := range values {
		meta, metaErr := i.ensurePathMetaLocked(value.Prefix, height, now)
		if metaErr != nil {
			return nil, metaErr
		}
		if meta.EndpointGeneration != value.Generation {
			result.Changed = append(result.Changed, PrefixGeneration{
				Prefix: value.Prefix, Generation: meta.EndpointGeneration,
			})
		}
	}
	return result, nil
}

// PrefixSnapshot returns current values (including FREE_LOCAL) and retained
// deletion floors for one collection under the same endpoint-generation lock.
func (i *Indexer) PrefixSnapshot(prefix string) (*PrefixSnapshot, error) {
	if i == nil {
		return nil, ErrInvalidRecord
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if !isCanonicalCollectionPath(prefix) {
		return nil, ErrInvalidKey
	}
	parsed, err := ParsePrefix(prefix)
	if err != nil || pathMode(parsed) == PathLocalOnly {
		return nil, ErrInvalidKey
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	meta, err := i.ensurePathMetaLocked(prefix, height, now)
	if err != nil {
		return nil, err
	}
	records, _, _, err := i.scanLocked(prefix, nil, MaxPrefixReadRecords+1, true, height, now)
	if err != nil {
		return nil, err
	}
	active := make([]*wire.DKVSRecord, 0, len(records))
	states := make([]DKVSKeyState, 0, len(records))
	totalBytes := 0
	for _, record := range records {
		if record == nil {
			continue
		}
		size := RecordSize(record)
		if size > MaxPrefixReadBytes-totalBytes {
			return nil, ErrBatchTooLarge
		}
		totalBytes += size
		state, stateErr := i.keyStateLocked(record.Key, false, height, now)
		if stateErr != nil {
			return nil, stateErr
		}
		if err := appendPrefixKeyState(&states, &totalBytes, state); err != nil {
			return nil, err
		}
		active = append(active, cloneRecord(record))
	}
	if err := i.appendPrefixDeleteStatesLocked(prefix, height, now, &states, &totalBytes); err != nil {
		return nil, err
	}
	sort.Slice(active, func(a, b int) bool { return active[a].Key < active[b].Key })
	sort.Slice(states, func(a, b int) bool { return states[a].Key < states[b].Key })
	if _, err := i.ensurePrefixChangeBaselineLocked(prefix, meta.EndpointGeneration); err != nil {
		return nil, err
	}
	return &PrefixSnapshot{
		EndpointID: i.endpointID(), Prefix: prefix, Generation: meta.EndpointGeneration,
		ViewHeight: height, Records: active, KeyStates: states,
	}, nil
}

// ReadPrefix performs a stateless direct read for an aggregate or read-only
// prefix. Unlike PrefixSnapshot it is not a managed-prefix synchronization
// primitive and therefore exposes neither PathMeta.Generation nor a cursor.
func (i *Indexer) ReadPrefix(prefix string) (*PrefixReadResult, error) {
	if i == nil {
		return nil, ErrInvalidRecord
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if len(prefix) > MaxPrefixLength {
		return nil, ErrInvalidKey
	}
	if err := validateReadablePrefix(prefix); err != nil {
		return nil, err
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	records, _, _, err := i.scanLocked(prefix, nil, MaxPrefixReadRecords+1, true, height, now)
	if err != nil {
		return nil, err
	}
	if len(records) > MaxPrefixReadRecords {
		return nil, ErrBatchTooLarge
	}
	totalBytes := 0
	active := make([]*wire.DKVSRecord, 0, len(records))
	states := make([]DKVSKeyState, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		size := RecordSize(record)
		if size > MaxPrefixReadBytes-totalBytes {
			return nil, ErrBatchTooLarge
		}
		totalBytes += size
		active = append(active, cloneRecord(record))
		state, stateErr := i.keyStateLocked(record.Key, false, height, now)
		if stateErr != nil {
			return nil, stateErr
		}
		states = append(states, state)
	}
	return &PrefixReadResult{
		EndpointID: i.endpointID(), Prefix: prefix, ViewHeight: height,
		Records: active, KeyStates: states,
	}, nil
}
