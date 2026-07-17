package dkvs

import (
	"bytes"
	"errors"

	indexercommon "github.com/sat20-labs/indexer/common"
)

// compactDeleteCommandsLocked drops the retained signed delete command after
// its relay window while preserving MaxSeq as the replay watermark.
func (i *Indexer) compactDeleteCommandsLocked(now uint64) (int, error) {
	if now == 0 {
		return 0, nil
	}
	batch := i.db.NewWriteBatch()
	defer batch.Close()

	compacted := 0
	err := i.db.BatchReadV2(deleteStateKeyPrefix, deleteStateKeyPrefix, false,
		func(key, value []byte) error {
			state, err := decodeDeleteState(value)
			if err != nil {
				return err
			}
			if len(state.Record) == 0 || state.RelayUntil == 0 || state.RelayUntil > now {
				return nil
			}
			command, err := deleteCommandFromState(state)
			if err != nil && !errors.Is(err, ErrRecordNotFound) {
				return err
			}
			if command != nil {
				if err := batch.Delete(deleteHashDBKey(RecordHash(command))); err != nil {
					return err
				}
			}
			state.Record = nil
			state.RelayUntil = 0
			encoded, err := encodeDeleteState(state)
			if err != nil {
				return err
			}
			if err := batch.Put(append([]byte(nil), key...), encoded); err != nil {
				return err
			}
			compacted++
			return nil
		})
	if err != nil {
		return 0, err
	}
	if compacted == 0 {
		return 0, nil
	}
	if err := batch.Flush(); err != nil {
		return 0, err
	}
	return compacted, nil
}

// CompactDeleteCommands is an explicit maintenance hook used by tests and
// local administration. Regular expired-record pruning invokes the same logic.
func (i *Indexer) CompactDeleteCommands() (int, error) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	return i.compactDeleteCommandsLocked(currentUnixMilli())
}

var (
	_ = bytes.Equal
	_ indexercommon.WriteBatch
)
