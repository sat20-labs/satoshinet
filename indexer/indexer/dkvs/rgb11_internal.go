package dkvs

import (
	"bytes"
	"errors"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/wire"
)

// PutInternalRGB11Registry persists one CoreNode-authenticated immutable RGB11
// registration. Ordinary DKVS Put/CAS APIs cannot create /rgb11 records.
//
// The caller is the CoreNode processing the corresponding transcend.tc deploy.
// The record is signed by that CoreNode wallet before reaching this method.
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
	validators := i.snapshotValidators()
	if validators.system == nil {
		return false, ErrPermissionDenied
	}
	if err := validators.system.CanWriteSystem(record.Key, record.PubKey); err != nil {
		return false, err
	}

	i.mutex.Lock()
	existing, err := i.getRaw(record.Key)
	if err == nil {
		if !isRGB11RegistryRecord(existing, parsed) {
			i.mutex.Unlock()
			return false, ErrWriteConflict
		}
		// A registration key is immutable. The same ContractID is an idempotent
		// retry even if a second authorized CoreNode signs the same business fact.
		if bytes.Equal(existing.Value, record.Value) {
			i.mutex.Unlock()
			return false, nil
		}
		i.mutex.Unlock()
		return false, ErrWriteConflict
	}
	if !errors.Is(err, ErrRecordNotFound) {
		i.mutex.Unlock()
		return false, err
	}
	if err := i.validateRGB11RegistryInsertLocked(record, parsed, height, now); err != nil {
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
	if err := batch.Put(recordDBKey(record.Key), encoded); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if err := batch.Put(hashDBKey(hash), []byte(record.Key)); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if err := i.markPathMetaDirtyLocked(batch, []*wire.DKVSRecord{record}, height, now); err != nil {
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
