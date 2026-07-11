package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/sat20-labs/satoshinet/wire"
)

func (i *Indexer) validateBlobLocked(record *wire.DKVSRecord, parsed ParsedKey, height, now uint64) error {
	if len(parsed.Segments) < 2 {
		return ErrInvalidKey
	}
	if IsTombstone(record.Flags) {
		return nil
	}

	accountID := parsed.Segments[0]
	objectID := parsed.Segments[1]
	switch parsed.Segments[2] {
	case "manifest":
		_, err := parseBlobManifest(record.Value, i.blob)
		return err
	case "chunk":
		if len(record.Value) > i.blob.MaxChunkSize {
			return ErrRecordTooLarge
		}
		index, err := strconv.Atoi(parsed.Segments[3])
		if err != nil || index < 0 {
			return ErrInvalidKey
		}
		manifestRecord, manifest, err := i.getActiveBlobManifestLocked(accountID, objectID, height, now)
		if errors.Is(err, ErrRecordNotFound) {
			return ErrBlobManifestInvalid
		}
		if err != nil {
			return err
		}
		if index >= int(manifest.ChunkCount) {
			return ErrBlobChunkInvalid
		}
		if !bytes.Equal(record.PubKey, manifestRecord.PubKey) || record.Seq != manifestRecord.Seq ||
			record.ExpiryHeight != manifestRecord.ExpiryHeight {
			return ErrBlobChunkInvalid
		}
		if err := validateBlobChunkHash(manifest, uint32(index), record.Value); err != nil {
			return err
		}
		return nil
	default:
		return ErrInvalidKey
	}
}

func parseBlobManifest(value []byte, policy BlobPolicy) (*BlobManifest, error) {
	var manifest BlobManifest
	if err := json.Unmarshal(value, &manifest); err != nil {
		return nil, ErrBlobManifestInvalid
	}
	if manifest.ContentHash == "" || manifest.ChunkSize == 0 || manifest.ChunkCount == 0 ||
		uint64(manifest.ChunkSize) > uint64(policy.MaxChunkSize) ||
		manifest.ChunkCount > policy.MaxChunks ||
		manifest.TotalSize > policy.MaxTotalSize ||
		uint64(manifest.ChunkCount)*uint64(manifest.ChunkSize) < manifest.TotalSize ||
		len(manifest.ChunkHashes) != int(manifest.ChunkCount) {
		return nil, ErrBlobManifestInvalid
	}
	if _, err := decodeHashHex(manifest.ContentHash); err != nil {
		return nil, ErrBlobManifestInvalid
	}
	for _, hash := range manifest.ChunkHashes {
		if _, err := decodeHashHex(hash); err != nil {
			return nil, ErrBlobManifestInvalid
		}
	}
	return &manifest, nil
}

func (i *Indexer) getActiveBlobManifestLocked(accountID, objectID string, height, now uint64) (*wire.DKVSRecord, *BlobManifest, error) {
	record, err := i.getRaw("/blob/" + accountID + "/" + objectID + "/manifest")
	if err != nil {
		return nil, nil, err
	}
	if err := i.activeError(record, height, now); err != nil || IsTombstone(record.Flags) {
		return nil, nil, ErrRecordNotFound
	}
	manifest, err := parseBlobManifest(record.Value, i.blob)
	return record, manifest, err
}

func validateBlobChunkHash(manifest *BlobManifest, index uint32, value []byte) error {
	if index >= manifest.ChunkCount || int(index) >= len(manifest.ChunkHashes) {
		return ErrBlobChunkInvalid
	}
	sum := sha256.Sum256(value)
	want, err := decodeHashHex(manifest.ChunkHashes[index])
	if err != nil || !bytes.Equal(sum[:], want) {
		return ErrBlobChunkInvalid
	}
	return nil
}

func decodeHashHex(hash string) ([]byte, error) {
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrBlobManifestInvalid
	}
	return decoded, nil
}
