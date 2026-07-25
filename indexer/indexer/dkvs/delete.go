package dkvs

import (
	"encoding/binary"
	"errors"
	"sync/atomic"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const deleteStateVersion = uint32(2)

const deleteRelayRetention = uint64((7 * 24 * time.Hour) / time.Millisecond)

var deleteKeyPrefix = []byte("dkvs:delete:")

type deleteState struct {
	FloorSeq   uint64
	RelayUntil uint64
	PubKey     []byte
	Record     *wire.DKVSRecord
	LocalOnly  bool
}

func deleteDBKey(key string) []byte {
	out := make([]byte, 0, len(deleteKeyPrefix)+len(key))
	out = append(out, deleteKeyPrefix...)
	out = append(out, key...)
	return out
}

func marshalDeleteState(state *deleteState) ([]byte, error) {
	if state == nil || len(state.PubKey) > wire.MaxDKVSPubKeySize {
		return nil, ErrInvalidRecord
	}
	var recordBytes []byte
	var err error
	if state.Record != nil {
		if !IsTombstone(state.Record.Flags) || len(state.Record.Value) != 0 {
			return nil, ErrInvalidRecord
		}
		recordBytes, err = MarshalRecord(state.Record)
		if err != nil {
			return nil, err
		}
	}
	encoded := make([]byte, 4+8+8+1+2+4+len(state.PubKey)+len(recordBytes))
	binary.LittleEndian.PutUint32(encoded[0:4], deleteStateVersion)
	binary.LittleEndian.PutUint64(encoded[4:12], state.FloorSeq)
	binary.LittleEndian.PutUint64(encoded[12:20], state.RelayUntil)
	if state.LocalOnly {
		encoded[20] = 1
	}
	binary.LittleEndian.PutUint16(encoded[21:23], uint16(len(state.PubKey)))
	binary.LittleEndian.PutUint32(encoded[23:27], uint32(len(recordBytes)))
	copy(encoded[27:], state.PubKey)
	copy(encoded[27+len(state.PubKey):], recordBytes)
	return encoded, nil
}

func unmarshalDeleteState(encoded []byte) (*deleteState, error) {
	if len(encoded) < 26 {
		return nil, ErrInvalidRecord
	}
	version := binary.LittleEndian.Uint32(encoded[0:4])
	pubKeyOffset := 20
	recordSizeOffset := 22
	dataOffset := 26
	localOnly := false
	if version == deleteStateVersion {
		if len(encoded) < 27 {
			return nil, ErrInvalidRecord
		}
		localOnly = encoded[20] == 1
		pubKeyOffset = 21
		recordSizeOffset = 23
		dataOffset = 27
	} else if version != 1 {
		return nil, ErrInvalidRecord
	}
	pubKeySize := int(binary.LittleEndian.Uint16(encoded[pubKeyOffset : pubKeyOffset+2]))
	recordSize := int(binary.LittleEndian.Uint32(encoded[recordSizeOffset : recordSizeOffset+4]))
	if pubKeySize < 0 || pubKeySize > wire.MaxDKVSPubKeySize || recordSize < 0 ||
		dataOffset+pubKeySize+recordSize != len(encoded) {
		return nil, ErrInvalidRecord
	}
	state := &deleteState{
		FloorSeq:   binary.LittleEndian.Uint64(encoded[4:12]),
		RelayUntil: binary.LittleEndian.Uint64(encoded[12:20]),
		PubKey:     append([]byte{}, encoded[dataOffset:dataOffset+pubKeySize]...),
		LocalOnly:  localOnly,
	}
	if recordSize == 0 {
		return state, nil
	}
	record, err := UnmarshalRecord(encoded[dataOffset+pubKeySize:])
	if err != nil || !IsTombstone(record.Flags) || len(record.Value) != 0 {
		return nil, ErrInvalidRecord
	}
	state.Record = record
	if len(state.PubKey) == 0 {
		state.PubKey = append([]byte{}, record.PubKey...)
	}
	return state, nil
}

func (i *Indexer) getDeleteStateLocked(key string) (*deleteState, error) {
	encoded, err := i.db.Read(deleteDBKey(key))
	if err != nil {
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	state, err := unmarshalDeleteState(encoded)
	if err != nil {
		return nil, err
	}
	if state.Record != nil {
		if state.Record.Key != key || state.Record.Seq > state.FloorSeq ||
			!bytesEqual(state.Record.PubKey, state.PubKey) {
			return nil, ErrInvalidRecord
		}
	}
	return state, nil
}

func putDeleteStateBatch(batch indexercommon.WriteBatch, key string, state *deleteState) error {
	encoded, err := marshalDeleteState(state)
	if err != nil {
		return err
	}
	return batch.Put(deleteDBKey(key), encoded)
}

func deleteDeleteStateBatch(batch indexercommon.WriteBatch, key string) error {
	return batch.Delete(deleteDBKey(key))
}

func deleteFloorBlocksRecord(parsed ParsedKey, state *deleteState, record *wire.DKVSRecord) bool {
	if state == nil || record == nil || IsTombstone(record.Flags) || record.Seq > state.FloorSeq {
		return false
	}
	// Name and service ownership can rotate. A currently authorized different
	// signer is allowed to restart its own sequence after the previous owner
	// deleted the key.
	if (parsed.Namespace == "name" || parsed.Namespace == "svc") && len(state.PubKey) != 0 &&
		!bytesEqual(state.PubKey, record.PubKey) {
		return false
	}
	return true
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

func deleteRelayUntil(now uint64) uint64 {
	if now > ^uint64(0)-deleteRelayRetention {
		return ^uint64(0)
	}
	return now + deleteRelayRetention
}

func deleteRecordForRelay(state *deleteState, now uint64) *wire.DKVSRecord {
	if state == nil || state.Record == nil || state.RelayUntil == 0 || now >= state.RelayUntil {
		return nil
	}
	return state.Record
}

// GetForRelay returns either the active record or a retained signed delete
// command. Delete commands are never exposed through the normal Get API.
func (i *Indexer) GetForRelay(key string) (*wire.DKVSRecord, error) {
	if _, err := ParseKey(key); err != nil {
		return nil, err
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	if record, err := i.getRaw(key); err == nil {
		if i.activeError(record, i.currentHeight(), currentUnixMilli()) == nil {
			if i.isLocalOnlyRecord(record) {
				return nil, ErrRecordNotFound
			}
			return record, nil
		}
	} else if !errors.Is(err, ErrRecordNotFound) {
		return nil, err
	}
	state, err := i.getDeleteStateLocked(key)
	if err != nil {
		return nil, err
	}
	if deleteRecordForRelay(state, currentUnixMilli()) == nil {
		return nil, ErrRecordNotFound
	}
	if state.LocalOnly {
		return nil, ErrRecordNotFound
	}
	return state.Record, nil
}

func (i *Indexer) commitDeleteLocked(parsed ParsedKey, record, deleteRecord *wire.DKVSRecord, floorSeq uint64, retainCommand bool, clearNameTransfer bool, height, now uint64) (chainhash.Hash, error) {
	if record == nil {
		return chainhash.Hash{}, ErrRecordNotFound
	}
	recordsToDelete := []*wire.DKVSRecord{record}
	oldHash := RecordHash(record)
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	for _, candidate := range recordsToDelete {
		if err := batch.Delete(recordDBKey(candidate.Key)); err != nil {
			return chainhash.Hash{}, err
		}
		if err := batch.Delete(hashDBKey(RecordHash(candidate))); err != nil {
			return chainhash.Hash{}, err
		}
	}
	state := &deleteState{
		FloorSeq:  floorSeq,
		PubKey:    append([]byte{}, record.PubKey...),
		LocalOnly: isFreeLocalRecord(record) || isFreeLocalRecord(deleteRecord),
	}
	if retainCommand {
		state.PubKey = append(state.PubKey[:0], deleteRecord.PubKey...)
		state.RelayUntil = deleteRelayUntil(now)
		state.Record = deleteRecord
	}
	if err := putDeleteStateBatch(batch, record.Key, state); err != nil {
		return chainhash.Hash{}, err
	}
	if clearNameTransfer && parsed.Namespace == "name" && len(parsed.Segments) == 1 {
		if err := batch.Delete(nameTransferDBKey(parsed.Segments[0])); err != nil {
			return chainhash.Hash{}, err
		}
	}
	meta, err := i.pathMetaForMutationLocked(parsed, record, nil, height, now)
	if err != nil {
		return chainhash.Hash{}, err
	}
	if err := putPathMetaBatch(batch, meta); err != nil {
		return chainhash.Hash{}, err
	}
	if err := batch.Flush(); err != nil {
		return chainhash.Hash{}, err
	}
	paidRetentionCacheFor(i).remove([]string{record.Key})
	for _, candidate := range recordsToDelete {
		if i.feeUsageInitialized {
			i.removeFeeUsageLocked(candidate.Key)
		}
		if i.freeLocalUsageInitialized {
			i.removeFreeLocalUsageLocked(candidate.Key)
		}
		if i.recordExpiryInitialized {
			delete(i.recordExpiryEntries, candidate.Key)
		}
	}
	if deleteRecord != nil {
		return RecordHash(deleteRecord), nil
	}
	return oldHash, nil
}

// retainDeleteCommandLocked keeps a verified command only for the bounded
// relay window. It is not part of the durable DKVS record set.
func (i *Indexer) retainDeleteCommandLocked(record *wire.DKVSRecord, now uint64, localOnly bool) (chainhash.Hash, error) {
	if record == nil || !IsTombstone(record.Flags) {
		return chainhash.Hash{}, ErrInvalidRecord
	}
	previous, err := i.getDeleteStateLocked(record.Key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return chainhash.Hash{}, err
	}
	if err == nil {
		if previous.FloorSeq >= record.Seq {
			if previous.Record != nil {
				return RecordHash(previous.Record), nil
			}
			return RecordHash(record), nil
		}
		if previous.Record != nil && CompareRecords(previous.Record, record) >= 0 {
			return RecordHash(previous.Record), nil
		}
	}
	state := &deleteState{
		FloorSeq:   record.Seq,
		RelayUntil: deleteRelayUntil(now),
		PubKey:     append([]byte{}, record.PubKey...),
		Record:     record,
		LocalOnly:  localOnly,
	}
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	if err := putDeleteStateBatch(batch, record.Key, state); err != nil {
		return chainhash.Hash{}, err
	}
	if err := batch.Flush(); err != nil {
		return chainhash.Hash{}, err
	}
	return RecordHash(record), nil
}

func (i *Indexer) compactExpiredDeleteCommandsLocked(batch indexercommon.WriteBatch, now uint64) (int, error) {
	if now == 0 {
		return 0, nil
	}
	compacted := 0
	err := i.db.BatchReadV2(deleteKeyPrefix, deleteKeyPrefix, false, func(key, value []byte) error {
		state, err := unmarshalDeleteState(value)
		if err != nil {
			return err
		}
		if len(key) < len(deleteKeyPrefix) {
			return ErrInvalidRecord
		}
		recordKey := string(key[len(deleteKeyPrefix):])
		if state.Record != nil && (state.Record.Key != recordKey || state.Record.Seq > state.FloorSeq ||
			!bytesEqual(state.Record.PubKey, state.PubKey)) {
			return ErrInvalidRecord
		}
		if state.Record == nil || state.RelayUntil == 0 || now < state.RelayUntil {
			return nil
		}
		state.Record = nil
		state.RelayUntil = 0
		if err := putDeleteStateBatch(batch, recordKey, state); err != nil {
			return err
		}
		compacted++
		return nil
	})
	return compacted, err
}

func (i *Indexer) deleteMirrorRecordLocked(record *wire.DKVSRecord, height, now uint64) error {
	if record == nil {
		return ErrRecordNotFound
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return err
	}
	meta, err := i.pathMetaForMutationLocked(parsed, record, nil, height, now)
	if err != nil {
		return err
	}
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	if err := batch.Delete(recordDBKey(record.Key)); err != nil {
		return err
	}
	if err := batch.Delete(hashDBKey(RecordHash(record))); err != nil {
		return err
	}
	if err := deleteDeleteStateBatch(batch, record.Key); err != nil {
		return err
	}
	if err := putPathMetaBatch(batch, meta); err != nil {
		return err
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	if i.feeUsageInitialized {
		i.removeFeeUsageLocked(record.Key)
	}
	if i.freeLocalUsageInitialized {
		i.removeFreeLocalUsageLocked(record.Key)
	}
	if i.recordExpiryInitialized {
		delete(i.recordExpiryEntries, record.Key)
	}
	return nil
}

// DeleteMirrorKeys removes records omitted by an authoritative mirror sync.
// Omission is not a signed delete command and therefore never creates a floor.
func (i *Indexer) DeleteMirrorKeys(keys []string) (int, error) {
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	deleted := 0
	for _, key := range keys {
		if _, err := ParseKey(key); err != nil {
			return deleted, err
		}
		record, err := i.getRaw(key)
		if errors.Is(err, ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return deleted, err
		}
		if err := i.deleteMirrorRecordLocked(record, height, now); err != nil {
			return deleted, err
		}
		deleted++
		atomicAddGeneration(&i.generation)
	}
	return deleted, nil
}

func atomicAddGeneration(generation *uint64) {
	// Kept as a small helper so deletion code does not depend on the caller's
	// lock implementation for checkpoint cache invalidation.
	atomic.AddUint64(generation, 1)
}
