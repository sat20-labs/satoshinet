package dkvs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	ActiveSyncPageRecords    = 256
	ActiveSyncPageBytes      = MaxPrefixReadBytes
	ActiveWatchTimeout       = 20 * time.Second
	MaxActiveWatchReferences = 4096
)

// ActiveScope identifies an exact replication view. Generations belong to the
// source endpoint and collection, never to a global mutation stream. Network
// views exclude placement-bound data; endpoint views include local cache data.
// Keys, when nonempty, restrict the view to these exact keys.
type ActiveScope struct {
	Prefix  string   `json:"prefix"`
	Keys    []string `json:"keys,omitempty"`
	Network bool     `json:"network,omitempty"`
}

type ActiveMeta struct {
	EndpointID string         `json:"endpoint_id"`
	Scope      ActiveScope    `json:"scope"`
	Generation uint64         `json:"generation"`
	Root       chainhash.Hash `json:"root"`
	ViewHeight uint64         `json:"view_height"`
}

// ActivePageCursor pins every page to the same current-state view. It is not
// persisted by the source and does not retain historical mutations. A source
// mutation invalidates the cursor and the consumer restarts the whole read.
type ActivePageCursor struct {
	Meta           ActiveMeta `json:"meta"`
	After          uint64     `json:"after"`
	Full           bool       `json:"full"`
	LastGeneration uint64     `json:"last_generation,omitempty"`
	LastKey        string     `json:"last_key"`
}

type ActiveSyncRequest struct {
	Scope      ActiveScope       `json:"scope"`
	EndpointID string            `json:"endpoint_id"`
	After      uint64            `json:"after"`
	Full       bool              `json:"full"`
	PageSize   int               `json:"page_size,omitempty"`
	Cursor     *ActivePageCursor `json:"cursor,omitempty"`
}

type ActivePage struct {
	Meta     ActiveMeta         `json:"meta"`
	Records  []*wire.DKVSRecord `json:"records"`
	Complete bool               `json:"complete"`
	Next     *ActivePageCursor  `json:"next,omitempty"`
}

type ActiveWatchScope struct {
	Scope      ActiveScope    `json:"scope"`
	Generation uint64         `json:"generation"`
	Root       chainhash.Hash `json:"root"`
}

type ActiveWatchRequest struct {
	EndpointID string             `json:"endpoint_id"`
	Scopes     []ActiveWatchScope `json:"scopes"`
}

type ActiveWatchResult struct {
	// One changed scope per response bounds aggregate memory independently of
	// the number of subscriptions. The next watch drains other current changes.
	Page *ActivePage `json:"page,omitempty"`
}

func NormalizeActiveScope(scope ActiveScope) (ActiveScope, error) {
	scope.Prefix = strings.TrimSuffix(strings.TrimSpace(scope.Prefix), "/")
	if len(scope.Prefix) > MaxPrefixLength || !isCanonicalCollectionPath(scope.Prefix) {
		return ActiveScope{}, ErrInvalidKey
	}
	parsed, err := ParsePrefix(scope.Prefix)
	if err != nil || pathMode(parsed) == PathLocalOnly {
		return ActiveScope{}, ErrInvalidKey
	}
	if scope.Network && replicationMode(parsed, nil) != ReplicationNetwork {
		return ActiveScope{}, ErrFreeLocalNotRelayable
	}
	if len(scope.Keys) > MaxPrefixReadRecords {
		return ActiveScope{}, ErrBatchTooLarge
	}
	keys := append([]string(nil), scope.Keys...)
	sort.Strings(keys)
	for n, key := range keys {
		path, err := CollectionPathForKey(key)
		if err != nil || path != scope.Prefix || (n > 0 && key == keys[n-1]) {
			return ActiveScope{}, ErrInvalidKey
		}
	}
	scope.Keys = keys
	return scope, nil
}

func SameActiveScope(a, b ActiveScope) bool {
	if a.Prefix != b.Prefix || a.Network != b.Network || len(a.Keys) != len(b.Keys) {
		return false
	}
	for n := range a.Keys {
		if a.Keys[n] != b.Keys[n] {
			return false
		}
	}
	return true
}

func ActiveScopeMatches(scope ActiveScope, key string) bool {
	path, err := CollectionPathForKey(key)
	if err != nil || path != scope.Prefix {
		return false
	}
	if len(scope.Keys) == 0 {
		return true
	}
	n := sort.SearchStrings(scope.Keys, key)
	return n < len(scope.Keys) && scope.Keys[n] == key
}

// ActiveRecordsRoot deliberately excludes source generations and all removed
// records. Both nodes and SDK replicas use exactly this set commitment.
func ActiveRecordsRoot(records []*wire.DKVSRecord) (chainhash.Hash, error) {
	var root chainhash.Hash
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record == nil || IsTombstone(record.Flags) {
			return root, ErrInvalidRecord
		}
		if _, ok := seen[record.Key]; ok {
			return root, ErrInvalidSnapshot
		}
		seen[record.Key] = struct{}{}
		xorPathMetaRoot(&root, record)
	}
	return root, nil
}

func (i *Indexer) activeScopeRecord(record *wire.DKVSRecord, scope ActiveScope, height, now uint64) bool {
	return record != nil && ActiveScopeMatches(scope, record.Key) &&
		i.activeError(record, height, now) == nil && (!scope.Network || i.networkPathRecordVisible(record))
}

// The root pass streams through the database. Only one bounded page of values
// is retained in memory; a directory can contain more than one page limit.
func (i *Indexer) activeMetaLocked(ctx context.Context, scope ActiveScope, height uint64) (ActiveMeta, error) {
	meta := ActiveMeta{EndpointID: i.endpointID(), Scope: scope, ViewHeight: height}
	pathMeta, err := i.readPathMetaLocked(scope.Prefix)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return meta, err
	}
	if pathMeta != nil {
		meta.Generation = pathMeta.EndpointGeneration
	}
	base, now := recordDBKey(scope.Prefix), currentUnixMilli()
	err = i.db.BatchReadV2(base, base, false, func(_, encoded []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := UnmarshalRecord(encoded)
		if err != nil {
			return err
		}
		if i.activeScopeRecord(record, scope, height, now) {
			xorPathMetaRoot(&meta.Root, record)
		}
		return nil
	})
	return meta, err
}

func (i *Indexer) ActiveMetadata(ctx context.Context, scope ActiveScope) (ActiveMeta, error) {
	if i == nil {
		return ActiveMeta{}, ErrInvalidRecord
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scope, err := NormalizeActiveScope(scope)
	if err != nil {
		return ActiveMeta{}, err
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	return i.activeMetaLocked(ctx, scope, i.currentHeight())
}

func (i *Indexer) ActiveSyncPage(ctx context.Context, request ActiveSyncRequest) (*ActivePage, error) {
	if i == nil {
		return nil, ErrInvalidRecord
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scope, err := NormalizeActiveScope(request.Scope)
	if err != nil {
		return nil, err
	}
	if request.EndpointID == "" || request.EndpointID != i.EndpointID() {
		return nil, ErrEndpointMismatch
	}
	limit := request.PageSize
	if limit <= 0 || limit > ActiveSyncPageRecords {
		limit = ActiveSyncPageRecords
	}
	i.mutex.RLock()
	defer i.mutex.RUnlock()
	height := i.currentHeight()
	if request.Cursor != nil {
		c := request.Cursor
		if c.Meta.EndpointID != request.EndpointID || !SameActiveScope(c.Meta.Scope, scope) ||
			c.After != request.After || c.Full != request.Full || c.LastKey == "" ||
			!ActiveScopeMatches(scope, c.LastKey) || c.Meta.ViewHeight > height {
			return nil, ErrInvalidSnapshot
		}
		height = c.Meta.ViewHeight
	}
	meta, err := i.activeMetaLocked(ctx, scope, height)
	if err != nil {
		return nil, err
	}
	if request.Cursor != nil && (request.Cursor.Meta.Generation != meta.Generation || request.Cursor.Meta.Root != meta.Root) {
		return nil, ErrStaleGeneration
	}
	if !request.Full && request.After > meta.Generation {
		return nil, ErrStaleGeneration
	}
	page := &ActivePage{Meta: meta, Records: make([]*wire.DKVSRecord, 0, limit), Complete: true}
	total, now := 0, currentUnixMilli()
	lastKey, lastGeneration := "", uint64(0)
	appendRecord := func(record *wire.DKVSRecord, generation uint64) error {
		if !i.activeScopeRecord(record, scope, height, now) {
			return nil
		}
		size := RecordSize(record)
		if size > ActiveSyncPageBytes {
			return ErrRecordTooLarge
		}
		if len(page.Records) >= limit || size > ActiveSyncPageBytes-total {
			page.Complete = false
			return errStopScan
		}
		page.Records = append(page.Records, record)
		total += size
		lastKey, lastGeneration = record.Key, generation
		return nil
	}
	if request.Full {
		base := recordDBKey(scope.Prefix)
		seek := base
		if request.Cursor != nil {
			seek = recordDBKey(request.Cursor.LastKey)
		}
		err = i.db.BatchReadV2(base, seek, false, func(k, encoded []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if request.Cursor != nil && bytes.Equal(k, seek) {
				return nil
			}
			record, err := UnmarshalRecord(encoded)
			if err != nil {
				return err
			}
			return appendRecord(record, 0)
		})
	} else {
		base := prefixChangeDBPrefix(scope.Prefix)
		seek := append([]byte(nil), base...)
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], request.After)
		seek = append(seek, encoded[:]...)
		if request.Cursor != nil {
			seek = prefixChangeDBKey(scope.Prefix, request.Cursor.LastGeneration, request.Cursor.LastKey)
		}
		err = i.db.BatchReadV2(base, seek, false, func(k, _ []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if request.Cursor != nil && bytes.Equal(k, seek) {
				return nil
			}
			if !bytes.HasPrefix(k, base) || len(k) <= len(base)+8 {
				return ErrInvalidRecord
			}
			generation := binary.BigEndian.Uint64(k[len(base) : len(base)+8])
			if generation <= request.After {
				return nil
			}
			if generation > meta.Generation {
				return errStopScan
			}
			key := string(k[len(base)+8:])
			current, found, err := i.changedGenerationLocked(key)
			if err != nil {
				return err
			}
			if !found || current != generation {
				return nil
			}
			record, err := i.getRaw(key)
			if errors.Is(err, ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			return appendRecord(record, generation)
		})
	}
	if errors.Is(err, errStopScan) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if !page.Complete {
		page.Next = &ActivePageCursor{Meta: meta, After: request.After, Full: request.Full,
			LastKey: lastKey, LastGeneration: lastGeneration}
	}
	return page, nil
}

// WatchActive registers signals BEFORE comparing metadata. It stores no event
// queue, mutation log, or offline terminal state. Slow consumers simply read
// the latest current records on their next request.
func (i *Indexer) WatchActive(ctx context.Context, request ActiveWatchRequest) (*ActiveWatchResult, error) {
	if i == nil || len(request.Scopes) == 0 || len(request.Scopes) > MaxPrefixesPerTerminal {
		return nil, ErrTooManySubscriptions
	}
	if request.EndpointID == "" || request.EndpointID != i.EndpointID() {
		return nil, ErrEndpointMismatch
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, ActiveWatchTimeout)
	defer cancel()
	scopes := append([]ActiveWatchScope(nil), request.Scopes...)
	paths := make(map[string]struct{}, len(scopes))
	for n := range scopes {
		scope, err := NormalizeActiveScope(scopes[n].Scope)
		if err != nil {
			return nil, err
		}
		scopes[n].Scope = scope
		if _, exists := paths[scope.Prefix]; exists {
			return nil, ErrInvalidKey
		}
		paths[scope.Prefix] = struct{}{}
	}
	i.watchMutex.Lock()
	refs := 0
	for _, signal := range i.pathSignals {
		refs += signal.refs
	}
	if refs+len(paths) > MaxActiveWatchReferences {
		i.watchMutex.Unlock()
		return nil, ErrTooManySubscriptions
	}
	if i.pathSignals == nil {
		i.pathSignals = make(map[string]*pathWatchSignal)
	}
	signals := make([]*pathWatchSignal, len(scopes))
	for n, item := range scopes {
		signal := i.pathSignals[item.Scope.Prefix]
		if signal == nil {
			signal = &pathWatchSignal{ch: make(chan struct{})}
			i.pathSignals[item.Scope.Prefix] = signal
		}
		signal.refs++
		signals[n] = signal
	}
	i.watchMutex.Unlock()
	defer func() {
		for n, item := range scopes {
			i.unsubscribePath(item.Scope.Prefix, signals[n])
		}
	}()
	for {
		cases := make([]reflect.SelectCase, len(signals)+1)
		cases[0] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}
		for n, signal := range signals {
			cases[n+1] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(i.pathSignal(signal))}
		}
		for _, item := range scopes {
			meta, err := i.ActiveMetadata(ctx, item.Scope)
			if err != nil {
				return nil, err
			}
			// An unrelated write in this collection does not expose data or spin
			// exact-key subscriptions. Equal content needs no retransmission.
			if meta.Root == item.Root && meta.Generation >= item.Generation {
				continue
			}
			full := item.Generation > meta.Generation
			page, err := i.ActiveSyncPage(ctx, ActiveSyncRequest{Scope: item.Scope,
				EndpointID: request.EndpointID, After: item.Generation, Full: full})
			if err != nil {
				return nil, err
			}
			return &ActiveWatchResult{Page: page}, nil
		}
		chosen, _, _ := reflect.Select(cases)
		if chosen == 0 {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return &ActiveWatchResult{}, nil
			}
			return nil, ctx.Err()
		}
	}
}
