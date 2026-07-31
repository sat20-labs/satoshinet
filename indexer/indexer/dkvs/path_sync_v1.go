package dkvs

import (
	"errors"
	"sort"
	"sync/atomic"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type validatedPathSnapshot struct {
	path          string
	meta          *PathMeta
	active        []*wire.DKVSRecord
	tombstones    []*wire.DKVSRecord
	floors        []DeleteFloor
	serverTimeMS  uint64
	policyVersion uint64
}

func cloneDeleteFloor(floor DeleteFloor) DeleteFloor {
	floor.PubKey = append([]byte(nil), floor.PubKey...)
	return floor
}

func clonePathSnapshot(snapshot *PathSnapshot) *PathSnapshot {
	if snapshot == nil {
		return nil
	}
	cloned := &PathSnapshot{
		Path:         snapshot.Path,
		PathMeta:     clonePathMeta(snapshot.PathMeta),
		ServerTimeMS: snapshot.ServerTimeMS,
		Records:      make([]*wire.DKVSRecord, 0, len(snapshot.Records)),
		DeleteFloors: make([]DeleteFloor, 0, len(snapshot.DeleteFloors)),
	}
	for _, record := range snapshot.Records {
		cloned.Records = append(cloned.Records, cloneRecord(record))
	}
	for _, floor := range snapshot.DeleteFloors {
		cloned.DeleteFloors = append(cloned.DeleteFloors, cloneDeleteFloor(floor))
	}
	return cloned
}

func (i *Indexer) GetPathSnapshot(path string) (*PathSnapshot, error) {
	path = stringsTrimPath(path)
	if !isCanonicalCollectionPath(path) {
		return nil, ErrInvalidKey
	}
	parsed, err := ParsePrefix(path)
	if err != nil {
		return nil, err
	}
	if pathMode(parsed) == PathLocalOnly {
		return nil, ErrFreeLocalNotRelayable
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return nil, err
	}
	active, _, _, err := i.scanLocked(path, nil, 0, true, height, now)
	if err != nil {
		return nil, err
	}
	states, err := i.scanPathDeleteStatesLocked(path)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	records := make([]*wire.DKVSRecord, 0, len(active)+len(states))
	for _, record := range active {
		if record != nil && !i.isLocalOnlyRecord(record) {
			records = append(records, cloneRecord(record))
		}
	}
	floors := make([]DeleteFloor, 0, len(states))
	for _, key := range keys {
		state := states[key]
		if state.Record != nil && deleteRecordForRelay(state, now) != nil {
			records = append(records, cloneRecord(state.Record))
			continue
		}
		floors = append(floors, state.publicFloor(key))
	}
	sort.Slice(records, func(a, b int) bool {
		if records[a].Key == records[b].Key {
			return CompareRecords(records[a], records[b]) < 0
		}
		return records[a].Key < records[b].Key
	})
	return &PathSnapshot{
		Path:         path,
		PathMeta:     clonePathMeta(meta),
		Records:      records,
		DeleteFloors: floors,
		ServerTimeMS: now,
	}, nil
}

func recordBelongsToPath(record *wire.DKVSRecord, path string) (ParsedKey, error) {
	if record == nil {
		return ParsedKey{}, ErrInvalidRecord
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return ParsedKey{}, err
	}
	if collectionPath(parsed) != path {
		return ParsedKey{}, ErrInvalidKey
	}
	return parsed, nil
}

func validateRelayableExpiry(record *wire.DKVSRecord) error {
	if record == nil {
		return ErrInvalidRecord
	}
	if isFreeLocalRecord(record) {
		return ErrFreeLocalNotRelayable
	}
	if record.PathGeneration == 0 || record.TTL != 0 {
		return ErrInvalidRecord
	}
	return nil
}

func validateSnapshotPermission(record *wire.DKVSRecord, parsed ParsedKey, validators runtimeValidators) error {
	if record == nil {
		return ErrInvalidRecord
	}
	if parsed.Namespace == "mail" {
		return validateMailWritePermissionWith(parsed, record, nil, validators.resolver, validators.system)
	}
	if len(record.PubKey) == 0 {
		return ValidateRecordIdentity(record, parsed)
	}
	return validatePermissionWith(parsed, record.PubKey, validators.resolver, validators.system)
}

func validatePathFloor(path string, floor DeleteFloor) error {
	if floor.Key == "" || floor.FloorSeq == 0 || floor.PathGeneration == 0 ||
		floor.EffectiveHash == (chainhash.Hash{}) {
		return ErrInvalidSnapshot
	}
	parsed, err := ParseKey(floor.Key)
	if err != nil || collectionPath(parsed) != path {
		return ErrInvalidSnapshot
	}
	return nil
}

func (i *Indexer) validatePathSnapshot(snapshot *PathSnapshot) (validatedPathSnapshot, error) {
	if snapshot == nil || snapshot.PathMeta == nil {
		return validatedPathSnapshot{}, ErrInvalidSnapshot
	}
	path := stringsTrimPath(snapshot.Path)
	if !isCanonicalCollectionPath(path) || snapshot.PathMeta.Path != path ||
		snapshot.PathMeta.Version != pathMetaVersion {
		return validatedPathSnapshot{}, ErrInvalidSnapshot
	}
	prefix, err := ParsePrefix(path)
	if err != nil || pathMode(prefix) == PathLocalOnly {
		return validatedPathSnapshot{}, ErrInvalidSnapshot
	}
	validators := i.snapshotValidators()
	now := currentUnixMilli()
	viewHeight := snapshot.PathMeta.ViewHeight
	selected := make(map[string]*wire.DKVSRecord, len(snapshot.Records))
	for _, raw := range snapshot.Records {
		record := cloneRecord(raw)
		parsed, err := recordBelongsToPath(record, path)
		if err != nil {
			return validatedPathSnapshot{}, ErrInvalidSnapshot
		}
		if err := validateRelayableExpiry(record); err != nil {
			return validatedPathSnapshot{}, err
		}
		if _, err := validateParsedCoreWithVerifier(record, viewHeight, now, true, false, nil); err != nil {
			return validatedPathSnapshot{}, err
		}
		if err := validateSnapshotPermission(record, parsed, validators); err != nil {
			return validatedPathSnapshot{}, err
		}
		if !IsTombstone(record.Flags) {
			if err := verifyFeeProofWith(validators.feeVerifier, record, parsed); err != nil {
				return validatedPathSnapshot{}, err
			}
		}
		if previous := selected[record.Key]; previous == nil || CompareRecords(previous, record) < 0 {
			selected[record.Key] = record
		}
	}
	floors := make(map[string]DeleteFloor, len(snapshot.DeleteFloors))
	for _, raw := range snapshot.DeleteFloors {
		floor := cloneDeleteFloor(raw)
		if err := validatePathFloor(path, floor); err != nil {
			return validatedPathSnapshot{}, err
		}
		if _, duplicate := floors[floor.Key]; duplicate {
			return validatedPathSnapshot{}, ErrInvalidSnapshot
		}
		floors[floor.Key] = floor
	}
	active := make([]*wire.DKVSRecord, 0, len(selected))
	tombstones := make([]*wire.DKVSRecord, 0, len(selected))
	for key, record := range selected {
		if floor, ok := floors[key]; ok {
			if floor.FloorSeq > record.Seq ||
				(floor.FloorSeq == record.Seq && floor.EffectiveHash != RecordHash(record)) {
				return validatedPathSnapshot{}, ErrInvalidSnapshot
			}
			delete(floors, key)
		}
		if IsTombstone(record.Flags) {
			tombstones = append(tombstones, record)
		} else if !IsExpired(record, viewHeight, now) {
			active = append(active, record)
		}
	}
	sort.Slice(active, func(a, b int) bool { return active[a].Key < active[b].Key })
	sort.Slice(tombstones, func(a, b int) bool { return tombstones[a].Key < tombstones[b].Key })
	orderedFloors := make([]DeleteFloor, 0, len(floors))
	for _, floor := range floors {
		orderedFloors = append(orderedFloors, floor)
	}
	sort.Slice(orderedFloors, func(a, b int) bool { return orderedFloors[a].Key < orderedFloors[b].Key })
	computed := &PathMeta{
		Version:    pathMetaVersion,
		Path:       path,
		Generation: snapshot.PathMeta.Generation,
		ViewHeight: viewHeight,
	}
	var visibleGeneration uint64
	for _, record := range active {
		computed.ActiveRecords++
		computed.ActiveTotalSize += uint64(RecordSize(record))
		xorPathMetaRoot(&computed.StateRoot, record)
		updateMinExpiry(computed, record)
		if record.PathGeneration > visibleGeneration {
			visibleGeneration = record.PathGeneration
		}
	}
	for _, record := range tombstones {
		state := &deleteState{
			FloorSeq: record.Seq, PathGeneration: record.PathGeneration,
			PubKey: append([]byte(nil), record.PubKey...), Record: record,
			EffectiveHash: RecordHash(record),
		}
		xorDeleteFloorRoot(&computed.StateRoot, record.Key, state)
		if record.PathGeneration > visibleGeneration {
			visibleGeneration = record.PathGeneration
		}
	}
	for _, floor := range orderedFloors {
		state := &deleteState{
			FloorSeq: floor.FloorSeq, PathGeneration: floor.PathGeneration,
			PubKey: append([]byte(nil), floor.PubKey...), EffectiveHash: floor.EffectiveHash,
		}
		xorDeleteFloorRoot(&computed.StateRoot, floor.Key, state)
		if floor.PathGeneration > visibleGeneration {
			visibleGeneration = floor.PathGeneration
		}
	}
	if computed.Generation < visibleGeneration ||
		computed.StateRoot != snapshot.PathMeta.StateRoot ||
		computed.ActiveRecords != snapshot.PathMeta.ActiveRecords ||
		computed.ActiveTotalSize != snapshot.PathMeta.ActiveTotalSize ||
		computed.MinExpiryHeight != snapshot.PathMeta.MinExpiryHeight {
		return validatedPathSnapshot{}, ErrPathDiverged
	}
	normalizePathMetaAliases(computed)
	return validatedPathSnapshot{
		path:          path,
		meta:          computed,
		active:        active,
		tombstones:    tombstones,
		floors:        orderedFloors,
		serverTimeMS:  snapshot.ServerTimeMS,
		policyVersion: validators.policyGeneration,
	}, nil
}

func (i *Indexer) ApplyPathSnapshot(snapshot *PathSnapshot) (int, error) {
	validated, err := i.validatePathSnapshot(clonePathSnapshot(snapshot))
	if err != nil {
		return 0, err
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if atomic.LoadUint64(&i.policyGeneration) != validated.policyVersion {
		return 0, ErrConcurrentUpdate
	}
	height := validated.meta.ViewHeight
	now := currentUnixMilli()
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
	for _, record := range validated.tombstones {
		state := &deleteState{
			FloorSeq: record.Seq, PathGeneration: record.PathGeneration,
			RelayUntil: deleteRelayUntil(now), PubKey: append([]byte(nil), record.PubKey...),
			Record: record, EffectiveHash: RecordHash(record),
		}
		if err := putDeleteStateBatch(batch, record.Key, state); err != nil {
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
	i.resetFeeUsageLocked()
	i.resetFreeLocalUsageLocked()
	i.resetRecordExpiryLocked()
	atomic.AddUint64(&i.generation, 1)
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
