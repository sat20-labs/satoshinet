package dkvs

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type writeSnapshot struct {
	existing         *wire.DKVSRecord
	deleteMaxSeq     uint64
	nameTransferDirty bool
	resolver         DIDResolver
	feeVerifier      FeeVerifier
	systemVerifier   SystemVerifier
	policyGeneration uint64
	dataGeneration   uint64
}

func (i *Indexer) captureWriteSnapshot(parsed ParsedKey, key string) (writeSnapshot, error) {
	var snapshot writeSnapshot
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	existing, err := i.getRaw(key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return snapshot, err
	}
	if err == nil {
		snapshot.existing = existing
	}
	if state, err := i.getDeleteStateRaw(key); err == nil {
		snapshot.deleteMaxSeq = state.MaxSeq
	} else if !errors.Is(err, ErrRecordNotFound) {
		return snapshot, err
	}
	if parsed.Namespace == "name" && len(parsed.Segments) == 1 {
		dirty, err := i.nameTransferDirty(parsed.Segments[0])
		if err != nil {
			return snapshot, err
		}
		snapshot.nameTransferDirty = dirty
	}
	snapshot.resolver = i.resolver
	snapshot.feeVerifier = i.feeVerifier
	snapshot.systemVerifier = i.system
	snapshot.policyGeneration = atomic.LoadUint64(&i.policyGeneration)
	snapshot.dataGeneration = atomic.LoadUint64(&i.generation)
	return snapshot, nil
}

func validateRecordEnvelope(record *wire.DKVSRecord, height, now uint64,
	allowExpiredTombstone bool) (ParsedKey, error) {

	var parsed ParsedKey
	if record == nil || record.Version != Version {
		return parsed, ErrInvalidRecord
	}
	if len(record.Value) > MaxRecordValueSize || RecordSize(record) > wire.MaxDKVSRecordSize {
		return parsed, ErrRecordTooLarge
	}
	var err error
	parsed, err = ParseKey(record.Key)
	if err != nil {
		return parsed, err
	}
	if record.Flags&^FlagTombstone != 0 || record.IssueTime == 0 ||
		(now != 0 && record.IssueTime > now+MaxFutureIssueTimeSkew) {
		return parsed, ErrInvalidRecord
	}
	if IsExpired(record, height, now) &&
		!(allowExpiredTombstone && IsTombstone(record.Flags)) {
		return parsed, ErrExpiredRecord
	}
	if err := VerifySignature(record); err != nil {
		return parsed, err
	}
	if IsTombstone(record.Flags) && len(record.Value) != 0 {
		return parsed, ErrInvalidRecord
	}
	return parsed, nil
}

func verifyFeeWith(verifier FeeVerifier, record *wire.DKVSRecord,
	parsed ParsedKey) error {

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

func validatePermissionWith(parsed ParsedKey, pubKey []byte,
	resolver DIDResolver, system SystemVerifier) error {

	switch parsed.Namespace {
	case "personal":
		if len(parsed.Segments) < 2 || parsed.Segments[0] != personalAccountID(pubKey) {
			return ErrPermissionDenied
		}
	case "name", "svc":
		identity, err := resolveIdentityWith(parsed, resolver)
		if err != nil {
			return err
		}
		return identity.CanSign(pubKey)
	case "mail":
		if len(parsed.Segments) >= 2 && parsed.Segments[1] == "share" &&
			parsed.Segments[0] != personalAccountID(pubKey) {
			return ErrPermissionDenied
		}
	case "blob":
		if len(parsed.Segments) < 3 || parsed.Segments[0] != personalAccountID(pubKey) {
			return ErrPermissionDenied
		}
	case "sys":
		if system == nil {
			return ErrPermissionDenied
		}
		return system.CanWriteSystem("/"+parsed.Namespace+"/"+
			strings.Join(parsed.Segments, "/"), pubKey)
	}
	return nil
}

func validateWritePermissionWith(i *Indexer, parsed ParsedKey,
	record, existing *wire.DKVSRecord, resolver DIDResolver,
	system SystemVerifier, requiresResolve bool) (bool, error) {

	if record == nil {
		return false, ErrInvalidRecord
	}
	switch parsed.Namespace {
	case "name", "svc":
		if existing != nil && bytes.Equal(existing.PubKey, record.PubKey) && !requiresResolve {
			return false, nil
		}
		identity, err := resolveIdentityWith(parsed, resolver)
		if err != nil {
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
	case "mail":
		return false, i.validateMailWritePermission(parsed, record, existing)
	default:
		return false, validatePermissionWith(parsed, record.PubKey, resolver, system)
	}
}

func resolveIdentityWith(parsed ParsedKey, resolver DIDResolver) (DIDIdentity, error) {
	var identity DIDIdentity
	if resolver == nil {
		return identity, ErrDIDResolverUnavailable
	}
	var err error
	switch parsed.Namespace {
	case "name":
		identity, err = resolver.ResolveName(parsed.Segments[0])
	case "svc":
		identity, err = resolver.ResolveService(parsed.Segments[0])
	default:
		return identity, ErrInvalidNamespace
	}
	if err != nil {
		return identity, err
	}
	if err := validateResolvedIdentity(parsed, identity); err != nil {
		return identity, err
	}
	return identity, nil
}

type preparedFeeCapacity struct {
	indexed        IndexedFeeCapacityVerifier
	descriptor     FeeCapacityDescriptor
	fallback       FeeCapacityVerifier
	dataGeneration uint64
}

func (i *Indexer) prepareFeeCapacity(verifier FeeVerifier,
	record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord,
	height, now, dataGeneration uint64) (preparedFeeCapacity, error) {

	var prepared preparedFeeCapacity
	if indexed, ok := verifier.(IndexedFeeCapacityVerifier); ok {
		descriptor, err := indexed.FeeCapacity(record, parsed)
		if err != nil {
			return prepared, err
		}
		prepared.indexed = indexed
		prepared.descriptor = descriptor
		return prepared, nil
	}
	fallback, ok := verifier.(FeeCapacityVerifier)
	if !ok {
		return prepared, nil
	}
	records, _, _, err := i.scan("", nil, 0, false)
	if err != nil {
		return prepared, err
	}
	if err := fallback.VerifyFeeCapacity(record, parsed, existing, records, height, now); err != nil {
		return prepared, err
	}
	prepared.fallback = fallback
	prepared.dataGeneration = dataGeneration
	return prepared, nil
}

func (i *Indexer) validatePreparedFeeCapacityLocked(prepared preparedFeeCapacity,
	record *wire.DKVSRecord, height, now uint64) error {

	if prepared.indexed != nil {
		descriptor := prepared.descriptor
		if descriptor.UsageKey == "" {
			return nil
		}
		if descriptor.MaxRecords == 0 {
			return ErrFeeCapacityExceeded
		}
		if err := i.ensureFeeUsageLocked(prepared.indexed, height, now); err != nil {
			return err
		}
		projected := i.feeUsageCounts[descriptor.UsageKey]
		entry, replacing := i.feeUsageEntries[record.Key]
		if !replacing || entry.usageKey != descriptor.UsageKey {
			projected++
		}
		if projected > descriptor.MaxRecords {
			return ErrFeeCapacityExceeded
		}
		return nil
	}
	if prepared.fallback != nil &&
		atomic.LoadUint64(&i.generation) != prepared.dataGeneration {
		return ErrConcurrentUpdate
	}
	return nil
}

func sameStoredRecord(a, b *wire.DKVSRecord) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return RecordHash(a) == RecordHash(b)
}

func recordHashOrZero(record *wire.DKVSRecord) chainhash.Hash {
	if record == nil {
		return chainhash.Hash{}
	}
	return RecordHash(record)
}
