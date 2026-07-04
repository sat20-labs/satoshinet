package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"

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
	db          indexercommon.KVDB
	resolver    DIDResolver
	feeVerifier FeeVerifier
	system      SystemVerifier
	mailbox     MailboxPolicy
	blob        BlobPolicy
	tmp         TmpPolicy
	subs        *subscriptionSet
	notify      NotifyFunc
	subNotify   SubscriptionNotifyFunc
	height      func() uint64
	sourceNode  string
	mutex       sync.RWMutex
}

func New(db indexercommon.KVDB, cfg Config) *Indexer {
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = defaultResolver{}
	}
	feeVerifier := cfg.FeeVerifier
	if feeVerifier == nil {
		feeVerifier = defaultFeeVerifier{allowFreeLocal: cfg.AllowFreeLocal}
	}
	systemVerifier := cfg.SystemVerifier
	if systemVerifier == nil {
		systemVerifier = defaultSystemVerifier{}
	}
	return &Indexer{
		db:          db,
		resolver:    resolver,
		feeVerifier: feeVerifier,
		system:      systemVerifier,
		mailbox:     normalizeMailboxPolicy(cfg.MailboxPolicy),
		blob:        normalizeBlobPolicy(cfg.BlobPolicy),
		tmp:         normalizeTmpPolicy(cfg.TmpPolicy),
		subs:        newSubscriptionSet(),
		notify:      cfg.Notify,
		subNotify:   cfg.Subscription,
		height:      cfg.CurrentHeight,
		sourceNode:  cfg.SourceNode,
	}
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
}

func (i *Indexer) SetFeeVerifier(verifier FeeVerifier) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if verifier == nil {
		verifier = defaultFeeVerifier{}
	}
	i.feeVerifier = verifier
}

func (i *Indexer) SetSystemVerifier(verifier SystemVerifier) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if verifier == nil {
		verifier = defaultSystemVerifier{}
	}
	i.system = verifier
}

func (i *Indexer) PutLocal(record *wire.DKVSRecord) (bool, error) {
	updated, eventType, hash, err := i.put(record)
	if err != nil {
		return false, err
	}
	if updated {
		i.emit(eventType, record, hash)
	}
	return updated, nil
}

func (i *Indexer) PutRemote(record *wire.DKVSRecord) (bool, error) {
	updated, _, _, err := i.put(record)
	return updated, err
}

func (i *Indexer) NotifyNameTransfers(names []string) error {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	for _, name := range names {
		name = strings.TrimSpace(strings.ToLower(name))
		if name == "" {
			continue
		}
		if len(name) > MaxKeySegmentSize || !validSegment(name) {
			return ErrInvalidKey
		}
		if err := i.db.Write(nameTransferDBKey(name), []byte{1}); err != nil {
			return err
		}
	}
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
	records, _, _, err := i.scan(prefix, nil, limit+start, true)
	if err != nil {
		return nil, 0, err
	}
	total := len(records)
	if start >= total {
		return nil, total, nil
	}
	end := start + limit
	if end > total {
		end = total
	}
	return records[start:end], total, nil
}

func (i *Indexer) Usage(prefix string) (*Usage, error) {
	if len(prefix) == 0 || prefix[0] != '/' {
		return nil, ErrInvalidKey
	}
	if _, err := ParsePrefix(prefix); err != nil {
		return nil, err
	}
	records, _, _, err := i.scan(prefix, nil, 0, true)
	if err != nil {
		return nil, err
	}
	usage := &Usage{Prefix: strings.TrimSuffix(prefix, "/")}
	for _, record := range records {
		usage.ActiveRecords++
		usage.ActiveTotalSize += uint64(RecordSize(record))
	}
	return usage, nil
}

func (i *Indexer) Sync(cursor []byte, limit uint32) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
	if limit == 0 || limit > wire.MaxDKVSRecordsPerMsg {
		limit = 100
	}
	records, next, done, err := i.scan("", cursor, int(limit), true)
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	cp, err := i.Checkpoint()
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	rootBytes, _ := hex.DecodeString(cp.ActiveRecordRoot)
	var root chainhash.Hash
	copy(root[:], rootBytes)
	return records, next, done, root, nil
}

func (i *Indexer) SyncFiltered(cursor []byte, limit uint32, filters []Subscription) ([]*wire.DKVSRecord, []byte, bool, chainhash.Hash, error) {
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
	records, next, done, err := i.scanFiltered(cursor, int(limit), func(record *wire.DKVSRecord) bool {
		for _, filter := range normalized {
			if subscriptionMatchesKey(filter, record.Key) {
				return true
			}
		}
		return false
	})
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	cp, err := i.Checkpoint()
	if err != nil {
		return nil, nil, false, chainhash.Hash{}, err
	}
	rootBytes, _ := hex.DecodeString(cp.ActiveRecordRoot)
	var root chainhash.Hash
	copy(root[:], rootBytes)
	return records, next, done, root, nil
}

func (i *Indexer) Subscribe(sub Subscription) ([]*wire.DKVSRecord, int, error) {
	normalized, err := validateSubscription(sub)
	if err != nil {
		return nil, 0, err
	}
	added := i.subs.add(normalized)
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
	records, _, _, err := i.scan("", nil, 0, true)
	if err != nil {
		return nil, err
	}
	return checkpointFromRecords(records, i.currentHeight())
}

func (i *Indexer) Snapshot() (*Snapshot, error) {
	records, _, _, err := i.scan("", nil, 0, true)
	if err != nil {
		return nil, err
	}
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
	applied := 0
	for _, record := range snapshot.Records {
		updated, err := i.PutRemote(record)
		if err != nil {
			return applied, err
		}
		if updated {
			applied++
		}
	}
	return applied, nil
}

func (i *Indexer) PruneExpired() (int, error) {
	return i.PruneExpiredAt(i.currentHeight())
}

func (i *Indexer) PruneExpiredAt(height uint64) (int, error) {
	now := currentUnixMilli()
	i.mutex.Lock()

	records, _, _, err := i.scanLocked("", nil, 0, false, height, now)
	if err != nil {
		i.mutex.Unlock()
		return 0, err
	}
	type expiredRecord struct {
		record *wire.DKVSRecord
		hash   chainhash.Hash
	}
	expiredRecords := make([]expiredRecord, 0)
	pruned := 0
	for _, record := range records {
		if !IsExpired(record, height, now) {
			continue
		}
		if recordHasPaidFeeProof(record) {
			continue
		}
		if err := i.db.Delete(recordDBKey(record.Key)); err != nil {
			i.mutex.Unlock()
			return pruned, err
		}
		hash := RecordHash(record)
		if err := i.db.Delete(hashDBKey(hash)); err != nil {
			i.mutex.Unlock()
			return pruned, err
		}
		expiredRecords = append(expiredRecords, expiredRecord{record: record, hash: hash})
		pruned++
	}
	i.mutex.Unlock()
	for _, expired := range expiredRecords {
		i.emit(EventExpired, expired.record, expired.hash)
	}
	return pruned, nil
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

func (i *Indexer) put(record *wire.DKVSRecord) (bool, uint32, chainhash.Hash, error) {
	height := i.currentHeight()
	now := currentUnixMilli()
	parsed, err := i.validateParsedBasic(record, height, now)
	if err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	existing, err := i.getRaw(record.Key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return false, 0, chainhash.Hash{}, err
	}
	forceReplace, err := i.validateWritePermission(parsed, record, existing)
	if err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	if err := i.validateStatefulLocked(record, parsed, existing, height, now); err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	clearNameTransfer := parsed.Namespace == "name" && i.isNameTransferDirty(parsed.Segments[0])
	if existing != nil && !forceReplace && i.activeError(existing, height, now) == nil && CompareRecords(existing, record) >= 0 {
		if clearNameTransfer {
			if err := i.clearNameTransferDirty(parsed.Segments[0]); err != nil {
				return false, 0, chainhash.Hash{}, err
			}
		}
		return false, 0, RecordHash(existing), nil
	}
	if err := i.validateFeeCapacityLocked(record, parsed, existing, height, now); err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	data, err := MarshalRecord(record)
	if err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	hash := RecordHash(record)
	if err := i.db.Write(recordDBKey(record.Key), data); err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	if existing != nil {
		existingHash := RecordHash(existing)
		if existingHash != hash {
			if err := i.db.Delete(hashDBKey(existingHash)); err != nil {
				return false, 0, chainhash.Hash{}, err
			}
		}
	}
	if err := i.db.Write(hashDBKey(hash), []byte(record.Key)); err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	if clearNameTransfer {
		if err := i.clearNameTransferDirty(parsed.Segments[0]); err != nil {
			return false, 0, chainhash.Hash{}, err
		}
	}
	eventType := notifyEventType(parsed, record, existing)
	return true, eventType, hash, nil
}

func (i *Indexer) validate(record *wire.DKVSRecord) error {
	_, err := i.validateParsed(record, i.currentHeight(), currentUnixMilli())
	return err
}

func (i *Indexer) validateAt(record *wire.DKVSRecord, height, now uint64) error {
	parsed, err := i.validateParsedBasic(record, height, now)
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

func (i *Indexer) validateParsedBasic(record *wire.DKVSRecord, height, now uint64) (ParsedKey, error) {
	var parsed ParsedKey
	if record == nil || record.Version != Version {
		return parsed, ErrInvalidRecord
	}
	if len(record.Value) > MaxRecordValueSize || len(record.Data) > MaxRecordDataSize ||
		RecordSize(record) > wire.MaxDKVSRecordSize {
		return parsed, ErrRecordTooLarge
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return parsed, err
	}
	if IsExpired(record, height, now) {
		return parsed, ErrExpiredRecord
	}
	if err := VerifySignature(record); err != nil {
		return parsed, err
	}
	if IsTombstone(record.Flags) && len(record.Value) != 0 {
		return parsed, ErrInvalidRecord
	}
	if err := i.verifyFeeProof(record, parsed); err != nil {
		return parsed, err
	}
	return parsed, nil
}

func (i *Indexer) verifyFeeProof(record *wire.DKVSRecord, parsed ParsedKey) error {
	if verifier, ok := i.feeVerifier.(RecordFeeVerifier); ok {
		return verifier.VerifyRecordFeeProof(record, parsed)
	}
	hash := FeeAnchorHash(record)
	var hash32 [32]byte
	copy(hash32[:], hash[:])
	keyHash := KeyHash(record.Key)
	var keyHash32 [32]byte
	copy(keyHash32[:], keyHash[:])
	if err := i.feeVerifier.VerifyFeeProof(hash32, keyHash32, parsed.Namespace, RecordSize(record), record.ExpiryHeight, record.FeeProof); err != nil {
		return err
	}
	return nil
}

func (i *Indexer) validateFeeCapacityLocked(record *wire.DKVSRecord, parsed ParsedKey, existing *wire.DKVSRecord, height, now uint64) error {
	verifier, ok := i.feeVerifier.(FeeCapacityVerifier)
	if !ok {
		return nil
	}
	records, _, _, err := i.scanLocked("", nil, 0, false, height, now)
	if err != nil {
		return err
	}
	return verifier.VerifyFeeCapacity(record, parsed, existing, records, height, now)
}

func (i *Indexer) activeError(record *wire.DKVSRecord, height, now uint64) error {
	return i.validateAt(record, height, now)
}

func (i *Indexer) validatePermission(parsed ParsedKey, pubKey []byte) error {
	switch parsed.Namespace {
	case "personal":
		if len(parsed.Segments) < 2 || parsed.Segments[0] != personalAccountID(pubKey) {
			return ErrPermissionDenied
		}
	case "name":
		identity, err := i.resolver.ResolveName(parsed.Segments[0])
		if err != nil {
			return err
		}
		return identity.CanSign(pubKey)
	case "svc":
		identity, err := i.resolver.ResolveService(parsed.Segments[0])
		if err != nil {
			return err
		}
		return identity.CanSign(pubKey)
	case "mail":
		if len(parsed.Segments) >= 2 && parsed.Segments[1] == "share" &&
			parsed.Segments[0] != personalAccountID(pubKey) {
			return ErrPermissionDenied
		}
	case "sys":
		return i.system.CanWriteSystem("/"+parsed.Namespace+"/"+strings.Join(parsed.Segments, "/"), pubKey)
	}
	return nil
}

func (i *Indexer) validateWritePermission(parsed ParsedKey, record, existing *wire.DKVSRecord) (bool, error) {
	if record == nil {
		return false, ErrInvalidRecord
	}
	switch parsed.Namespace {
	case "name", "svc":
		if existing != nil && bytes.Equal(existing.PubKey, record.PubKey) && !i.requiresNameResolve(parsed) {
			return false, nil
		}
		if err := i.validatePermission(parsed, record.PubKey); err != nil {
			return false, err
		}
		return existing != nil && !bytes.Equal(existing.PubKey, record.PubKey), nil
	default:
		return false, i.validatePermission(parsed, record.PubKey)
	}
}

func (i *Indexer) requiresNameResolve(parsed ParsedKey) bool {
	if parsed.Namespace != "name" || len(parsed.Segments) != 1 {
		return false
	}
	return i.isNameTransferDirty(parsed.Segments[0])
}

func (i *Indexer) isNameTransferDirty(name string) bool {
	_, err := i.db.Read(nameTransferDBKey(name))
	return err == nil
}

func (i *Indexer) clearNameTransferDirty(name string) error {
	if !i.isNameTransferDirty(name) {
		return nil
	}
	return i.db.Delete(nameTransferDBKey(name))
}

func (i *Indexer) validateStoredPermission(parsed ParsedKey, record *wire.DKVSRecord) error {
	switch parsed.Namespace {
	case "name", "svc":
		return nil
	default:
		return i.validatePermission(parsed, record.PubKey)
	}
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
	if len(record.Value) > i.tmp.MaxSize || len(record.Data) > i.tmp.MaxSize {
		return ErrRecordTooLarge
	}
	return nil
}

func (i *Indexer) getRaw(key string) (*wire.DKVSRecord, error) {
	data, err := i.db.Read(recordDBKey(key))
	if err != nil {
		return nil, ErrRecordNotFound
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
	if prefix != "" {
		scanPrefix = recordDBKey(prefix)
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
			return nil
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

func (i *Indexer) emit(eventType uint32, record *wire.DKVSRecord, hash chainhash.Hash) {
	i.mutex.RLock()
	notify := i.notify
	sourceNode := i.sourceNode
	i.mutex.RUnlock()
	if notify == nil || record == nil {
		return
	}
	event, err := NewNotifyEvent(eventType, record, sourceNode)
	if err != nil {
		return
	}
	event.RecordHash = hash
	notify(event.EventType, event.Key, event.RecordHash, event.Seq, event.ExpiryHeight, event.Size, event.Flags)
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

func notifyEventType(parsed ParsedKey, record, existing *wire.DKVSRecord) uint32 {
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

func systemReadyEventType(parsed ParsedKey) uint32 {
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
		record.ExpiryHeight > existing.ExpiryHeight &&
		record.TTL >= existing.TTL &&
		bytes.Equal(record.Value, existing.Value) &&
		bytes.Equal(record.Data, existing.Data) &&
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
