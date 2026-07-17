package dkvs

import (
	"encoding/binary"
	"errors"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	deleteStateVersion  = byte(1)
	deleteStateHeadSize = 1 + 8 + 8 + 4

	// Delete records remain available only for the normal notify/get/data relay
	// window. Afterwards only MaxSeq remains to prevent stale record resurrection.
	DefaultDeleteRelayTTL = uint64(24 * 60 * 60 * 1000)
)

var (
	deleteStateKeyPrefix = []byte("dkvs:delete:")
	deleteHashKeyPrefix  = []byte("dkvs:delete-hash:")
)

type deleteState struct {
	MaxSeq     uint64
	RelayUntil uint64
	Record     *wire.DKVSRecord
}

func deleteStateDBKey(key string) []byte {
	out := make([]byte, 0, len(deleteStateKeyPrefix)+len(key))
	out = append(out, deleteStateKeyPrefix...)
	out = append(out, key...)
	return out
}

func deleteHashDBKey(hash chainhash.Hash) []byte {
	out := make([]byte, 0, len(deleteHashKeyPrefix)+chainhash.HashSize)
	out = append(out, deleteHashKeyPrefix...)
	out = append(out, hash[:]...)
	return out
}

func encodeDeleteState(state deleteState) ([]byte, error) {
	var recordBytes []byte
	var err error
	if state.Record != nil {
		recordBytes, err = MarshalRecord(state.Record)
		if err != nil {
			return nil, err
		}
	}
	encoded := make([]byte, deleteStateHeadSize+len(recordBytes))
	encoded[0] = deleteStateVersion
	binary.LittleEndian.PutUint64(encoded[1:9], state.MaxSeq)
	binary.LittleEndian.PutUint64(encoded[9:17], state.RelayUntil)
	binary.LittleEndian.PutUint32(encoded[17:21], uint32(len(recordBytes)))
	copy(encoded[deleteStateHeadSize:], recordBytes)
	return encoded, nil
}

func decodeDeleteState(encoded []byte) (*deleteState, error) {
	if len(encoded) < deleteStateHeadSize || encoded[0] != deleteStateVersion {
		return nil, ErrInvalidRecord
	}
	recordLen := int(binary.LittleEndian.Uint32(encoded[17:21]))
	if recordLen < 0 || len(encoded) != deleteStateHeadSize+recordLen {
		return nil, ErrInvalidRecord
	}
	state := &deleteState{
		MaxSeq:     binary.LittleEndian.Uint64(encoded[1:9]),
		RelayUntil: binary.LittleEndian.Uint64(encoded[9:17]),
	}
	if recordLen != 0 {
		record, err := UnmarshalRecord(encoded[deleteStateHeadSize:])
		if err != nil || record == nil || !IsTombstone(record.Flags) {
			return nil, ErrInvalidRecord
		}
		state.Record = record
	}
	return state, nil
}

func (i *Indexer) getDeleteStateLocked(key string) (*deleteState, error) {
	encoded, err := i.db.Read(deleteStateDBKey(key))
	if err != nil {
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	return decodeDeleteState(encoded)
}

func (i *Indexer) putDeleteStateBatchLocked(batch indexercommon.WriteBatch, key string,
	previous *deleteState, state deleteState) error {

	if previous != nil && previous.Record != nil {
		previousHash := RecordHash(previous.Record)
		if state.Record == nil || RecordHash(state.Record) != previousHash {
			if err := batch.Delete(deleteHashDBKey(previousHash)); err != nil {
				return err
			}
		}
	}
	encoded, err := encodeDeleteState(state)
	if err != nil {
		return err
	}
	if err := batch.Put(deleteStateDBKey(key), encoded); err != nil {
		return err
	}
	if state.Record != nil {
		if err := batch.Put(deleteHashDBKey(RecordHash(state.Record)), []byte(key)); err != nil {
			return err
		}
	}
	return nil
}

func (i *Indexer) removeDeleteStateBatchLocked(batch indexercommon.WriteBatch, key string, state *deleteState) error {
	if state == nil {
		return nil
	}
	if state.Record != nil {
		if err := batch.Delete(deleteHashDBKey(RecordHash(state.Record))); err != nil {
			return err
		}
	}
	return batch.Delete(deleteStateDBKey(key))
}

func (i *Indexer) deleteStateForReplacementLocked(record *wire.DKVSRecord) (*deleteState, error) {
	state, err := i.getDeleteStateLocked(record.Key)
	if errors.Is(err, ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.Seq <= state.MaxSeq {
		return state, ErrStaleRecord
	}
	return state, nil
}

func (i *Indexer) GetForSync(key string) (*wire.DKVSRecord, error) {
	if _, err := ParseKey(key); err != nil {
		return nil, err
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	if record, err := i.getRaw(key); err == nil && i.activeError(record, height, now) == nil {
		return record, nil
	}
	state, err := i.getDeleteStateLocked(key)
	if err != nil || state.Record == nil || state.RelayUntil == 0 || now >= state.RelayUntil {
		return nil, ErrRecordNotFound
	}
	return cloneDKVSRecord(state.Record), nil
}

func (i *Indexer) GetForSyncByHash(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	if keyBytes, err := i.db.Read(hashDBKey(hash)); err == nil {
		record, err := i.getRaw(string(keyBytes))
		if err == nil && RecordHash(record) == hash && i.activeError(record, height, now) == nil {
			return record, nil
		}
	}
	keyBytes, err := i.db.Read(deleteHashDBKey(hash))
	if err != nil {
		return nil, ErrRecordNotFound
	}
	state, err := i.getDeleteStateLocked(string(keyBytes))
	if err != nil || state.Record == nil || state.RelayUntil == 0 || now >= state.RelayUntil || RecordHash(state.Record) != hash {
		return nil, ErrRecordNotFound
	}
	return cloneDKVSRecord(state.Record), nil
}

func (i *Indexer) pruneDeletePayloads(now uint64) (int, error) {
	if now == 0 {
		return 0, nil
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()

	batch := i.db.NewWriteBatch()
	defer batch.Close()
	compacted := 0
	err := i.db.BatchRead(deleteStateKeyPrefix, false, func(key, value []byte) error {
		state, err := decodeDeleteState(value)
		if err != nil {
			return err
		}
		if state.Record == nil || state.RelayUntil == 0 || now < state.RelayUntil {
			return nil
		}
		if err := batch.Delete(deleteHashDBKey(RecordHash(state.Record))); err != nil {
			return err
		}
		state.Record = nil
		state.RelayUntil = 0
		encoded, err := encodeDeleteState(*state)
		if err != nil {
			return err
		}
		if err := batch.Put(key, encoded); err != nil {
			return err
		}
		compacted++
		return nil
	})
	if err != nil || compacted == 0 {
		return compacted, err
	}
	return compacted, batch.Flush()
}
