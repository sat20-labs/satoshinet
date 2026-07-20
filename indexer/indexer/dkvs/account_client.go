package dkvs

import (
	"strconv"
	"strings"

	"github.com/sat20-labs/satoshinet/wire"
)

// VerifyAccountRecordForClient verifies a pubkey-free account-scoped version-1
// record without relying on server-side permission checks.
func VerifyAccountRecordForClient(record *wire.DKVSRecord, opts RecordVerificationOptions) error {
	if record == nil || record.Version != Version || len(record.PubKey) != 0 {
		return ErrInvalidRecord
	}
	if opts.ExpectedKey != "" && record.Key != opts.ExpectedKey {
		return ErrInvalidKey
	}
	if len(record.Value) > MaxRecordValueSize || RecordSize(record) > wire.MaxDKVSRecordSize {
		return ErrRecordTooLarge
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return err
	}
	if IsExpired(record, opts.Height, opts.Now) {
		return ErrExpiredRecord
	}
	if err := VerifySignature(record); err != nil {
		return err
	}
	if err := ValidateRecordIdentity(record, parsed); err != nil {
		return err
	}
	if IsTombstone(record.Flags) && len(record.Value) != 0 {
		return ErrInvalidRecord
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

// AssembleAccountBlobFromRecords verifies and assembles a pubkey-free blob
// whose owner is derived from its /blob/<account_id>/... keys.
func AssembleAccountBlobFromRecords(manifestRecord *wire.DKVSRecord, chunkRecords []*wire.DKVSRecord,
	policy BlobPolicy, opts RecordVerificationOptions) (*BlobManifest, []byte, error) {
	if manifestRecord == nil || IsTombstone(manifestRecord.Flags) {
		return nil, nil, ErrBlobManifestInvalid
	}
	manifestOpts := opts
	manifestOpts.ExpectedKey = manifestRecord.Key
	if err := VerifyAccountRecordForClient(manifestRecord, manifestOpts); err != nil {
		return nil, nil, err
	}
	parsed, err := ParseKey(manifestRecord.Key)
	if err != nil {
		return nil, nil, err
	}
	if parsed.Namespace != "blob" || len(parsed.Segments) != 3 || parsed.Segments[2] != "manifest" {
		return nil, nil, ErrInvalidKey
	}
	accountID := parsed.Segments[0]
	objectID := parsed.Segments[1]
	manifest, err := ParseBlobManifestValue(manifestRecord.Value, policy)
	if err != nil {
		return nil, nil, err
	}
	if manifest.TTL != manifestRecord.TTL || manifest.ExpiryHeight != manifestRecord.ExpiryHeight {
		return nil, nil, ErrBlobManifestInvalid
	}
	chunks := make([][]byte, manifest.ChunkCount)
	for _, record := range chunkRecords {
		if record == nil || IsTombstone(record.Flags) {
			return nil, nil, ErrBlobChunkInvalid
		}
		chunkOpts := opts
		chunkOpts.ExpectedKey = record.Key
		if err := VerifyAccountRecordForClient(record, chunkOpts); err != nil {
			return nil, nil, err
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return nil, nil, err
		}
		if parsed.Namespace != "blob" || len(parsed.Segments) != 4 ||
			parsed.Segments[0] != accountID || parsed.Segments[1] != objectID || parsed.Segments[2] != "chunk" {
			return nil, nil, ErrInvalidKey
		}
		if record.Seq != manifestRecord.Seq || record.IssueTime != manifestRecord.IssueTime ||
			record.TTL != manifestRecord.TTL || record.ExpiryHeight != manifestRecord.ExpiryHeight {
			return nil, nil, ErrBlobChunkInvalid
		}
		index64, err := strconv.ParseUint(parsed.Segments[3], 10, 32)
		if err != nil || index64 >= uint64(manifest.ChunkCount) {
			return nil, nil, ErrInvalidKey
		}
		index := uint32(index64)
		if chunks[index] != nil {
			return nil, nil, ErrBlobChunkInvalid
		}
		if err := validateBlobChunkHash(manifest, index, record.Value); err != nil {
			return nil, nil, err
		}
		chunks[index] = append([]byte(nil), record.Value...)
	}
	for _, chunk := range chunks {
		if chunk == nil {
			return nil, nil, ErrBlobChunkInvalid
		}
	}
	content, err := AssembleBlob(manifest, chunks)
	if err != nil {
		return nil, nil, err
	}
	return manifest, content, nil
}
