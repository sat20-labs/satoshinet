package dkvs

import (
	"errors"

	"github.com/sat20-labs/satoshinet/wire"
)

func (i *Indexer) pathStateForRecordLocked(record *wire.DKVSRecord) (*PathMeta, *PathLocalStatus, *wire.DKVSRecord, error) {
	if record == nil { return nil, nil, nil, ErrInvalidRecord }
	parsed, err := ParseKey(record.Key)
	if err != nil { return nil, nil, nil, err }
	path := collectionPath(parsed)
	if path == "" || !isCanonicalCollectionPath(path) { return nil, nil, nil, ErrInvalidKey }
	meta, err := i.ensurePathMetaLocked(path, i.currentHeight(), currentUnixMilli())
	if err != nil { return nil, nil, nil, err }
	status, err := i.readPathStatusLocked(path)
	if err != nil { return nil, nil, nil, err }
	existing, err := i.getRaw(record.Key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) { return nil, nil, nil, err }
	return meta, status, existing, nil
}

func exactStoredRecord(record, existing *wire.DKVSRecord) bool {
	return record != nil && existing != nil && RecordHash(existing) == RecordHash(record)
}

func (i *Indexer) setPathStaleLocked(path, peer, retryState string) error {
	status, err := i.readPathStatusLocked(path)
	if err != nil { return err }
	status.Stale = true
	status.LastSyncPeer = peer
	status.LocalRetryState = retryState
	status.UpdatedAt = currentUnixMilli()
	encoded, err := marshalPathStatus(status)
	if err != nil { return err }
	return i.db.Write(pathStatusDBKey(path), encoded)
}

// AcceptRemoteRecord accepts an unsequenced inventory hint, not authority to
// install or resurrect a record. Live authenticated P2P notifications use
// AcceptCurrentRecord; source-bound reconciliation resolves differing hints.
func (i *Indexer) AcceptRemoteRecord(record *wire.DKVSRecord, peer string) (bool, error) {
	record = cloneRecord(record)
	if record == nil { return false, ErrInvalidRecord }
	if err := validateRelayableExpiry(record); err != nil { return false, err }
	parsed, err := validateParsedCoreWithVerifier(record, i.currentHeight(), true, false, nil)
	if err != nil { return false, err }
	if parsed.Namespace == "mail" { return false, ErrFreeLocalNotRelayable }
	path := collectionPath(parsed)
	if path == "" || pathMode(parsed) == PathLocalOnly { return false, ErrFreeLocalNotRelayable }
	i.mutex.Lock()
	defer i.mutex.Unlock()
	_, _, existing, err := i.pathStateForRecordLocked(record)
	if err != nil { return false, err }
	if exactStoredRecord(record, existing) { return false, nil }
	if err := i.setPathStaleLocked(path, peer, "current_state_reconciliation_required"); err != nil { return false, err }
	return false, ErrPathDiverged
}
