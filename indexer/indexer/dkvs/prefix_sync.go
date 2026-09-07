package dkvs

import (
	"sort"
	"strings"

	"github.com/sat20-labs/satoshinet/wire"
)

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

// PrefixSnapshot returns every current record for exactly one collection path
// on this endpoint, including FREE_LOCAL, together with the existing
// PathMeta.EndpointGeneration sampled under the same DKVS lock.
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
	totalBytes := 0
	for _, record := range records {
		if record == nil {
			continue
		}
		if len(active) >= MaxPrefixReadRecords {
			return nil, ErrBatchTooLarge
		}
		size := RecordSize(record)
		if size > MaxPrefixReadBytes-totalBytes {
			return nil, ErrBatchTooLarge
		}
		totalBytes += size
		active = append(active, cloneRecord(record))
	}
	sort.Slice(active, func(a, b int) bool { return active[a].Key < active[b].Key })
	states := make([]DKVSKeyState, 0, len(active))
	for _, record := range active {
		state, stateErr := i.keyStateLocked(record.Key, false, height, now)
		if stateErr != nil {
			return nil, stateErr
		}
		states = append(states, state)
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
