package dkvs

import (
	"errors"
	"sync/atomic"

	indexercommon "github.com/sat20-labs/indexer/common"
)

func (i *Indexer) ApplyPathSnapshot(snapshot *PathSnapshot) (int, error) {
	validated, err := i.validatePathSnapshot(clonePathSnapshot(snapshot))
	if err != nil {
		return 0, err
	}
	i.mutex.Lock()
	changed := false
	committed := false
	defer func() {
		i.mutex.Unlock()
		if committed && changed {
			i.notifyPath(validated.path)
		}
	}()
	if atomic.LoadUint64(&i.policyGeneration) != validated.policyVersion {
		return 0, ErrConcurrentUpdate
	}
	height := validated.meta.ViewHeight
	now := currentUnixMilli()
	previous, _ := i.readPathMetaLocked(validated.path)
	if previous == nil {
		previous, _ = i.computePathMetaViewLocked(validated.path, i.currentHeight(), now)
	} else {
		status, statusErr := i.readPathStatusLocked(validated.path)
		if statusErr != nil || status.Dirty {
			previous, _ = i.computePathMetaViewLocked(validated.path, previous.ViewHeight, now)
		}
	}
	changed = previous == nil || previous.Generation != validated.meta.Generation ||
		previous.StateRoot != validated.meta.StateRoot || previous.ViewHeight != validated.meta.ViewHeight
	endpointGeneration := uint64(0)
	if previous != nil {
		endpointGeneration = previous.EndpointGeneration
	}
	if changed {
		if endpointGeneration == ^uint64(0) {
			return 0, ErrStaleGeneration
		}
		endpointGeneration++
	}
	validated.meta.EndpointGeneration = endpointGeneration
	current, _, _, err := i.scanLocked(validated.path, nil, 0, false, height, now)
	if err != nil {
		return 0, err
	}
	currentFloors, err := i.scanPathDeleteStatesLocked(validated.path)
	if err != nil {
		return 0, err
	}
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	retentionRemovals := make([]string, 0, len(current))
	for _, record := range current {
		if record == nil || isFreeLocalRecord(record) {
			continue
		}
		if err := batch.Delete(recordDBKey(record.Key)); err != nil {
			return 0, err
		}
		if err := batch.Delete(hashDBKey(RecordHash(record))); err != nil {
			return 0, err
		}
		retentionRemovals = append(retentionRemovals, record.Key)
	}
	for key, state := range currentFloors {
		if state != nil && state.LocalOnly {
			continue
		}
		if err := deleteDeleteStateBatch(batch, key); err != nil {
			return 0, err
		}
	}
	applied := 0
	for _, record := range validated.active {
		encoded, err := MarshalRecord(record)
		if err != nil {
			return 0, err
		}
		hash := RecordHash(record)
		if err := batch.Put(recordDBKey(record.Key), encoded); err != nil {
			return 0, err
		}
		if err := batch.Put(hashDBKey(hash), []byte(record.Key)); err != nil {
			return 0, err
		}
		applied++
	}
	for _, floor := range validated.floors {
		state := &deleteState{
			FloorSeq: floor.FloorSeq, PathGeneration: floor.PathGeneration,
			PubKey: append([]byte(nil), floor.PubKey...), EffectiveHash: floor.EffectiveHash,
		}
		if err := putDeleteStateBatch(batch, floor.Key, state); err != nil {
			return 0, err
		}
	}
	if err := putPathMetaBatch(batch, validated.meta); err != nil {
		return 0, err
	}
	if err := putPathStatusBatch(batch, &PathLocalStatus{
		Path: validated.path, UpdatedAt: now, LastSyncAt: now, Dirty: false, Stale: false,
	}); err != nil {
		return 0, err
	}
	if err := batch.Flush(); err != nil {
		return 0, err
	}
	committed = true
	i.resetFeeUsageLocked()
	i.resetFreeLocalUsageLocked()
	i.resetRecordExpiryLocked()
	atomic.AddUint64(&i.generation, 1)
	retentionCache := paidRetentionCacheFor(i)
	retentionCache.remove(retentionRemovals)
	for _, record := range validated.active {
		if retention := validated.retentions[record.Key]; retention != nil {
			retentionCache.set(record.Key, *retention)
		}
	}
	return applied, nil
}

func (i *Indexer) markPathStale(path, peer string, retryState string) error {
	path = stringsTrimPath(path)
	if !isCanonicalCollectionPath(path) {
		return ErrInvalidKey
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
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

func replacePathStatusBatch(batch indexercommon.WriteBatch, path string, now uint64) error {
	return putPathStatusBatch(batch, &PathLocalStatus{Path: path, UpdatedAt: now, LastSyncAt: now})
}

func isNotFound(err error) bool {
	return errors.Is(err, ErrRecordNotFound)
}
