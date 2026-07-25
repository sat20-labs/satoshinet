package dkvs

import (
	"errors"
	"sort"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type preparedRecordSet struct {
	ordered          []*wire.DKVSRecord
	capacities       map[string]preparedFeeCapacity
	forceReplace     map[string]bool
	generation       uint64
	policyGeneration uint64
}

func cloneRecordSet(records []*wire.DKVSRecord) ([]*wire.DKVSRecord, error) {
	seen := make(map[string]struct{}, len(records))
	ordered := make([]*wire.DKVSRecord, 0, len(records))
	for _, record := range records {
		if record == nil || IsTombstone(record.Flags) {
			return nil, ErrInvalidSnapshot
		}
		if _, exists := seen[record.Key]; exists {
			return nil, ErrInvalidSnapshot
		}
		seen[record.Key] = struct{}{}
		ordered = append(ordered, cloneRecord(record))
	}
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].Key < ordered[b].Key })
	return ordered, nil
}

func (i *Indexer) prevalidateRecordSet(records []*wire.DKVSRecord, rejectFreeLocal bool) (preparedRecordSet, error) {
	ordered, err := cloneRecordSet(records)
	if err != nil {
		return preparedRecordSet{}, err
	}
	validators := i.snapshotValidators()
	height := i.currentHeight()
	now := currentUnixMilli()
	generation := atomic.LoadUint64(&i.generation)
	capacities := make(map[string]preparedFeeCapacity, len(ordered))
	forceReplace := make(map[string]bool, len(ordered))
	for _, record := range ordered {
		parsed, err := validateParsedCoreWithVerifier(record, height, now, false, false, nil)
		if err != nil {
			return preparedRecordSet{}, err
		}
		if err := verifyFeeProofWith(validators.feeVerifier, record, parsed); err != nil {
			return preparedRecordSet{}, err
		}
		if rejectFreeLocal && i.isLocalOnlyRecord(record) {
			return preparedRecordSet{}, ErrFreeLocalNotRelayable
		}
		state, err := i.readWriteStateSnapshot(record.Key, parsed, validators)
		if err != nil {
			return preparedRecordSet{}, err
		}
		force, err := validateWritePermissionWith(parsed, record, state.existing, state.requiresResolve, validators)
		if err != nil {
			return preparedRecordSet{}, err
		}
		forceReplace[record.Key] = force
		capacity, err := i.prepareFeeCapacity(record, parsed, state.existing, validators.feeVerifier, height, now)
		if err != nil {
			return preparedRecordSet{}, err
		}
		capacities[record.Key] = capacity
	}
	return preparedRecordSet{
		ordered:          ordered,
		capacities:       capacities,
		forceReplace:     forceReplace,
		generation:       generation,
		policyGeneration: validators.policyGeneration,
	}, nil
}

func (i *Indexer) validatePreparedFeeSetLocked(records []*wire.DKVSRecord, capacities map[string]preparedFeeCapacity, height, now uint64) error {
	projected := make(map[string]uint64)
	for _, record := range records {
		prepared := capacities[record.Key]
		if prepared.indexed == nil {
			if prepared.fallback != nil && atomic.LoadUint64(&i.generation) != prepared.generation {
				return ErrConcurrentUpdate
			}
			continue
		}
		if err := i.ensureFeeUsageLocked(prepared.indexed, height, now); err != nil {
			return err
		}
		usageKey := prepared.descriptor.UsageKey
		if usageKey == "" {
			continue
		}
		count, ok := projected[usageKey]
		if !ok {
			count = i.feeUsageCounts[usageKey]
		}
		entry, replacing := i.feeUsageEntries[record.Key]
		if !replacing || entry.usageKey != usageKey {
			count++
		}
		if prepared.descriptor.MaxRecords == 0 || count > prepared.descriptor.MaxRecords {
			return ErrFeeCapacityExceeded
		}
		projected[usageKey] = count
	}
	return nil
}

func (i *Indexer) selectMergeRecordSetLocked(prepared preparedRecordSet, height, now uint64) ([]*wire.DKVSRecord, error) {
	selected := make([]*wire.DKVSRecord, 0, len(prepared.ordered))
	for _, record := range prepared.ordered {
		existing, err := i.getRaw(record.Key)
		if err != nil && !errors.Is(err, ErrRecordNotFound) {
			return nil, err
		}
		if err == nil && !prepared.forceReplace[record.Key] && i.activeError(existing, height, now) == nil &&
			CompareRecords(existing, record) >= 0 {
			continue
		}
		selected = append(selected, record)
	}
	return selected, nil
}

func (i *Indexer) applyRecordSetAtomic(records []*wire.DKVSRecord, replace []syncRange, expectedRoot *chainhash.Hash, authoritative, rejectFreeLocal bool) (int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		prepared, err := i.prevalidateRecordSet(records, rejectFreeLocal)
		if err != nil {
			return 0, err
		}
		if expectedRoot != nil {
			root, err := recordsRoot(prepared.ordered, i.currentHeight())
			if err != nil || root != *expectedRoot {
				return 0, ErrInvalidSnapshot
			}
		}

		i.mutex.Lock()
		if atomic.LoadUint64(&i.generation) != prepared.generation ||
			atomic.LoadUint64(&i.policyGeneration) != prepared.policyGeneration {
			i.mutex.Unlock()
			continue
		}
		height := i.currentHeight()
		now := currentUnixMilli()
		ordered := prepared.ordered
		if !authoritative {
			ordered, err = i.selectMergeRecordSetLocked(prepared, height, now)
			if err != nil {
				i.mutex.Unlock()
				return 0, err
			}
		}
		if err := i.validatePreparedFeeSetLocked(ordered, prepared.capacities, height, now); err != nil {
			i.mutex.Unlock()
			if errors.Is(err, ErrConcurrentUpdate) {
				continue
			}
			return 0, err
		}
		incoming := make(map[string]*wire.DKVSRecord, len(ordered))
		for _, record := range ordered {
			incoming[record.Key] = record
		}
		var current []*wire.DKVSRecord
		if replace != nil {
			// Mirror omission applies to the active view. Inactive paid records
			// retain their physical-storage policy and cannot be erased merely
			// because an active snapshot omits them.
			current, err = i.recordsForRangesLocked(replace, authoritative, height, now)
			if err != nil {
				i.mutex.Unlock()
				return 0, err
			}
			current = i.relayableRecords(current)
		}
		currentByKey := make(map[string]*wire.DKVSRecord, len(current))
		for _, record := range current {
			currentByKey[record.Key] = record
		}
		batch := i.db.NewWriteBatch()
		touched := make([]*wire.DKVSRecord, 0, len(current)+len(ordered))
		for _, record := range currentByKey {
			if _, keep := incoming[record.Key]; keep {
				continue
			}
			if err = batch.Delete(recordDBKey(record.Key)); err == nil {
				err = batch.Delete(hashDBKey(RecordHash(record)))
			}
			if err == nil {
				err = deleteDeleteStateBatch(batch, record.Key)
			}
			if err != nil {
				break
			}
			touched = append(touched, record)
		}
		applied := 0
		for _, record := range ordered {
			if err != nil {
				break
			}
			existing, readErr := i.getRaw(record.Key)
			if readErr != nil && !errors.Is(readErr, ErrRecordNotFound) {
				err = readErr
				break
			}
			if readErr == nil {
				existingHash := RecordHash(existing)
				if existingHash == RecordHash(record) {
					continue
				}
				if err = batch.Delete(hashDBKey(existingHash)); err != nil {
					break
				}
				touched = append(touched, existing)
			}
			encoded, marshalErr := MarshalRecord(record)
			if marshalErr != nil {
				err = marshalErr
				break
			}
			hash := RecordHash(record)
			if err = batch.Put(recordDBKey(record.Key), encoded); err == nil {
				err = batch.Put(hashDBKey(hash), []byte(record.Key))
			}
			if err == nil {
				err = deleteDeleteStateBatch(batch, record.Key)
			}
			if err != nil {
				break
			}
			touched = append(touched, record)
			applied++
		}
		if err == nil {
			err = i.markPathMetaDirtyLocked(batch, touched, height, now)
		}
		if err == nil && len(touched) != 0 {
			err = batch.Flush()
		}
		batch.Close()
		if err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		if len(touched) != 0 {
			i.resetFeeUsageLocked()
			i.resetFreeLocalUsageLocked()
			i.resetRecordExpiryLocked()
			atomic.AddUint64(&i.generation, 1)
		}
		i.mutex.Unlock()
		return applied, nil
	}
	return 0, ErrConcurrentUpdate
}

// ApplyMirror atomically replaces the active records covered by filters.
func (i *Indexer) ApplyMirror(filters []Subscription, records []*wire.DKVSRecord, root chainhash.Hash) (int, error) {
	ranges, err := syncRangesForFilters(filters)
	if err != nil {
		return 0, err
	}
	return i.applyRecordSetAtomic(records, ranges, &root, true, true)
}
