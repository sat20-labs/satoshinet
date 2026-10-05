package dkvs

import (
	"fmt"
	"sort"

	"github.com/sat20-labs/satoshinet/wire"
)

const MaxNetworkSnapshotBytes = 64 * 1024 * 1024

type validatedPathSnapshot struct {
	path string
	meta *PathMeta
	active []*wire.DKVSRecord
	retentions map[string]*PaidRecordRetention
	serverTimeMS uint64
	policyVersion uint64
}

func clonePathSnapshot(snapshot *PathSnapshot) *PathSnapshot {
	if snapshot == nil { return nil }
	cloned := &PathSnapshot{Path: snapshot.Path, PathMeta: clonePathMeta(snapshot.PathMeta), ServerTimeMS: snapshot.ServerTimeMS,
		Records: make([]*wire.DKVSRecord, 0, len(snapshot.Records))}
	for _, record := range snapshot.Records { cloned.Records = append(cloned.Records, cloneRecord(record)) }
	return cloned
}

// Network snapshots contain the current active set only. Removed keys have no
// representation in the snapshot, root, accounting or protocol payload.
func (i *Indexer) GetPathSnapshot(path string) (*PathSnapshot, error) {
	scope, err := NormalizeActiveScope(ActiveScope{Prefix: path, Network: true})
	if err != nil { return nil, err }
	height, now := i.currentHeight(), currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	previous, err := i.ensurePathMetaLocked(scope.Prefix, height, now)
	if err != nil { return nil, err }
	meta := &PathMeta{Version: pathMetaVersion, Path: scope.Prefix, Generation: previous.Generation,
		EndpointGeneration: previous.EndpointGeneration, ViewHeight: height}
	records := make([]*wire.DKVSRecord, 0)
	base := recordDBKey(scope.Prefix)
	err = i.db.BatchReadV2(base, base, false, func(_, encoded []byte) error {
		record, err := UnmarshalRecord(encoded)
		if err != nil { return err }
		if !i.activeScopeRecord(record, scope, height, now) { return nil }
		size := uint64(RecordSize(record))
		if size > MaxNetworkSnapshotBytes-meta.ActiveTotalSize { return ErrBatchTooLarge }
		meta.ActiveRecords++
		meta.ActiveTotalSize += size
		xorPathMetaRoot(&meta.StateRoot, record)
		updateMinExpiry(meta, record)
		records = append(records, record)
		return nil
	})
	if err != nil { return nil, err }
	normalizePathMetaAliases(meta)
	sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })
	return &PathSnapshot{Path: scope.Prefix, PathMeta: meta, Records: records, ServerTimeMS: now}, nil
}

func recordBelongsToPath(record *wire.DKVSRecord, path string) (ParsedKey, error) {
	if record == nil { return ParsedKey{}, ErrInvalidRecord }
	parsed, err := ParseKey(record.Key)
	if err != nil { return ParsedKey{}, err }
	if collectionPath(parsed) != path { return ParsedKey{}, ErrInvalidKey }
	return parsed, nil
}

func validateRelayableExpiry(record *wire.DKVSRecord) error {
	if record == nil { return ErrInvalidRecord }
	if isEndpointCacheRecord(record) { return ErrFreeLocalNotRelayable }
	return nil
}

func validateSnapshotPermission(record *wire.DKVSRecord, parsed ParsedKey, validators runtimeValidators) error {
	if record == nil { return ErrInvalidRecord }
	if len(record.PubKey) == 0 { return ValidateRecordIdentity(record, parsed) }
	if parsed.Namespace == "mail" { return validateMailWritePermissionWith(parsed, record, nil, validators.resolver, validators.system) }
	return validatePermissionWith(parsed, record.PubKey, validators.resolver, validators.system)
}

func (i *Indexer) validatePathSnapshot(snapshot *PathSnapshot) (validatedPathSnapshot, error) {
	if snapshot == nil || snapshot.PathMeta == nil { return validatedPathSnapshot{}, ErrInvalidSnapshot }
	path := stringsTrimPath(snapshot.Path)
	if _, err := NormalizeActiveScope(ActiveScope{Prefix: path, Network: true}); err != nil || snapshot.PathMeta.Path != path || snapshot.PathMeta.Version != pathMetaVersion {
		return validatedPathSnapshot{}, ErrInvalidSnapshot
	}
	viewHeight := snapshot.PathMeta.ViewHeight
	if viewHeight > i.currentHeight() { return validatedPathSnapshot{}, ErrStaleEndpoint }
	validators := i.snapshotValidators()
	computed := &PathMeta{Version: pathMetaVersion, Path: path, Generation: snapshot.PathMeta.Generation, ViewHeight: viewHeight}
	selected := make(map[string]struct{}, len(snapshot.Records))
	active := make([]*wire.DKVSRecord, 0, len(snapshot.Records))
	retentions := make(map[string]*PaidRecordRetention, len(snapshot.Records))
	for _, record := range snapshot.Records {
		parsed, err := recordBelongsToPath(record, path)
		if err != nil || record.Flags != 0 { return validatedPathSnapshot{}, ErrInvalidSnapshot }
		if _, exists := selected[record.Key]; exists { return validatedPathSnapshot{}, ErrInvalidSnapshot }
		selected[record.Key] = struct{}{}
		if replicationMode(parsed, record) != ReplicationNetwork || isEndpointCacheRecord(record) { return validatedPathSnapshot{}, ErrFreeLocalNotRelayable }
		size := uint64(RecordSize(record))
		if size > MaxNetworkSnapshotBytes-computed.ActiveTotalSize { return validatedPathSnapshot{}, ErrBatchTooLarge }
		if _, err := validateParsedCoreWithVerifier(record, viewHeight, false, false, nil); err != nil { return validatedPathSnapshot{}, err }
		if err := validateSnapshotPermission(record, parsed, validators); err != nil { return validatedPathSnapshot{}, err }
		if err := verifyFeeProofWith(validators.feeVerifier, record, parsed); err != nil { return validatedPathSnapshot{}, err }
		retention, err := verifiedPaidRetentionAfterFeeVerification(record, parsed, validators.feeVerifier, viewHeight)
		if err != nil { return validatedPathSnapshot{}, err }
		retentions[record.Key] = retention
		computed.ActiveRecords++
		computed.ActiveTotalSize += size
		xorPathMetaRoot(&computed.StateRoot, record)
		updateMinExpiry(computed, record)
		active = append(active, record)
	}
	if computed.StateRoot != snapshot.PathMeta.StateRoot || computed.ActiveRecords != snapshot.PathMeta.ActiveRecords ||
		computed.ActiveTotalSize != snapshot.PathMeta.ActiveTotalSize || computed.MinExpiryHeight != snapshot.PathMeta.MinExpiryHeight {
		return validatedPathSnapshot{}, fmt.Errorf("%w: active snapshot commitment mismatch for %s", ErrPathDiverged, path)
	}
	sort.Slice(active, func(a, b int) bool { return active[a].Key < active[b].Key })
	normalizePathMetaAliases(computed)
	return validatedPathSnapshot{path: path, meta: computed, active: active, retentions: retentions,
		serverTimeMS: snapshot.ServerTimeMS, policyVersion: validators.policyGeneration}, nil
}
