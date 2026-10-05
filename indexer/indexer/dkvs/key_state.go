package dkvs

import (
	"errors"

	"github.com/sat20-labs/satoshinet/wire"
)

func storageModeForRecord(record *wire.DKVSRecord) StorageMode {
	if record == nil { return "" }
	proof, err := ParseFeeProof(record.FeeProof)
	if err != nil { return StorageModePaid }
	switch proof.Mode {
	case FeeModeFreeLocal: return StorageModeFreeLocal
	case FeeModeAutopay: return StorageModeAutopay
	default: return StorageModePaid
	}
}

func (i *Indexer) keyStateLocked(key string, includeRecord bool, height, now uint64) (DKVSKeyState, error) {
	if _, err := ParseKey(key); err != nil { return DKVSKeyState{}, err }
	state := DKVSKeyState{Key: key, Status: KeyStateNeverSeen}
	record, err := i.getRaw(key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) { return DKVSKeyState{}, err }
	// A raw row awaiting expiry cleanup is not a hidden CAS floor. Reads and
	// writes agree that an expired or physically absent key is absent.
	if record == nil || !existingRecordActive(i, record, height, now) { return state, nil }
	state.Status = KeyStateActive
	state.Seq = record.Seq
	state.ETag = RecordHash(record).String()
	state.ExpiryHeight = RecordExpiryHeight(record)
	state.StorageMode = storageModeForRecord(record)
	if includeRecord { state.Record = cloneRecord(record) }
	return state, nil
}

func (i *Indexer) GetKeyState(key string) (DKVSKeyState, error) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.keyStateLocked(key, true, i.currentHeight(), currentUnixMilli())
}
