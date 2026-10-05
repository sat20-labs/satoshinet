package dkvs

import (
	"context"
	"errors"
	"sync/atomic"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

// Originless installation only initializes empty state or verifies equality.
// Authenticated P2P callers bind every page to one sync session/source and
// provide the receiver-local baseline captured before downloading the snapshot.
func (i *Indexer) ApplyPathSnapshot(snapshot *PathSnapshot) (int, error) {
	return i.applyCurrentPathSnapshot(snapshot, nil)
}

func (i *Indexer) ApplyPathSnapshotFrom(snapshot *PathSnapshot, baseline ActiveMeta) (int, error) {
	if snapshot == nil || baseline.Scope.Prefix != snapshot.Path ||
		!baseline.Scope.Network || len(baseline.Scope.Keys) != 0 {
		return 0, ErrInvalidSnapshot
	}
	return i.applyCurrentPathSnapshot(snapshot, &baseline)
}

func (i *Indexer) applyCurrentPathSnapshot(snapshot *PathSnapshot, baseline *ActiveMeta) (int, error) {
	validated, err := i.validatePathSnapshot(clonePathSnapshot(snapshot))
	if err != nil {
		return 0, err
	}

	i.mutex.Lock()
	committed, changed := false, false
	defer func() {
		i.mutex.Unlock()
		if committed && changed {
			i.notifyPath(validated.path)
		}
	}()

	if atomic.LoadUint64(&i.policyGeneration) != validated.policyVersion {
		return 0, ErrConcurrentUpdate
	}
	height, now := validated.meta.ViewHeight, currentUnixMilli()

	// Only receiver-local concurrent changes matter here. Remote source
	// generations are never compared across peers and no source ownership is
	// persisted after the sync session completes.
	if baseline != nil {
		if baseline.EndpointID != i.endpointID() {
			return 0, ErrEndpointMismatch
		}
		current, err := i.activeMetaLocked(context.Background(), baseline.Scope, baseline.ViewHeight)
		if err != nil {
			return 0, err
		}
		if current.Generation != baseline.Generation || current.Root != baseline.Root {
			return 0, ErrConcurrentUpdate
		}
	}

	previous, err := i.ensurePathMetaLocked(validated.path, i.currentHeight(), now)
	if err != nil {
		return 0, err
	}
	if baseline == nil && previous.Generation != 0 && previous.StateRoot != validated.meta.StateRoot {
		return 0, ErrStaleEndpoint
	}

	current, _, _, err := i.scanLocked(validated.path, nil, 0, false, height, now)
	if err != nil {
		return 0, err
	}
	incoming := make(map[string]*wire.DKVSRecord, len(validated.active))
	for _, record := range validated.active {
		incoming[record.Key] = record
	}
	// Snapshot omissions replace only the network view. Placement-bound data
	// and unpaid AUTOPAY retention stay local until their existing prune path
	// removes them. An incoming record for the same key can still replace them.
	// Capture the visibility decision once: retention refresh can run while
	// this installation holds the indexer lock.
	replaceable := current[:0]
	for _, record := range current {
		if incoming[record.Key] != nil || !i.isLocalOnlyRecord(record) {
			replaceable = append(replaceable, record)
		}
	}
	current = replaceable
	oldByKey := make(map[string]*wire.DKVSRecord, len(current))
	for _, record := range current {
		oldByKey[record.Key] = record
		next := incoming[record.Key]
		if next == nil {
			changed = true
		}
		if next != nil && RecordHash(next) != RecordHash(record) {
			changed = true
		}
	}
	for _, record := range validated.active {
		if oldByKey[record.Key] == nil {
			changed = true
		}
	}

	// A complete, signed current view replaces the cached network view as a
	// set. Old per-key Seq/IssueHeight values are not lifetime floors: the
	// source may have deleted and recreated a key while this node was offline.
	meta := clonePathMeta(validated.meta)
	meta.Generation, meta.EndpointGeneration = previous.Generation, previous.EndpointGeneration
	if changed {
		if meta.Generation == ^uint64(0) || meta.EndpointGeneration == ^uint64(0) {
			return 0, ErrStaleGeneration
		}
		meta.Generation++
		meta.EndpointGeneration++
	}

	batch := i.db.NewWriteBatch()
	defer batch.Close()
	removed := make([]string, 0)

	for _, record := range current {
		next := incoming[record.Key]
		if next != nil && RecordHash(next) == RecordHash(record) {
			continue
		}
		if err := batch.Delete(recordDBKey(record.Key)); err != nil {
			return 0, err
		}
		if err := batch.Delete(hashDBKey(RecordHash(record))); err != nil {
			return 0, err
		}
		if err := i.clearChangedRecordBatch(batch, validated.path, record.Key); err != nil {
			return 0, err
		}
		removed = append(removed, record.Key)
	}

	for _, record := range validated.active {
		old := oldByKey[record.Key]
		if old != nil && RecordHash(old) == RecordHash(record) {
			continue
		}
		encoded, err := MarshalRecord(record)
		if err != nil {
			return 0, err
		}
		if err := batch.Put(recordDBKey(record.Key), encoded); err != nil {
			return 0, err
		}
		if err := batch.Put(hashDBKey(RecordHash(record)), []byte(record.Key)); err != nil {
			return 0, err
		}
		if err := i.markChangedRecordBatch(batch, validated.path, record.Key, meta.EndpointGeneration); err != nil {
			return 0, err
		}
	}

	if err := putPathMetaBatch(batch, meta); err != nil {
		return 0, err
	}
	if err := putPathStatusBatch(batch, &PathLocalStatus{
		Path: validated.path, UpdatedAt: now, LastSyncAt: now,
	}); err != nil {
		return 0, err
	}
	if err := batch.Flush(); err != nil {
		return 0, err
	}

	committed = true
	if changed {
		atomic.AddUint64(&i.generation, 1)
	}
	i.resetFeeUsageLocked()
	i.resetFreeLocalUsageLocked()
	i.resetRecordExpiryLocked()
	cache := paidRetentionCacheFor(i)
	cache.remove(removed)
	for _, record := range validated.active {
		if retention := validated.retentions[record.Key]; retention != nil {
			cache.set(record.Key, *retention)
		}
	}
	return len(validated.active), nil
}

func (i *Indexer) markPathStale(path, peer string, retryState string) error {
	path = stringsTrimPath(path)
	if !isCanonicalCollectionPath(path) {
		return ErrInvalidKey
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return i.setPathStaleLocked(path, peer, retryState)
}

func replacePathStatusBatch(batch indexercommon.WriteBatch, path string, now uint64) error {
	return putPathStatusBatch(batch, &PathLocalStatus{Path: path, UpdatedAt: now, LastSyncAt: now})
}

func isNotFound(err error) bool { return errors.Is(err, ErrRecordNotFound) }
