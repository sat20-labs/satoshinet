package dkvs

import (
	"bytes"
	"encoding/hex"
	"errors"
	"sort"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

type preparedCASMutation struct {
	mutation       CASMutation
	parsed         ParsedKey
	snapshot       writeStateSnapshot
	forceReplace   bool
	capacity       preparedFeeCapacity
	retention      *PaidRecordRetention
	applied        bool
	pathGeneration uint64
}

type batchCASPreparation struct {
	mutations        []preparedCASMutation
	pathConditions   []PathWritePrecondition
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

func batchMutationOwner(record *wire.DKVSRecord, parsed ParsedKey) string {
	if record == nil {
		return ""
	}
	switch parsed.Namespace {
	case "personal", "blob":
		if len(parsed.Segments) > 0 {
			return "account:" + parsed.Segments[0]
		}
	case "mail":
		if len(parsed.Segments) >= 3 && parsed.Segments[1] == "msg" {
			if IsTombstone(record.Flags) {
				return "account:" + parsed.Segments[0]
			}
			return "account:" + parsed.Segments[2]
		}
		if len(parsed.Segments) > 0 {
			return "account:" + parsed.Segments[0]
		}
	case "account":
		if len(parsed.Segments) == 2 {
			accountID, err := RecordSignerAccountID(record, parsed)
			if err != nil {
				return ""
			}
			return "account:" + accountID
		}
	case "name", "svc", "sys":
		return "authority:" + hex.EncodeToString(record.PubKey)
	case "tmp":
		return "local:" + hex.EncodeToString(record.PubKey)
	}
	return ""
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
	owner := ""
	for _, mutation := range mutations {
		if mutation.Record == nil || !mutation.Precondition.Valid() {
			return ErrInvalidRecord
		}
		if _, ok := seen[mutation.Record.Key]; ok {
			return ErrInvalidRecord
		}
		seen[mutation.Record.Key] = struct{}{}
		parsed, err := ParseKey(mutation.Record.Key)
		if err != nil {
			return err
		}
		candidateOwner := batchMutationOwner(mutation.Record, parsed)
		if candidateOwner == "" {
			return ErrPermissionDenied
		}
		if owner == "" {
			owner = candidateOwner
		} else if owner != candidateOwner {
			return ErrPermissionDenied
		}
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
	return snapshot.deleteState != nil && snapshot.deleteState.effectiveHash(record.Key) == want
}

func clonePathWritePreconditions(conditions []PathWritePrecondition) []PathWritePrecondition {
	cloned := make([]PathWritePrecondition, len(conditions))
	copy(cloned, conditions)
	return cloned
}

func relayableTouchedPaths(mutations []CASMutation) (map[string]struct{}, error) {
	touched := make(map[string]struct{})
	for _, mutation := range mutations {
		if mutation.Record == nil || isFreeLocalRecord(mutation.Record) {
			continue
		}
		path, err := CollectionPathForKey(mutation.Record.Key)
		if err != nil {
			return nil, err
		}
		touched[path] = struct{}{}
	}
	return touched, nil
}

func validatePathWritePreconditions(mutations []CASMutation, conditions []PathWritePrecondition) error {
	touched, err := relayableTouchedPaths(mutations)
	if err != nil {
		return err
	}
	if len(conditions) != len(touched) {
		return ErrStaleGeneration
	}
	seen := make(map[string]struct{}, len(conditions))
	for _, condition := range conditions {
		path := stringsTrimPath(condition.Path)
		if !isCanonicalCollectionPath(path) {
			return ErrInvalidKey
		}
		if _, ok := touched[path]; !ok {
			return ErrInvalidRecord
		}
		if _, ok := seen[path]; ok {
			return ErrInvalidRecord
		}
		seen[path] = struct{}{}
	}
	return nil
}

func stringsTrimPath(path string) string {
	for len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	return path
}

func (i *Indexer) prepareBatchCAS(mutations []CASMutation, options BatchCASOptions) (batchCASPreparation, error) {
	mutations = cloneCASMutations(mutations)
	if err := validateCASMutations(mutations); err != nil {
		return batchCASPreparation{}, err
	}
	if err := validatePathWritePreconditions(mutations, options.PathPreconditions); err != nil {
		return batchCASPreparation{}, err
	}
	validators := i.snapshotValidators()
	prep := batchCASPreparation{
		mutations:        make([]preparedCASMutation, 0, len(mutations)),
		pathConditions:   clonePathWritePreconditions(options.PathPreconditions),
		height:           i.currentHeight(),
		now:              currentUnixMilli(),
		policyGeneration: validators.policyGeneration,
	}
	for _, mutation := range mutations {
		record := mutation.Record
		parsed, err := validateParsedCoreWithVerifier(record, prep.height, false, false, nil)
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
			prepared.retention, err = verifiedPaidRetentionAfterFeeVerification(record, parsed, validators.feeVerifier, prep.height)
			if err != nil {
				return batchCASPreparation{}, err
			}
		}
		prepared.forceReplace, err = validateWritePermissionWith(parsed, record, snapshot.existing, snapshot.requiresResolve, validators)
		if err != nil {
			return batchCASPreparation{}, err
		}
		if !IsTombstone(record.Flags) {
			prepared.capacity, err = i.prepareFeeCapacity(record, parsed, snapshot.existing, validators.feeVerifier, prep.height, prep.now)
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

func nextCASSequence(snapshot writeStateSnapshot) (uint64, error) {
	var current uint64
	if snapshot.existing != nil {
		current = snapshot.existing.Seq
	}
	if snapshot.deleteState != nil && snapshot.deleteState.FloorSeq > current {
		current = snapshot.deleteState.FloorSeq
	}
	if current == ^uint64(0) {
		return 0, ErrInvalidSequence
	}
	return current + 1, nil
}

func (i *Indexer) validatePathWritePreconditionsLocked(conditions []PathWritePrecondition, height, now uint64) error {
	for _, condition := range conditions {
		meta, err := i.ensurePathMetaLocked(stringsTrimPath(condition.Path), height, now)
		if err != nil {
			return err
		}
		if meta.Generation != condition.ExpectedGeneration {
			return ErrStaleGeneration
		}
		if meta.StateRoot != condition.ExpectedRoot {
			return ErrPathDiverged
		}
	}
	return nil
}

func groupedReadyByPath(ready []preparedCASMutation) map[string][]preparedCASMutation {
	groups := make(map[string][]preparedCASMutation)
	for _, prepared := range ready {
		if isFreeLocalRecord(prepared.mutation.Record) {
			continue
		}
		path := collectionPath(prepared.parsed)
		groups[path] = append(groups[path], prepared)
	}
	for path := range groups {
		sort.Slice(groups[path], func(a, b int) bool {
			return groups[path][a].mutation.Record.Key < groups[path][b].mutation.Record.Key
		})
	}
	return groups
}

func (i *Indexer) assignPathGenerationsLocked(ready []preparedCASMutation, height, now uint64) ([]preparedCASMutation, error) {
	indicesByPath := make(map[string][]int)
	for index, prepared := range ready {
		if isFreeLocalRecord(prepared.mutation.Record) {
			continue
		}
		path := collectionPath(prepared.parsed)
		indicesByPath[path] = append(indicesByPath[path], index)
	}
	for path, indices := range indicesByPath {
		meta, err := i.ensurePathMetaLocked(path, height, now)
		if err != nil {
			return nil, err
		}
		sort.Slice(indices, func(a, b int) bool {
			return ready[indices[a]].mutation.Record.Key < ready[indices[b]].mutation.Record.Key
		})
		for offset, index := range indices {
			generation := meta.Generation + uint64(offset) + 1
			if generation <= meta.Generation {
				return nil, ErrStaleGeneration
			}
			ready[index].pathGeneration = generation
		}
	}
	return ready, nil
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
	if err := i.validatePathWritePreconditionsLocked(prep.pathConditions, height, now); err != nil {
		return nil, err
	}
	ready := make([]preparedCASMutation, 0, len(prep.mutations))
	for _, prepared := range prep.mutations {
		record := prepared.mutation.Record
		existing := prepared.snapshot.existing
		if IsExpired(record, height) {
			return nil, ErrExpiredRecord
		}
		if !writePreconditionMatches(i, existing, prepared.mutation.Precondition, height, now) {
			return nil, ErrWriteConflict
		}
		nextSeq, err := nextCASSequence(prepared.snapshot)
		if err != nil || record.Seq != nextSeq {
			return nil, ErrInvalidSequence
		}
		if prepared.parsed.Namespace == "mail" && len(prepared.parsed.Segments) == 4 &&
			prepared.parsed.Segments[1] == "msg" && !IsTombstone(record.Flags) {
			if !prepared.mutation.Precondition.ExpectAbsent || prepared.snapshot.deleteState != nil {
				return nil, ErrWriteConflict
			}
		}
		if IsTombstone(record.Flags) {
			if !existingRecordActive(i, existing, height, now) || (!prepared.forceReplace && record.Seq <= existing.Seq) {
				return nil, ErrWriteConflict
			}
		} else {
			if deleteFloorBlocksRecord(prepared.parsed, prepared.snapshot.deleteState, record) {
				return nil, ErrWriteConflict
			}
			if existingRecordActive(i, existing, height, now) && !prepared.forceReplace && CompareRecords(existing, record) >= 0 {
				return nil, ErrWriteConflict
			}
		}
		ready = append(ready, prepared)
	}
	return i.assignPathGenerationsLocked(ready, height, now)
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
	if policy.MaxTotalRecords == 0 || total.records > policy.MaxTotalRecords || policy.MaxTotalBytes == 0 || total.bytes > policy.MaxTotalBytes {
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

func (i *Indexer) validateProjectedFeeLocked(ready []preparedCASMutation, records map[string]*wire.DKVSRecord, height, now uint64) error {
	limits := make(map[string]uint64)
	verifiers := make(map[string]IndexedFeeCapacityVerifier)
	for _, prepared := range ready {
		if prepared.capacity.indexed == nil || IsTombstone(prepared.mutation.Record.Flags) {
			continue
		}
		key := prepared.capacity.descriptor.UsageKey
		if key == "" {
			continue
		}
		limits[key] = prepared.capacity.descriptor.MaxRecords
		verifiers[key] = prepared.capacity.indexed
	}
	counts := make(map[string]uint64)
	for _, record := range records {
		for usageKey, verifier := range verifiers {
			candidateKey, err := verifier.FeeUsageKey(record)
			if err == nil && candidateKey == usageKey {
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
		if err := prepared.capacity.fallback.VerifyFeeCapacity(prepared.mutation.Record, prepared.parsed, prepared.snapshot.existing, projected, height, now); err != nil {
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
		if (record.TTL == 0 && !isAutopayRecord(record)) || (record.TTL != 0 && i.mailbox.MaxMsgTTL > 0 && record.TTL > i.mailbox.MaxMsgTTL) {
			return ErrInvalidRecord
		}
	case "share":
		if RecordSize(record) > i.mailbox.MaxShareSize {
			return ErrRecordTooLarge
		}
		if (record.TTL == 0 && !isAutopayRecord(record)) || (record.TTL != 0 && i.mailbox.MaxShareTTL > 0 && record.TTL > i.mailbox.MaxShareTTL) {
			return ErrInvalidRecord
		}
	default:
		return ErrInvalidKey
	}
	return nil
}

func (i *Indexer) projectPathMetasLocked(ready []preparedCASMutation, height, now uint64) (map[string]*PathMeta, error) {
	groups := groupedReadyByPath(ready)
	metas := make(map[string]*PathMeta, len(groups))
	for path, group := range groups {
		current, err := i.ensurePathMetaLocked(path, height, now)
		if err != nil {
			return nil, err
		}
		active, _, _, err := i.scanLocked(path, nil, 0, true, height, now)
		if err != nil {
			return nil, err
		}
		activeByKey := make(map[string]*wire.DKVSRecord, len(active))
		for _, record := range active {
			if i.networkPathRecordVisible(record) {
				activeByKey[record.Key] = record
			}
		}
		deletes, err := i.scanPathDeleteStatesLocked(path)
		if err != nil {
			return nil, err
		}
		for _, prepared := range group {
			record := prepared.mutation.Record
			delete(activeByKey, record.Key)
			delete(deletes, record.Key)
			if IsTombstone(record.Flags) {
				deletes[record.Key] = &deleteState{
					FloorSeq: record.Seq, PathGeneration: prepared.pathGeneration,
					PubKey: append([]byte{}, record.PubKey...), Record: record,
					EffectiveHash: RecordHash(record),
				}
			} else if prepared.retention != nil || i.networkPathRecordVisible(record) {
				activeByKey[record.Key] = record
			}
		}
		generation := current.Generation + uint64(len(group))
		if generation < current.Generation {
			return nil, ErrStaleGeneration
		}
		meta := &PathMeta{Version: pathMetaVersion, Path: path, ViewHeight: height, Generation: generation}
		activeKeys := make([]string, 0, len(activeByKey))
		for key := range activeByKey {
			activeKeys = append(activeKeys, key)
		}
		sort.Strings(activeKeys)
		for _, key := range activeKeys {
			record := activeByKey[key]
			meta.ActiveRecords++
			meta.ActiveTotalSize += uint64(RecordSize(record))
			xorPathMetaRoot(&meta.StateRoot, record)
			updateMinExpiry(meta, record)
		}
		deleteKeys := make([]string, 0, len(deletes))
		for key := range deletes {
			deleteKeys = append(deleteKeys, key)
		}
		sort.Strings(deleteKeys)
		for _, key := range deleteKeys {
			state := deletes[key]
			xorDeleteFloorRoot(&meta.StateRoot, key, state)
			if state.PathGeneration > meta.Generation {
				meta.Generation = state.PathGeneration
			}
		}
		normalizePathMetaAliases(meta)
		metas[path] = meta
	}
	return metas, nil
}

func (i *Indexer) commitBatchCASLocked(ready []preparedCASMutation, height, now uint64) ([]batchCASEvent, map[string]*PathMeta, error) {
	metas, err := i.projectPathMetasLocked(ready, height, now)
	if err != nil {
		return nil, nil, err
	}
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	events := make([]batchCASEvent, 0, len(ready))
	for _, prepared := range ready {
		record := prepared.mutation.Record
		existing := prepared.snapshot.existing
		if existing != nil {
			if err := batch.Delete(hashDBKey(RecordHash(existing))); err != nil {
				return nil, nil, err
			}
		}
		if IsTombstone(record.Flags) {
			if err := batch.Delete(recordDBKey(record.Key)); err != nil {
				return nil, nil, err
			}
			state := &deleteState{
				FloorSeq: record.Seq, PathGeneration: prepared.pathGeneration,
				RelayUntil: deleteRelayUntil(now), PubKey: append([]byte(nil), record.PubKey...),
				Record: record, EffectiveHash: RecordHash(record),
				LocalOnly: isFreeLocalRecord(existing) || isFreeLocalRecord(record),
			}
			if err := putDeleteStateBatch(batch, record.Key, state); err != nil {
				return nil, nil, err
			}
			events = append(events, batchCASEvent{EventRecordTombstone, record, !state.LocalOnly})
		} else {
			encoded, err := MarshalRecord(record)
			if err != nil {
				return nil, nil, err
			}
			hash := RecordHash(record)
			if err := batch.Put(recordDBKey(record.Key), encoded); err != nil {
				return nil, nil, err
			}
			if err := batch.Put(hashDBKey(hash), []byte(record.Key)); err != nil {
				return nil, nil, err
			}
			if err := deleteDeleteStateBatch(batch, record.Key); err != nil {
				return nil, nil, err
			}
			relay := prepared.retention != nil || !i.isLocalOnlyRecord(record)
			events = append(events, batchCASEvent{notifyEventType(prepared.parsed, record, existing), record, relay})
		}
		if prepared.snapshot.requiresResolve && prepared.parsed.Namespace == "name" {
			if err := batch.Delete(nameTransferDBKey(prepared.parsed.Segments[0])); err != nil {
				return nil, nil, err
			}
		}
	}
	for path, meta := range metas {
		if err := putPathMetaBatch(batch, meta); err != nil {
			return nil, nil, err
		}
		if err := putPathStatusBatch(batch, &PathLocalStatus{Path: path, UpdatedAt: now}); err != nil {
			return nil, nil, err
		}
	}
	if err := batch.Flush(); err != nil {
		return nil, nil, err
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
	return events, metas, nil
}

func (i *Indexer) PutLocalBatchCASResultWithOptions(mutations []CASMutation, options BatchCASOptions) (*WriteResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		prep, err := i.prepareBatchCAS(mutations, options)
		if err != nil {
			if errors.Is(err, ErrConcurrentUpdate) {
				continue
			}
			return nil, err
		}
		i.mutex.Lock()
		height := i.currentHeight()
		now := currentUnixMilli()
		ready, err := i.batchCASReadyLocked(prep, height, now)
		if err == nil && len(ready) != 0 {
			err = i.validateBatchStateLocked(ready, height, now)
		}
		var events []batchCASEvent
		metas := make(map[string]*PathMeta)
		if err == nil && len(ready) != 0 {
			events, metas, err = i.commitBatchCASLocked(ready, height, now)
		}
		i.mutex.Unlock()
		if errors.Is(err, ErrConcurrentUpdate) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			i.emit(event.eventType, event.record, event.relay)
		}
		result := &WriteResult{Applied: len(ready), PathMeta: metas, ServerTimeMS: now}
		result.Records = make([]*wire.DKVSRecord, 0, len(prep.mutations))
		result.Hashes = make([]string, 0, len(prep.mutations))
		for _, prepared := range prep.mutations {
			record := cloneRecord(prepared.mutation.Record)
			result.Records = append(result.Records, record)
			result.Hashes = append(result.Hashes, RecordHash(record).String())
			if isFreeLocalRecord(record) {
				result.LocalOnly = true
			}
		}
		if result.LocalOnly {
			result.EndpointID = i.endpointID()
		}
		return result, nil
	}
	return nil, ErrConcurrentUpdate
}

func (i *Indexer) PutLocalBatchCASWithOptions(mutations []CASMutation, options BatchCASOptions) (int, error) {
	result, err := i.PutLocalBatchCASResultWithOptions(mutations, options)
	if err != nil {
		return 0, err
	}
	return result.Applied, nil
}

func (i *Indexer) PutLocalBatchCAS(mutations []CASMutation) (int, error) {
	return i.PutLocalBatchCASWithOptions(mutations, BatchCASOptions{})
}

func (i *Indexer) PutLocalCAS(record *wire.DKVSRecord, precondition WritePrecondition) (bool, error) {
	result, err := i.PutLocalBatchCASResultWithOptions([]CASMutation{{Record: record, Precondition: precondition}}, BatchCASOptions{})
	return err == nil && result.Applied != 0, err
}

func hashBytesEqual(a, b chainhash.Hash) bool { return bytes.Equal(a[:], b[:]) }
