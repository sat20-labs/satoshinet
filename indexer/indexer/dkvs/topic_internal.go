package dkvs

import (
	"errors"
	"sort"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/wire"
)

func isInternalTopicRecord(record *wire.DKVSRecord) bool {
	if record == nil || record.Version != Version || record.Seq == 0 || record.TTL != 0 ||
		IsTombstone(record.Flags) || len(record.Value) == 0 || len(record.PubKey) != 0 ||
		len(record.Signature) != 0 || len(record.FeeProof) != 0 {
		return false
	}
	parsed, err := ParseKey(record.Key)
	if err != nil || parsed.Namespace != "topic" {
		return false
	}
	return true
}

// PutInternalTopicValues atomically persists TopicManager-owned service state.
// The outer DKVS records are local Host integrity/state records; membership
// authorization is performed by TopicManager before this method is called.
// Generic Put/CAS and P2P relay are deliberately unable to create them.
func (i *Indexer) PutInternalTopicValues(values map[string][]byte) (int, error) {
	if i == nil || len(values) == 0 {
		return 0, ErrInvalidRecord
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		parsed, err := ParseKey(key)
		if err != nil || parsed.Namespace != "topic" || len(value) == 0 || len(value) > MaxRecordValueSize {
			return 0, ErrInvalidRecord
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	height := i.currentHeight()
	now := currentUnixMilli()

	i.mutex.Lock()
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	changed := make([]*wire.DKVSRecord, 0, len(keys))
	touched := make([]*wire.DKVSRecord, 0, len(keys)*2)
	for _, key := range keys {
		value := values[key]
		existing, err := i.getRaw(key)
		seq := uint64(1)
		if err == nil {
			if !isInternalTopicRecord(existing) {
				i.mutex.Unlock()
				return 0, ErrWriteConflict
			}
			if existing.Seq == ^uint64(0) {
				i.mutex.Unlock()
				return 0, ErrInvalidSequence
			}
			if string(existing.Value) == string(value) {
				continue
			}
			seq = existing.Seq + 1
			touched = append(touched, existing)
		} else if !errors.Is(err, ErrRecordNotFound) {
			i.mutex.Unlock()
			return 0, err
		}
		record := &wire.DKVSRecord{
			Version: Version, Key: key, Value: append([]byte(nil), value...),
			Seq: seq, IssueHeight: height,
		}
		parsed, _ := ParseKey(key)
		if err := validateRecordSizeForParsed(record, parsed); err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		encoded, err := MarshalRecord(record)
		if err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		hash := RecordHash(record)
		if err := batch.Put(recordDBKey(key), encoded); err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		if existing != nil {
			oldHash := RecordHash(existing)
			if oldHash != hash {
				if err := batch.Delete(hashDBKey(oldHash)); err != nil {
					i.mutex.Unlock()
					return 0, err
				}
			}
		}
		if err := batch.Put(hashDBKey(hash), []byte(key)); err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		touched = append(touched, record)
		changed = append(changed, record)
	}
	if len(changed) == 0 {
		i.mutex.Unlock()
		return 0, nil
	}
	if err := i.markPathMetaDirtyLocked(batch, touched, height, now); err != nil {
		i.mutex.Unlock()
		return 0, err
	}
	if err := batch.Flush(); err != nil {
		i.mutex.Unlock()
		return 0, err
	}
	atomic.AddUint64(&i.generation, 1)
	i.mutex.Unlock()
	i.notifyPathMutations(changed)
	return len(changed), nil
}

func (i *Indexer) ListInternalTopicRecords() ([]*wire.DKVSRecord, error) {
	if i == nil {
		return nil, ErrInvalidRecord
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	records, _, _, err := i.scanLocked("/topic", nil, 0, false, height, now)
	if err != nil {
		return nil, err
	}
	out := make([]*wire.DKVSRecord, 0, len(records))
	for _, record := range records {
		if isInternalTopicRecord(record) {
			out = append(out, cloneRecord(record))
		}
	}
	return out, nil
}
