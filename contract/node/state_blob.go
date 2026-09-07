package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/sat20-labs/satoshinet/database"
)

// Small snapshots retain their original encoding. Large snapshots use bounded
// database values; this is a storage envelope and does not affect state roots.
var stateBlobMagic = []byte("SAT20:STATE:CHUNKS:1\x00")

func stateChunkBucketKey(key []byte) []byte {
	return append(append([]byte(nil), key...), []byte(":chunks")...)
}

func putStateBlob(bucket database.Bucket, key, encoded []byte) error {
	if err := deleteStateBlob(bucket, key); err != nil {
		return err
	}
	if len(encoded) <= MaxPersistedContractStateBytes {
		return bucket.Put(key, encoded)
	}
	chunks, err := bucket.CreateBucket(stateChunkBucketKey(key))
	if err != nil {
		return err
	}
	for offset, index := 0, uint64(0); offset < len(encoded); index++ {
		end := offset + MaxPersistedContractStateBytes
		if end > len(encoded) {
			end = len(encoded)
		}
		var chunkKey [8]byte
		binary.BigEndian.PutUint64(chunkKey[:], index)
		if err := chunks.Put(chunkKey[:], encoded[offset:end]); err != nil {
			return err
		}
		offset = end
	}
	digest := sha256.Sum256(encoded)
	marker := append([]byte(nil), stateBlobMagic...)
	marker = binary.BigEndian.AppendUint64(marker, uint64(len(encoded)))
	marker = append(marker, digest[:]...)
	return bucket.Put(key, marker)
}

func getStateBlob(bucket database.Bucket, key []byte) ([]byte, error) {
	encoded := bucket.Get(key)
	if !bytes.HasPrefix(encoded, stateBlobMagic) {
		return encoded, nil
	}
	if len(encoded) != len(stateBlobMagic)+8+sha256.Size {
		return nil, fmt.Errorf("invalid contract state chunk manifest")
	}
	length := binary.BigEndian.Uint64(encoded[len(stateBlobMagic):])
	if length <= MaxPersistedContractStateBytes {
		return nil, fmt.Errorf("invalid chunked contract state length")
	}
	chunks := bucket.Bucket(stateChunkBucketKey(key))
	if chunks == nil {
		return nil, fmt.Errorf("missing contract state chunks")
	}
	// Iterate actual records, never allocate from an untrusted manifest length.
	var result []byte
	var index uint64
	err := chunks.ForEach(func(key, value []byte) error {
		if len(key) != 8 || binary.BigEndian.Uint64(key) != index || len(value) == 0 || len(value) > MaxPersistedContractStateBytes {
			return fmt.Errorf("invalid contract state chunk %d", index)
		}
		remaining := length - uint64(len(result))
		expected := uint64(MaxPersistedContractStateBytes)
		if remaining < expected {
			expected = remaining
		}
		if uint64(len(value)) != expected {
			return fmt.Errorf("invalid contract state chunk length")
		}
		result = append(result, value...)
		index++
		return nil
	})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(result)
	if uint64(len(result)) != length || !bytes.Equal(digest[:], encoded[len(stateBlobMagic)+8:]) {
		return nil, fmt.Errorf("incomplete or corrupt contract state chunks")
	}
	return result, nil
}

func deleteStateBlob(bucket database.Bucket, key []byte) error {
	if bucket.Bucket(stateChunkBucketKey(key)) != nil {
		if err := bucket.DeleteBucket(stateChunkBucketKey(key)); err != nil {
			return err
		}
	}
	return bucket.Delete(key)
}
