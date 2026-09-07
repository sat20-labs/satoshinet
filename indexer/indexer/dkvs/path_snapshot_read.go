package dkvs

import (
	"fmt"
	"sort"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type validatedPathSnapshot struct {
	path          string
	meta          *PathMeta
	active        []*wire.DKVSRecord
	floors        []DeleteFloor
	retentions    map[string]*PaidRecordRetention
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
	if replicationMode(parsed, nil) != ReplicationNetwork {
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
	records := make([]*wire.DKVSRecord, 0, len(active))
	for _, record := range active {
		if record != nil && !i.isLocalOnlyRecord(record) {
			records = append(records, cloneRecord(record))
		}
	}
	floors := make([]DeleteFloor, 0, len(states))
	for _, key := range keys {
		floors = append(floors, states[key].publicFloor(key))
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
	return nil
}

func validateSnapshotPermission(record *wire.DKVSRecord, parsed ParsedKey, validators runtimeValidators) error {
	if record == nil {
		return ErrInvalidRecord
	}
	if len(record.PubKey) == 0 {
		return ValidateRecordIdentity(record, parsed)
	}
	if parsed.Namespace == "mail" {
		return validateMailWritePermissionWith(parsed, record, nil, validators.resolver, validators.system)
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
	if err != nil || replicationMode(prefix, nil) != ReplicationNetwork {
		return validatedPathSnapshot{}, ErrInvalidSnapshot
	}
	validators := i.snapshotValidators()
	viewHeight := snapshot.PathMeta.ViewHeight
	selected := make(map[string]*wire.DKVSRecord, len(snapshot.Records))
	retentions := make(map[string]*PaidRecordRetention, len(snapshot.Records))
	for _, raw := range snapshot.Records {
		record := cloneRecord(raw)
		parsed, err := recordBelongsToPath(record, path)
		if err != nil {
			return validatedPathSnapshot{}, ErrInvalidSnapshot
		}
		if err := validateRelayableExpiry(record); err != nil {
			return validatedPathSnapshot{}, err
		}
		if _, err := validateParsedCoreWithVerifier(record, viewHeight, true, false, nil); err != nil {
			return validatedPathSnapshot{}, err
		}
		if err := validateSnapshotPermission(record, parsed, validators); err != nil {
			return validatedPathSnapshot{}, err
		}
		if IsTombstone(record.Flags) {
			return validatedPathSnapshot{}, ErrInvalidSnapshot
		}
		if err := verifyFeeProofWith(validators.feeVerifier, record, parsed); err != nil {
			return validatedPathSnapshot{}, err
		}
		retention, err := verifiedPaidRetentionAfterFeeVerification(record, parsed, validators.feeVerifier, viewHeight)
		if err != nil {
			return validatedPathSnapshot{}, err
		}
		if previous := selected[record.Key]; previous == nil || CompareRecords(previous, record) < 0 {
			selected[record.Key] = record
			retentions[record.Key] = retention
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
	for key, record := range selected {
		if _, deleted := floors[key]; deleted {
			return validatedPathSnapshot{}, ErrInvalidSnapshot
		}
		if !IsExpired(record, viewHeight) {
			active = append(active, record)
		}
	}
	sort.Slice(active, func(a, b int) bool { return active[a].Key < active[b].Key })
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
		return validatedPathSnapshot{}, fmt.Errorf(
			"%w: path=%s generation=%d visible_generation=%d root=%s want_root=%s records=%d want_records=%d bytes=%d want_bytes=%d min_expiry=%d want_min_expiry=%d snapshot_records=%d floors=%d view_height=%d",
			ErrPathDiverged, path, computed.Generation, visibleGeneration,
			computed.StateRoot, snapshot.PathMeta.StateRoot,
			computed.ActiveRecords, snapshot.PathMeta.ActiveRecords,
			computed.ActiveTotalSize, snapshot.PathMeta.ActiveTotalSize,
			computed.MinExpiryHeight, snapshot.PathMeta.MinExpiryHeight,
			len(snapshot.Records), len(snapshot.DeleteFloors), viewHeight,
		)
	}
	normalizePathMetaAliases(computed)
	return validatedPathSnapshot{
		path:          path,
		meta:          computed,
		active:        active,
		floors:        orderedFloors,
		retentions:    retentions,
		serverTimeMS:  snapshot.ServerTimeMS,
		policyVersion: validators.policyGeneration,
	}, nil
}
