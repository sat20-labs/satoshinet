package dkvs

import (
	"github.com/sat20-labs/satoshinet/wire"
)

// This is the only deployment-funded blob path. Ordinary account blobs retain
// their existing ownership and fee rules.
func IsEVMSourceKey(parsed ParsedKey) bool {
	return parsed.Namespace == "blob" && len(parsed.Segments) == 3 &&
		parsed.Segments[0] == "evm" && parsed.Segments[1] == "source"
}

func validateEVMSourceEnvelope(record *wire.DKVSRecord) error {
	if record == nil || record.Seq != 1 || record.TTL != 0 || record.Flags != 0 ||
		len(record.FeeProof) != 0 || len(record.PubKey) != 33 || len(record.Value) == 0 {
		return ErrInvalidRecord
	}
	return nil
}

func validateEVMSourceWrite(record, existing *wire.DKVSRecord, verify func(*wire.DKVSRecord) error) error {
	if err := validateEVMSourceEnvelope(record); err != nil {
		return err
	}
	if existing != nil && RecordHash(existing) != RecordHash(record) {
		return ErrWriteConflict
	}
	if verify == nil {
		return ErrPermissionDenied
	}
	return verify(record)
}

func protectEVMSourceReplacement(current []*wire.DKVSRecord, incoming map[string]*wire.DKVSRecord) error {
	for _, record := range current {
		parsed, err := ParseKey(record.Key)
		if err == nil && IsEVMSourceKey(parsed) {
			if next := incoming[record.Key]; next == nil || RecordHash(next) != RecordHash(record) {
				return ErrWriteConflict
			}
		}
	}
	return nil
}
