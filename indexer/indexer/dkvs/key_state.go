package dkvs

import (
	"errors"

	"github.com/sat20-labs/satoshinet/wire"
)

func storageModeForRecord(record *wire.DKVSRecord) StorageMode {
	if record == nil {
		return ""
	}
	proof, err := ParseFeeProof(record.FeeProof)
	if err != nil {
		if isFreeLocalRecord(record) {
			return StorageModeFreeLocal
		}
		return StorageModePaid
	}
	switch proof.Mode {
	case FeeModeFreeLocal:
		return StorageModeFreeLocal
	case FeeModeAutopay:
		return StorageModeAutopay
	default:
		return StorageModePaid
	}
}

func (i *Indexer) keyStateLocked(key string, includeRecord bool, height, now uint64) (DKVSKeyState, error) {
	if _, err := ParseKey(key); err != nil {
		return DKVSKeyState{}, err
	}
	state := DKVSKeyState{Key: key, Status: KeyStateNeverSeen}
	record, recordErr := i.getRaw(key)
	if recordErr != nil && !errors.Is(recordErr, ErrRecordNotFound) {
		return DKVSKeyState{}, recordErr
	}
	deleteState, deleteErr := i.getDeleteStateLocked(key)
	if deleteErr != nil && !errors.Is(deleteErr, ErrRecordNotFound) {
		return DKVSKeyState{}, deleteErr
	}
	if recordErr == nil && existingRecordActive(i, record, height, now) {
		hash := RecordHash(record)
		state.Status = KeyStateActive
		state.Seq = record.Seq
		state.ETag = hash.String()
		state.ExpiryHeight = RecordExpiryHeight(record)
		state.StorageMode = storageModeForRecord(record)
		if includeRecord {
			state.Record = cloneRecord(record)
		}
		return state, nil
	}
	if deleteErr == nil && deleteState != nil {
		state.Status = KeyStateDeleted
		state.Seq = deleteState.FloorSeq
		state.ETag = deleteState.effectiveHash(key).String()
		return state, nil
	}
	// An expired record may still exist between the height transition and the
	// expiry worker's atomic cleanup. It is not active, but its signed hash is
	// still the current sequence floor and therefore remains the key ETag.
	if recordErr == nil && record != nil {
		state.Status = KeyStateDeleted
		state.Seq = record.Seq
		state.ETag = RecordHash(record).String()
		return state, nil
	}
	return state, nil
}

func (i *Indexer) GetKeyState(key string) (DKVSKeyState, error) {
	height, now := i.currentHeight(), currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.keyStateLocked(key, true, height, now)
}
