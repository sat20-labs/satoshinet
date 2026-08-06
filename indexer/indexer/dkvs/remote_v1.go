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

// PutRemoteV1 treats a relayable record as a path-change notification.
// PathMeta.Generation is not carried by the record, so any non-idempotent
// update must be resolved by the authenticated full path snapshot flow.
func (i *Indexer) PutRemoteV1(record *wire.DKVSRecord, peer string) (bool, error) {
	record = cloneRecord(record)
	if record == nil {
		return false, ErrInvalidRecord
	}
	if err := validateRelayableExpiry(record); err != nil {
		return false, err
	}
	if _, err := validateParsedCoreWithVerifier(
		record, i.currentHeight(), true, false, nil,
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
	_, _, existing, floor, err := i.pathStateForRecordLocked(record)
	if err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if exactStoredRecord(record, existing, floor) {
		i.mutex.Unlock()
		return false, nil
	}
	_ = i.setPathStaleLocked(path, peer, "path_snapshot_required")
	i.mutex.Unlock()
	return false, ErrPathDiverged
}
