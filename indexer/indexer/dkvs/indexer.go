package dkvs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

var (
	recordKeyPrefix       = []byte("dkvs:record:")
	hashKeyPrefix         = []byte("dkvs:hash:")
	nameTransferKeyPrefix = []byte("dkvs:name-transfer:")
)

type Indexer struct {
	db                        indexercommon.KVDB
	resolver                  DIDResolver
	feeVerifier               FeeVerifier
	feeUsageInitialized       bool
	feeUsageCounts            map[string]uint64
	feeUsageEntries           map[string]feeUsageEntry
	feeExpiryHeights          feeExpiryHeap
	freeLocal                 FreeLocalCachePolicy
	freeLocalUsageInitialized bool
	freeLocalUsageBySigner    map[string]freeLocalUsage
	freeLocalUsageEntries     map[string]freeLocalUsageEntry
	freeLocalTotal            freeLocalUsage
	recordExpiryInitialized   bool
	recordExpiryEntries       map[string]recordExpiryEntry
	recordExpiryHeights       feeExpiryHeap
	system                    SystemVerifier
	mailbox                   MailboxPolicy
	blob                      BlobPolicy
	tmp                       TmpPolicy
	endpointIdentity          string
	subs                      *subscriptionSet
	notify                    NotifyFunc
	subNotify                 SubscriptionNotifyFunc
	height                    func() uint64
	mutex                     sync.RWMutex
	watchMutex                sync.Mutex
	pathSignals               map[string]*pathWatchSignal
	generation                uint64
	policyGeneration          uint64
	checkpointMutex           sync.Mutex
	checkpointGeneration      uint64
	checkpointHeight          uint64
	checkpointCache           *Checkpoint
}

func New(db indexercommon.KVDB, cfg Config) *Indexer {
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = defaultResolver{}
	}
	freeLocal := normalizeFreeLocalCachePolicy(cfg.FreeLocalCache, cfg.AllowFreeLocal)
	feeVerifier := cfg.FeeVerifier
	if feeVerifier == nil {
		feeVerifier = defaultFeeVerifier{
			allowFreeLocal:     cfg.AllowFreeLocal,
			allowEmptyFeeProof: !freeLocal.Enabled,
		}
	}
	systemVerifier := cfg.SystemVerifier
	if systemVerifier == nil {
		systemVerifier = defaultSystemVerifier{}
	}
	indexer := &Indexer{
		db:               db,
		resolver:         resolver,
		feeVerifier:      feeVerifier,
		system:           systemVerifier,
		mailbox:          normalizeMailboxPolicy(cfg.MailboxPolicy),
		blob:             normalizeBlobPolicy(cfg.BlobPolicy),
		tmp:              normalizeTmpPolicy(cfg.TmpPolicy),
		endpointIdentity: strings.TrimSpace(cfg.EndpointID),
		freeLocal:        freeLocal,
		subs:             newSubscriptionSet(),
		notify:           cfg.Notify,
		subNotify:        cfg.Subscription,
		height:           cfg.CurrentHeight,
		pathSignals:      make(map[string]*pathWatchSignal),
	}
	indexer.resetFeeUsageLocked()
	indexer.resetFreeLocalUsageLocked()
	indexer.resetRecordExpiryLocked()
	return indexer
}

func (i *Indexer) SetNotify(fn NotifyFunc) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.notify = fn
}

func (i *Indexer) SetSubscriptionNotify(fn SubscriptionNotifyFunc) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.subNotify = fn
}

func (i *Indexer) SetResolver(resolver DIDResolver) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if resolver == nil {
		resolver = defaultResolver{}
	}
	i.resolver = resolver
	atomic.AddUint64(&i.policyGeneration, 1)
}

func (i *Indexer) SetFeeVerifier(verifier FeeVerifier) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if verifier == nil {
		verifier = defaultFeeVerifier{}
	}
	i.feeVerifier = verifier
	i.resetFeeUsageLocked()
	atomic.AddUint64(&i.policyGeneration, 1)
}

func (i *Indexer) SetSystemVerifier(verifier SystemVerifier) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if verifier == nil {
		verifier = defaultSystemVerifier{}
	}
	i.system = verifier
	atomic.AddUint64(&i.policyGeneration, 1)
}

func (i *Indexer) PutLocal(record *wire.DKVSRecord) (bool, error) {
	updated, _, err := i.PutLocalWithHash(record)
	return updated, err
}

func (i *Indexer) PutLocalWithHash(record *wire.DKVSRecord) (bool, chainhash.Hash, error) {
	record = cloneRecord(record)
	updated, eventType, hash, relay, err := i.put(record, false)
	if err != nil {
		return false, chainhash.Hash{}, err
	}
	if updated {
		i.emit(eventType, record, relay)
	}
	return updated, hash, nil
}

func (i *Indexer) PutRemote(record *wire.DKVSRecord) (bool, error) {
	record = cloneRecord(record)
	updated, _, _, _, err := i.put(record, true)
	if err == nil && updated {
		i.notifyPathMutation(record)
	}
	return updated, err
}

func (i *Indexer) NotifyNameTransfers(names []string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	changed := false
	for _, name := range names {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" {
			continue
		}
		name = NormalizeNameID(name)
		if len(name) > MaxKeySegmentSize || !validSegment(name) {
			return ErrInvalidKey
		}
		if err := batch.Put(nameTransferDBKey(name), []byte{1}); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	atomic.AddUint64(&i.generation, 1)
	return nil
}

func (i *Indexer) Get(key string) (*wire.DKVSRecord, error) {
	if _, err := ParseKey(key); err != nil {
		return nil, err
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	record, err := i.getRaw(key)
	if err != nil {
		return nil, err
	}
	if err := i.activeError(record, i.currentHeight(), currentUnixMilli()); err != nil {
		return nil, ErrRecordNotFound
	}
	return record, nil
}

func (i *Indexer) GetByHash(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	keyBytes, err := i.db.Read(hashDBKey(hash))
	if err != nil {
		return nil, ErrRecordNotFound
	}
	record, err := i.getRaw(string(keyBytes))
	if err != nil {
		return nil, err
	}
	if RecordHash(record) != hash {
		return nil, ErrRecordNotFound
	}
	if err := i.activeError(record, i.currentHeight(), currentUnixMilli()); err != nil {
		return nil, ErrRecordNotFound
	}
	return record, nil
}

// GetByHashForRelay never exposes node-local free-cache records to a peer.
func (i *Indexer) GetByHashForRelay(hash chainhash.Hash) (*wire.DKVSRecord, error) {
	record, err := i.GetByHash(hash)
	if err != nil {
		return nil, err
	}
	if i.isLocalOnlyRecord(record) {
		return nil, ErrRecordNotFound
	}
	return record, nil
}

func (i *Indexer) ListPrefix(prefix string, start, limit int) ([]*wire.DKVSRecord, int, error) {
	if len(prefix) == 0 || prefix[0] != '/' {
		return nil, 0, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return nil, 0, err
	}
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = 100
	}
	prefix = strings.TrimSuffix(prefix, "/")
	height := i.currentHeight()
	now := currentUnixMilli()
	// PathMeta is the network-relayable view and deliberately excludes
	// endpoint-local FREE_LOCAL records. Application listing includes those
	// records, so a PathMeta count must never be used as an early-return hint.
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.listPrefixLocked(prefix, start, limit, -1, height, now)
}

func (i *Indexer) ClientConfig() ClientConfig {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return ClientConfig{
		FreeLocal:              i.freeLocal,
		Blob:                   i.blob,
		MaxBatchMutations:      MaxBatchCASMutations,
		MaxBatchBytes:          MaxBatchCASTotalSize,
		MaxPrefixesPerTerminal: MaxPrefixesPerTerminal,
		EndpointID:             i.endpointID(),
	}
}

func (i *Indexer) Usage(prefix string) (*Usage, error) {
	if len(prefix) == 0 || prefix[0] != '/' {
		return nil, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return nil, err
	}
	prefix = strings.TrimSuffix(prefix, "/")
	if collectionPathForPrefix(prefix) != "" {
		meta, err := i.GetPathMeta(prefix)
		if err != nil {
			return nil, err
		}
		return &Usage{
			Prefix:          prefix,
			ActiveRecords:   meta.ActiveRecords,
			ActiveTotalSize: meta.ActiveTotalSize,
		}, nil
	}
	records, _, _, err := i.scan(prefix, nil, 0, true)
	if err != nil {
		return nil, err
	}
	usage := &Usage{Prefix: prefix}
	for _, record := range records {
		usage.ActiveRecords++
		usage.ActiveTotalSize += uint64(RecordSize(record))
	}
	return usage, nil
}

func (i *Indexer) Sync(cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	ranges, err := syncRangesForFilters(nil)
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	return i.syncRanges(cursor, limit, ranges, true, false)
}

func (i *Indexer) SyncFiltered(cursor []byte, limit uint32, filters []Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if len(filters) == 0 {
		return i.Sync(cursor, limit)
	}
	ranges, err := syncRangesForFilters(filters)
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	return i.syncRanges(cursor, limit, ranges, true, false)
}

// SyncFilteredForClient includes node-local temporary records. It is intended
// for the wallet connected to this HTTP service and must never be used for P2P
// relay or mirror synchronization.
func (i *Indexer) SyncFilteredForClient(cursor []byte, limit uint32,
	filters []Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {

	if len(filters) == 0 {
		return nil, nil, false, chainhash.Hash{}, ErrInvalidRecord
	}
	ranges, err := syncRangesForFilters(filters)
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	return i.syncRanges(cursor, limit, ranges, false, true)
}

// SyncDirectory is the application-facing RPC view for one DKVS directory.
// It includes node-local FREE_LOCAL records and retained signed tombstones.
func (i *Indexer) SyncDirectory(prefix string, cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return nil, nil, false, chainhash.Hash{}, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	ranges := []syncRange{{target: prefix}}
	return i.syncRanges(cursor, limit, ranges, false, true)
}

func (i *Indexer) WaitDirectory(ctx context.Context, prefix string, knownRoot chainhash.Hash) (chainhash.Hash, bool, error) {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return chainhash.Hash{}, false, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return chainhash.Hash{}, false, err
	}
	return i.WaitFilteredForClient(ctx, []Subscription{{Type: SubscriptionPrefix, Target: prefix}}, knownRoot)
}

func (i *Indexer) Subscribe(sub Subscription) ([]*wire.DKVSRecord, int, error) {
	normalized, err := validateSubscription(sub)
	if err != nil {
		return nil, 0, err
	}
	added, err := i.subs.add(normalized, wire.MaxDKVSSyncFilters)
	if err != nil {
		return nil, 0, err
	}
	records, total, err := i.recordsForSubscription(normalized)
	if err != nil {
		return nil, 0, err
	}
	if added {
		i.emitSubscription(normalized)
	}
	return records, total, nil
}

func (i *Indexer) Unsubscribe(sub Subscription) error {
	normalized, err := validateSubscription(sub)
	if err != nil {
		return err
	}
	i.subs.remove(normalized)
	return nil
}

func (i *Indexer) Subscriptions() []Subscription {
	return i.subs.list()
}

func (i *Indexer) IsSubscribed(key string) bool {
	if _, err := ParseKey(key); err != nil {
		return false
	}
	return i.subs.matchKey(key)
}

func (i *Indexer) recordsForSubscription(sub Subscription) ([]*wire.DKVSRecord, int, error) {
	switch sub.Type {
	case SubscriptionKey:
		record, err := i.Get(sub.Target)
		if err != nil {
			if errors.Is(err, ErrRecordNotFound) {
				return nil, 0, nil
			}
			return nil, 0, err
		}
		return []*wire.DKVSRecord{record}, 1, nil
	case SubscriptionPrefix, SubscriptionMailbox, SubscriptionService:
		return i.ListPrefix(sub.Target, 0, int(wire.MaxDKVSRecordsPerMsg))
	default:
		return nil, 0, ErrInvalidRecord
	}
}

func (i *Indexer) Checkpoint() (*Checkpoint, error) {
	height := i.currentHeight()
	generation := atomic.LoadUint64(&i.generation)
	i.checkpointMutex.Lock()
	if i.checkpointCache != nil && i.checkpointGeneration == generation && i.checkpointHeight == height {
		checkpoint := cloneCheckpoint(i.checkpointCache)
		i.checkpointMutex.Unlock()
		return checkpoint, nil
	}
	i.checkpointMutex.Unlock()
	records, _, _, err := i.scan("", nil, 0, true)
	if err != nil {
		return nil, err
	}
	records = i.relayableRecords(records)
	checkpoint, err := checkpointFromRecords(records, height)
	if err != nil {
		return nil, err
	}
	if atomic.LoadUint64(&i.generation) == generation {
		i.checkpointMutex.Lock()
		i.checkpointGeneration = generation
		i.checkpointHeight = height
		i.checkpointCache = cloneCheckpoint(checkpoint)
		i.checkpointMutex.Unlock()
	}
	return checkpoint, nil
}

func (i *Indexer) Snapshot() (*Snapshot, error) {
	records, _, _, err := i.scan("", nil, 0, true)
	if err != nil {
		return nil, err
	}
	records = i.relayableRecords(records)
	checkpoint, err := checkpointFromRecords(records, i.currentHeight())
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		Checkpoint: checkpoint,
		Records:    records,
		CreatedAt:  currentUnixMilli(),
	}, nil
}

func (i *Indexer) ApplySnapshot(snapshot *Snapshot) (int, error) {
	if err := ValidateSnapshot(snapshot); err != nil {
		return 0, err
	}
	return i.applyRecordSetAtomic(snapshot.Records, nil, nil, false, false)
}

func (i *Indexer) PruneExpired() (int, error) {
	return i.PruneExpiredAt(i.currentHeight())
}

func (i *Indexer) PruneExpiredAt(height uint64) (int, error) {
	now := currentUnixMilli()
	i.mutex.Lock()
	if err := i.ensureRecordExpiryLocked(height, now); err != nil {
		i.mutex.Unlock()
		return 0, err
	}
	expiredRecords := make([]*wire.DKVSRecord, 0)
	batch := i.db.NewWriteBatch()
	defer batch.Close()
	for _, key := range i.expiredRecordKeysLocked(height) {
		record, err := i.getRaw(key)
		if errors.Is(err, ErrRecordNotFound) {
			continue
		}
		if err != nil {
			i.mutex.Unlock()
			return len(expiredRecords), err
		}
		if !IsExpired(record, height) {
			i.addRecordExpiryLocked(record)
			continue
		}
		if IsTombstone(record.Flags) {
			continue
		}
		expiredRecords = append(expiredRecords, record)
	}
	compactedDeletes, err := i.compactExpiredDeleteCommandsLocked(batch, now)
	if err != nil {
		i.mutex.Unlock()
		return len(expiredRecords), err
	}
	var expiryResult expiryCommitResult
	if len(expiredRecords) != 0 {
		expiryResult, err = i.stageExpiredRecordsLocked(batch, expiredRecords, height, now)
		if err != nil {
			i.mutex.Unlock()
			return 0, err
		}
	}
	if len(expiredRecords) != 0 || compactedDeletes > 0 {
		if err := batch.Flush(); err != nil {
			i.mutex.Unlock()
			return 0, err
		}
		atomic.AddUint64(&i.generation, 1)
	}
	removedKeys := make([]string, 0, len(expiredRecords))
	for _, record := range expiredRecords {
		removedKeys = append(removedKeys, record.Key)
		if i.freeLocalUsageInitialized {
			i.removeFreeLocalUsageLocked(record.Key)
		}
		if i.feeUsageInitialized {
			i.removeFeeUsageLocked(record.Key)
		}
		if i.recordExpiryInitialized {
			delete(i.recordExpiryEntries, record.Key)
		}
	}
	i.mutex.Unlock()
	if len(expiredRecords) != 0 {
		i.notifyExpiryCommit(expiryResult)
		paidRetentionCacheFor(i).remove(removedKeys)
	}
	return len(expiredRecords), nil
}

func recordHasPaidFeeProof(record *wire.DKVSRecord) bool {
	if record == nil || len(record.FeeProof) == 0 {
		return false
	}
	proof, err := ParseFeeProof(record.FeeProof)
	return err == nil && proof.Mode != FeeModeFreeLocal
}

func checkpointFromRecords(records []*wire.DKVSRecord, height uint64) (*Checkpoint, error) {
	namespaceHashes := make(map[string][][]byte)
	cp := &Checkpoint{
		Height:         height,
		NamespaceRoots: make(map[string]string),
	}
	allHashes := make([][]byte, 0, len(records))
	for _, record := range records {
		parsed, err := ParseKey(record.Key)
		if err != nil {
			continue
		}
		hash := RecordHash(record)
		hcopy := append([]byte{}, hash[:]...)
		namespaceHashes[parsed.Namespace] = append(namespaceHashes[parsed.Namespace], hcopy)
		allHashes = append(allHashes, hcopy)
		cp.ActiveRecordCount++
		cp.ActiveRecordTotalSize += uint64(RecordSize(record))
	}
	for ns, hashes := range namespaceHashes {
		cp.NamespaceRoots[ns] = hex.EncodeToString(merkleLikeRoot(hashes))
	}
	cp.ActiveRecordRoot = hex.EncodeToString(merkleLikeRoot(allHashes))
	return cp, nil
}

func (i *Indexer) put(record *wire.DKVSRecord, remote bool) (bool, uint8, chainhash.Hash, bool, error) {
	height := i.currentHeight()
	now := currentUnixMilli()
	for attempt := 0; attempt < 3; attempt++ {
		validators := i.snapshotValidators()
		parsed, err := validateParsedCoreWithVerifier(record, height, remote, false, nil)
		if err != nil {
			return false, 0, chainhash.Hash{}, false, err
		}
		snapshot, err := i.readWriteStateSnapshot(record.Key, parsed, validators)
		if err != nil {
			return false, 0, chainhash.Hash{}, false, err
		}
		if isFreeLocalRecord(record) &&
			((snapshot.existing != nil && !isFreeLocalRecord(snapshot.existing)) ||
				(snapshot.deleteState != nil && !snapshot.deleteState.LocalOnly)) {
			return false, 0, chainhash.Hash{}, false, ErrStorageModeDowngrade
		}

		// Delete commands free capacity and are authorized by the current key
		// owner, so they never require a storage fee proof.
		var verifiedRetention *PaidRecordRetention
		if !IsTombstone(record.Flags) {
			if err := verifyFeeProofWith(validators.feeVerifier, record, parsed); err != nil {
				return false, 0, chainhash.Hash{}, false, err
			}
			verifiedRetention, err = verifiedPaidRetentionAfterFeeVerification(
				record, parsed, validators.feeVerifier, height,
			)
			if err != nil {
				return false, 0, chainhash.Hash{}, false, err
			}
		}
		if remote && isFreeLocalRecord(record) {
			return false, 0, chainhash.Hash{}, false, ErrFreeLocalNotRelayable
		}
		forceReplace, err := validateWritePermissionWith(
			parsed, record, snapshot.existing, snapshot.requiresResolve, validators,
		)
		if err != nil {
			return false, 0, chainhash.Hash{}, false, err
		}
		if !IsTombstone(record.Flags) && deleteFloorBlocksRecord(parsed, snapshot.deleteState, record) {
			return false, 0, chainhash.Hash{}, false, nil
		}
		var preparedCapacity preparedFeeCapacity
		if !IsTombstone(record.Flags) {
			preparedCapacity, err = i.prepareFeeCapacity(
				record, parsed, snapshot.existing, validators.feeVerifier, height, now,
			)
			if err != nil {
				if errors.Is(err, ErrConcurrentUpdate) {
					continue
				}
				return false, 0, chainhash.Hash{}, false, err
			}
		}

		i.mutex.Lock()
		current, err := i.writeStateStillCurrentLocked(record.Key, parsed, snapshot)
		if err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, err
		}
		if !current {
			i.mutex.Unlock()
			continue
		}
		existing := snapshot.existing
		deleteState := snapshot.deleteState
		clearNameTransfer := snapshot.requiresResolve && parsed.Namespace == "name"
		if remote && IsTombstone(record.Flags) &&
			((existing != nil && (isEndpointCacheRecord(existing) || pathMode(parsed) == PathLocalOnly)) ||
				(deleteState != nil && deleteState.LocalOnly) ||
				isEndpointCacheRecord(record) ||
				parsed.Namespace == "mail" || pathMode(parsed) == PathLocalOnly) {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, ErrFreeLocalNotRelayable
		}

		if IsTombstone(record.Flags) {
			if existing == nil {
				// AccountBound mail and PathLocalOnly keys never create a durable
				// tombstone. A repeated owner delete is simply already applied.
				if isEndpointCacheRecord(record) || (deleteState != nil && deleteState.LocalOnly) ||
					parsed.Namespace == "mail" || pathMode(parsed) == PathLocalOnly {
					i.mutex.Unlock()
					return false, 0, RecordHash(record), false, nil
				}
				if deleteState != nil && deleteState.Record != nil &&
					CompareRecords(deleteState.Record, record) >= 0 {
					i.mutex.Unlock()
					return false, 0, RecordHash(deleteState.Record), !deleteState.LocalOnly, nil
				}
				hash, err := i.retainDeleteCommandLocked(record, now, deleteState != nil && deleteState.LocalOnly)
				i.mutex.Unlock()
				if err != nil {
					return false, 0, chainhash.Hash{}, false, err
				}
				return true, EventRecordTombstone, hash, deleteState == nil || !deleteState.LocalOnly, nil
			}
			// For the same authority a delete must advance the sequence; an
			// authorized owner rotation can replace an older owner's record with a
			// lower local sequence.
			if !forceReplace && record.Seq <= existing.Seq {
				if clearNameTransfer {
					if err := i.clearNameTransferDirty(parsed.Segments[0]); err != nil {
						i.mutex.Unlock()
						return false, 0, chainhash.Hash{}, false, err
					}
				}
				i.mutex.Unlock()
				return false, 0, RecordHash(existing), !i.isLocalOnlyRecord(existing), nil
			}
			floorSeq := record.Seq
			hash, err := i.commitDeleteLocked(
				parsed, existing, record, floorSeq, true, clearNameTransfer, height, now,
			)
			if err == nil {
				atomic.AddUint64(&i.generation, 1)
			}
			i.mutex.Unlock()
			if err != nil {
				return false, 0, chainhash.Hash{}, false, err
			}
			return true, EventRecordTombstone, hash,
				!(isEndpointCacheRecord(existing) || pathMode(parsed) == PathLocalOnly), nil
		}

		if deleteFloorBlocksRecord(parsed, deleteState, record) {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, nil
		}
		if err := i.validateStatefulLocked(record, parsed, existing, height, now); err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, err
		}
		if existing != nil && !forceReplace && i.activeError(existing, height, now) == nil && CompareRecords(existing, record) >= 0 {
			if verifiedRetention != nil && RecordHash(existing) == RecordHash(record) {
				paidRetentionCacheFor(i).set(record.Key, *verifiedRetention)
			}
			if clearNameTransfer {
				if err := i.clearNameTransferDirty(parsed.Segments[0]); err != nil {
					i.mutex.Unlock()
					return false, 0, chainhash.Hash{}, false, err
				}
			}
			i.mutex.Unlock()
			return false, 0, RecordHash(existing), !i.isLocalOnlyRecord(existing), nil
		}
		if err := i.validatePreparedFeeCapacityLocked(record, preparedCapacity, height, now); err != nil {
			i.mutex.Unlock()
			if errors.Is(err, ErrConcurrentUpdate) {
				continue
			}
			return false, 0, chainhash.Hash{}, false, err
		}
		if err := i.validateFreeLocalCapacityLocked(record, parsed, height, now); err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, err
		}
		meta, err := i.pathMetaForMutationLocked(parsed, existing, record, verifiedRetention != nil || i.networkPathRecordVisible(record), height, now)
		if err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, err
		}
		data, err := MarshalRecord(record)
		if err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, err
		}
		hash := RecordHash(record)
		batch := i.db.NewWriteBatch()
		err = batch.Put(recordDBKey(record.Key), data)
		if err == nil && existing != nil {
			existingHash := RecordHash(existing)
			if existingHash != hash {
				err = batch.Delete(hashDBKey(existingHash))
			}
		}
		if err == nil {
			err = batch.Put(hashDBKey(hash), []byte(record.Key))
		}
		if err == nil && deleteState != nil {
			err = deleteDeleteStateBatch(batch, record.Key)
		}
		if err == nil && clearNameTransfer {
			err = batch.Delete(nameTransferDBKey(parsed.Segments[0]))
		}
		if err == nil {
			err = putPathMetaBatch(batch, meta)
		}
		if err == nil {
			err = batch.Flush()
		}
		batch.Close()
		if err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, err
		}
		atomic.AddUint64(&i.generation, 1)
		if preparedCapacity.indexed != nil && i.feeUsageInitialized {
			i.replaceFeeUsageLocked(preparedCapacity.indexed, record)
		}
		if err := i.replaceFreeLocalUsageLocked(record, parsed); err != nil {
			i.mutex.Unlock()
			return false, 0, chainhash.Hash{}, false, err
		}
		if i.recordExpiryInitialized {
			i.replaceRecordExpiryLocked(record)
		}
		if verifiedRetention != nil {
			paidRetentionCacheFor(i).set(record.Key, *verifiedRetention)
		}
		eventType := notifyEventType(parsed, record, existing)
		relay := verifiedRetention != nil || !i.isLocalOnlyRecord(record)
		i.mutex.Unlock()
		return true, eventType, hash, relay, nil
	}
	return false, 0, chainhash.Hash{}, false, ErrConcurrentUpdate
}

func (i *Indexer) validate(record *wire.DKVSRecord) error {
	_, err := i.validateParsed(record, i.currentHeight(), currentUnixMilli())
	return err
}

func (i *Indexer) validateAt(record *wire.DKVSRecord, height uint64) error {
	parsed, err := i.validateParsedCore(record, height, true, false)
	if err != nil {
		return err
	}
	return i.validateStoredPermission(parsed, record)
}

func (i *Indexer) validateParsed(record *wire.DKVSRecord, height, now uint64) (ParsedKey, error) {
	parsed, err := i.validateParsedBasic(record, height, now)
	if err != nil {
		return parsed, err
	}
	if _, err := i.validateWritePermission(parsed, record, nil); err != nil {
		return parsed, err
	}
	return parsed, nil
}

func (i *Indexer) validateParsedBasic(record *wire.DKVSRecord, height, _ uint64) (ParsedKey, error) {
	return i.validateParsedCore(record, height, false, true)
}

func (i *Indexer) validateParsedCore(record *wire.DKVSRecord, height uint64, allowExpiredTombstone, verifyFee bool) (ParsedKey, error) {
	var verifier FeeVerifier
	if verifyFee {
		verifier = i.snapshotValidators().feeVerifier
	}
	return validateParsedCoreWithVerifier(record, height, allowExpiredTombstone, verifyFee, verifier)
}

func (i *Indexer) verifyFeeProof(record *wire.DKVSRecord, parsed ParsedKey) error {
	return verifyFeeProofWith(i.snapshotValidators().feeVerifier, record, parsed)
}

func (i *Indexer) activeError(record *wire.DKVSRecord, height, _ uint64) error {
	if record == nil || IsTombstone(record.Flags) {
		return ErrRecordNotFound
	}
	return i.validateAt(record, height)
}

func (i *Indexer) validatePermission(parsed ParsedKey, pubKey []byte) error {
	validators := i.snapshotValidators()
	return validatePermissionWith(parsed, pubKey, validators.resolver, validators.system)
}

func (i *Indexer) validateWritePermission(parsed ParsedKey, record, existing *wire.DKVSRecord) (bool, error) {
	requiresResolve, err := i.requiresNameResolve(parsed)
	if err != nil {
		return false, err
	}
	return validateWritePermissionWith(parsed, record, existing, requiresResolve, i.snapshotValidators())
}

func (i *Indexer) resolveIdentity(parsed ParsedKey) (DIDIdentity, error) {
	return resolveIdentityWith(parsed, i.snapshotValidators().resolver)
}

func validateResolvedIdentity(parsed ParsedKey, identity DIDIdentity) error {
	if len(parsed.Segments) == 0 || identity.NameID == "" || identity.NameID != parsed.Segments[0] {
		return ErrPermissionDenied
	}
	return nil
}

func (i *Indexer) validateMailWritePermission(parsed ParsedKey, record, existing *wire.DKVSRecord) error {
	validators := i.snapshotValidators()
	return validateMailWritePermissionWith(parsed, record, existing, validators.resolver, validators.system)
}

func (i *Indexer) requiresNameResolve(parsed ParsedKey) (bool, error) {
	if parsed.Namespace != "name" || len(parsed.Segments) != 1 {
		return false, nil
	}
	return i.nameTransferDirty(parsed.Segments[0])
}

func (i *Indexer) nameTransferDirty(name string) (bool, error) {
	_, err := i.db.Read(nameTransferDBKey(name))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return false, nil
	}
	return false, err
}

func (i *Indexer) clearNameTransferDirty(name string) error {
	dirty, err := i.nameTransferDirty(name)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	return i.db.Delete(nameTransferDBKey(name))
}

func (i *Indexer) validateStoredPermission(parsed ParsedKey, record *wire.DKVSRecord) error {
	if record == nil {
		return ErrInvalidRecord
	}
	if isAccountScopedNamespace(parsed.Namespace) {
		return ValidateRecordIdentity(record, parsed)
	}
	// External authority checks are performed when a record is accepted. Reads,
	// scans, and path metadata rebuilds must remain local and must not call HTTP
	// or RPC services while holding the indexer lock.
	switch parsed.Namespace {
	case "name", "svc", "sys", "tmp":
		return nil
	case "personal":
		if len(parsed.Segments) < 2 || parsed.Segments[0] != AccountID(record.PubKey) {
			return ErrPermissionDenied
		}
	case "mail":
		if len(parsed.Segments) < 2 {
			return ErrInvalidKey
		}
		switch parsed.Segments[1] {
		case "msg":
			if len(parsed.Segments) != 4 || parsed.Segments[2] != AccountID(record.PubKey) {
				return ErrPermissionDenied
			}
		case "share":
			if parsed.Segments[0] != AccountID(record.PubKey) {
				return ErrPermissionDenied
			}
		default:
			return ErrInvalidKey
		}
	case "blob":
		if len(parsed.Segments) < 3 || parsed.Segments[0] != AccountID(record.PubKey) {
			return ErrPermissionDenied
		}
	}
	return nil
}

func (i *Indexer) validateStatefulLocked(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, height, now uint64) error {
	switch parsed.Namespace {
	case "mail":
		return i.validateMailboxLocked(record, parsed, existing, height, now)
	case "blob":
		return i.validateBlobLocked(record, parsed, height, now)
	case "tmp":
		return i.validateTmp(record)
	default:
		return nil
	}
}

func (i *Indexer) validateTmp(record *wire.DKVSRecord) error {
	if record.TTL == 0 || record.TTL > i.tmp.MaxTTL {
		return ErrInvalidRecord
	}
	if len(record.Value) > i.tmp.MaxSize {
		return ErrRecordTooLarge
	}
	return nil
}

func (i *Indexer) getRaw(key string) (*wire.DKVSRecord, error) {
	data, err := i.db.Read(recordDBKey(key))
	if err != nil {
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, ErrRecordNotFound
		}
		return nil, err
	}
	return UnmarshalRecord(data)
}

func (i *Indexer) scan(prefix string, cursor []byte, limit int, activeOnly bool) ([]*wire.DKVSRecord, []byte, bool, error) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.scanLocked(prefix, cursor, limit, activeOnly, i.currentHeight(), currentUnixMilli())
}

func (i *Indexer) scanFiltered(cursor []byte, limit int, match func(*wire.DKVSRecord) bool) ([]*wire.DKVSRecord, []byte, bool, error) {
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.scanFilteredLocked(cursor, limit, i.currentHeight(), currentUnixMilli(), match)
}

func (i *Indexer) scanLocked(prefix string, cursor []byte, limit int, activeOnly bool, height, now uint64) ([]*wire.DKVSRecord, []byte, bool, error) {
	scanPrefix := recordKeyPrefix
	normalizedPrefix := strings.TrimSuffix(prefix, "/")
	if prefix != "" {
		scanPrefix = recordDBKey(normalizedPrefix)
	}
	seek := cursor
	if len(seek) == 0 {
		seek = scanPrefix
	}
	records := make([]*wire.DKVSRecord, 0)
	var next []byte
	done := true
	err := i.db.BatchReadV2(scanPrefix, seek, false, func(k, v []byte) error {
		if len(cursor) != 0 && bytes.Equal(k, cursor) {
			return nil
		}
		record, err := UnmarshalRecord(v)
		if err != nil {
			return err
		}
		if normalizedPrefix != "" && record.Key != normalizedPrefix && !strings.HasPrefix(record.Key, normalizedPrefix+"/") {
			return nil
		}
		if activeOnly && i.activeError(record, height, now) != nil {
			return nil
		}
		records = append(records, record)
		if limit > 0 && len(records) >= limit {
			done = false
			next = append([]byte{}, k...)
			return errStopScan
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}
	return records, next, done, err
}

func (i *Indexer) listPrefixLocked(prefix string, start, limit, totalHint int, height, now uint64) ([]*wire.DKVSRecord, int, error) {
	if totalHint >= 0 && start >= totalHint {
		return nil, totalHint, nil
	}
	scanPrefix := recordDBKey(prefix)
	records := make([]*wire.DKVSRecord, 0, limit)
	total := 0
	err := i.db.BatchReadV2(scanPrefix, scanPrefix, false, func(_, value []byte) error {
		record, err := UnmarshalRecord(value)
		if err != nil {
			return err
		}
		if record.Key != prefix && !strings.HasPrefix(record.Key, prefix+"/") {
			return nil
		}
		if i.activeError(record, height, now) != nil {
			return nil
		}
		if total >= start && len(records) < limit {
			records = append(records, record)
		}
		total++
		if totalHint >= 0 && len(records) >= limit {
			return errStopScan
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}
	if totalHint >= 0 {
		total = totalHint
	}
	return records, total, err
}

func (i *Indexer) scanFilteredLocked(cursor []byte, limit int, height, now uint64, match func(*wire.DKVSRecord) bool) ([]*wire.DKVSRecord, []byte, bool, error) {
	seek := cursor
	if len(seek) == 0 {
		seek = recordKeyPrefix
	}
	records := make([]*wire.DKVSRecord, 0)
	var next []byte
	done := true
	err := i.db.BatchReadV2(recordKeyPrefix, seek, false, func(k, v []byte) error {
		if len(cursor) != 0 && bytes.Equal(k, cursor) {
			return nil
		}
		record, err := UnmarshalRecord(v)
		if err != nil {
			return err
		}
		if i.activeError(record, height, now) != nil || (match != nil && !match(record)) {
			return nil
		}
		records = append(records, record)
		if limit > 0 && len(records) >= limit {
			done = false
			next = append([]byte{}, k...)
			return errStopScan
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		err = nil
	}
	return records, next, done, err
}

func (i *Indexer) emit(eventType uint8, record *wire.DKVSRecord, relay bool) {
	i.notifyPathMutation(record)
	i.mutex.RLock()
	notify := i.notify
	i.mutex.RUnlock()
	if notify == nil || record == nil {
		return
	}
	event, err := NewNotifyEvent(eventType, record)
	if err != nil {
		return
	}
	event.Relay = relay
	notify(event)
}

func (i *Indexer) emitSubscription(sub Subscription) {
	i.mutex.RLock()
	notify := i.subNotify
	i.mutex.RUnlock()
	if notify != nil {
		notify(sub)
	}
}

func (i *Indexer) currentHeight() uint64 {
	if i.height == nil {
		return 0
	}
	return i.height()
}

func notifyEventType(parsed ParsedKey, record, existing *wire.DKVSRecord) uint8 {
	if IsTombstone(record.Flags) {
		return EventRecordTombstone
	}
	if existing != nil {
		if isRenewal(record, existing) {
			return EventRenewal
		}
		if eventType := systemReadyEventType(parsed); eventType != 0 {
			return eventType
		}
		if parsed.Namespace == "mail" && len(parsed.Segments) >= 2 && parsed.Segments[1] == "msg" {
			return EventMailboxMessage
		}
		return EventRecordUpdate
	}
	if eventType := systemReadyEventType(parsed); eventType != 0 {
		return eventType
	}
	if parsed.Namespace == "mail" && len(parsed.Segments) >= 2 && parsed.Segments[1] == "msg" {
		return EventMailboxMessage
	}
	return EventRecordPut
}

func systemReadyEventType(parsed ParsedKey) uint8 {
	if parsed.Namespace != "sys" || len(parsed.Segments) < 2 {
		return 0
	}
	switch parsed.Segments[0] {
	case "checkpoint":
		return EventCheckpointReady
	case "snapshot":
		return EventSnapshotReady
	default:
		return 0
	}
}

func isRenewal(record, existing *wire.DKVSRecord) bool {
	if record == nil || existing == nil {
		return false
	}
	return record.Key == existing.Key &&
		record.Seq == existing.Seq &&
		record.Flags == existing.Flags &&
		RecordExpiryHeight(record) > RecordExpiryHeight(existing) &&
		bytes.Equal(record.Value, existing.Value) &&
		bytes.Equal(record.PubKey, existing.PubKey)
}

func recordDBKey(key string) []byte {
	out := make([]byte, 0, len(recordKeyPrefix)+len(key))
	out = append(out, recordKeyPrefix...)
	out = append(out, key...)
	return out
}

func hashDBKey(hash chainhash.Hash) []byte {
	out := make([]byte, 0, len(hashKeyPrefix)+chainhash.HashSize)
	out = append(out, hashKeyPrefix...)
	out = append(out, hash[:]...)
	return out
}

func nameTransferDBKey(name string) []byte {
	out := make([]byte, 0, len(nameTransferKeyPrefix)+len(name))
	out = append(out, nameTransferKeyPrefix...)
	out = append(out, name...)
	return out
}

var errStopScan = errors.New("stop dkvs scan")

func cloneCheckpoint(checkpoint *Checkpoint) *Checkpoint {
	if checkpoint == nil {
		return nil
	}
	copyCheckpoint := *checkpoint
	copyCheckpoint.NamespaceRoots = make(map[string]string, len(checkpoint.NamespaceRoots))
	for namespace, root := range checkpoint.NamespaceRoots {
		copyCheckpoint.NamespaceRoots[namespace] = root
	}
	return &copyCheckpoint
}

func merkleLikeRoot(hashes [][]byte) []byte {
	if len(hashes) == 0 {
		zero := sha256.Sum256(nil)
		return zero[:]
	}
	sort.Slice(hashes, func(a, b int) bool {
		return bytes.Compare(hashes[a], hashes[b]) < 0
	})
	var buf bytes.Buffer
	for _, h := range hashes {
		buf.Write(h)
	}
	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}
