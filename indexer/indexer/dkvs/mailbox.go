package dkvs

import "github.com/sat20-labs/satoshinet/wire"

func (i *Indexer) validateMailboxLocked(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, height, now uint64) error {
	if len(parsed.Segments) < 2 {
		return ErrInvalidKey
	}
	if IsTombstone(record.Flags) {
		return nil
	}

	kind := parsed.Segments[1]
	path := "/mail/" + parsed.Segments[0] + "/" + kind
	meta, err := i.ensurePathMetaLocked(path, height, now)
	if err != nil {
		return err
	}
	usedBytes := meta.ActiveTotalSize
	usedCount := meta.ActiveRecords
	if existingRecordActive(i, existing, height, now) {
		existingSize := uint64(RecordSize(existing))
		if usedCount > 0 {
			usedCount--
		}
		if usedBytes >= existingSize {
			usedBytes -= existingSize
		} else {
			usedBytes = 0
		}
	}

	switch kind {
	case "msg":
		if RecordSize(record) > i.mailbox.MaxMsgSize {
			return ErrRecordTooLarge
		}
		if record.TTL == 0 || (i.mailbox.MaxMsgTTL > 0 && record.TTL > i.mailbox.MaxMsgTTL) {
			return ErrInvalidRecord
		}
		if usedCount+1 > i.mailbox.MaxMessages || usedBytes+uint64(RecordSize(record)) > i.mailbox.MaxMsgBytes {
			return ErrMailboxFull
		}
	case "share":
		if RecordSize(record) > i.mailbox.MaxShareSize {
			return ErrRecordTooLarge
		}
		if record.TTL == 0 || (i.mailbox.MaxShareTTL > 0 && record.TTL > i.mailbox.MaxShareTTL) {
			return ErrInvalidRecord
		}
		if usedCount+1 > i.mailbox.MaxShares || usedBytes+uint64(RecordSize(record)) > i.mailbox.MaxShareBytes {
			return ErrMailboxFull
		}
	default:
		return ErrInvalidKey
	}
	return nil
}
