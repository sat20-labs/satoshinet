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

	objectID := parsed.Segments[0]
	switch parsed.Segments[1] {
	case "manifest":
		manifest, err := parseBlobManifest(record.Value, i.blob)
		if err != nil {
			return err
		}
		return i.validateBlobContentLocked(objectID, manifest, -1, nil, height, now)
	case "chunk":
		if len(record.Value) > i.blob.MaxChunkSize {
			return ErrRecordTooLarge
		}
		index, err := strconv.Atoi(parsed.Segments[2])
		if err != nil || index < 0 {
			return ErrInvalidKey
		}
		manifest, err := i.getActiveBlobManifestLocked(objectID, height, now)
		if errors.Is(err, ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if index >= int(manifest.ChunkCount) {
			return ErrBlobChunkInvalid
		}
		if err := validateBlobChunkHash(manifest, uint32(index), record.Value); err != nil {
			return err
		}
		return i.validateBlobContentLocked(objectID, manifest, index, record.Value, height, now)
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

func (i *Indexer) getActiveBlobManifestLocked(objectID string, height, now uint64) (*BlobManifest, error) {
	record, err := i.getRaw("/blob/" + objectID + "/manifest")
	if err != nil {
		return nil, err
	}
	if err := i.activeError(record, height, now); err != nil || IsTombstone(record.Flags) {
		return nil, ErrRecordNotFound
	}
	return parseBlobManifest(record.Value, i.blob)
}

func (i *Indexer) validateBlobContentLocked(objectID string, manifest *BlobManifest, pendingIndex int, pendingValue []byte, height, now uint64) error {
	chunks := make([][]byte, 0, manifest.ChunkCount)
	allFound := true
	for n := uint32(0); n < manifest.ChunkCount; n++ {
		var value []byte
		if int(n) == pendingIndex {
			value = pendingValue
		} else {
			record, err := i.getRaw("/blob/" + objectID + "/chunk/" + strconv.Itoa(int(n)))
			if err != nil {
				allFound = false
				continue
			}
			if err := i.activeError(record, height, now); err != nil || IsTombstone(record.Flags) {
				allFound = false
				continue
			}
			value = record.Value
		}
		if err := validateBlobChunkHash(manifest, n, value); err != nil {
			return err
		}
		chunks = append(chunks, value)
	}
	if !allFound {
		return nil
	}
	var content bytes.Buffer
	for _, chunk := range chunks {
		content.Write(chunk)
	}
	if uint64(content.Len()) != manifest.TotalSize {
		return ErrBlobChunkInvalid
	}
	sum := sha256.Sum256(content.Bytes())
	want, _ := decodeHashHex(manifest.ContentHash)
	if !bytes.Equal(sum[:], want) {
		return ErrBlobChunkInvalid
	}
	return nil
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
