package dkvs

import (
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
)

// Deleting a durable record is a change, not removal of its change-index
// entry. Use the endpoint generation (not the canonical path generation) so
// a terminal's cursor observes the deletion even after local-only writes.
func (i *Indexer) markDeletedKeyChangeBatch(batch indexercommon.WriteBatch, meta *PathMeta, key string, localOnly bool) error {
	if meta == nil {
		return nil
	}
	if localOnly {
		// Endpoint cache eviction deliberately has no persistent delete floor.
		return i.clearChangedRecordBatch(batch, meta.Path, key)
	}
	return i.markChangedRecordBatch(batch, meta.Path, key, meta.EndpointGeneration)
}

// Account for compact key-state metadata as well as record payloads. A prefix
// containing many deleted keys must not bypass the existing response limits.
func prefixKeyStateSize(state DKVSKeyState) int {
	return len(state.Key) + len(state.ETag) + 128
}

func appendPrefixKeyState(states *[]DKVSKeyState, totalBytes *int, state DKVSKeyState) error {
	if len(*states) >= MaxPrefixReadRecords {
		return ErrBatchTooLarge
	}
	size := prefixKeyStateSize(state)
	if size > MaxPrefixReadBytes-*totalBytes {
		return ErrBatchTooLarge
	}
	*totalBytes += size
	*states = append(*states, state)
	return nil
}

// A full directory baseline must include durable delete floors, even after
// their relay tombstone bodies were compacted. Absence without a retained
// floor remains absence, not an invented deletion of endpoint-local history.
func (i *Indexer) appendPrefixDeleteStatesLocked(prefix string, height, now uint64,
	states *[]DKVSKeyState, totalBytes *int) error {

	base := deleteDBKey(prefix)
	return i.db.BatchReadV2(base, base, false, func(key, _ []byte) error {
		if len(key) < len(deleteKeyPrefix) {
			return ErrInvalidRecord
		}
		recordKey := string(key[len(deleteKeyPrefix):])
		if recordKey != prefix && !strings.HasPrefix(recordKey, prefix+"/") {
			return nil
		}
		state, err := i.keyStateLocked(recordKey, false, height, now)
		if err != nil {
			return err
		}
		if state.Status != KeyStateDeleted {
			return nil
		}
		return appendPrefixKeyState(states, totalBytes, state)
	})
}
