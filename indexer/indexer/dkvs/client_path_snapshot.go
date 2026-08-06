package dkvs

import (
	"sort"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

// ValidatePathSnapshotForClient validates the deterministic, network-comparable
// portion of a path snapshot without requiring access to node-local resolver or
// fee-verifier state. It verifies record signatures and identities, path
// membership, selector convergence, delete floors, generation watermark and
// the complete state root. Applications may additionally provide a FeeVerifier
// through opts when they need to validate the fee proof locally.
func ValidatePathSnapshotForClient(snapshot *PathSnapshot, opts RecordVerificationOptions) error {
	if snapshot == nil || snapshot.PathMeta == nil {
		return ErrInvalidSnapshot
	}
	path := stringsTrimPath(snapshot.Path)
	meta := snapshot.PathMeta
	if !isCanonicalCollectionPath(path) || meta.Path != path || meta.Version != pathMetaVersion {
		return ErrInvalidSnapshot
	}
	prefix, err := ParsePrefix(path)
	if err != nil || pathMode(prefix) == PathLocalOnly {
		return ErrInvalidSnapshot
	}
	if opts.Height == 0 {
		opts.Height = meta.ViewHeight
	}

	selected := make(map[string]*wire.DKVSRecord, len(snapshot.Records))
	for _, record := range snapshot.Records {
		if record == nil {
			return ErrInvalidSnapshot
		}
		parsed, err := recordBelongsToPath(record, path)
		if err != nil {
			return ErrInvalidSnapshot
		}
		if err := validateRelayableExpiry(record); err != nil {
			return err
		}
		recordOpts := opts
		recordOpts.ExpectedKey = record.Key
		if err := VerifyRecordForClient(record, recordOpts); err != nil {
			return err
		}
		// Account-scoped records have deterministic owner identity. Authority
		// paths require a resolver at the accepting node; clients still verify
		// the signed bytes and deterministic snapshot root here.
		if isAccountScopedNamespace(parsed.Namespace) {
			if err := ValidateRecordIdentity(record, parsed); err != nil {
				return err
			}
		}
		previous := selected[record.Key]
		if previous == nil || CompareRecords(previous, record) < 0 {
			selected[record.Key] = record
		}
	}

	floors := make(map[string]DeleteFloor, len(snapshot.DeleteFloors))
	for _, floor := range snapshot.DeleteFloors {
		if err := validatePathFloor(path, floor); err != nil {
			return err
		}
		if _, exists := floors[floor.Key]; exists {
			return ErrInvalidSnapshot
		}
		floors[floor.Key] = cloneDeleteFloor(floor)
	}

	active := make([]*wire.DKVSRecord, 0, len(selected))
	for key, record := range selected {
		if IsTombstone(record.Flags) {
			return ErrInvalidSnapshot
		}
		if _, deleted := floors[key]; deleted {
			return ErrInvalidSnapshot
		}
		if !IsExpired(record, meta.ViewHeight) {
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
		Generation: meta.Generation,
		ViewHeight: meta.ViewHeight,
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
			FloorSeq:       floor.FloorSeq,
			PathGeneration: floor.PathGeneration,
			PubKey:         append([]byte(nil), floor.PubKey...),
			EffectiveHash:  floor.EffectiveHash,
		}
		xorDeleteFloorRoot(&computed.StateRoot, floor.Key, state)
		if floor.PathGeneration > visibleGeneration {
			visibleGeneration = floor.PathGeneration
		}
	}
	if meta.Generation < visibleGeneration ||
		computed.StateRoot != meta.StateRoot ||
		computed.ActiveRecords != meta.ActiveRecords ||
		computed.ActiveTotalSize != meta.ActiveTotalSize ||
		computed.MinExpiryHeight != meta.MinExpiryHeight {
		return ErrPathDiverged
	}
	if computed.StateRoot == (chainhash.Hash{}) && meta.StateRoot != (chainhash.Hash{}) {
		return ErrPathDiverged
	}
	return nil
}
