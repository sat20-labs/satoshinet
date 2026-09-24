package dkvs

import (
	"encoding/binary"
	"errors"

	indexercommon "github.com/sat20-labs/indexer/common"
)

// These keys are endpoint-local. They do not participate in record signatures,
// canonical path roots, or P2P snapshots. A key has one current generation
// entry, so the index grows with active records rather than write history.
var (
	recordChangeKeyPrefix         = []byte("dkvs:record-change:")
	prefixChangeKeyPrefix         = []byte("dkvs:prefix-change:")
	prefixChangeBaselineKeyPrefix = []byte("dkvs:prefix-change-baseline:")
)

func prefixChangeBaselineDBKey(path string) []byte {
	out := append([]byte{}, prefixChangeBaselineKeyPrefix...)
	return append(out, path...)
}

func (i *Indexer) prefixChangeBaselineLocked(path string) (uint64, bool, error) {
	encoded, err := i.db.Read(prefixChangeBaselineDBKey(path))
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if len(encoded) != 8 {
		return 0, false, ErrInvalidRecord
	}
	return binary.BigEndian.Uint64(encoded), true, nil
}

// A pre-index client generation must take one full snapshot. Thereafter every
// mutation is indexed and can be served incrementally.
func (i *Indexer) ensurePrefixChangeBaselineLocked(path string, generation uint64) (uint64, error) {
	baseline, found, err := i.prefixChangeBaselineLocked(path)
	if err != nil || found {
		return baseline, err
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], generation)
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	if err := batch.Put(prefixChangeBaselineDBKey(path), encoded[:]); err != nil {
		return 0, err
	}
	if err := batch.Flush(); err != nil {
		return 0, err
	}
	return generation, nil
}

func recordChangeDBKey(key string) []byte {
	out := append([]byte{}, recordChangeKeyPrefix...)
	return append(out, key...)
}

func prefixChangeDBPrefix(path string) []byte {
	out := append([]byte{}, prefixChangeKeyPrefix...)
	out = append(out, path...)
	return append(out, 0)
}

func prefixChangeDBKey(path string, generation uint64, key string) []byte {
	out := prefixChangeDBPrefix(path)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], generation)
	out = append(out, encoded[:]...)
	return append(out, key...)
}

func (i *Indexer) changedGenerationLocked(key string) (uint64, bool, error) {
	encoded, err := i.db.Read(recordChangeDBKey(key))
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if len(encoded) != 8 {
		return 0, false, ErrInvalidRecord
	}
	return binary.BigEndian.Uint64(encoded), true, nil
}

func (i *Indexer) markChangedRecordBatch(batch indexercommon.WriteBatch, path, key string, generation uint64) error {
	if batch == nil || path == "" || key == "" || generation == 0 {
		return ErrInvalidRecord
	}
	old, found, err := i.changedGenerationLocked(key)
	if err != nil {
		return err
	}
	if found {
		if err := batch.Delete(prefixChangeDBKey(path, old, key)); err != nil {
			return err
		}
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], generation)
	if err := batch.Put(recordChangeDBKey(key), encoded[:]); err != nil {
		return err
	}
	return batch.Put(prefixChangeDBKey(path, generation, key), []byte{1})
}

func (i *Indexer) clearChangedRecordBatch(batch indexercommon.WriteBatch, path, key string) error {
	if batch == nil || path == "" || key == "" {
		return ErrInvalidRecord
	}
	old, found, err := i.changedGenerationLocked(key)
	if err != nil || !found {
		return err
	}
	if err := batch.Delete(prefixChangeDBKey(path, old, key)); err != nil {
		return err
	}
	return batch.Delete(recordChangeDBKey(key))
}

// markPathMetaDirtyLocked advances each touched path once. Its callers use
// this helper in the same batch to stamp individual upserts/removals with that
// new endpoint generation.
func (i *Indexer) markDirtyChangedRecordBatch(batch indexercommon.WriteBatch, key string, present bool) error {
	parsed, err := ParseKey(key)
	if err != nil {
		return err
	}
	path := collectionPath(parsed)
	if path == "" || pathMode(parsed) == PathLocalOnly {
		return nil
	}
	if !present {
		return i.clearChangedRecordBatch(batch, path, key)
	}
	meta, err := i.readPathMetaLocked(path)
	if errors.Is(err, ErrRecordNotFound) {
		return i.markChangedRecordBatch(batch, path, key, 1)
	}
	if err != nil {
		return err
	}
	if meta.EndpointGeneration == ^uint64(0) {
		return ErrStaleGeneration
	}
	return i.markChangedRecordBatch(batch, path, key, meta.EndpointGeneration+1)
}
