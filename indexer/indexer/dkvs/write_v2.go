package dkvs

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const maxOptimisticWriteAttempts = 4

type writeSnapshot struct {
	existing         *wire.DKVSRecord
	existingHash     chainhash.Hash
	existingFound    bool
	authorityDirty   bool
	policyGeneration uint64
	dataGeneration   uint64
	resolver         DIDResolver
	feeVerifier      FeeVerifier
	systemVerifier   SystemVerifier
	legacyRecords    []*wire.DKVSRecord
}

type feeCapacityPlan struct {
	indexed    IndexedFeeCapacityVerifier
	descriptor FeeCapacityDescriptor
	legacy     FeeCapacityVerifier
}

type writeCommitResult struct {
	updated   bool
	eventType uint32
	hash      chainhash.Hash
	track     *wire.DKVSRecord
}

func (i *Indexer) putOptimistic(record *wire.DKVSRecord, remote bool) (bool, uint32, chainhash.Hash, error) {
	height := i.currentHeight()
	now := currentUnixMilli()
	parsed, err := i.validateParsedCore(record, height, now, remote, false)
	if err != nil {
		return false, 0, chainhash.Hash{}, err
	}

	for attempt := 0; attempt < maxOptimisticWriteAttempts; attempt++ {
		snapshot, err := i.captureWriteSnapshot(record.Key, parsed, height, now)
		if err != nil {
			return false, 0, chainhash.Hash{}, err
		}
		if !(remote && IsTombstone(record.Flags) && IsExpired(record, height, now)) {
			if err := verifyFeeProofWith(snapshot.feeVerifier, record, parsed); err != nil {
				return false, 0, chainhash.Hash{}, err
			}
		}
		forceReplace, err := validateExternalWritePermission(
			parsed, record, snapshot.existing, snapshot.authorityDirty,
			snapshot.resolver, snapshot.systemVerifier,
		)
		if err != nil {
			return false, 0, chainhash.Hash{}, err
		}
		capacity, err := prepareFeeCapacity(snapshot.feeVerifier, record, parsed,
			snapshot.existing, snapshot.legacyRecords, height, now)
		if err != nil {
			return false, 0, chainhash.Hash{}, err
		}

		i.mutex.Lock()
		if i.policyGeneration != snapshot.policyGeneration ||
			(capacity.legacy != nil && atomic.LoadUint64(&i.generation) != snapshot.dataGeneration) {
			i.mutex.Unlock()
			continue
		}
		latest, err := i.getRaw(record.Key)
		latestFound := err == nil
		if err != nil && !errors.Is(err, ErrRecordNotFound) {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		if !sameRecordSnapshot(latest, latestFound, snapshot) {
			i.mutex.Unlock()
			continue
		}
		currentDirty, err := i.authorityDirty(parsed)
		if err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, err
		}
		if currentDirty != snapshot.authorityDirty {
			i.mutex.Unlock()
			continue
		}

		result, err := i.commitPreparedWriteLocked(parsed, record, latest, forceReplace,
			snapshot.authorityDirty, capacity, height, now)
		i.mutex.Unlock()
		if err != nil {
			return false, 0, chainhash.Hash{}, err
		}
		if result.track != nil {
			i.recordPathSyncChange(result.track)
		}
		return result.updated, result.eventType, result.hash, nil
	}
	return false, 0, chainhash.Hash{}, ErrConcurrentUpdate
}

func (i *Indexer) captureWriteSnapshot(key string, parsed ParsedKey, height, now uint64) (writeSnapshot, error) {
	var snapshot writeSnapshot
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	snapshot.resolver = i.resolver
	snapshot.feeVerifier = i.feeVerifier
	snapshot.systemVerifier = i.system
	snapshot.policyGeneration = i.policyGeneration
	snapshot.dataGeneration = atomic.LoadUint64(&i.generation)

	existing, err := i.getRaw(key)
	if err == nil {
		snapshot.existing = existing
		snapshot.existingHash = RecordHash(existing)
		snapshot.existingFound = true
	} else if !errors.Is(err, ErrRecordNotFound) {
		return snapshot, err
	}
	dirty, err := i.authorityDirty(parsed)
	if err != nil {
		return snapshot, err
	}
	snapshot.authorityDirty = dirty

	if _, indexed := snapshot.feeVerifier.(IndexedFeeCapacityVerifier); !indexed {
		if _, legacy := snapshot.feeVerifier.(FeeCapacityVerifier); legacy {
			snapshot.legacyRecords, _, _, err = i.scanLocked("", nil, 0, false, height, now)
			if err != nil {
				return snapshot, err
			}
		}
	}
	return snapshot, nil
}

func sameRecordSnapshot(record *wire.DKVSRecord, found bool, snapshot writeSnapshot) bool {
	if found != snapshot.existingFound {
		return false
	}
	if !found {
		return true
	}
	return RecordHash(record) == snapshot.existingHash
}

func verifyFeeProofWith(verifier FeeVerifier, record *wire.DKVSRecord, parsed ParsedKey) error {
	if verifier == nil {
		return ErrFeeProofRequired
	}
	if recordVerifier, ok := verifier.(RecordFeeVerifier); ok {
		return recordVerifier.VerifyRecordFeeProof(record, parsed)
	}
	hash := FeeAnchorHash(record)
	var hash32 [32]byte
	copy(hash32[:], hash[:])
	keyHash := KeyHash(record.Key)
	var keyHash32 [32]byte
	copy(keyHash32[:], keyHash[:])
	return verifier.VerifyFeeProof(hash32, keyHash32, parsed.Namespace,
		RecordSize(record), record.ExpiryHeight, record.FeeProof)
}

func validateExternalWritePermission(parsed ParsedKey, record, existing *wire.DKVSRecord,
	dirty bool, resolver DIDResolver, system SystemVerifier) (bool, error) {

	switch parsed.Namespace {
	case "name", "svc":
		if existing != nil && bytes.Equal(existing.PubKey, record.PubKey) && !dirty {
			return false, nil
		}
		if resolver == nil {
			return false, ErrDIDResolverUnavailable
		}
		var (
			identity DIDIdentity
			err      error
		)
		if parsed.Namespace == "name" {
			identity, err = resolver.ResolveName(parsed.Segments[0])
		} else {
			identity, err = resolver.ResolveService(parsed.Segments[0])
		}
		if err != nil {
			return false, err
		}
		if err := validateResolvedIdentity(parsed, identity); err != nil {
			return false, err
		}
		if err := identity.CanSign(record.PubKey); err != nil {
			return false, err
		}
		if existing == nil || bytes.Equal(existing.PubKey, record.PubKey) {
			return false, nil
		}
		if err := identity.CanSign(existing.PubKey); err == nil {
			return false, nil
		}
		return true, nil
	case "sys":
		if system == nil {
			return false, ErrPermissionDenied
		}
		key := "/" + parsed.Namespace + "/" + strings.Join(parsed.Segments, "/")
		return false, system.CanWriteSystem(key, record.PubKey)
	default:
		return false, nil
	}
}

func prepareFeeCapacity(verifier FeeVerifier, record *wire.DKVSRecord, parsed ParsedKey,
	existing *wire.DKVSRecord, legacyRecords []*wire.DKVSRecord, height, now uint64) (feeCapacityPlan, error) {

	var plan feeCapacityPlan
	if IsTombstone(record.Flags) || verifier == nil {
		return plan, nil
	}
	if indexed, ok := verifier.(IndexedFeeCapacityVerifier); ok {
		descriptor, err := indexed.FeeCapacity(record, parsed)
		if err != nil {
			return plan, err
		}
		plan.indexed = indexed
		plan.descriptor = descriptor
		return plan, nil
	}
	legacy, ok := verifier.(FeeCapacityVerifier)
	if !ok {
		return plan, nil
	}
	if err := legacy.VerifyFeeCapacity(record, parsed, existing, legacyRecords, height, now); err != nil {
		return plan, err
	}
	plan.legacy = legacy
	return plan, nil
}

func (i *Indexer) validateLocalWritePermissionLocked(parsed ParsedKey, record,
	existing *wire.DKVSRecord) error {

	switch parsed.Namespace {
	case "name", "svc", "sys":
		return nil
	case "mail":
		return i.validateMailWritePermission(parsed, record, existing)
	default:
		return i.validatePermission(parsed, record.PubKey)
	}
}

func (i *Indexer) validateIndexedCapacityLocked(plan feeCapacityPlan, record *wire.DKVSRecord,
	height, now uint64) error {

	if plan.indexed == nil || plan.descriptor.UsageKey == "" {
		return nil
	}
	if plan.descriptor.MaxRecords == 0 {
		return ErrFeeCapacityExceeded
	}
	if err := i.ensureFeeUsageLocked(plan.indexed, height, now); err != nil {
		return err
	}
	projected := i.feeUsageCounts[plan.descriptor.UsageKey]
	entry, replacing := i.feeUsageEntries[record.Key]
	if !replacing || entry.usageKey != plan.descriptor.UsageKey {
		projected++
	}
	if projected > plan.descriptor.MaxRecords {
		return ErrFeeCapacityExceeded
	}
	return nil
}

func (i *Indexer) commitPreparedWriteLocked(parsed ParsedKey, record, existing *wire.DKVSRecord,
	forceReplace, clearAuthority bool, capacity feeCapacityPlan, height, now uint64) (writeCommitResult, error) {

	var result writeCommitResult
	if err := i.validateLocalWritePermissionLocked(parsed, record, existing); err != nil {
		return result, err
	}
	if IsTombstone(record.Flags) {
		if existing == nil {
			return result, nil
		}
		if !forceReplace && record.Seq <= existing.Seq {
			return result, ErrStaleRecord
		}
		return i.commitDeleteLocked(parsed, record, existing, clearAuthority, forceReplace, height, now)
	}

	deleteState, err := i.deleteStateForReplacementLocked(record)
	if err != nil {
		return result, err
	}
	if existing != nil && !forceReplace && i.activeError(existing, height, now) == nil && CompareRecords(existing, record) >= 0 {
		if clearAuthority {
			if err := i.reactivateAuthorityRecordLocked(parsed, existing, height, now); err != nil {
				return result, err
			}
			result.track = cloneDKVSRecord(existing)
		}
		result.hash = RecordHash(existing)
		return result, nil
	}
	if err := i.validateStatefulLocked(record, parsed, existing, height, now); err != nil {
		return result, err
	}
	if err := i.validateIndexedCapacityLocked(capacity, record, height, now); err != nil {
		return result, err
	}

	data, err := MarshalRecord(record)
	if err != nil {
		return result, err
	}
	hash := RecordHash(record)
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	if err := batch.Put(recordDBKey(record.Key), data); err != nil {
		return result, err
	}
	if existing != nil {
		existingHash := RecordHash(existing)
		if existingHash != hash {
			if err := batch.Delete(hashDBKey(existingHash)); err != nil {
				return result, err
			}
		}
	}
	if err := batch.Put(hashDBKey(hash), []byte(record.Key)); err != nil {
		return result, err
	}
	if err := i.removeDeleteStateBatchLocked(batch, record.Key, deleteState); err != nil {
		return result, err
	}
	if clearAuthority {
		if err := i.clearAuthorityDirtyBatchLocked(batch, parsed); err != nil {
			return result, err
		}
	}
	if err := i.updatePathMetaBatchLocked(batch, parsed, existing, record, height, now); err != nil {
		return result, err
	}
	if err := batch.Flush(); err != nil {
		return result, err
	}

	atomic.AddUint64(&i.generation, 1)
	if capacity.indexed != nil && i.feeUsageInitialized {
		i.replaceFeeUsageLocked(capacity.indexed, record)
	}
	if i.recordExpiryInitialized {
		i.replaceRecordExpiryLocked(record)
	}
	result.updated = true
	result.eventType = notifyEventType(parsed, record, existing)
	result.hash = hash
	result.track = cloneDKVSRecord(record)
	return result, nil
}

func (i *Indexer) commitDeleteLocked(parsed ParsedKey, record, existing *wire.DKVSRecord,
	clearAuthority, forceReplace bool, height, now uint64) (writeCommitResult, error) {

	var result writeCommitResult
	previousDelete, err := i.getDeleteStateLocked(record.Key)
	if errors.Is(err, ErrRecordNotFound) {
		previousDelete = nil
	} else if err != nil {
		return result, err
	}
	maxSeq := record.Seq
	if forceReplace && existing.Seq > maxSeq {
		maxSeq = existing.Seq
	}
	relayUntil := now + DefaultDeleteRelayTTL
	if relayUntil < now {
		relayUntil = ^uint64(0)
	}
	state := deleteState{
		MaxSeq:     maxSeq,
		RelayUntil: relayUntil,
		Record:     cloneDKVSRecord(record),
	}
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	if err := batch.Delete(recordDBKey(existing.Key)); err != nil {
		return result, err
	}
	if err := batch.Delete(hashDBKey(RecordHash(existing))); err != nil {
		return result, err
	}
	if err := i.putDeleteStateBatchLocked(batch, record.Key, previousDelete, state); err != nil {
		return result, err
	}
	if clearAuthority {
		if err := i.clearAuthorityDirtyBatchLocked(batch, parsed); err != nil {
			return result, err
		}
	}
	if err := i.updatePathMetaBatchLocked(batch, parsed, existing, nil, height, now); err != nil {
		return result, err
	}
	if err := batch.Flush(); err != nil {
		return result, err
	}

	atomic.AddUint64(&i.generation, 1)
	if i.feeUsageInitialized {
		i.removeFeeUsageLocked(record.Key)
	}
	if i.recordExpiryInitialized {
		delete(i.recordExpiryEntries, record.Key)
	}
	result.updated = true
	result.eventType = EventRecordTombstone
	result.hash = RecordHash(record)
	result.track = cloneDKVSRecord(record)
	return result, nil
}

func (i *Indexer) reactivateAuthorityRecordLocked(parsed ParsedKey, existing *wire.DKVSRecord,
	height, now uint64) error {

	batch := i.db.NewWriteBatch()
	defer batch.Close()
	if err := i.clearAuthorityDirtyBatchLocked(batch, parsed); err != nil {
		return err
	}
	if err := i.updatePathMetaBatchLocked(batch, parsed, existing, existing, height, now); err != nil {
		return err
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	atomic.AddUint64(&i.generation, 1)
	return nil
}
