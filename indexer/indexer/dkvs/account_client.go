package dkvs

import (
	"strings"

	"github.com/sat20-labs/satoshinet/wire"
)

// VerifyAccountRecordForClient verifies a pubkey-free account-scoped record
// without relying on server-side permission checks.
func VerifyAccountRecordForClient(record *wire.DKVSRecord, opts RecordVerificationOptions) error {
	if record == nil || record.Version != Version || len(record.PubKey) != 0 {
		return ErrInvalidRecord
	}
	if opts.ExpectedKey != "" && record.Key != opts.ExpectedKey {
		return ErrInvalidKey
	}
	parsed, err := validateParsedCoreWithVerifier(record, opts.Height, true, false, nil)
	if err != nil {
		return err
	}
	if !isAccountScopedNamespace(parsed.Namespace) {
		return ErrInvalidRecord
	}
	if IsBlobKey(parsed) {
		if err := validateBlobRecord(record, parsed, DefaultBlobPolicy()); err != nil {
			return err
		}
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

func VerifyAccountRecordsForClient(records []*wire.DKVSRecord, prefix string, opts RecordVerificationOptions) error {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if prefix != "" {
		if _, err := ParsePrefix(prefix); err != nil {
			return err
		}
	}
	if opts.CheckHash && len(records) != 1 {
		return ErrInvalidRecord
	}
	for _, record := range records {
		if prefix != "" && (record == nil || (record.Key != prefix && !strings.HasPrefix(record.Key, prefix+"/"))) {
			return ErrInvalidKey
		}
		recordOpts := opts
		if prefix != "" {
			recordOpts.ExpectedKey = ""
		}
		if err := VerifyAccountRecordForClient(record, recordOpts); err != nil {
			return err
		}
	}
	return nil
}
