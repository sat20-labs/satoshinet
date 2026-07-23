package dkvs

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
)

const (
	blobManifestMagic   = "DKBM"
	blobManifestVersion = byte(1)
	maxBlobMetadataSize = 16 * 1024
)

func encodeBlobManifest(manifest *BlobManifest) ([]byte, error) {
	if manifest == nil || len(manifest.Metadata) > maxBlobMetadataSize {
		return nil, ErrBlobManifestInvalid
	}
	contentHash, err := decodeHashHex(manifest.ContentHash)
	if err != nil || len(manifest.ChunkHashes) != int(manifest.ChunkCount) {
		return nil, ErrBlobManifestInvalid
	}

	var buf bytes.Buffer
	buf.WriteString(blobManifestMagic)
	buf.WriteByte(blobManifestVersion)
	buf.Write(contentHash)
	for _, value := range []uint64{manifest.TotalSize, manifest.TTL, manifest.ExpiryHeight} {
		if err := binary.Write(&buf, binary.BigEndian, value); err != nil {
			return nil, err
		}
	}
	for _, value := range []uint32{manifest.ChunkSize, manifest.ChunkCount} {
		if err := binary.Write(&buf, binary.BigEndian, value); err != nil {
			return nil, err
		}
	}
	if err := binary.Write(&buf, binary.BigEndian, uint32(len(manifest.Metadata))); err != nil {
		return nil, err
	}
	buf.Write(manifest.Metadata)
	for _, hash := range manifest.ChunkHashes {
		decoded, err := decodeHashHex(hash)
		if err != nil {
			return nil, ErrBlobManifestInvalid
		}
		buf.Write(decoded)
	}
	return buf.Bytes(), nil
}

func decodeBlobManifest(value []byte) (*BlobManifest, error) {
	const fixedSize = len(blobManifestMagic) + 1 + 32 + 8 + 8 + 8 + 4 + 4 + 4
	if len(value) < fixedSize || string(value[:len(blobManifestMagic)]) != blobManifestMagic || value[len(blobManifestMagic)] != blobManifestVersion {
		return nil, ErrBlobManifestInvalid
	}
	reader := bytes.NewReader(value[len(blobManifestMagic)+1:])
	contentHash := make([]byte, 32)
	if _, err := reader.Read(contentHash); err != nil {
		return nil, ErrBlobManifestInvalid
	}
	manifest := &BlobManifest{ContentHash: hex.EncodeToString(contentHash)}
	for _, target := range []*uint64{&manifest.TotalSize, &manifest.TTL, &manifest.ExpiryHeight} {
		if err := binary.Read(reader, binary.BigEndian, target); err != nil {
			return nil, ErrBlobManifestInvalid
		}
	}
	for _, target := range []*uint32{&manifest.ChunkSize, &manifest.ChunkCount} {
		if err := binary.Read(reader, binary.BigEndian, target); err != nil {
			return nil, ErrBlobManifestInvalid
		}
	}
	var metadataLen uint32
	if err := binary.Read(reader, binary.BigEndian, &metadataLen); err != nil || metadataLen > maxBlobMetadataSize || uint64(metadataLen) > uint64(reader.Len()) {
		return nil, ErrBlobManifestInvalid
	}
	if metadataLen != 0 {
		manifest.Metadata = make([]byte, metadataLen)
		if _, err := reader.Read(manifest.Metadata); err != nil {
			return nil, ErrBlobManifestInvalid
		}
	}
	if manifest.ChunkCount > uint32(reader.Len()/32) || reader.Len() != int(manifest.ChunkCount)*32 {
		return nil, ErrBlobManifestInvalid
	}
	manifest.ChunkHashes = make([]string, manifest.ChunkCount)
	for index := range manifest.ChunkHashes {
		hash := make([]byte, 32)
		if _, err := reader.Read(hash); err != nil {
			return nil, ErrBlobManifestInvalid
		}
		manifest.ChunkHashes[index] = hex.EncodeToString(hash)
	}
	return manifest, nil
}

// RewriteBlobManifestRetention updates only the lifetime fields of a canonical
// DKVS blob manifest while preserving its content and chunk commitments.
func RewriteBlobManifestRetention(value []byte, ttl, expiryHeight uint64) ([]byte, error) {
	manifest, err := decodeBlobManifest(value)
	if err != nil {
		return nil, err
	}
	manifest.TTL = ttl
	manifest.ExpiryHeight = expiryHeight
	return encodeBlobManifest(manifest)
}
