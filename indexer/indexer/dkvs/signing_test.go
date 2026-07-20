package dkvs

import (
	"bytes"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/wire"
)

func NewSignedRecord(priv *btcec.PrivateKey, key string, value []byte, opts RecordOptions) (*wire.DKVSRecord, error) {
	if priv == nil {
		return nil, ErrInvalidSignature
	}
	parsed, err := ParseKey(key)
	if err != nil {
		return nil, err
	}
	var record *wire.DKVSRecord
	if isAccountScopedNamespace(parsed.Namespace) {
		record, err = NewAccountRecord(key, value, opts)
	} else {
		record, err = NewRecord(key, value, priv.PubKey().SerializeCompressed(), opts)
	}
	if err != nil {
		return nil, err
	}
	SignRecord(priv, record)
	return record, nil
}

func NewSignedTombstone(priv *btcec.PrivateKey, key string, opts RecordOptions) (*wire.DKVSRecord, error) {
	opts.Flags |= FlagTombstone
	return NewSignedRecord(priv, key, nil, opts)
}

func NewSignedRenewalRecord(priv *btcec.PrivateKey, existing *wire.DKVSRecord, opts RecordOptions) (*wire.DKVSRecord, error) {
	if priv == nil || existing == nil {
		return nil, ErrInvalidSignature
	}
	if IsTombstone(existing.Flags) {
		return nil, ErrInvalidRecord
	}
	if _, err := ParseKey(existing.Key); err != nil {
		return nil, err
	}
	pubKey := priv.PubKey().SerializeCompressed()
	parsed, err := ParseKey(existing.Key)
	if err != nil {
		return nil, err
	}
	if isAccountScopedNamespace(parsed.Namespace) {
		want, err := RecordSignerAccountID(existing, parsed)
		if err != nil {
			return nil, err
		}
		got, err := CanonicalAccountID(pubKey)
		if err != nil || got != want {
			return nil, ErrPermissionDenied
		}
	} else if !bytes.Equal(existing.PubKey, pubKey) {
		return nil, ErrPermissionDenied
	}
	if opts.ExpiryHeight <= existing.ExpiryHeight {
		return nil, ErrInvalidRecord
	}
	record := *existing
	record.PubKey = append([]byte{}, existing.PubKey...)
	record.Value = append([]byte{}, existing.Value...)
	record.Signature = nil
	record.IssueTime = opts.IssueTime
	if record.IssueTime == 0 {
		record.IssueTime = currentUnixMilli()
	}
	if opts.TTL != 0 {
		record.TTL = opts.TTL
	}
	record.ExpiryHeight = opts.ExpiryHeight
	if opts.FeeProof != nil {
		record.FeeProof = append([]byte{}, opts.FeeProof...)
	} else {
		record.FeeProof = append([]byte{}, existing.FeeProof...)
	}
	if RecordSize(&record) > wire.MaxDKVSRecordSize ||
		len(record.Value) > MaxRecordValueSize {
		return nil, ErrRecordTooLarge
	}
	SignRecord(priv, &record)
	return &record, nil
}

func SignRecord(priv *btcec.PrivateKey, record *wire.DKVSRecord) {
	if priv == nil || record == nil {
		return
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return
	}
	hash := SigningHash(record)
	if isAccountScopedNamespace(parsed.Namespace) {
		record.PubKey = nil
		hash = SigningHash(record)
		sig, err := schnorr.Sign(priv, hash[:])
		if err == nil {
			record.Signature = sig.Serialize()
		}
		return
	}
	record.PubKey = priv.PubKey().SerializeCompressed()
	hash = SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
}

func AttachSignedFeeProof(record *wire.DKVSRecord, proof *FeeProof, priv *btcec.PrivateKey) error {
	if record == nil || proof == nil || priv == nil {
		return ErrInvalidFeeProof
	}
	encoded, err := EncodeFeeProof(proof)
	if err != nil {
		return err
	}
	record.FeeProof = encoded
	return nil
}

func BuildSignedBlobRecords(priv *btcec.PrivateKey, objectID string, chunks [][]byte, metadata []byte, opts RecordOptions) (*wire.DKVSRecord, []*wire.DKVSRecord, error) {
	if opts.IssueTime == 0 {
		opts.IssueTime = currentUnixMilli()
	}
	accountID := AccountID(priv.PubKey().SerializeCompressed())
	manifest, manifestValue, err := BuildBlobManifest(chunks, metadata, opts.TTL, opts.ExpiryHeight)
	if err != nil {
		return nil, nil, err
	}
	manifestKey, err := BlobManifestKey(accountID, objectID)
	if err != nil {
		return nil, nil, err
	}
	manifestRecord, err := NewSignedRecord(priv, manifestKey, manifestValue, opts)
	if err != nil {
		return nil, nil, err
	}
	chunkRecords := make([]*wire.DKVSRecord, 0, manifest.ChunkCount)
	for n, chunk := range chunks {
		chunkKey, err := BlobChunkKey(accountID, objectID, uint32(n))
		if err != nil {
			return nil, nil, err
		}
		chunkRecord, err := NewSignedRecord(priv, chunkKey, chunk, opts)
		if err != nil {
			return nil, nil, err
		}
		chunkRecords = append(chunkRecords, chunkRecord)
	}
	return manifestRecord, chunkRecords, nil
}
