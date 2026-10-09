package dkvs

import (
	"bytes"
	"errors"

	"github.com/sat20-labs/satoshinet/wire"
)

// IsAuthorityContractKey identifies opaque authority-managed contract slots.
// EVM source slots retain their existing deployment-based admission verifier.
func IsAuthorityContractKey(parsed ParsedKey) bool {
	return parsed.Namespace == "contract" && len(parsed.Segments) >= 2 && !IsEVMSourceKey(parsed)
}

func validateContractEnvelope(record *wire.DKVSRecord) error {
	if record == nil || record.Seq != 1 || record.TTL != 0 || record.Flags != 0 || len(record.FeeProof) != 0 || len(record.PubKey) != 33 || len(record.Value) == 0 {
		return ErrInvalidRecord
	}
	return nil
}

func validateAuthorityContractPermissionWith(record *wire.DKVSRecord, parsed ParsedKey, system SystemVerifier) error {
	if record == nil || !IsAuthorityContractKey(parsed) {
		return ErrInvalidRecord
	}
	if system == nil {
		return ErrPermissionDenied
	}
	if err := system.CanWriteSystem(record.Key, record.PubKey); err != nil {
		return err
	}
	if err := validateContractEnvelope(record); err != nil {
		return err
	}
	return VerifySignature(record)
}

// Check immutable bytes before merge ordering can discard an older conflicting
// envelope. Replacement snapshots must retain every existing contract value.
// Payload interpretation is exclusively the authority's business responsibility.
func (i *Indexer) validateContractIncomingLocked(records []*wire.DKVSRecord, replacedKeys map[string]struct{}) error {
	incoming := make(map[string]*wire.DKVSRecord)
	for _, record := range records {
		parsed, err := ParseKey(record.Key)
		if err != nil || !IsAuthorityContractKey(parsed) {
			continue
		}
		if old := incoming[record.Key]; old != nil && !bytes.Equal(old.Value, record.Value) {
			return ErrWriteConflict
		}
		incoming[record.Key] = record
		old, err := i.getRaw(record.Key)
		if err != nil && !errors.Is(err, ErrRecordNotFound) {
			return err
		}
		if old != nil && !bytes.Equal(old.Value, record.Value) {
			return ErrWriteConflict
		}
	}
	for key := range replacedKeys {
		parsed, err := ParseKey(key)
		if err != nil || !IsAuthorityContractKey(parsed) {
			continue
		}
		if incoming[key] == nil {
			return ErrWriteConflict
		}
	}
	return nil
}

// PutInternalContract is the single storage entry for business authorities.
// It accepts signed opaque bytes after the caller has validated its domain.
// Generic wallet writes remain forbidden, including for an authority key.
func (i *Indexer) PutInternalContract(record *wire.DKVSRecord) (bool, error) {
	if i == nil || record == nil {
		return false, ErrInvalidRecord
	}
	record = cloneRecord(record)
	parsed, err := ParseKey(record.Key)
	if err != nil || !IsAuthorityContractKey(parsed) {
		return false, ErrInvalidKey
	}
	updated, eventType, _, relay, err := i.put(record, false)
	if err == nil && updated {
		i.emit(eventType, record, relay)
	}
	return updated, err
}
