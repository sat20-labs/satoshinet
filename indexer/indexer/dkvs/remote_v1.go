package dkvs

import (
	"errors"

	"github.com/sat20-labs/satoshinet/wire"
)

func (i *Indexer) pathStateForRecordLocked(record *wire.DKVSRecord) (*PathMeta, *PathLocalStatus, *wire.DKVSRecord, *deleteState, error) {
	if record == nil {
		return nil, nil, nil, nil, ErrInvalidRecord
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	path := collectionPath(parsed)
	if path == "" || !isCanonicalCollectionPath(path) {
		return nil, nil, nil, nil, ErrInvalidKey
	}
	meta, err := i.ensurePathMetaLocked(path, i.currentHeight(), currentUnixMilli())
	if err != nil {
		return nil, nil, nil, nil, err
	}
	status, err := i.readPathStatusLocked(path)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	existing, err := i.getRaw(record.Key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return nil, nil, nil, nil, err
	}
	if errors.Is(err, ErrRecordNotFound) {
		existing = nil
	}
	floor, err := i.getDeleteStateLocked(record.Key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return nil, nil, nil, nil, err
	}
	if errors.Is(err, ErrRecordNotFound) {
		floor = nil
	}
	return meta, status, existing, floor, nil
}

func exactStoredRecord(record, existing *wire.DKVSRecord, floor *deleteState) bool {
	if record == nil {
		return false
	}
	want := RecordHash(record)
	if existing != nil && RecordHash(existing) == want {
		return true
	}
	return floor != nil && floor.effectiveHash(record.Key) == want
}

func (i *Indexer) setPathStaleLocked(path, peer, retryState string) error {
	status, err := i.readPathStatusLocked(path)
	if err != nil {
		return err
	}
	status.Stale = true
	status.LastSyncPeer = peer
	status.LocalRetryState = retryState
	status.UpdatedAt = currentUnixMilli()
	encoded, err := marshalPathStatus(status)
	if err != nil {
		return err
	}
	return i.db.Write(pathStatusDBKey(path), encoded)
}

// PutRemoteV1 applies a relayable record according to its signed path
// generation. It never derives a generation from arrival order.
func (i *Indexer) PutRemoteV1(record *wire.DKVSRecord, peer string) (bool, error) {
	record = cloneRecord(record)
	if record == nil {
		return false, ErrInvalidRecord
	}
	if err := validateRelayableExpiry(record); err != nil {
		return false, err
	}
	if _, err := validateParsedCoreWithVerifier(
		record, i.currentHeight(), currentUnixMilli(), true, false, nil,
	); err != nil {
		return false, err
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return false, err
	}
	path := collectionPath(parsed)
	if path == "" || pathMode(parsed) == PathLocalOnly {
		return false, ErrFreeLocalNotRelayable
	}

	i.mutex.Lock()
	meta, status, existing, floor, err := i.pathStateForRecordLocked(record)
	if err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if exactStoredRecord(record, existing, floor) {
		i.mutex.Unlock()
		return false, nil
	}
	if record.PathGeneration > meta.Generation+1 || meta.Generation == ^uint64(0) {
		_ = i.setPathStaleLocked(path, peer, "generation_gap")
		i.mutex.Unlock()
		return false, ErrPathGenerationGap
	}
	if status.Stale && record.PathGeneration == meta.Generation+1 {
		i.mutex.Unlock()
		return false, ErrStaleEndpoint
	}
	if record.PathGeneration <= meta.Generation {
		var current *wire.DKVSRecord
		if existing != nil {
			current = existing
		} else if floor != nil {
			current = floor.Record
		}
		if current == nil {
			// The visible state for this generation was compacted, expired or is
			// otherwise incomplete. A single record cannot safely repair it.
			_ = i.setPathStaleLocked(path, peer, "same_generation_missing_state")
			i.mutex.Unlock()
			return false, ErrPathDiverged
		}
		if CompareRecords(current, record) >= 0 {
			i.mutex.Unlock()
			return false, nil
		}
		if record.PathGeneration != current.PathGeneration {
			_ = i.setPathStaleLocked(path, peer, "historical_generation_conflict")
			i.mutex.Unlock()
			return false, ErrPathDiverged
		}
	}
	i.mutex.Unlock()

	updated, _, _, _, err := i.put(record, true)
	if err != nil {
		return false, err
	}
	return updated, nil
}
