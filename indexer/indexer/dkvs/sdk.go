package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type RecordOptions struct {
	Seq          uint64
	IssueTime    uint64
	TTL          uint64
	ExpiryHeight uint64
	FeeProof     []byte
	Data         []byte
	Flags        uint32
}

type RecordVerificationOptions struct {
	ExpectedKey  string
	ExpectedHash chainhash.Hash
	CheckHash    bool
	Height       uint64
	Now          uint64
	FeeVerifier  FeeVerifier
}

func NewRecord(key string, value []byte, pubKey []byte, opts RecordOptions) (*wire.DKVSRecord, error) {
	if _, err := ParseKey(key); err != nil {
		return nil, err
	}
	record := &wire.DKVSRecord{
		Version:      Version,
		Key:          key,
		Value:        append([]byte{}, value...),
		Data:         append([]byte{}, opts.Data...),
		PubKey:       append([]byte{}, pubKey...),
		Seq:          opts.Seq,
		IssueTime:    opts.IssueTime,
		TTL:          opts.TTL,
		ExpiryHeight: opts.ExpiryHeight,
		FeeProof:     append([]byte{}, opts.FeeProof...),
		Flags:        opts.Flags,
	}
	if record.IssueTime == 0 {
		record.IssueTime = currentUnixMilli()
	}
	if RecordSize(record) > wire.MaxDKVSRecordSize ||
		len(record.Value) > MaxRecordValueSize ||
		len(record.Data) > MaxRecordDataSize {
		return nil, ErrRecordTooLarge
	}
	return record, nil
}

func VerifyRecordForClient(record *wire.DKVSRecord, opts RecordVerificationOptions) error {
	if record == nil || record.Version != Version {
		return ErrInvalidRecord
	}
	if opts.ExpectedKey != "" && record.Key != opts.ExpectedKey {
		return ErrInvalidKey
	}
	if len(record.Value) > MaxRecordValueSize || len(record.Data) > MaxRecordDataSize ||
		RecordSize(record) > wire.MaxDKVSRecordSize {
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
	if IsTombstone(record.Flags) && len(record.Value) != 0 {
		return ErrInvalidRecord
	}
	recordHash := RecordHash(record)
	if opts.CheckHash && recordHash != opts.ExpectedHash {
		return ErrInvalidRecord
	}
	if opts.FeeVerifier != nil {
		feeAnchorHash := FeeAnchorHash(record)
		var recordHash32 [32]byte
		copy(recordHash32[:], feeAnchorHash[:])
		keyHash := KeyHash(record.Key)
		var keyHash32 [32]byte
		copy(keyHash32[:], keyHash[:])
		if err := opts.FeeVerifier.VerifyFeeProof(recordHash32, keyHash32, parsed.Namespace, RecordSize(record), record.ExpiryHeight, record.FeeProof); err != nil {
			return err
		}
	}
	return nil
}

func VerifyRecordsForClient(records []*wire.DKVSRecord, prefix string, opts RecordVerificationOptions) error {
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
		if err := VerifyRecordForClient(record, recordOpts); err != nil {
			return err
		}
	}
	return nil
}

func VerifySubscriptionRecordsForClient(records []*wire.DKVSRecord, sub Subscription, opts RecordVerificationOptions) error {
	sub, err := validateSubscription(sub)
	if err != nil {
		return err
	}
	if opts.CheckHash && len(records) != 1 {
		return ErrInvalidRecord
	}
	for _, record := range records {
		if record == nil || !subscriptionMatchesKey(sub, record.Key) {
			return ErrInvalidKey
		}
		recordOpts := opts
		recordOpts.ExpectedKey = ""
		if err := VerifyRecordForClient(record, recordOpts); err != nil {
			return err
		}
	}
	return nil
}

func NewSignedRecord(priv *btcec.PrivateKey, key string, value []byte, opts RecordOptions) (*wire.DKVSRecord, error) {
	if priv == nil {
		return nil, ErrInvalidSignature
	}
	record, err := NewRecord(key, value, priv.PubKey().SerializeCompressed(), opts)
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
	if !bytes.Equal(existing.PubKey, pubKey) {
		return nil, ErrPermissionDenied
	}
	if opts.ExpiryHeight <= existing.ExpiryHeight {
		return nil, ErrInvalidRecord
	}
	record := *existing
	record.PubKey = append([]byte{}, existing.PubKey...)
	record.Value = append([]byte{}, existing.Value...)
	record.Data = append([]byte{}, existing.Data...)
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
		len(record.Value) > MaxRecordValueSize ||
		len(record.Data) > MaxRecordDataSize {
		return nil, ErrRecordTooLarge
	}
	SignRecord(priv, &record)
	return &record, nil
}

func SignRecord(priv *btcec.PrivateKey, record *wire.DKVSRecord) {
	if priv == nil || record == nil {
		return
	}
	record.PubKey = priv.PubKey().SerializeCompressed()
	hash := SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
}

func AccountID(pubKey []byte) string {
	return personalAccountID(pubKey)
}

func PersonalKey(pubKey []byte, path string) (string, error) {
	key := "/personal/" + AccountID(pubKey) + "/" + normalizePath(path)
	_, err := ParseKey(key)
	return key, err
}

func NameKey(name string) (string, error) {
	key := "/name/" + NormalizeNameID(name)
	_, err := ParseKey(key)
	return key, err
}

func ServiceKey(serviceName, path string) (string, error) {
	key := "/svc/" + NormalizeNameID(serviceName) + "/" + normalizePath(path)
	_, err := ParseKey(key)
	return key, err
}

func MailMsgKey(mailboxID, msgID string) (string, error) {
	key := "/mail/" + mailboxID + "/msg/" + msgID
	_, err := ParseKey(key)
	return key, err
}

func MailShareKey(mailboxID, packageID, shareID string) (string, error) {
	key := "/mail/" + mailboxID + "/share/" + packageID + "/" + shareID
	_, err := ParseKey(key)
	return key, err
}

func BlobManifestKey(objectID string) (string, error) {
	key := "/blob/" + objectID + "/manifest"
	_, err := ParseKey(key)
	return key, err
}

func BlobChunkKey(objectID string, index uint32) (string, error) {
	key := "/blob/" + objectID + "/chunk/" + strconv.FormatUint(uint64(index), 10)
	_, err := ParseKey(key)
	return key, err
}

func TmpKey(randomID string) (string, error) {
	key := "/tmp/" + randomID
	_, err := ParseKey(key)
	return key, err
}

func BuildBlobManifest(chunks [][]byte, metadata json.RawMessage, ttl, expiryHeight uint64) (*BlobManifest, []byte, error) {
	if len(chunks) == 0 {
		return nil, nil, ErrBlobManifestInvalid
	}
	var content []byte
	chunkHashes := make([]string, 0, len(chunks))
	chunkSize := 0
	for _, chunk := range chunks {
		if len(chunk) == 0 || len(chunk) > MaxRecordValueSize {
			return nil, nil, ErrBlobManifestInvalid
		}
		if len(chunk) > chunkSize {
			chunkSize = len(chunk)
		}
		sum := sha256.Sum256(chunk)
		chunkHashes = append(chunkHashes, hex.EncodeToString(sum[:]))
		content = append(content, chunk...)
	}
	contentHash := sha256.Sum256(content)
	manifest := &BlobManifest{
		ContentHash:  hex.EncodeToString(contentHash[:]),
		TotalSize:    uint64(len(content)),
		ChunkSize:    uint32(chunkSize),
		ChunkCount:   uint32(len(chunks)),
		ChunkHashes:  chunkHashes,
		TTL:          ttl,
		ExpiryHeight: expiryHeight,
		Metadata:     metadata,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, nil, err
	}
	if _, err := parseBlobManifest(encoded, normalizeBlobPolicy(BlobPolicy{})); err != nil {
		return nil, nil, err
	}
	return manifest, encoded, nil
}

func BuildSignedBlobRecords(priv *btcec.PrivateKey, objectID string, chunks [][]byte, metadata json.RawMessage, opts RecordOptions) (*wire.DKVSRecord, []*wire.DKVSRecord, error) {
	manifest, manifestValue, err := BuildBlobManifest(chunks, metadata, opts.TTL, opts.ExpiryHeight)
	if err != nil {
		return nil, nil, err
	}
	manifestKey, err := BlobManifestKey(objectID)
	if err != nil {
		return nil, nil, err
	}
	manifestRecord, err := NewSignedRecord(priv, manifestKey, manifestValue, opts)
	if err != nil {
		return nil, nil, err
	}
	chunkRecords := make([]*wire.DKVSRecord, 0, manifest.ChunkCount)
	for n, chunk := range chunks {
		chunkKey, err := BlobChunkKey(objectID, uint32(n))
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

func ParseBlobManifestValue(value []byte, policy BlobPolicy) (*BlobManifest, error) {
	return parseBlobManifest(value, normalizeBlobPolicy(policy))
}

func AssembleBlob(manifest *BlobManifest, chunks [][]byte) ([]byte, error) {
	if manifest == nil || len(chunks) != int(manifest.ChunkCount) {
		return nil, ErrBlobChunkInvalid
	}
	var content bytes.Buffer
	for n, chunk := range chunks {
		if err := validateBlobChunkHash(manifest, uint32(n), chunk); err != nil {
			return nil, err
		}
		content.Write(chunk)
	}
	if uint64(content.Len()) != manifest.TotalSize {
		return nil, ErrBlobChunkInvalid
	}
	sum := sha256.Sum256(content.Bytes())
	want, err := decodeHashHex(manifest.ContentHash)
	if err != nil || !bytes.Equal(sum[:], want) {
		return nil, ErrBlobChunkInvalid
	}
	return content.Bytes(), nil
}

func AssembleBlobFromRecords(manifestRecord *wire.DKVSRecord, chunkRecords []*wire.DKVSRecord, policy BlobPolicy) (*BlobManifest, []byte, error) {
	if manifestRecord == nil || IsTombstone(manifestRecord.Flags) {
		return nil, nil, ErrBlobManifestInvalid
	}
	if err := VerifySignature(manifestRecord); err != nil {
		return nil, nil, err
	}
	parsed, err := ParseKey(manifestRecord.Key)
	if err != nil {
		return nil, nil, err
	}
	if parsed.Namespace != "blob" || len(parsed.Segments) != 2 || parsed.Segments[1] != "manifest" {
		return nil, nil, ErrInvalidKey
	}
	objectID := parsed.Segments[0]
	manifest, err := ParseBlobManifestValue(manifestRecord.Value, policy)
	if err != nil {
		return nil, nil, err
	}
	chunks := make([][]byte, manifest.ChunkCount)
	for _, record := range chunkRecords {
		if record == nil || IsTombstone(record.Flags) {
			return nil, nil, ErrBlobChunkInvalid
		}
		if err := VerifySignature(record); err != nil {
			return nil, nil, err
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return nil, nil, err
		}
		if parsed.Namespace != "blob" || len(parsed.Segments) != 3 ||
			parsed.Segments[0] != objectID || parsed.Segments[1] != "chunk" {
			return nil, nil, ErrInvalidKey
		}
		index64, err := strconv.ParseUint(parsed.Segments[2], 10, 32)
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
		chunks[index] = append([]byte{}, record.Value...)
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

func CheckpointFromRecords(records []*wire.DKVSRecord, height uint64) (*Checkpoint, error) {
	return checkpointFromRecords(records, height)
}

func ValidateSnapshot(snapshot *Snapshot) error {
	if snapshot == nil || snapshot.Checkpoint == nil {
		return ErrInvalidSnapshot
	}
	checkpoint, err := checkpointFromRecords(snapshot.Records, snapshot.Checkpoint.Height)
	if err != nil {
		return err
	}
	if checkpoint.ActiveRecordRoot != snapshot.Checkpoint.ActiveRecordRoot ||
		checkpoint.ActiveRecordCount != snapshot.Checkpoint.ActiveRecordCount ||
		checkpoint.ActiveRecordTotalSize != snapshot.Checkpoint.ActiveRecordTotalSize {
		return ErrInvalidSnapshot
	}
	if len(checkpoint.NamespaceRoots) != len(snapshot.Checkpoint.NamespaceRoots) {
		return ErrInvalidSnapshot
	}
	for namespace, root := range checkpoint.NamespaceRoots {
		if snapshot.Checkpoint.NamespaceRoots[namespace] != root {
			return ErrInvalidSnapshot
		}
	}
	return nil
}

func normalizePath(path string) string {
	return strings.Trim(strings.TrimSpace(path), "/")
}
