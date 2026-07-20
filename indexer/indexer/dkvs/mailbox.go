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
	switch kind {
	case "msg":
		if RecordSize(record) > i.mailbox.MaxMsgSize {
			return ErrRecordTooLarge
		}
		if record.TTL == 0 || (i.mailbox.MaxMsgTTL > 0 && record.TTL > i.mailbox.MaxMsgTTL) {
			return ErrInvalidRecord
		}
		if len(parsed.Segments) != 4 {
			return ErrInvalidKey
		}
		senderPath := "/mail/" + parsed.Segments[0] + "/msg/" + parsed.Segments[2]
		senderMeta, err := i.ensurePathMetaLocked(senderPath, height, now)
		if err != nil {
			return err
		}
		senderCount, senderBytes := mailboxUsageWithoutExisting(i, senderMeta.ActiveRecords, senderMeta.ActiveTotalSize, existing, height, now)
		if senderCount+1 > i.mailbox.MaxMessagesPerSender || senderBytes+uint64(RecordSize(record)) > i.mailbox.MaxMsgBytesPerSender {
			return ErrMailboxFull
		}
		mailboxPrefix := "/mail/" + parsed.Segments[0] + "/msg"
		records, _, _, err := i.scanLocked(mailboxPrefix, nil, 0, true, height, now)
		if err != nil {
			return err
		}
		var mailboxBytes uint64
		var mailboxCount uint64
		for _, candidate := range records {
			if existing != nil && candidate.Key == existing.Key {
				continue
			}
			mailboxCount++
			mailboxBytes += uint64(RecordSize(candidate))
		}
		if mailboxCount+1 > i.mailbox.MaxMessages || mailboxBytes+uint64(RecordSize(record)) > i.mailbox.MaxMsgBytes {
			return ErrMailboxFull
		}
	case "share":
		path := "/mail/" + parsed.Segments[0] + "/share"
		meta, err := i.ensurePathMetaLocked(path, height, now)
		if err != nil {
			return err
		}
		usedCount, usedBytes := mailboxUsageWithoutExisting(i, meta.ActiveRecords, meta.ActiveTotalSize, existing, height, now)
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

func mailboxUsageWithoutExisting(i *Indexer, count, size uint64, existing *wire.DKVSRecord, height, now uint64) (uint64, uint64) {
	if !existingRecordActive(i, existing, height, now) {
		return count, size
	}
	if count > 0 {
		count--
	}
	existingSize := uint64(RecordSize(existing))
	if size >= existingSize {
		size -= existingSize
	} else {
		size = 0
	}
	return count, size
}
