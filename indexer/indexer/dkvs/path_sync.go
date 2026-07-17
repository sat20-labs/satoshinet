package dkvs

import (
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MaxPathSyncPendingRecords = 10_000
	MaxPathSyncPendingBytes   = 32 * 1024 * 1024
)

type pathSyncTracker struct {
	mutex    sync.Mutex
	sessions map[string]*pathSyncSession
}

type pathSyncSession struct {
	subscription Subscription
	pending      map[string]*wire.DKVSRecord
	pendingBytes int
	overflow     bool
}

func newPathSyncTracker() *pathSyncTracker {
	return &pathSyncTracker{sessions: make(map[string]*pathSyncSession)}
}

func normalizeInternalSyncSubscription(sub Subscription) (Subscription, error) {
	if sub.Type == "" && strings.TrimSpace(sub.Target) == "" {
		return Subscription{}, nil
	}
	return validateSubscription(sub)
}

func syncSubscriptionMatches(sub Subscription, key string) bool {
	if sub.Type == "" && sub.Target == "" {
		return true
	}
	return subscriptionMatchesKey(sub, key)
}

func (t *pathSyncTracker) begin(token string, sub Subscription) error {
	if t == nil || strings.TrimSpace(token) == "" {
		return ErrInvalidRecord
	}
	normalized, err := normalizeInternalSyncSubscription(sub)
	if err != nil {
		return err
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if existing, ok := t.sessions[token]; ok {
		if existing.subscription == normalized {
			return nil
		}
		return ErrConcurrentUpdate
	}
	t.sessions[token] = &pathSyncSession{
		subscription: normalized,
		pending:      make(map[string]*wire.DKVSRecord),
	}
	return nil
}

func (t *pathSyncTracker) record(record *wire.DKVSRecord) {
	if t == nil || record == nil {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	for _, session := range t.sessions {
		if session.overflow || !syncSubscriptionMatches(session.subscription, record.Key) {
			continue
		}
		cloned := cloneDKVSRecord(record)
		newSize := RecordSize(cloned)
		if previous := session.pending[record.Key]; previous != nil {
			session.pendingBytes -= RecordSize(previous)
		}
		session.pending[record.Key] = cloned
		session.pendingBytes += newSize
		if len(session.pending) > MaxPathSyncPendingRecords || session.pendingBytes > MaxPathSyncPendingBytes {
			session.overflow = true
			session.pending = nil
			session.pendingBytes = 0
		}
	}
}

func (t *pathSyncTracker) end(token string) ([]*wire.DKVSRecord, bool) {
	if t == nil {
		return nil, false
	}
	t.mutex.Lock()
	session := t.sessions[token]
	delete(t.sessions, token)
	t.mutex.Unlock()
	if session == nil {
		return nil, false
	}
	if session.overflow {
		return nil, true
	}
	records := make([]*wire.DKVSRecord, 0, len(session.pending))
	for _, record := range session.pending {
		records = append(records, record)
	}
	sort.Slice(records, func(a, b int) bool {
		return records[a].Key < records[b].Key
	})
	return records, false
}

func (t *pathSyncTracker) cancel(token string) {
	if t == nil {
		return
	}
	t.mutex.Lock()
	delete(t.sessions, token)
	t.mutex.Unlock()
}

func cloneDKVSRecord(record *wire.DKVSRecord) *wire.DKVSRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.Value = append([]byte(nil), record.Value...)
	cloned.PubKey = append([]byte(nil), record.PubKey...)
	cloned.Signature = append([]byte(nil), record.Signature...)
	cloned.FeeProof = append([]byte(nil), record.FeeProof...)
	return &cloned
}

func (i *Indexer) BeginPathSync(token string, sub Subscription) error {
	return i.pathSyncs.begin(token, sub)
}

func (i *Indexer) EndPathSync(token string) ([]*wire.DKVSRecord, bool) {
	return i.pathSyncs.end(token)
}

func (i *Indexer) CancelPathSync(token string) {
	i.pathSyncs.cancel(token)
}

func (i *Indexer) recordPathSyncChange(record *wire.DKVSRecord) {
	i.pathSyncs.record(record)
}

func (i *Indexer) ListActiveKeysForSync(sub Subscription) ([]string, error) {
	normalized, err := normalizeInternalSyncSubscription(sub)
	if err != nil {
		return nil, err
	}
	if normalized.Type == SubscriptionKey {
		record, err := i.Get(normalized.Target)
		if errors.Is(err, ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return []string{record.Key}, nil
	}
	records, _, _, err := i.scan(normalized.Target, nil, 0, true)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(records))
	for _, record := range records {
		if record != nil && syncSubscriptionMatches(normalized, record.Key) {
			keys = append(keys, record.Key)
		}
	}
	return keys, nil
}

func (i *Indexer) DeleteMirrorKeys(sub Subscription, keys []string) (int, error) {
	normalized, err := validateSubscription(sub)
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 0, nil
	}
	height := i.currentHeight()
	now := currentUnixMilli()
	i.mutex.Lock()
	defer i.mutex.Unlock()

	batch := i.db.NewWriteBatch()
	defer batch.Close()
	metas := make(map[string]*PathMeta)
	deleted := 0
	for _, key := range keys {
		if !syncSubscriptionMatches(normalized, key) {
			continue
		}
		record, err := i.getRaw(key)
		if errors.Is(err, ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return deleted, err
		}
		parsed, err := ParseKey(record.Key)
		if err != nil {
			return deleted, err
		}
		active := i.activeError(record, height, now) == nil
		if err := batch.Delete(recordDBKey(record.Key)); err != nil {
			return deleted, err
		}
		if err := batch.Delete(hashDBKey(RecordHash(record))); err != nil {
			return deleted, err
		}
		if active {
			if path, ok := collectionPathForParsed(parsed); ok {
				meta := metas[path]
				if meta == nil {
					meta, err = i.ensurePathMetaLocked(path, height, now)
					if err != nil {
						return deleted, err
					}
					metas[path] = meta
				}
				if meta.ActiveCount > 0 {
					meta.ActiveCount--
				}
				size := uint64(RecordSize(record))
				if meta.ActiveBytes >= size {
					meta.ActiveBytes -= size
				} else {
					meta.ActiveBytes = 0
				}
				meta.Generation++
				meta.UpdatedHeight = height
				meta.UpdatedAt = now
			}
		}
		if i.feeUsageInitialized {
			i.removeFeeUsageLocked(record.Key)
		}
		if i.recordExpiryInitialized {
			delete(i.recordExpiryEntries, record.Key)
		}
		deleted++
	}
	for path, meta := range metas {
		if err := batch.Put(pathMetaDBKey(path), encodePathMeta(*meta)); err != nil {
			return deleted, err
		}
	}
	if deleted == 0 {
		return 0, nil
	}
	if err := batch.Flush(); err != nil {
		return 0, err
	}
	atomic.AddUint64(&i.generation, 1)
	return deleted, nil
}

func (i *Indexer) syncFilteredV2(cursor []byte, limit uint32, filters []Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if len(filters) == 0 {
		return i.Sync(cursor, limit)
	}
	if limit == 0 || limit > wire.MaxDKVSRecordsPerMsg {
		limit = 100
	}
	normalized := make([]Subscription, 0, len(filters))
	for _, filter := range filters {
		sub, err := validateSubscription(filter)
		if err != nil {
			return nil, nil, false, chainhash.Hash{}, err
		}
		normalized = append(normalized, sub)
	}

	var (
		records []*wire.DKVSRecord
		next    []byte
		done    bool
		err     error
	)
	if len(normalized) == 1 {
		sub := normalized[0]
		if sub.Type == SubscriptionKey {
			done = true
			if len(cursor) == 0 {
				record, getErr := i.Get(sub.Target)
				switch {
				case getErr == nil:
					records = []*wire.DKVSRecord{record}
				case errors.Is(getErr, ErrRecordNotFound):
				default:
					return nil, nil, false, chainhash.Hash{}, getErr
				}
			}
		} else {
			records, next, done, err = i.scan(sub.Target, cursor, int(limit), true)
		}
	} else {
		records, next, done, err = i.scanFiltered(cursor, int(limit), func(record *wire.DKVSRecord) bool {
			for _, filter := range normalized {
				if subscriptionMatchesKey(filter, record.Key) {
					return true
				}
			}
			return false
		})
	}
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	checkpoint, err := i.Checkpoint()
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	rootBytes, err := hex.DecodeString(checkpoint.ActiveRecordRoot)
	if err != nil || len(rootBytes) != chainhash.HashSize {
		return nil, nil, false, chainhash.Hash{}, ErrInvalidCheckpoint
	}
	var root chainhash.Hash
	copy(root[:], rootBytes)
	return records, next, done, root, nil
}
