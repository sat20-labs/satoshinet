package dkvs

import "github.com/sat20-labs/satoshinet/wire"

// IsBlobKey reports whether parsed is the canonical single-record blob layout:
// /blob/<account_id>/<blob_key>.
func IsBlobKey(parsed ParsedKey) bool {
	return parsed.Namespace == "blob" && len(parsed.Segments) == 2
}

func validateRecordSizeForParsed(record *wire.DKVSRecord, parsed ParsedKey) error {
	if record == nil {
		return ErrInvalidRecord
	}
	valueLimit := MaxRecordValueSize
	recordLimit := wire.MaxDKVSRecordSize
	if IsBlobKey(parsed) {
		valueLimit = wire.MaxDKVSBlobValueSize
		recordLimit = wire.MaxDKVSBlobRecordSize
	}
	if len(record.Value) > valueLimit || RecordSize(record) > recordLimit {
		return ErrRecordTooLarge
	}
	return nil
}

func validateBlobRecord(record *wire.DKVSRecord, parsed ParsedKey, policy BlobPolicy) error {
	if record == nil || !IsBlobKey(parsed) {
		return ErrInvalidKey
	}
	policy = normalizeBlobPolicy(policy)
	if err := validateRecordSizeForParsed(record, parsed); err != nil {
		return err
	}
	if IsTombstone(record.Flags) {
		return nil
	}
	if len(record.Value) == 0 {
		return ErrInvalidRecord
	}
	if len(record.Value) > policy.MaxValueSize {
		return ErrRecordTooLarge
	}
	proof, err := ParseFeeProof(record.FeeProof)
	if err != nil {
		return err
	}
	switch proof.Mode {
	case FeeModeAutopay:
		if record.TTL != 0 || record.ExpiryHeight != 0 {
			return ErrInvalidFeeProof
		}
	case FeeModeFreeLocal:
		if record.TTL == 0 || record.ExpiryHeight != 0 {
			return ErrInvalidFeeProof
		}
	default:
		return ErrInvalidFeeProof
	}
	return nil
}

func (i *Indexer) validateBlobLocked(record *wire.DKVSRecord, parsed ParsedKey, _, _ uint64) error {
	return validateBlobRecord(record, parsed, i.blob)
}

// VerifyBlobRecordForClient validates the deterministic signed blob envelope.
func VerifyBlobRecordForClient(record *wire.DKVSRecord, opts RecordVerificationOptions) error {
	if record == nil || record.Version != Version || len(record.PubKey) != 0 {
		return ErrInvalidRecord
	}
	if opts.ExpectedKey != "" && record.Key != opts.ExpectedKey {
		return ErrInvalidKey
	}
	parsed, err := validateParsedCoreWithVerifier(record, opts.Height, opts.Now, true, false, nil)
	if err != nil {
		return err
	}
	if err := validateBlobRecord(record, parsed, DefaultBlobPolicy()); err != nil {
		return err
	}
	if opts.CheckHash && RecordHash(record) != opts.ExpectedHash {
		return ErrInvalidRecord
	}
	if opts.FeeVerifier != nil && !IsTombstone(record.Flags) {
		if err := verifyFeeProofWith(opts.FeeVerifier, record, parsed); err != nil {
			return err
		}
	}
	return nil
}
