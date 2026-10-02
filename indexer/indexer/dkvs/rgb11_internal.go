package dkvs

import (
	"errors"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/wire"
)

// PutInternalRGB11Registry persists one CoreNode-authenticated RGB11 registry
// record. It is deliberately unreachable through ordinary DKVS Put/CAS APIs.
// The caller must be the CoreNode that has accepted the corresponding
// transcend.tc deployment and must sign the DKVS record with its node wallet.
func (i *Indexer) PutInternalRGB11Registry(record *wire.DKVSRecord) (bool, error) {
	record = cloneRecord(record)
	if i == nil || record == nil {
		return false, ErrInvalidRecord
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	parsed, err := validateParsedCoreWithVerifier(record, height, false, false, nil)
	if err != nil {
		return false, err
	}
	if parsed.Namespace != RGB11RegistryNamespace {
		return false, ErrInvalidKey
	}
	if err := validateRGB11RegistryStored(record, parsed); err != nil {
		return false, err
	}

	i.mutex.Lock()
	existing, err := i.getRaw(record.Key)
	if err == nil {
		if RecordHash(existing) == RecordHash(record) {
			i.mutex.Unlock()
			return false, nil
		}
		if !isRGB11RegistryRecord(existing, parsed) {
			i.mutex.Unlock()
			return false, ErrWriteConflict
		}
		if existing.Seq == ^uint64(0) || record.Seq != existing.Seq+1 {
			i.mutex.Unlock()
			return false, ErrInvalidSequence
		}
	} else if errors.Is(err, ErrRecordNotFound) {
		existing = nil
		if record.Seq != 1 {
			i.mutex.Unlock()
			return false, ErrInvalidSequence
		}
	} else {
		i.mutex.Unlock()
		return false, err
	}

	if err := i.validateRGB11RegistryMutationLocked(record, parsed, existing, height, now); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	encoded, err := MarshalRecord(record)
	if err != nil {
		i.mutex.Unlock()
		return false, err
	}
	hash := RecordHash(record)
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	touched := []*wire.DKVSRecord{record}
	if existing != nil {
		touched = append(touched, existing)
		oldHash := RecordHash(existing)
		if oldHash != hash {
			if err := batch.Delete(hashDBKey(oldHash)); err != nil {
				i.mutex.Unlock()
				return false, err
			}
		}
	}
	if err := batch.Put(recordDBKey(record.Key), encoded); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if err := batch.Put(hashDBKey(hash), []byte(record.Key)); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if err := i.markPathMetaDirtyLocked(batch, touched, height, now); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if err := i.markDirtyChangedRecordBatch(batch, record.Key, true); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if err := batch.Flush(); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	atomic.AddUint64(&i.generation, 1)
	i.mutex.Unlock()
	i.notifyPathMutation(record)
	return true, nil
}
