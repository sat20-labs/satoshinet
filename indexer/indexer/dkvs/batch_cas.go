package dkvs

import (
	"errors"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/wire"
)

type preparedCASMutation struct {
	mutation     CASMutation
	parsed       ParsedKey
	snapshot     writeStateSnapshot
	forceReplace bool
	capacity     preparedFeeCapacity
	retention    *PaidRecordRetention
	applied      bool
}

type batchCASPreparation struct {
	mutations        []preparedCASMutation
	height           uint64
	now              uint64
	policyGeneration uint64
}

type batchCASEvent struct {
	eventType uint8
	record    *wire.DKVSRecord
	relay     bool
}

func cloneCASMutations(mutations []CASMutation) []CASMutation {
	cloned := make([]CASMutation, len(mutations))
	for i := range mutations {
		cloned[i] = mutations[i]
		cloned[i].Record = cloneRecord(mutations[i].Record)
		if mutations[i].Precondition.ExpectedHash != nil {
			h := *mutations[i].Precondition.ExpectedHash
			cloned[i].Precondition.ExpectedHash = &h
		}
	}
	return cloned
}

func validateCASMutations(mutations []CASMutation) error {
	if len(mutations) == 0 {
		return ErrInvalidRecord
	}
	if len(mutations) > MaxBatchCASMutations {
		return ErrBatchTooLarge
	}
	seen := make(map[string]struct{}, len(mutations))
	total := 0
	for _, mutation := range mutations {
		if mutation.Record == nil || !mutation.Precondition.Valid() {
			return ErrInvalidRecord
		}
		if _, ok := seen[mutation.Record.Key]; ok {
			return ErrInvalidRecord
		}
		seen[mutation.Record.Key] = struct{}{}
		total += RecordSize(mutation.Record)
		if total > MaxBatchCASTotalSize {
			return ErrBatchTooLarge
		}
	}
	return nil
}

func mutationAlreadyApplied(record *wire.DKVSRecord, snapshot writeStateSnapshot) bool {
	if record == nil {
		return false
	}
	want := RecordHash(record)
	if !IsTombstone(record.Flags) {
		return snapshot.existing != nil && RecordHash(snapshot.existing) == want
	}
	return snapshot.deleteState != nil && snapshot.deleteState.Record != nil &&
		RecordHash(snapshot.deleteState.Record) == want
}

func (i *Indexer) prepareBatchCAS(mutations []CASMutation) (batchCASPreparation, error) {
	mutations = cloneCASMutations(mutations)
	if err := validateCASMutations(mutations); err != nil {
		return batchCASPreparation{}, err
	}
	validators := i.snapshotValidators()
	prep := batchCASPreparation{
		mutations:        make([]preparedCASMutation, 0, len(mutations)),
		height:           i.currentHeight(),
		now:              currentUnixMilli(),
		policyGeneration: validators.policyGeneration,
	}
	for _, mutation := range mutations {
		record := mutation.Record
		parsed, err := validateParsedCoreWithVerifier(record, prep.height, prep.now, false, false, nil)
		if err != nil {
			return batchCASPreparation{}, err
		}
		snapshot, err := i.readWriteStateSnapshot(record.Key, parsed, validators)
		if err != nil {
			return batchCASPreparation{}, err
		}
		prepared := preparedCASMutation{mutation: mutation, parsed: parsed, snapshot: snapshot}
		if mutationAlreadyApplied(record, snapshot) {
			prepared.applied = true
			prep.mutations = append(prep.mutations, prepared)
			continue
		}
		if !IsTombstone(record.Flags) {
			if err := verifyFeeProofWith(validators.feeVerifier, record, parsed); err != nil {
				return batchCASPreparation{}, err
			}
			prepared.retention, err = verifiedPaidRetentionAfterFeeVerification(
				record, parsed, validators.feeVerifier, prep.height,
			)
			if err != nil {
				return batchCASPreparation{}, err
			}
		}
		prepared.forceReplace, err = validateWritePermissionWith(
			parsed, record, snapshot.existing, snapshot.requiresResolve, validators,
		)
		if err != nil {
			return batchCASPreparation{}, err
		}
		if !IsTombstone(record.Flags) {
			prepared.capacity, err = i.prepareFeeCapacity(
				record, parsed, snapshot.existing, validators.feeVerifier, prep.height, prep.now,
			)
			if err != nil {
				return batchCASPreparation{}, err
			}
		}
		prep.mutations = append(prep.mutations, prepared)
	}
	return prep, nil
}

func writePreconditionMatches(i *Indexer, existing *wire.DKVSRecord, condition WritePrecondition, height, now uint64) bool {
	if condition.ExpectAbsent {
		return !existingRecordActive(i, existing, height, now)
	}
	if condition.ExpectedHash == nil || !existingRecordActive(i, existing, height, now) {
		return false
	}
	hash := RecordHash(existing)
	return *condition.ExpectedHash == hash
}

func (i *Indexer) batchCASReadyLocked(prep batchCASPreparation, height, now uint64) ([]preparedCASMutation, error) {
	if atomic.LoadUint64(&i.policyGeneration) != prep.policyGeneration {
		return nil, ErrConcurrentUpdate
	}
	already := 0
	for _, prepared := range prep.mutations {
		current, err := i.writeStateStillCurrentLocked(prepared.mutation.Record.Key, prepared.parsed, prepared.snapshot)
		if err != nil {
			return nil, err
		}
		if !current {
			return nil, ErrConcurrentUpdate
		}
		if prepared.applied {
			already++
		}
	}
	if already != 0 {
		if already == len(prep.mutations) {
			return nil, nil
		}
		return nil, ErrWriteConflict
	}
	ready := make([]preparedCASMutation, 0, len(prep.mutations))
	for _, prepared := range prep.mutations {
		record := prepared.mutation.Record
		existing := prepared.snapshot.existing
		if IsExpired(record, height, now) {
			return nil, ErrExpiredRecord
		}
		if !writePreconditionMatches(i, existing, prepared.mutation.Precondition, height, now) {
			return nil, ErrWriteConflict
		}
		if IsTombstone(record.Flags) {
			if !existingRecordActive(i, existing, height, now) ||
				(!prepared.forceReplace && record.Seq <= existing.Seq) {
				return nil, ErrWriteConflict
			}
		} else {
			if deleteFloorBlocksRecord(prepared.parsed, prepared.snapshot.deleteState, record) {
				return nil, ErrWriteConflict
			}
			if existingRecordActive(i, existing, height, now) && !prepared.forceReplace &&
				CompareRecords(existing, record) >= 0 {
				return nil, ErrWriteConflict
			}
		}
		ready = append(ready, prepared)
	}
	return ready, nil
}

func (i *Indexer) projectedActiveRecordsLocked(ready []preparedCASMutation, height, now uint64) (map[string]*wire.DKVSRecord, error) {
	records, _, _, err := i.scanLocked("", nil, 0, true, height, now)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*wire.DKVSRecord, len(records)+len(ready))
	for _, record := range records {
		byKey[record.Key] = record
	}
	for _, prepared := range ready {
		record := prepared.mutation.Record
		delete(byKey, record.Key)
		if !IsTombstone(record.Flags) {
			byKey[record.Key] = record
		}
	}
	return byKey, nil
}

func (i *Indexer) validateProjectedMailboxLocked(records map[string]*wire.DKVSRecord) error {
	mailboxes := make(map[string]projectedUsage)
	senders := make(map[string]projectedUsage)
	shares := make(map[string]projectedUsage)
	for _, record := range records {
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return err
		}
		if parsed.Namespace != "mail" {
			continue
		}
		if len(parsed.Segments) != 4 {
			return ErrInvalidKey
		}
		size := uint64(RecordSize(record))
		switch parsed.Segments[1] {
		case "msg":
			mailboxKey := parsed.Segments[0]
			senderKey := mailboxKey + "\x00" + parsed.Segments[2]
			m := mailboxes[mailboxKey]
			m.records++
			m.bytes += size
			mailboxes[mailboxKey] = m
			s := senders[senderKey]
			s.records++
			s.bytes += size
			senders[senderKey] = s
		case "share":
			x := shares[parsed.Segments[0]]
			x.records++
			x.bytes += size
			shares[parsed.Segments[0]] = x
		default:
			return ErrInvalidKey
		}
	}
	for _, u := range mailboxes {
		if u.records > i.mailbox.MaxMessages || u.bytes > i.mailbox.MaxMsgBytes {
			return ErrMailboxFull
		}
	}
	for _, u := range senders {
		if u.records > i.mailbox.MaxMessagesPerSender || u.bytes > i.mailbox.MaxMsgBytesPerSender {
			return ErrMailboxFull
		}
	}
	for _, u := range shares {
		if u.records > i.mailbox.MaxShares || u.bytes > i.mailbox.MaxShareBytes {
			return ErrMailboxFull
		}
	}
	return nil
}

type projectedUsage struct{ records, bytes uint64 }

func (i *Indexer) validateProjectedFreeLocalLocked(records map[string]*wire.DKVSRecord) error {
	policy := i.freeLocal
	bySigner := make(map[string]projectedUsage)
	blobKeys := make(map[string]uint64)
	var total projectedUsage
	for _, record := range records {
		if !isFreeLocalRecord(record) {
			continue
		}
		if !policy.Enabled || record.TTL == 0 || record.TTL > policy.MaxTTL {
			return ErrFreeLocalDisabled
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return err
		}
		signer, err := freeLocalSigner(record, parsed)
		if err != nil {
			return err
		}
		u := bySigner[signer]
		u.records++
		u.bytes += uint64(RecordSize(record))
		bySigner[signer] = u
		total.records++
		total.bytes += uint64(RecordSize(record))
		if IsBlobKey(parsed) {
			blobKeys[signer]++
		}
	}
	if total.records == 0 {
		return nil
	}
	if policy.MaxTotalRecords == 0 || total.records > policy.MaxTotalRecords ||
		policy.MaxTotalBytes == 0 || total.bytes > policy.MaxTotalBytes {
		return ErrFreeLocalQuotaExceeded
	}
	for signer, u := range bySigner {
		if policy.MaxRecordsPerSigner == 0 || u.records > policy.MaxRecordsPerSigner ||
			policy.MaxBytesPerSigner == 0 || u.bytes > policy.MaxBytesPerSigner ||
			blobKeys[signer] > i.blob.MaxFreeLocalKeysPerSigner {
			return ErrFreeLocalQuotaExceeded
		}
	}
	return nil
}

func (i *Indexer) validateProjectedFeeLocked(ready []preparedCASMutation,
	records map[string]*wire.DKVSRecord, height, now uint64) error {
	limits := make(map[string]uint64)
	verifiers := make(map[string]IndexedFeeCapacityVerifier)
	for _, prepared := range ready {
		if prepared.capacity.indexed == nil || IsTombstone(prepared.mutation.Record.Flags) {
			continue
		}
		key := prepared.capacity.descriptor.UsageKey
		limits[key] = prepared.capacity.descriptor.MaxRecords
		verifiers[key] = prepared.capacity.indexed
	}
	counts := make(map[string]uint64)
	for _, record := range records {
		for usageKey, verifier := range verifiers {
			candidateKey, err := verifier.FeeUsageKey(record)
			if err != nil {
				continue
			}
			if candidateKey == usageKey {
				counts[usageKey]++
			}
		}
	}
	for key, max := range limits {
		if max == 0 || counts[key] > max {
			return ErrFeeCapacityExceeded
		}
	}
	projected := make([]*wire.DKVSRecord, 0, len(records))
	for _, record := range records {
		projected = append(projected, record)
	}
	for _, prepared := range ready {
		if prepared.capacity.fallback == nil || IsTombstone(prepared.mutation.Record.Flags) {
			continue
		}
		if err := prepared.capacity.fallback.VerifyFeeCapacity(
			prepared.mutation.Record, prepared.parsed, prepared.snapshot.existing,
			projected, height, now,
		); err != nil {
			return err
		}
	}
	return nil
}

func (i *Indexer) validateBatchStateLocked(ready []preparedCASMutation, height, now uint64) error {
	for _, prepared := range ready {
		record := prepared.mutation.Record
		if IsTombstone(record.Flags) {
			continue
		}
		switch prepared.parsed.Namespace {
		case "blob":
			if err := validateBlobRecord(record, prepared.parsed, i.blob); err != nil {
				return err
			}
		case "tmp":
			if err := i.validateTmp(record); err != nil {
				return err
			}
		case "mail":
			if err := i.validateMailboxRecordStatic(record, prepared.parsed); err != nil {
				return err
			}
		}
	}
	records, err := i.projectedActiveRecordsLocked(ready, height, now)
	if err != nil {
		return err
	}
	if err := i.validateProjectedMailboxLocked(records); err != nil {
		return err
	}
	if err := i.validateProjectedFreeLocalLocked(records); err != nil {
		return err
	}
	return i.validateProjectedFeeLocked(ready, records, height, now)
}

func (i *Indexer) validateMailboxRecordStatic(record *wire.DKVSRecord, parsed ParsedKey) error {
	if record == nil || parsed.Namespace != "mail" || len(parsed.Segments) != 4 {
		return ErrInvalidKey
	}
	switch parsed.Segments[1] {
	case "msg":
		if RecordSize(record) > i.mailbox.MaxMsgSize {
			return ErrRecordTooLarge
		}
		if (record.TTL == 0 && !isAutopayRecord(record)) ||
			(record.TTL != 0 && i.mailbox.MaxMsgTTL > 0 && record.TTL > i.mailbox.MaxMsgTTL) {
			return ErrInvalidRecord
		}
	case "share":
		if RecordSize(record) > i.mailbox.MaxShareSize {
			return ErrRecordTooLarge
		}
		if (record.TTL == 0 && !isAutopayRecord(record)) ||
			(record.TTL != 0 && i.mailbox.MaxShareTTL > 0 && record.TTL > i.mailbox.MaxShareTTL) {
			return ErrInvalidRecord
		}
	default:
		return ErrInvalidKey
	}
	return nil
}

func (i *Indexer) commitBatchCASLocked(ready []preparedCASMutation, height, now uint64) ([]batchCASEvent, error) {
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	touched := make([]*wire.DKVSRecord, 0, len(ready)*2)
	events := make([]batchCASEvent, 0, len(ready))
	for _, prepared := range ready {
		record := prepared.mutation.Record
		existing := prepared.snapshot.existing
		if existing != nil {
			if err := batch.Delete(hashDBKey(RecordHash(existing))); err != nil {
				return nil, err
			}
			touched = append(touched, existing)
		}
		if IsTombstone(record.Flags) {
			if err := batch.Delete(recordDBKey(record.Key)); err != nil {
				return nil, err
			}
			state := &deleteState{
				FloorSeq:   record.Seq,
				RelayUntil: deleteRelayUntil(now),
				PubKey:     append([]byte(nil), record.PubKey...),
				Record:     record,
				LocalOnly:  isFreeLocalRecord(existing) || isFreeLocalRecord(record),
			}
			if err := putDeleteStateBatch(batch, record.Key, state); err != nil {
				return nil, err
			}
			events = append(events, batchCASEvent{EventRecordTombstone, record, !state.LocalOnly})
		} else {
			encoded, err := MarshalRecord(record)
			if err != nil {
				return nil, err
			}
			hash := RecordHash(record)
			if err := batch.Put(recordDBKey(record.Key), encoded); err != nil {
				return nil, err
			}
			if err := batch.Put(hashDBKey(hash), []byte(record.Key)); err != nil {
				return nil, err
			}
			if err := deleteDeleteStateBatch(batch, record.Key); err != nil {
				return nil, err
			}
			relay := prepared.retention != nil || !i.isLocalOnlyRecord(record)
			events = append(events, batchCASEvent{notifyEventType(prepared.parsed, record, existing), record, relay})
		}
		if prepared.snapshot.requiresResolve && prepared.parsed.Namespace == "name" {
			if err := batch.Delete(nameTransferDBKey(prepared.parsed.Segments[0])); err != nil {
				return nil, err
			}
		}
		touched = append(touched, record)
	}
	if err := i.markPathMetaDirtyLocked(batch, touched, height, now); err != nil {
		return nil, err
	}
	if err := batch.Flush(); err != nil {
		return nil, err
	}
	atomic.AddUint64(&i.generation, 1)
	i.resetFeeUsageLocked()
	i.resetFreeLocalUsageLocked()
	i.resetRecordExpiryLocked()
	for _, prepared := range ready {
		record := prepared.mutation.Record
		if IsTombstone(record.Flags) {
			paidRetentionCacheFor(i).remove([]string{record.Key})
		} else if prepared.retention != nil {
			paidRetentionCacheFor(i).set(record.Key, *prepared.retention)
		}
	}
	return events, nil
}

func (i *Indexer) PutLocalBatchCAS(mutations []CASMutation) (int, error) {
	for attempt := 0; attempt < 3; attempt++ {
		prep, err := i.prepareBatchCAS(mutations)
		if err != nil {
			if errors.Is(err, ErrConcurrentUpdate) {
				continue
			}
			return 0, err
		}
		i.mutex.Lock()
		height := i.currentHeight()
		now := currentUnixMilli()
		ready, err := i.batchCASReadyLocked(prep, height, now)
		if err == nil && len(ready) != 0 {
			err = i.validateBatchStateLocked(ready, height, now)
		}
		var events []batchCASEvent
		if err == nil && len(ready) != 0 {
			events, err = i.commitBatchCASLocked(ready, height, now)
		}
		i.mutex.Unlock()
		if errors.Is(err, ErrConcurrentUpdate) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, event := range events {
			i.emit(event.eventType, event.record, event.relay)
		}
		return len(ready), nil
	}
	return 0, ErrConcurrentUpdate
}

func (i *Indexer) PutLocalCAS(record *wire.DKVSRecord, precondition WritePrecondition) (bool, error) {
	applied, err := i.PutLocalBatchCAS([]CASMutation{{Record: record, Precondition: precondition}})
	return applied != 0, err
}
