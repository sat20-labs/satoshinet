package dkvs

import (
	"bytes"
	"errors"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type runtimeValidators struct {
	resolver          DIDResolver
	feeVerifier       FeeVerifier
	system            SystemVerifier
	evmSourceVerifier func(*wire.DKVSRecord) error
	policyGeneration  uint64
}

type writeStateSnapshot struct {
	existing          *wire.DKVSRecord
	existingHash      chainhash.Hash
	existingPubKey    []byte
	existingSeq       uint64
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
	defer i.mutex.RUnlock()
	return runtimeValidators{resolver: i.resolver, feeVerifier: i.feeVerifier, system: i.system,
		evmSourceVerifier: i.evmSourceVerifier,
		policyGeneration:  atomic.LoadUint64(&i.policyGeneration)}
}

func recordStateHash(record *wire.DKVSRecord) chainhash.Hash {
	if record == nil {
		return chainhash.Hash{}
	}
	return RecordHash(record)
}

func (i *Indexer) readWriteStateSnapshot(key string, parsed ParsedKey, validators runtimeValidators) (writeStateSnapshot, error) {
	snapshot := writeStateSnapshot{runtimeValidators: validators}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	existing, err := i.getRaw(key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return snapshot, err
	}
	if existing != nil {
		snapshot.existing = existing
		snapshot.existingHash = RecordHash(existing)
		snapshot.existingPubKey = append([]byte(nil), existing.PubKey...)
		snapshot.existingSeq = existing.Seq
	}
	// Raw identity fences races with expiry even if the cached row is no
	// longer active. Absence itself has no retained version or delete floor.
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
	if IsEVMSourceKey(parsed) {
		return validateEVMSourceEnvelope(record)
	}
	if IsAuthorityContractKey(parsed) {
		return validateContractEnvelope(record)
	}
	if isAccountMappingBindingControlRecord(record, parsed) {
		return nil
	}
	if record != nil && isAutopayRecord(record) && record.TTL != 0 {
		return ErrInvalidFeeProof
	}
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
	return verifier.VerifyFeeProof(hash32, keyHash32, parsed.Namespace, RecordSize(record), RecordExpiryHeight(record), record.FeeProof)
}

func validatePermissionWith(parsed ParsedKey, pubKey []byte, resolver DIDResolver, system SystemVerifier) error {
	switch parsed.Namespace {
	case "contract", "account":
		return ErrPermissionDenied
	case "personal":
		if len(parsed.Segments) < 2 || parsed.Segments[0] != AccountID(pubKey) {
			return ErrPermissionDenied
		}
	case "name", "svc":
		identity, err := resolveIdentityWith(parsed, resolver)
		if err != nil {
			return err
		}
		return identity.CanSign(pubKey)
	case "mail", "topic":
		return ErrPermissionDenied
	case "blob":
		if len(parsed.Segments) < 2 || parsed.Segments[0] != AccountID(pubKey) {
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
	var identity DIDIdentity
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

func validateMailWritePermissionWith(parsed ParsedKey, record, existing *wire.DKVSRecord, resolver DIDResolver, system SystemVerifier) error {
	if len(parsed.Segments) < 2 || record == nil {
		return ErrInvalidKey
	}
	if parsed.Segments[1] == "share" {
		return ValidateRecordIdentity(record, parsed)
	}
	if parsed.Segments[1] != "msg" && parsed.Segments[1] != "topic" {
		return ErrInvalidKey
	}
	if IsTombstone(record.Flags) {
		signer, err := RecordSignerAccountID(record, parsed)
		if err != nil || signer != parsed.Segments[0] {
			return ErrPermissionDenied
		}
		return nil
	}
	return ErrPermissionDenied
}

func validateWritePermissionWith(parsed ParsedKey, record, existing *wire.DKVSRecord, requiresResolve bool, validators runtimeValidators) (bool, error) {
	if record == nil {
		return false, ErrInvalidRecord
	}
	if IsEVMSourceKey(parsed) {
		return false, validateEVMSourceWrite(record, existing, validators.evmSourceVerifier)
	}
	if IsAuthorityContractKey(parsed) {
		return false, validateAuthorityContractPermissionWith(record, parsed, validators.system)
	}
	if isPrimaryDIDRecord(record, parsed) {
		if err := ValidateRecordIdentity(record, parsed); err != nil {
			return false, err
		}
		return false, validatePrimaryDIDRecordWith(record, parsed, validators.resolver)
	}
	if parsed.Namespace == "mail" {
		return false, validateMailWritePermissionWith(parsed, record, existing, validators.resolver, validators.system)
	}
	if isInternalMailboxRecord(record) || isInternalTopicRecord(record) {
		return false, ErrPermissionDenied
	}
	if len(record.PubKey) == 0 {
		return false, ValidateRecordIdentity(record, parsed)
	}
	switch parsed.Namespace {
	case "name", "svc":
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
	default:
		return false, validatePermissionWith(parsed, record.PubKey, validators.resolver, validators.system)
	}
}

func (i *Indexer) prepareFeeCapacity(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, verifier FeeVerifier, height, now uint64) (preparedFeeCapacity, error) {
	prepared := preparedFeeCapacity{}
	if IsEVMSourceKey(parsed) || IsAuthorityContractKey(parsed) {
		return prepared, nil
	}
	if isAccountMappingBindingControlRecord(record, parsed) {
		return prepared, nil
	}
	if indexed, ok := verifier.(IndexedFeeCapacityVerifier); ok {
		descriptor, err := indexed.FeeCapacity(record, parsed)
		if err != nil {
			return prepared, err
		}
		prepared.indexed, prepared.descriptor = indexed, descriptor
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
	prepared.fallback, prepared.generation = fallback, generation
	return prepared, nil
}

func atomicLoadGeneration(generation *uint64) uint64 { return atomic.LoadUint64(generation) }

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

func validateParsedCoreWithVerifier(record *wire.DKVSRecord, height uint64, allowExpiredTombstone, verifyFee bool, feeVerifier FeeVerifier) (ParsedKey, error) {
	var parsed ParsedKey
	if record == nil || record.Version != Version {
		return parsed, ErrInvalidRecord
	}
	var err error
	parsed, err = ParseKey(record.Key)
	if err != nil {
		return parsed, err
	}
	if IsEVMSourceKey(parsed) {
		if err := validateEVMSourceEnvelope(record); err != nil {
			return parsed, err
		}
	}
	if IsAuthorityContractKey(parsed) {
		if err := validateContractEnvelope(record); err != nil {
			return parsed, err
		}
	}
	if err := validateRecordSizeForParsed(record, parsed); err != nil {
		return parsed, err
	}
	if record.Flags&^FlagTombstone != 0 || (height != 0 && record.IssueHeight > height) || (record.TTL != 0 && RecordExpiryHeight(record) == 0) {
		return parsed, ErrInvalidRecord
	}
	if verifyFee && !IsTombstone(record.Flags) && isAutopayRecord(record) && record.TTL != 0 {
		return parsed, ErrInvalidFeeProof
	}
	if IsExpired(record, height) && !(allowExpiredTombstone && IsTombstone(record.Flags)) {
		return parsed, ErrExpiredRecord
	}
	if isInternalMailboxRecord(record) || isInternalTopicRecord(record) {
		if verifyFee {
			return parsed, ErrPermissionDenied
		}
		return parsed, nil
	}
	if err := VerifySignature(record); err != nil {
		return parsed, err
	}
	if err := ValidateRecordIdentity(record, parsed); err != nil {
		return parsed, err
	}
	if IsTombstone(record.Flags) {
		if _, err := DeleteTargetHash(record); err != nil {
			return parsed, err
		}
	}
	if verifyFee && !IsTombstone(record.Flags) {
		if err := verifyFeeProofWith(feeVerifier, record, parsed); err != nil {
			return parsed, err
		}
	}
	return parsed, nil
}
