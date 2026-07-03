package dkvs

import "github.com/sat20-labs/satoshinet/wire"

func (i *Indexer) validateMailboxLocked(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, height, now uint64) error {
	_ = existing
	if len(parsed.Segments) < 2 {
		return ErrInvalidKey
	}
	if IsTombstone(record.Flags) {
		return nil
	}

	switch parsed.Segments[1] {
	case "msg":
		if RecordSize(record) > i.mailbox.MaxMsgSize {
			return ErrRecordTooLarge
		}
		if i.mailbox.MaxMsgTTL > 0 && record.TTL > i.mailbox.MaxMsgTTL {
			return ErrInvalidRecord
		}
		usedBytes, usedCount, err := i.mailboxUsageLocked(parsed.Segments[0], "msg", record.Key, height, now)
		if err != nil {
			return err
		}
		if usedCount+1 > i.mailbox.MaxMessages || usedBytes+uint64(RecordSize(record)) > i.mailbox.MaxMsgBytes {
			return ErrMailboxFull
		}
	case "share":
		if RecordSize(record) > i.mailbox.MaxShareSize {
			return ErrRecordTooLarge
		}
		if i.mailbox.MaxShareTTL > 0 && record.TTL > i.mailbox.MaxShareTTL {
			return ErrInvalidRecord
		}
		usedBytes, usedCount, err := i.mailboxUsageLocked(parsed.Segments[0], "share", record.Key, height, now)
		if err != nil {
			return err
		}
		if usedCount+1 > i.mailbox.MaxShares || usedBytes+uint64(RecordSize(record)) > i.mailbox.MaxShareBytes {
			return ErrMailboxFull
		}
	default:
		return ErrInvalidKey
	}
	return nil
}

func (i *Indexer) mailboxUsageLocked(mailboxID, kind, excludeKey string, height, now uint64) (uint64, uint64, error) {
	prefix := "/mail/" + mailboxID + "/" + kind + "/"
	records, _, _, err := i.scanLocked(prefix, nil, 0, true, height, now)
	if err != nil {
		return 0, 0, err
	}
	var bytes uint64
	var count uint64
	for _, record := range records {
		if record.Key == excludeKey || IsTombstone(record.Flags) {
			continue
		}
		count++
		bytes += uint64(RecordSize(record))
	}
	return bytes, count, nil
}
