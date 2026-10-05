package dkvs

import (
	"github.com/sat20-labs/satoshinet/wire"
	"strings"
)

func (i *Indexer) ReadPrefix(prefix string) (*PrefixReadResult, error) {
	if i == nil {
		return nil, ErrInvalidRecord
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if len(prefix) > MaxPrefixLength {
		return nil, ErrInvalidKey
	}
	if err := validateReadablePrefix(prefix); err != nil {
		return nil, err
	}
	height, now := i.currentHeight(), currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()
	records, _, _, err := i.scanLocked(prefix, nil, MaxPrefixReadRecords+1, true, height, now)
	if err != nil {
		return nil, err
	}
	if len(records) > MaxPrefixReadRecords {
		return nil, ErrBatchTooLarge
	}
	totalBytes := 0
	active := make([]*wire.DKVSRecord, 0, len(records))
	states := make([]DKVSKeyState, 0, len(records))
	for _, record := range records {
		if record == nil {
			continue
		}
		size := RecordSize(record)
		if size > MaxPrefixReadBytes-totalBytes {
			return nil, ErrBatchTooLarge
		}
		totalBytes += size
		state, err := i.keyStateLocked(record.Key, false, height, now)
		if err != nil {
			return nil, err
		}
		if err := appendPrefixKeyState(&states, &totalBytes, state); err != nil {
			return nil, err
		}
		active = append(active, cloneRecord(record))
	}
	return &PrefixReadResult{EndpointID: i.endpointID(), Prefix: prefix, ViewHeight: height, Records: active, KeyStates: states}, nil
}
