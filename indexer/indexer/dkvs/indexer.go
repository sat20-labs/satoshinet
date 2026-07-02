package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

var (
	recordKeyPrefix = []byte("dkvs:record:")
	hashKeyPrefix   = []byte("dkvs:hash:")
)

type Indexer struct {
	db          indexercommon.KVDB
	resolver    DIDResolver
	feeVerifier FeeVerifier
	notify      NotifyFunc
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
	return &Indexer{
		db:          db,
		resolver:    resolver,
		feeVerifier: feeVerifier,
		notify:      cfg.Notify,
		height:      cfg.CurrentHeight,
		sourceNode:  cfg.SourceNode,
	}
}

func (i *Indexer) SetNotify(fn NotifyFunc) {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	i.notify = fn
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
	if IsExpired(record, i.currentHeight(), currentUnixMilli()) {
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
	if IsExpired(record, i.currentHeight(), currentUnixMilli()) {
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

func (i *Indexer) Checkpoint() (*Checkpoint, error) {
	records, _, _, err := i.scan("", nil, 0, true)
	if err != nil {
		return nil, err
	}
	namespaceHashes := make(map[string][][]byte)
	cp := &Checkpoint{
		Height:         i.currentHeight(),
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
	if err := i.validate(record); err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	i.mutex.Lock()
	defer i.mutex.Unlock()
	existing, err := i.getRaw(record.Key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return false, 0, chainhash.Hash{}, err
	}
	if existing != nil && CompareRecords(existing, record) >= 0 {
		return false, 0, RecordHash(existing), nil
	}
	data, err := MarshalRecord(record)
	if err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	hash := RecordHash(record)
	if err := i.db.Write(recordDBKey(record.Key), data); err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	if err := i.db.Write(hashDBKey(hash), []byte(record.Key)); err != nil {
		return false, 0, chainhash.Hash{}, err
	}
	eventType := EventRecordPut
	if existing != nil {
		eventType = EventRecordUpdate
	}
	if IsTombstone(record.Flags) {
		eventType = EventRecordTombstone
	}
	return true, eventType, hash, nil
}

func (i *Indexer) validate(record *wire.DKVSRecord) error {
	if record == nil || record.Version != Version {
		return ErrInvalidRecord
	}
	if len(record.Value) > MaxRecordValueSize || len(record.Data) > MaxRecordDataSize ||
		RecordSize(record) > wire.MaxDKVSRecordSize {
		return ErrRecordTooLarge
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		return err
	}
	if IsExpired(record, i.currentHeight(), currentUnixMilli()) {
		return ErrExpiredRecord
	}
	if err := VerifySignature(record); err != nil {
		return err
	}
	if IsTombstone(record.Flags) && len(record.Value) != 0 {
		return ErrInvalidRecord
	}
	switch parsed.Namespace {
	case "personal":
		if len(parsed.Segments) < 2 || parsed.Segments[0] != personalAccountID(record.PubKey) {
			return ErrPermissionDenied
		}
	case "name":
		if err := i.resolver.CanWriteName(parsed.Segments[0], record.PubKey); err != nil {
			return err
		}
	case "svc":
		if err := i.resolver.CanWriteService(parsed.Segments[0], record.PubKey); err != nil {
			return err
		}
	case "sys":
		return ErrPermissionDenied
	}
	hash := RecordHash(record)
	var hash32 [32]byte
	copy(hash32[:], hash[:])
	return i.feeVerifier.VerifyFeeProof(hash32, parsed.Namespace, RecordSize(record), record.FeeProof)
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
	height := i.currentHeight()
	now := currentUnixMilli()
	err := i.db.BatchReadV2(scanPrefix, seek, false, func(k, v []byte) error {
		if len(cursor) != 0 && bytes.Equal(k, cursor) {
			return nil
		}
		record, err := UnmarshalRecord(v)
		if err != nil {
			return nil
		}
		if activeOnly && IsExpired(record, height, now) {
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
	i.mutex.RUnlock()
	if notify == nil || record == nil {
		return
	}
	var hash32 [32]byte
	copy(hash32[:], hash[:])
	notify(eventType, record.Key, hash32, record.Seq, record.ExpiryHeight, uint32(RecordSize(record)), record.Flags)
}

func (i *Indexer) currentHeight() uint64 {
	if i.height == nil {
		return 0
	}
	return i.height()
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
