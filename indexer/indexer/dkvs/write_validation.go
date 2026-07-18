package dkvs

import (
	"bytes"
	"errors"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type runtimeValidators struct {
	resolver         DIDResolver
	feeVerifier      FeeVerifier
	system           SystemVerifier
	policyGeneration uint64
}

type writeStateSnapshot struct {
	existing          *wire.DKVSRecord
	existingHash      chainhash.Hash
	existingPubKey    []byte
	existingSeq       uint64
	deleteState       *deleteState
	deleteStateHash   chainhash.Hash
	requiresResolve   bool
	generation        uint64
	policyGeneration  uint64
	runtimeValidators runtimeValidators
}

type preparedFeeCapacity struct {
	indexed    IndexedFeeCapacityVerifier
	descriptor FeeCapacityDescriptor
	fallback   FeeCapacityVerifier
	generation uint64
}

func (i *Indexer) snapshotValidators() runtimeValidators {
	i.mutex.RLock()
	validators := runtimeValidators{
		resolver:         i.resolver,
		feeVerifier:      i.feeVerifier,
		system:           i.system,
		policyGeneration: atomic.LoadUint64(&i.policyGeneration),
	}
	i.mutex.RUnlock()
	return validators
}

func recordStateHash(record *wire.DKVSRecord) chainhash.Hash {
	if record == nil {
		return chainhash.Hash{}
	}
	return RecordHash(record)
}

func deleteStateIdentity(state *deleteState) chainhash.Hash {
	if state == nil {
		return chainhash.Hash{}
	}
	encoded, err := marshalDeleteState(state)
	if err != nil {
		return chainhash.Hash{}
	}
	return chainhash.DoubleHashH(encoded)
}

func (i *Indexer) readWriteStateSnapshot(key string, parsed ParsedKey, validators runtimeValidators) (writeStateSnapshot, error) {
	snapshot := writeStateSnapshot{runtimeValidators: validators}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	existing, err := i.getRaw(key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return snapshot, err
	}
	if err == nil {
		snapshot.existing = existing
		snapshot.existingHash = RecordHash(existing)
		snapshot.existingPubKey = append([]byte{}, existing.PubKey...)
		snapshot.existingSeq = existing.Seq
	}
	state, err := i.getDeleteStateLocked(key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return snapshot, err
	}
	if err == nil {
		snapshot.deleteState = state
		snapshot.deleteStateHash = deleteStateIdentity(state)
	}
	if parsed.Namespace == "name" && len(parsed.Segments) == 1 {
		snapshot.requiresResolve, err = i.nameTransferDirty(parsed.Segments[0])
		if err != nil {
			return snapshot, err
		}
	}
	snapshot.generation = atomic.LoadUint64(&i.generation)
	snapshot.policyGeneration = validators.policyGeneration
	return snapshot, nil
}

func (i *Indexer) writeStateStillCurrentLocked(key string, parsed ParsedKey, snapshot writeStateSnapshot) (bool, error) {
	if atomic.LoadUint64(&i.policyGeneration) != snapshot.policyGeneration {
		return false, nil
	}
	existing, err := i.getRaw(key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return false, err
	}
	if recordStateHash(existing) != snapshot.existingHash ||
		(existing != nil && (existing.Seq != snapshot.existingSeq || !bytes.Equal(existing.PubKey, snapshot.existingPubKey))) {
		return false, nil
	}
	state, err := i.getDeleteStateLocked(key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return false, err
	}
	if deleteStateIdentity(state) != snapshot.deleteStateHash {
		return false, nil
	}
	if parsed.Namespace == "name" && len(parsed.Segments) == 1 {
		requiresResolve, err := i.nameTransferDirty(parsed.Segments[0])
		if err != nil {
			return false, err
		}
		if requiresResolve != snapshot.requiresResolve {
			return false, nil
		}
	}
	return true, nil
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
	return verifier.VerifyFeeProof(hash32, keyHash32, parsed.Namespace, RecordSize(record), record.ExpiryHeight, record.FeeProof)
}

func validatePermissionWith(parsed ParsedKey, pubKey []byte, resolver DIDResolver, system SystemVerifier) error {
	switch parsed.Namespace {
	case "personal":
		if len(parsed.Segments) < 2 || parsed.Segments[0] != personalAccountID(pubKey) {
			return ErrPermissionDenied
		}
	case "name":
		if resolver == nil {
			return ErrDIDResolverUnavailable
		}
		identity, err := resolver.ResolveName(parsed.Segments[0])
		if err != nil {
			return err
		}
		if err := validateResolvedIdentity(parsed, identity); err != nil {
			return err
		}
		return identity.CanSign(pubKey)
	case "svc":
		if resolver == nil {
			return ErrDIDResolverUnavailable
		}
		identity, err := resolver.ResolveService(parsed.Segments[0])
		if err != nil {
			return err
		}
		if err := validateResolvedIdentity(parsed, identity); err != nil {
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
		return system.CanWriteSystem("/"+parsed.Namespace+"/"+stringsJoin(parsed.Segments, "/"), pubKey)
	}
	return nil
}

func stringsJoin(values []string, separator string) string {
	if len(values) == 0 {
		return ""
	}
	result := values[0]
	for _, value := range values[1:] {
		result += separator + value
	}
	return result
}

func resolveIdentityWith(parsed ParsedKey, resolver DIDResolver) (DIDIdentity, error) {
	if resolver == nil {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	var (
		identity DIDIdentity
		err      error
	)
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

func validateMailWritePermissionWith(parsed ParsedKey, record, existing *wire.DKVSRecord, resolver DIDResolver, system SystemVerifier) error {
	if len(parsed.Segments) < 2 {
		return ErrInvalidKey
	}
	if parsed.Segments[1] == "share" {
		return validatePermissionWith(parsed, record.PubKey, resolver, system)
	}
	if parsed.Segments[1] != "msg" {
		return ErrInvalidKey
	}
	if IsTombstone(record.Flags) {
		if parsed.Segments[0] != personalAccountID(record.PubKey) {
			return ErrPermissionDenied
		}
		return nil
	}
	if existing == nil || IsTombstone(existing.Flags) {
		return nil
	}
	if !bytes.Equal(existing.PubKey, record.PubKey) {
		return ErrPermissionDenied
	}
	return nil
}

func validateWritePermissionWith(parsed ParsedKey, record, existing *wire.DKVSRecord, requiresResolve bool, validators runtimeValidators) (bool, error) {
	if record == nil {
		return false, ErrInvalidRecord
	}
	switch parsed.Namespace {
	case "name", "svc":
		if existing != nil && bytes.Equal(existing.PubKey, record.PubKey) && !requiresResolve {
			return false, nil
		}
		identity, err := resolveIdentityWith(parsed, validators.resolver)
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
		return false, validateMailWritePermissionWith(parsed, record, existing, validators.resolver, validators.system)
	default:
		return false, validatePermissionWith(parsed, record.PubKey, validators.resolver, validators.system)
	}
}

func (i *Indexer) prepareFeeCapacity(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, verifier FeeVerifier, height, now uint64) (preparedFeeCapacity, error) {
	prepared := preparedFeeCapacity{}
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
	generation := atomicLoadGeneration(&i.generation)
	records, _, _, err := i.scan("", nil, 0, false)
	if err != nil {
		return prepared, err
	}
	if atomicLoadGeneration(&i.generation) != generation {
		return prepared, ErrConcurrentUpdate
	}
	if err := fallback.VerifyFeeCapacity(record, parsed, existing, records, height, now); err != nil {
		return prepared, err
	}
	prepared.fallback = fallback
	prepared.generation = generation
	return prepared, nil
}

func atomicLoadGeneration(generation *uint64) uint64 {
	return atomic.LoadUint64(generation)
}

func (i *Indexer) validatePreparedFeeCapacityLocked(record *wire.DKVSRecord, prepared preparedFeeCapacity, height, now uint64) error {
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
	if prepared.fallback != nil && atomicLoadGeneration(&i.generation) != prepared.generation {
		return ErrConcurrentUpdate
	}
	return nil
}

func validateParsedCoreWithVerifier(record *wire.DKVSRecord, height, now uint64, allowExpiredTombstone, verifyFee bool, feeVerifier FeeVerifier) (ParsedKey, error) {
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
	if IsExpired(record, height, now) && !(allowExpiredTombstone && IsTombstone(record.Flags)) {
		return parsed, ErrExpiredRecord
	}
	if err := VerifySignature(record); err != nil {
		return parsed, err
	}
	if IsTombstone(record.Flags) && len(record.Value) != 0 {
		return parsed, ErrInvalidRecord
	}
	if verifyFee && !IsTombstone(record.Flags) {
		if err := verifyFeeProofWith(feeVerifier, record, parsed); err != nil {
			return parsed, err
		}
	}
	return parsed, nil
}
