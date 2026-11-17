package dkvs

import (
	"errors"

	"github.com/sat20-labs/satoshinet/wire"
)

// Quota statistics describe current rows, not a transaction journal. They are
// rebuilt once from the database and changed only after a successful commit,
// under the same mutex as the rows. No record values are retained here.
type quotaUsage struct {
	initialized      bool
	height           uint64
	tipHeight        uint64
	nextIssueHeight  uint64
	freeInvalid      uint64
	freeOverfull     uint64
	mailboxOverfull  uint64
	mailboxMinExpiry uint64
	mailboxEntries   map[string]mailboxUsageEntry
	mailboxes        map[string]projectedUsage
	senders          map[string]projectedUsage
	shares           map[string]projectedUsage
}

type mailboxUsageEntry struct {
	mailbox string
	sender  string
	bytes   uint64
	expiry  uint64
	share   bool
}

func adjustOverfull(count uint64, before, after bool) uint64 {
	if before && !after {
		return count - 1
	}
	if !before && after {
		return count + 1
	}
	return count
}

func usageExceeds(usage projectedUsage, records, bytes uint64) bool {
	return usage.records > records || usage.bytes > bytes
}

func (i *Indexer) freeLocalSignerOverfull(usage freeLocalUsage) bool {
	if usage.records == 0 {
		return false
	}
	return i.freeLocal.MaxRecordsPerSigner == 0 || usage.records > i.freeLocal.MaxRecordsPerSigner ||
		i.freeLocal.MaxBytesPerSigner == 0 || usage.bytes > i.freeLocal.MaxBytesPerSigner ||
		usage.blobKeys > i.blob.MaxFreeLocalKeysPerSigner
}

func (i *Indexer) resetQuotaUsageLocked() {
	i.resetFeeUsageLocked()
	i.resetFreeLocalUsageLocked()
	i.quotaUsage = quotaUsage{
		mailboxEntries: make(map[string]mailboxUsageEntry),
		mailboxes:      make(map[string]projectedUsage),
		senders:        make(map[string]projectedUsage),
		shares:         make(map[string]projectedUsage),
	}
}

func (i *Indexer) quotaUsageNeedsRebuild(height, tip uint64) bool {
	u := &i.quotaUsage
	return !u.initialized || height < u.height || tip < u.tipHeight ||
		(u.height == 0 && height != 0) ||
		(u.nextIssueHeight != 0 && tip >= u.nextIssueHeight)
}

func (i *Indexer) advanceQuotaUsageLocked(height, tip uint64) {
	i.expireFeeUsageLocked(height)
	i.expireFreeLocalUsageLocked(height)
	i.expireMailboxUsageLocked(height)
	i.quotaUsage.height, i.quotaUsage.tipHeight = height, tip
}

func (i *Indexer) ensureQuotaUsageLocked(height, now uint64) error {
	tip := i.issueHeightTip(height)
	if !i.quotaUsageNeedsRebuild(height, tip) {
		i.advanceQuotaUsageLocked(height, tip)
		return nil
	}

	i.resetQuotaUsageLocked()
	err := i.db.BatchReadV2(recordKeyPrefix, recordKeyPrefix, false, func(_, value []byte) error {
		record, err := UnmarshalRecord(value)
		if err != nil {
			return err
		}
		return i.addActiveQuotaUsageLocked(record, height, tip)
	})
	if err != nil {
		i.resetQuotaUsageLocked()
		return err
	}
	i.quotaUsage.height, i.quotaUsage.tipHeight = height, tip
	i.quotaUsage.initialized = true
	return nil
}

// Match the active-row eligibility used by the previous full projection. Fee
// payment/relay state does not remove a stored row from quota accounting.
func (i *Indexer) addActiveQuotaUsageLocked(record *wire.DKVSRecord, height, tip uint64) error {
	if IsTombstone(record.Flags) || IsExpired(record, height) {
		return nil
	}
	if (height != 0 || i.tipHeight != nil) && record.IssueHeight > tip {
		if next := i.quotaUsage.nextIssueHeight; next == 0 || record.IssueHeight < next {
			i.quotaUsage.nextIssueHeight = record.IssueHeight
		}
		return nil
	}
	var issueTips []uint64
	if i.tipHeight != nil {
		issueTips = []uint64{tip}
	}
	parsed, err := validateParsedCoreWithVerifier(record, height, true, false, nil, issueTips...)
	if err != nil || i.validateStoredPermission(parsed, record) != nil {
		return nil
	}
	return i.addQuotaUsageLocked(record, parsed)
}

func (i *Indexer) addQuotaUsageLocked(record *wire.DKVSRecord, parsed ParsedKey) error {
	if indexed, ok := i.feeVerifier.(IndexedFeeCapacityVerifier); ok {
		if key, err := indexed.FeeUsageKey(record); err == nil && key != "" {
			i.addFeeUsageLocked(record, key)
		}
	}
	if isFreeLocalRecord(record) {
		signer, err := freeLocalSigner(record, parsed)
		if err != nil {
			return err
		}
		i.addFreeLocalUsageLocked(record, signer)
	}
	if parsed.Namespace == "mail" {
		entry, err := mailboxContribution(record, parsed)
		if err != nil {
			return err
		}
		i.addMailboxUsageLocked(record.Key, entry)
	}
	return nil
}

func (i *Indexer) removeQuotaUsageLocked(key string) {
	i.removeFeeUsageLocked(key)
	i.removeFreeLocalUsageLocked(key)
	i.removeMailboxUsageLocked(key)
}

// The database commit is already complete. A cache that cannot be advanced is
// invalidated, never published as an empty valid quota view. The next admission
// must rebuild successfully before it can authorize another write.
func (i *Indexer) updateQuotaUsageLocked(removed []string, records []*wire.DKVSRecord, height uint64) {
	if !i.quotaUsage.initialized {
		return
	}
	tip := i.issueHeightTip(height)
	if i.quotaUsageNeedsRebuild(height, tip) {
		i.resetQuotaUsageLocked()
		return
	}
	i.advanceQuotaUsageLocked(height, tip)
	for _, key := range removed {
		i.removeQuotaUsageLocked(key)
	}
	for _, record := range records {
		if record == nil {
			continue
		}
		i.removeQuotaUsageLocked(record.Key)
		if err := i.addActiveQuotaUsageLocked(record, height, tip); err != nil {
			i.resetQuotaUsageLocked()
			return
		}
	}
}

func mailboxContribution(record *wire.DKVSRecord, parsed ParsedKey) (mailboxUsageEntry, error) {
	if record == nil || parsed.Namespace != "mail" || len(parsed.Segments) == 0 {
		return mailboxUsageEntry{}, ErrInvalidKey
	}
	entry := mailboxUsageEntry{mailbox: parsed.Segments[0],
		bytes: uint64(RecordSize(record)), expiry: RecordExpiryHeight(record)}
	if isInternalMailboxRecord(record) {
		entry.share = isInternalTopicKeyPackage(parsed)
		entry.sender = internalMailboxSender(parsed)
		return entry, nil
	}
	if len(parsed.Segments) != 4 {
		return entry, ErrInvalidKey
	}
	switch parsed.Segments[1] {
	case "msg":
		entry.sender = parsed.Segments[2]
	case "share":
		entry.share = true
	default:
		return entry, ErrInvalidKey
	}
	return entry, nil
}

func (i *Indexer) changeMailboxGroupLocked(groups map[string]projectedUsage, key string,
	size uint64, adding bool, maxRecords, maxBytes uint64) {
	before := groups[key]
	after := before
	if adding {
		after.records++
		after.bytes += size
	} else {
		after.records--
		after.bytes -= size
	}
	i.quotaUsage.mailboxOverfull = adjustOverfull(i.quotaUsage.mailboxOverfull,
		usageExceeds(before, maxRecords, maxBytes), usageExceeds(after, maxRecords, maxBytes))
	if after.records == 0 {
		delete(groups, key)
	} else {
		groups[key] = after
	}
}

func (i *Indexer) changeMailboxUsageLocked(entry mailboxUsageEntry, adding bool) {
	u := &i.quotaUsage
	if entry.share {
		i.changeMailboxGroupLocked(u.shares, entry.mailbox, entry.bytes, adding, i.mailbox.MaxShares, i.mailbox.MaxShareBytes)
		return
	}
	i.changeMailboxGroupLocked(u.mailboxes, entry.mailbox, entry.bytes, adding, i.mailbox.MaxMessages, i.mailbox.MaxMsgBytes)
	if entry.sender != "" {
		i.changeMailboxGroupLocked(u.senders, entry.mailbox+"\x00"+entry.sender, entry.bytes, adding,
			i.mailbox.MaxMessagesPerSender, i.mailbox.MaxMsgBytesPerSender)
	}
}

func (i *Indexer) addMailboxUsageLocked(key string, entry mailboxUsageEntry) {
	i.quotaUsage.mailboxEntries[key] = entry
	i.changeMailboxUsageLocked(entry, true)
	if entry.expiry != 0 && (i.quotaUsage.mailboxMinExpiry == 0 || entry.expiry < i.quotaUsage.mailboxMinExpiry) {
		i.quotaUsage.mailboxMinExpiry = entry.expiry
	}
}

func (i *Indexer) removeMailboxUsageLocked(key string) {
	entry, ok := i.quotaUsage.mailboxEntries[key]
	if !ok {
		return
	}
	delete(i.quotaUsage.mailboxEntries, key)
	i.changeMailboxUsageLocked(entry, false)
}

func (i *Indexer) expireMailboxUsageLocked(height uint64) {
	if height == 0 || i.quotaUsage.mailboxMinExpiry == 0 || height < i.quotaUsage.mailboxMinExpiry {
		return
	}
	var next uint64
	for key, entry := range i.quotaUsage.mailboxEntries {
		if entry.expiry == 0 {
			continue
		}
		if entry.expiry <= height {
			i.removeMailboxUsageLocked(key)
		} else if next == 0 || entry.expiry < next {
			next = entry.expiry
		}
	}
	i.quotaUsage.mailboxMinExpiry = next
}

func projectUsage(base, projected map[string]projectedUsage, key string, size uint64, adding bool) error {
	usage, ok := projected[key]
	if !ok {
		usage = base[key]
	}
	if adding {
		usage.records++
		usage.bytes += size
	} else {
		if usage.records == 0 || usage.bytes < size {
			return ErrConcurrentUpdate
		}
		usage.records--
		usage.bytes -= size
	}
	projected[key] = usage
	return nil
}

func projectedOverfull(count uint64, base, projected map[string]projectedUsage, maxRecords, maxBytes uint64) uint64 {
	for key, usage := range projected {
		count = adjustOverfull(count, usageExceeds(base[key], maxRecords, maxBytes),
			usageExceeds(usage, maxRecords, maxBytes))
	}
	return count
}

func (i *Indexer) validateMailboxUsageProjectionLocked(ready []preparedCASMutation) error {
	mailboxes := make(map[string]projectedUsage)
	senders := make(map[string]projectedUsage)
	shares := make(map[string]projectedUsage)
	project := func(entry mailboxUsageEntry, adding bool) error {
		if entry.share {
			return projectUsage(i.quotaUsage.shares, shares, entry.mailbox, entry.bytes, adding)
		}
		if err := projectUsage(i.quotaUsage.mailboxes, mailboxes, entry.mailbox, entry.bytes, adding); err != nil {
			return err
		}
		if entry.sender != "" {
			return projectUsage(i.quotaUsage.senders, senders, entry.mailbox+"\x00"+entry.sender, entry.bytes, adding)
		}
		return nil
	}
	for _, item := range ready {
		record := item.mutation.Record
		if entry, ok := i.quotaUsage.mailboxEntries[record.Key]; ok {
			if err := project(entry, false); err != nil {
				return err
			}
		}
		if !IsTombstone(record.Flags) && item.parsed.Namespace == "mail" {
			entry, err := mailboxContribution(record, item.parsed)
			if err != nil {
				return err
			}
			if err := project(entry, true); err != nil {
				return err
			}
		}
	}
	overfull := i.quotaUsage.mailboxOverfull
	overfull = projectedOverfull(overfull, i.quotaUsage.mailboxes, mailboxes, i.mailbox.MaxMessages, i.mailbox.MaxMsgBytes)
	overfull = projectedOverfull(overfull, i.quotaUsage.senders, senders, i.mailbox.MaxMessagesPerSender, i.mailbox.MaxMsgBytesPerSender)
	overfull = projectedOverfull(overfull, i.quotaUsage.shares, shares, i.mailbox.MaxShares, i.mailbox.MaxShareBytes)
	if overfull != 0 {
		return ErrMailboxFull
	}
	return nil
}

func (i *Indexer) freeLocalContribution(record *wire.DKVSRecord, signer string) freeLocalUsageEntry {
	parsed, _ := ParseKey(record.Key)
	return freeLocalUsageEntry{
		signer: signer, bytes: uint64(RecordSize(record)), blob: IsBlobKey(parsed),
		expiry:  RecordExpiryHeight(record),
		invalid: !i.freeLocal.Enabled || record.TTL == 0 || record.TTL > i.freeLocal.MaxTTL,
	}
}

func (i *Indexer) validateFreeLocalUsageProjectionLocked(ready []preparedCASMutation) error {
	bySigner := make(map[string]freeLocalUsage)
	total, invalid := i.freeLocalTotal, i.quotaUsage.freeInvalid
	project := func(entry freeLocalUsageEntry, adding bool) error {
		usage, ok := bySigner[entry.signer]
		if !ok {
			usage = i.freeLocalUsageBySigner[entry.signer]
		}
		if adding {
			usage.records++
			usage.bytes += entry.bytes
			total.records++
			total.bytes += entry.bytes
			if entry.blob {
				usage.blobKeys++
			}
			if entry.invalid {
				invalid++
			}
		} else {
			if usage.records == 0 || usage.bytes < entry.bytes || total.records == 0 || total.bytes < entry.bytes ||
				(entry.blob && usage.blobKeys == 0) || (entry.invalid && invalid == 0) {
				return ErrConcurrentUpdate
			}
			usage.records--
			usage.bytes -= entry.bytes
			total.records--
			total.bytes -= entry.bytes
			if entry.blob {
				usage.blobKeys--
			}
			if entry.invalid {
				invalid--
			}
		}
		bySigner[entry.signer] = usage
		return nil
	}
	for _, item := range ready {
		record := item.mutation.Record
		if entry, ok := i.freeLocalUsageEntries[record.Key]; ok {
			if err := project(entry, false); err != nil {
				return err
			}
		}
		if !IsTombstone(record.Flags) && isFreeLocalRecord(record) {
			signer, err := freeLocalSigner(record, item.parsed)
			if err != nil {
				return err
			}
			if err := project(i.freeLocalContribution(record, signer), true); err != nil {
				return err
			}
		}
	}
	if invalid != 0 {
		return ErrFreeLocalDisabled
	}
	if total.records == 0 {
		return nil
	}
	policy := i.freeLocal
	if policy.MaxTotalRecords == 0 || total.records > policy.MaxTotalRecords ||
		policy.MaxTotalBytes == 0 || total.bytes > policy.MaxTotalBytes {
		return ErrFreeLocalQuotaExceeded
	}
	overfull := i.quotaUsage.freeOverfull
	for signer, usage := range bySigner {
		overfull = adjustOverfull(overfull, i.freeLocalSignerOverfull(i.freeLocalUsageBySigner[signer]),
			i.freeLocalSignerOverfull(usage))
	}
	if overfull != 0 {
		return ErrFreeLocalQuotaExceeded
	}
	return nil
}

func (i *Indexer) validateFeeUsageProjectionLocked(ready []preparedCASMutation) error {
	limits := make(map[string]uint64)
	for _, item := range ready {
		if item.capacity.indexed != nil && !IsTombstone(item.mutation.Record.Flags) &&
			item.capacity.descriptor.UsageKey != "" {
			limits[item.capacity.descriptor.UsageKey] = item.capacity.descriptor.MaxRecords
		}
	}
	if len(limits) == 0 {
		return nil
	}
	indexed, ok := i.feeVerifier.(IndexedFeeCapacityVerifier)
	if !ok {
		return ErrConcurrentUpdate
	}
	counts := make(map[string]uint64)
	project := func(key string, adding bool) error {
		count, ok := counts[key]
		if !ok {
			count = i.feeUsageCounts[key]
		}
		if adding {
			count++
		} else {
			if count == 0 {
				return ErrConcurrentUpdate
			}
			count--
		}
		counts[key] = count
		return nil
	}
	for _, item := range ready {
		record := item.mutation.Record
		if entry, ok := i.feeUsageEntries[record.Key]; ok {
			if err := project(entry.usageKey, false); err != nil {
				return err
			}
		}
		if !IsTombstone(record.Flags) {
			if key, err := indexed.FeeUsageKey(record); err == nil && key != "" {
				if err := project(key, true); err != nil {
					return err
				}
			}
		}
	}
	for key, max := range limits {
		count, ok := counts[key]
		if !ok {
			count = i.feeUsageCounts[key]
		}
		if max == 0 || count > max {
			return ErrFeeCapacityExceeded
		}
	}
	return nil
}

func (i *Indexer) validateQuotaUsageProjectionLocked(ready []preparedCASMutation, height, now uint64) error {
	if err := i.ensureQuotaUsageLocked(height, now); err != nil {
		return err
	}
	err := i.validateMailboxUsageProjectionLocked(ready)
	if err == nil {
		err = i.validateFreeLocalUsageProjectionLocked(ready)
	}
	if err == nil {
		err = i.validateFeeUsageProjectionLocked(ready)
	}
	if errors.Is(err, ErrConcurrentUpdate) {
		// A contribution mismatch is not permission to undercount. Retry only
		// after rebuilding from the authoritative rows.
		i.resetQuotaUsageLocked()
	}
	return err
}
