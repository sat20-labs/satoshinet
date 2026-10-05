package dkvs

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/wire"
)

// NetworkSyncBaseline fences a complete prefix sync against local changes that
// happen while the snapshot is being downloaded. It contains no remote-source
// ownership and is never compared across nodes.
func (i *Indexer) NetworkSyncBaseline(path string) (ActiveMeta, error) {
	return i.ActiveMetadata(context.Background(), ActiveScope{Prefix: path, Network: true})
}

func (i *Indexer) NetworkPaths() ([]string, error) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	paths := make([]string, 0)
	err := i.db.BatchRead(pathMetaKeyPrefix, false, func(key, _ []byte) error {
		path := strings.TrimPrefix(string(key), string(pathMetaKeyPrefix))
		if _, err := NormalizeActiveScope(ActiveScope{Prefix: path, Network: true}); err == nil {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

// AcceptCurrentRecord installs one realtime P2P operation after the P2P
// layer has already validated the sending CoreNode. Source identity and
// generation do not enter the KV state machine.
func (i *Indexer) AcceptCurrentRecord(record *wire.DKVSRecord) (bool, error) {
	if i == nil {
		return false, ErrInvalidRecord
	}
	record = cloneRecord(record)
	for attempt := 0; attempt < 3; attempt++ {
		height, now := i.currentHeight(), currentUnixMilli()
		validators := i.snapshotValidators()
		parsed, err := validateParsedCoreWithVerifier(record, height, false, false, nil)
		if err != nil {
			return false, err
		}
		if replicationMode(parsed, record) != ReplicationNetwork || isEndpointCacheRecord(record) {
			return false, ErrFreeLocalNotRelayable
		}
		snapshot, err := i.readWriteStateSnapshot(record.Key, parsed, validators)
		if err != nil {
			return false, err
		}
		force, err := validateWritePermissionWith(parsed, record, snapshot.existing, snapshot.requiresResolve, validators)
		if err != nil {
			return false, err
		}
		prepared := preparedCASMutation{
			mutation:     CASMutation{Record: record},
			parsed:       parsed,
			snapshot:     snapshot,
			forceReplace: force,
		}
		if IsTombstone(record.Flags) {
			if _, err := DeleteTargetHash(record); err != nil {
				return false, err
			}
		} else {
			if err := verifyFeeProofWith(validators.feeVerifier, record, parsed); err != nil {
				return false, err
			}
			prepared.retention, err = verifiedPaidRetentionAfterFeeVerification(
				record, parsed, validators.feeVerifier, height,
			)
			if err != nil {
				return false, err
			}
			prepared.capacity, err = i.prepareFeeCapacity(
				record, parsed, snapshot.existing, validators.feeVerifier, height, now,
			)
			if err != nil {
				if errors.Is(err, ErrConcurrentUpdate) {
					continue
				}
				return false, err
			}
		}

		i.mutex.Lock()
		current, err := i.writeStateStillCurrentLocked(record.Key, parsed, snapshot)
		if err != nil || !current || atomic.LoadUint64(&i.policyGeneration) != validators.policyGeneration {
			i.mutex.Unlock()
			if err != nil {
				return false, err
			}
			continue
		}

		existing := snapshot.existing
		if existing != nil && RecordHash(existing) == RecordHash(record) {
			i.mutex.Unlock()
			return false, nil
		}

		if IsTombstone(record.Flags) {
			if existing == nil {
				i.mutex.Unlock()
				return false, nil
			}
			if isEndpointCacheRecord(existing) || !deleteTargetsRecord(record, existing) {
				i.mutex.Unlock()
				return false, ErrWriteConflict
			}
		} else {
			active := existingRecordActive(i, existing, height, now)
			if !active || isEndpointCacheRecord(existing) {
				// A node that already completed synchronization should first see a
				// newly created key at Seq=1. A higher sequence with no current
				// local key means this connection missed part of the lifecycle and
				// needs prefix synchronization rather than guessed installation.
				if record.Seq != 1 {
					i.mutex.Unlock()
					return false, ErrPathDiverged
				}
			} else {
				// The current key's sequence orders realtime notifications.
				// A later signing height cannot make a lower/equal sequence
				// current; conflicting equal sequences require calibration.
				switch {
				case record.Seq < existing.Seq:
					i.mutex.Unlock()
					return false, nil
				case record.Seq == existing.Seq:
					i.mutex.Unlock()
					return false, ErrPathDiverged
				}
			}
		}

		ready := []preparedCASMutation{prepared}
		if err := i.validateBatchStateLocked(ready, height, now); err != nil {
			i.mutex.Unlock()
			return false, err
		}
		events, _, err := i.commitBatchCASLocked(ready, height, now)
		i.mutex.Unlock()
		if err != nil {
			return false, err
		}
		i.emitCommittedEvents(events)
		return true, nil
	}
	return false, ErrConcurrentUpdate
}
