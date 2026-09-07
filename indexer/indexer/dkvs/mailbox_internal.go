package dkvs

import (
	"bytes"
	"errors"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/wire"
)

func isInternalMailboxRecord(record *wire.DKVSRecord) bool {
	if record == nil || IsTombstone(record.Flags) || len(record.PubKey) != 0 || len(record.Signature) != 0 {
		return false
	}
	parsed, err := ParseKey(record.Key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) < 2 {
		return false
	}
	if parsed.Segments[1] == "msg" {
		if len(parsed.Segments) != 4 {
			return false
		}
		// A TTL-bound Direct message is a standard FREE_LOCAL record. Paid
		// persistent Direct records are trusted internal records with no proof.
		return len(record.FeeProof) == 0 || isFreeLocalRecord(record)
	}
	return len(record.FeeProof) == 0 && parsed.Segments[1] == "topic" && ((len(parsed.Segments) == 6 && parsed.Segments[3] == "msg") || (len(parsed.Segments) == 5 && parsed.Segments[3] == "key"))
}

func isInternalDirectMailbox(parsed ParsedKey) bool {
	return parsed.Namespace == "mail" && len(parsed.Segments) == 4 && parsed.Segments[1] == "msg"
}

func internalMailboxSender(parsed ParsedKey) string {
	if parsed.Namespace != "mail" {
		return ""
	}
	if len(parsed.Segments) == 4 && parsed.Segments[1] == "msg" {
		return parsed.Segments[2]
	}
	if len(parsed.Segments) == 6 && parsed.Segments[1] == "topic" && parsed.Segments[3] == "msg" {
		return parsed.Segments[4]
	}
	return ""
}

func isInternalTopicKeyPackage(parsed ParsedKey) bool {
	return parsed.Namespace == "mail" && len(parsed.Segments) == 5 &&
		parsed.Segments[1] == "topic" && parsed.Segments[3] == "key"
}

func (i *Indexer) validateInternalMailboxRecord(record *wire.DKVSRecord, parsed ParsedKey, height uint64) error {
	if !isInternalMailboxRecord(record) || record.Version != Version || record.Seq != 1 || record.IssueHeight > height {
		return ErrInvalidRecord
	}
	maxTTL := i.mailbox.MaxMsgTTL
	maxSize := i.mailbox.MaxMsgSize
	if isInternalTopicKeyPackage(parsed) {
		maxTTL = i.mailbox.MaxShareTTL
		maxSize = i.mailbox.MaxShareSize
	}
	if isInternalDirectMailbox(parsed) {
		if (record.TTL == 0 && len(record.FeeProof) != 0) || (record.TTL != 0 && !isFreeLocalRecord(record)) {
			return ErrInvalidRecord
		}
	} else if record.TTL == 0 {
		return ErrInvalidRecord
	}
	if (maxTTL != 0 && record.TTL > maxTTL) || IsExpired(record, height) {
		return ErrInvalidRecord
	}
	if len(record.Value) == 0 || (maxSize > 0 && len(record.Value) > maxSize) {
		return ErrRecordTooLarge
	}
	return nil
}

func (i *Indexer) validateInternalMailboxQuotaLocked(record *wire.DKVSRecord, parsed ParsedKey, height, now uint64) error {
	prefix := "/mail/" + parsed.Segments[0]
	records, _, _, err := i.scanLocked(prefix, nil, 0, false, height, now)
	if err != nil {
		return err
	}
	keyPackage := isInternalTopicKeyPackage(parsed)
	var totalCount, totalBytes, senderCount, senderBytes uint64
	sender := internalMailboxSender(parsed)
	for _, existing := range records {
		if existing == nil || IsTombstone(existing.Flags) || IsExpired(existing, height) || !isInternalMailboxRecord(existing) {
			continue
		}
		existingParsed, err := ParseKey(existing.Key)
		if err != nil {
			continue
		}
		existingKeyPackage := isInternalTopicKeyPackage(existingParsed)
		if keyPackage {
			if !existingKeyPackage {
				continue
			}
			totalCount++
			totalBytes += uint64(RecordSize(existing))
			continue
		}
		if existingKeyPackage {
			continue
		}
		totalCount++
		totalBytes += uint64(RecordSize(existing))
		if sender != "" && internalMailboxSender(existingParsed) == sender {
			senderCount++
			senderBytes += uint64(RecordSize(existing))
		}
	}
	newSize := uint64(RecordSize(record))
	if keyPackage {
		if (i.mailbox.MaxShares != 0 && totalCount+1 > i.mailbox.MaxShares) ||
			(i.mailbox.MaxShareBytes != 0 && totalBytes+newSize > i.mailbox.MaxShareBytes) {
			return ErrMailboxFull
		}
		return nil
	}
	if (i.mailbox.MaxMessages != 0 && totalCount+1 > i.mailbox.MaxMessages) ||
		(i.mailbox.MaxMsgBytes != 0 && totalBytes+newSize > i.mailbox.MaxMsgBytes) ||
		(sender != "" && i.mailbox.MaxMessagesPerSender != 0 && senderCount+1 > i.mailbox.MaxMessagesPerSender) ||
		(sender != "" && i.mailbox.MaxMsgBytesPerSender != 0 && senderBytes+newSize > i.mailbox.MaxMsgBytesPerSender) {
		return ErrMailboxFull
	}
	return nil
}

// PutInternalMailbox stores a MessageManager-authenticated mailbox delivery.
// It is intentionally not reachable through ordinary DKVS Put/CAS APIs. The
// stable mailbox key plus immutable signed inner payload define idempotence;
// retry metadata such as IssueHeight must not turn the same business message
// into a second delivery or extend the first delivery's retention window.
func (i *Indexer) PutInternalMailbox(record *wire.DKVSRecord) (bool, error) {
	record = cloneRecord(record)
	if record == nil {
		return false, ErrInvalidRecord
	}
	parsed, err := ParseKey(record.Key)
	if err != nil || parsed.Namespace != "mail" {
		return false, ErrInvalidKey
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	if err := i.validateInternalMailboxRecord(record, parsed, height); err != nil {
		return false, err
	}

	i.mutex.Lock()
	existing, err := i.getRaw(record.Key)
	if err == nil {
		if isInternalMailboxRecord(existing) && bytes.Equal(existing.Value, record.Value) {
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
	if err := i.validateInternalMailboxQuotaLocked(record, parsed, height, now); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	if err := i.validateInternalFreeLocalCapacityLocked(record, parsed, height, now); err != nil {
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
	if err = batch.Put(recordDBKey(record.Key), encoded); err == nil {
		err = batch.Put(hashDBKey(hash), []byte(record.Key))
	}
	if err == nil {
		// Mailbox data is an endpoint-local cache. Clean up any historical local
		// delete floor while materializing the active entry; new deletions never
		// create one.
		err = deleteDeleteStateBatch(batch, record.Key)
	}
	if err == nil {
		err = i.markPathMetaDirtyLocked(batch, []*wire.DKVSRecord{record}, height, now)
	}
	if err == nil {
		err = batch.Flush()
	}
	if err != nil {
		i.mutex.Unlock()
		return false, err
	}
	atomic.AddUint64(&i.generation, 1)
	if i.recordExpiryInitialized {
		i.addRecordExpiryLocked(record)
	}
	if err := i.replaceFreeLocalUsageLocked(record, parsed); err != nil {
		i.mutex.Unlock()
		return false, err
	}
	i.mutex.Unlock()
	i.notifyPathMutation(record)
	return true, nil
}

// DeleteInternalMailbox accepts only an ordinary signed account tombstone for
// /mail. PutLocal performs the account-derived Schnorr verification, so a
// sender cannot delete a recipient's delivery even though the original outer
// MessageManager record itself is unsigned.
func (i *Indexer) DeleteInternalMailbox(tombstone *wire.DKVSRecord) (bool, error) {
	if tombstone == nil || !IsTombstone(tombstone.Flags) {
		return false, ErrInvalidRecord
	}
	parsed, err := ParseKey(tombstone.Key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) == 0 {
		return false, ErrInvalidKey
	}
	return i.PutLocal(tombstone)
}
