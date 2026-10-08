package dkvs

import (
	"bytes"
	"errors"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func bytesEqual(a, b []byte) bool { return bytes.Equal(a, b) }

func (i *Indexer) GetForRelay(key string) (*wire.DKVSRecord, error) {
	record, err := i.Get(key)
	if err != nil { return nil, err }
	if i.isLocalOnlyRecord(record) { return nil, ErrRecordNotFound }
	return record, nil
}

// Deletion removes the row, content-hash index and latest-generation index in
// one transaction. Commands are not stored as records or deletion history.
func (i *Indexer) commitDeleteLocked(parsed ParsedKey, record, command *wire.DKVSRecord,
	clearNameTransfer bool, height, now uint64) (chainhash.Hash, error) {
	if IsEVMSourceKey(parsed) { return chainhash.Hash{}, ErrPermissionDenied }
	if record == nil { return chainhash.Hash{}, ErrRecordNotFound }
	if command != nil && !deleteTargetsRecord(command, record) { return chainhash.Hash{}, ErrWriteConflict }
	oldHash := RecordHash(record)
	// Use the same current-state projection as RPC CAS. The cached relay
	// permission may be cold, so subtracting a leaf using current relayability
	// would leave a phantom leaf from the previously committed warm view.
	projection := command
	if projection == nil { projection = &wire.DKVSRecord{Key: record.Key, Flags: FlagTombstone} }
	metas, err := i.projectPathMetasLocked([]preparedCASMutation{{
		mutation: CASMutation{Record: projection}, parsed: parsed, snapshot: writeStateSnapshot{existing: record},
	}}, height, now)
	if err != nil { return chainhash.Hash{}, err }
	meta := metas[collectionPath(parsed)]
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	if err := batch.Delete(recordDBKey(record.Key)); err != nil { return chainhash.Hash{}, err }
	if err := batch.Delete(hashDBKey(oldHash)); err != nil { return chainhash.Hash{}, err }
	if meta != nil {
		if err := i.clearChangedRecordBatch(batch, meta.Path, record.Key); err != nil { return chainhash.Hash{}, err }
		if err := putPathStatusBatch(batch, &PathLocalStatus{Path: meta.Path, UpdatedAt: now}); err != nil { return chainhash.Hash{}, err }
	}
	if clearNameTransfer && parsed.Namespace == "name" && len(parsed.Segments) == 1 {
		if err := batch.Delete(nameTransferDBKey(parsed.Segments[0])); err != nil { return chainhash.Hash{}, err }
	}
	if err := putPathMetaBatch(batch, meta); err != nil { return chainhash.Hash{}, err }
	if err := batch.Flush(); err != nil { return chainhash.Hash{}, err }
	paidRetentionCacheFor(i).remove([]string{record.Key})
	i.resetFeeUsageLocked()
	i.resetFreeLocalUsageLocked()
	i.resetRecordExpiryLocked()
	if command != nil { return RecordHash(command), nil }
	return oldHash, nil
}

func (i *Indexer) deleteMirrorRecordLocked(record *wire.DKVSRecord, height, now uint64) error {
	if record == nil { return ErrRecordNotFound }
	parsed, err := ParseKey(record.Key)
	if err != nil { return err }
	if _, err := i.commitDeleteLocked(parsed, record, nil, false, height, now); err != nil { return err }
	i.notifyPathMutation(record)
	return nil
}

func (i *Indexer) DeleteMirrorKeys(keys []string) (int, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	deleted := 0
	for _, key := range keys {
		if _, err := ParseKey(key); err != nil { return deleted, err }
		record, err := i.getRaw(key)
		if errors.Is(err, ErrRecordNotFound) { continue }
		if err != nil { return deleted, err }
		if err := i.deleteMirrorRecordLocked(record, i.currentHeight(), currentUnixMilli()); err != nil { return deleted, err }
		deleted++
		atomicAddGeneration(&i.generation)
	}
	return deleted, nil
}

func atomicAddGeneration(generation *uint64) { atomic.AddUint64(generation, 1) }
