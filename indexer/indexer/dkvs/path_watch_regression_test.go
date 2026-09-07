package dkvs

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

type watchTestDB struct {
	indexercommon.KVDB
	scans             atomic.Int64
	globalDeleteScans atomic.Int64
	failFlush         atomic.Bool
}

func (db *watchTestDB) BatchReadV2(prefix, seek []byte, reverse bool, visit func([]byte, []byte) error) error {
	db.scans.Add(1)
	if bytes.Equal(prefix, deleteKeyPrefix) {
		db.globalDeleteScans.Add(1)
	}
	return db.KVDB.BatchReadV2(prefix, seek, reverse, visit)
}

type watchTestBatch struct {
	indexercommon.WriteBatch
	db *watchTestDB
}

func (db *watchTestDB) NewWriteBatch() indexercommon.WriteBatch {
	return &watchTestBatch{WriteBatch: db.KVDB.NewWriteBatch(), db: db}
}

var errWatchTestFlush = errors.New("watch test flush failure")

func (batch *watchTestBatch) Flush() error {
	if batch.db.failFlush.Load() {
		return errWatchTestFlush
	}
	return batch.WriteBatch.Flush()
}

func assertPathSignal(t *testing.T, signal <-chan struct{}, wantClosed bool) {
	t.Helper()
	closed := false
	select {
	case <-signal:
		closed = true
	default:
	}
	if closed != wantClosed {
		t.Fatalf("path signal closed=%v, want %v", closed, wantClosed)
	}
}

func assertNoPathSubscriptions(t *testing.T, idx *Indexer) {
	t.Helper()
	idx.watchMutex.Lock()
	defer idx.watchMutex.Unlock()
	if len(idx.pathSignals) != 0 {
		t.Fatalf("retained %d path subscriptions", len(idx.pathSignals))
	}
}

func waitPathSubscribers(t *testing.T, idx *Indexer, path string, want int) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		idx.watchMutex.Lock()
		refs := 0
		if signal := idx.pathSignals[path]; signal != nil {
			refs = signal.refs
		}
		idx.watchMutex.Unlock()
		if refs == want {
			return
		}
		select {
		case <-timer.C:
			t.Fatalf("path subscribers=%d, want %d", refs, want)
		case <-tick.C:
		}
	}
}

func TestWaitPathIdleDoesNotPollHeight(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "dirty"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			var height atomic.Uint64
			height.Store(1)
			idx, priv, path := pathWatchFixture(t, &height)
			putPathWatchRecord(t, idx, priv, 1, 1, 100)
			meta, err := idx.GetPathMeta(path)
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				err = idx.db.Delete(pathMetaDBKey(path))
				meta.Generation = 0
			} else {
				var encoded []byte
				encoded, err = marshalPathStatus(&PathLocalStatus{Path: path, Dirty: true})
				if err == nil {
					err = idx.db.Write(pathStatusDBKey(path), encoded)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			db := &watchTestDB{KVDB: idx.db}
			idx.db = db
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			_, changed, err := idx.WaitPath(ctx, path, meta.Generation, meta.StateRoot, meta.ViewHeight)
			if changed || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			// One record/floor scan at entry and one final recheck at timeout.
			// There is no periodic height ticker and therefore no intermediate scan.
			if got := db.scans.Load(); got != 4 {
				t.Fatalf("DB scans=%d, want 4", got)
			}
			assertNoPathSubscriptions(t, idx)
		})
	}
}

func TestWaitPathReleasesAllExitPaths(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, 1, 100)
	if _, changed, err := idx.WaitPath(context.Background(), path, 0, chainhash.Hash{}, 0); err != nil || !changed {
		t.Fatalf("immediate change=%v err=%v", changed, err)
	}
	assertNoPathSubscriptions(t, idx)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := idx.WaitPath(ctx, path+"-empty", 0, chainhash.Hash{}, 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertNoPathSubscriptions(t, idx)
	if _, _, err := idx.WaitPath(ctx, "/tmp/"+strings.Repeat("a", 64), 0, chainhash.Hash{}, 0); !errors.Is(err, ErrFreeLocalNotRelayable) {
		t.Fatal(err)
	}
	assertNoPathSubscriptions(t, idx)
}

func TestWaitPathHeightAdvanceAloneDoesNotWake(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, 1, 1)
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		meta    *PathMeta
		changed bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		got, changed, waitErr := idx.WaitPath(ctx, path, meta.Generation, meta.StateRoot, 0)
		done <- result{meta: got, changed: changed, err: waitErr}
	}()
	waitPathSubscribers(t, idx, path, 1)
	height.Store(2)
	select {
	case got := <-done:
		t.Fatalf("height-only change woke waiter: %+v", got)
	case <-time.After(350 * time.Millisecond):
	}
	cancel()
	got := <-done
	// The final read can observe the expired logical view, but only after the
	// caller ends the wait; height advancement itself is not a wake source.
	if got.meta == nil || !got.changed || got.err != nil || got.meta.ActiveRecords != 0 {
		t.Fatalf("final expiry view=%+v changed=%v err=%v", got.meta, got.changed, got.err)
	}
	assertNoPathSubscriptions(t, idx)
}

func TestWaitPathCancelPreservesOtherSubscriber(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, 1, 100)
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	second, cancelSecond := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelSecond()
	doneFirst, doneSecond := make(chan error, 1), make(chan error, 1)
	go func() {
		_, _, err := idx.WaitPath(first, path, meta.Generation, meta.StateRoot, 0)
		doneFirst <- err
	}()
	go func() {
		_, changed, err := idx.WaitPath(second, path, meta.Generation, meta.StateRoot, 0)
		if err == nil && !changed {
			err = errors.New("second watcher did not observe mutation")
		}
		doneSecond <- err
	}()
	waitPathSubscribers(t, idx, path, 2)
	cancelFirst()
	if err := <-doneFirst; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waitPathSubscribers(t, idx, path, 1)
	putPathWatchRecord(t, idx, priv, 2, 1, 100)
	if err := <-doneSecond; err != nil {
		t.Fatal(err)
	}
	assertNoPathSubscriptions(t, idx)
}

func TestPathMutationSignalsFollowCommittedChanges(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, 1, 100)
	snapshot, err := idx.GetPathSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	watch := idx.subscribePath(path)
	defer idx.unsubscribePath(path, watch)
	signal := idx.pathSignal(watch)
	if _, err := idx.ApplyPathSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	assertPathSignal(t, signal, false)
	filters := []Subscription{{Type: SubscriptionPrefix, Target: path}}
	mirrorRoot, err := recordsRoot(snapshot.Records, height.Load())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.ApplyMirror(filters, snapshot.Records, mirrorRoot); err != nil {
		t.Fatal(err)
	}
	assertPathSignal(t, signal, false)
	emptyRoot, err := recordsRoot(nil, height.Load())
	if err != nil {
		t.Fatal(err)
	}
	db := &watchTestDB{KVDB: idx.db}
	idx.db = db
	db.failFlush.Store(true)
	if _, err := idx.ApplyMirror(filters, nil, emptyRoot); !errors.Is(err, errWatchTestFlush) {
		t.Fatal(err)
	}
	assertPathSignal(t, signal, false)
	db.failFlush.Store(false)
	if applied, err := idx.ApplyMirror(filters, nil, emptyRoot); err != nil || applied != 0 {
		t.Fatalf("deletion-only mirror applied=%d err=%v", applied, err)
	}
	assertPathSignal(t, signal, true)
	signal = idx.pathSignal(watch)
	if _, err := idx.ApplyPathSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	assertPathSignal(t, signal, true)

	tombstone := signedPersonalRecordWithPath(t, priv, "watch/item", 2, "", FlagTombstone)
	if _, err := idx.PutLocal(tombstone); err != nil {
		t.Fatal(err)
	}
	deleted, err := idx.GetPathSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Records) != 0 || len(deleted.DeleteFloors) != 1 {
		t.Fatalf("invalid deletion fixture: %+v", deleted)
	}
	if _, err := idx.ApplyPathSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	signal = idx.pathSignal(watch)
	db.failFlush.Store(true)
	if _, err := idx.ApplyPathSnapshot(deleted); !errors.Is(err, errWatchTestFlush) {
		t.Fatal(err)
	}
	assertPathSignal(t, signal, false)
	db.failFlush.Store(false)
	if applied, err := idx.ApplyPathSnapshot(deleted); err != nil || applied != 0 {
		t.Fatalf("deletion-only path snapshot applied=%d err=%v", applied, err)
	}
	assertPathSignal(t, signal, true)
	signal = idx.pathSignal(watch)
	if _, err := idx.ApplyPathSnapshot(deleted); err != nil {
		t.Fatal(err)
	}
	assertPathSignal(t, signal, false)
}

func TestPathSnapshotSignalsMetadataChanges(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, _, path := pathWatchFixture(t, &height)
	empty, err := idx.GetPathSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.db.Delete(pathMetaDBKey(path)); err != nil {
		t.Fatal(err)
	}
	watch := idx.subscribePath(path)
	defer idx.unsubscribePath(path, watch)
	signal := idx.pathSignal(watch)
	if _, err := idx.ApplyPathSnapshot(empty); err != nil {
		t.Fatal(err)
	}
	assertPathSignal(t, signal, false)
	for _, field := range []*uint64{&empty.PathMeta.Generation, &empty.PathMeta.ViewHeight} {
		*field++
		signal = idx.pathSignal(watch)
		if applied, err := idx.ApplyPathSnapshot(empty); err != nil || applied != 0 {
			t.Fatalf("metadata-only snapshot applied=%d err=%v", applied, err)
		}
		assertPathSignal(t, signal, true)
	}
}

func TestPathDeleteScansStayWithinPrefix(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	exactPath := "/blob/" + AccountID(priv.PubKey().SerializeCompressed()) + "/" + strings.Repeat("a", 64)
	for key, local := range map[string]bool{
		path + "/local": true, path + "/network": false,
		path + "-other/local": true, exactPath: true,
	} {
		encoded, err := marshalDeleteState(&deleteState{FloorSeq: 1, LocalOnly: local})
		if err != nil {
			t.Fatal(err)
		}
		if err := idx.db.Write(deleteDBKey(key), encoded); err != nil {
			t.Fatal(err)
		}
	}
	db := &watchTestDB{KVDB: idx.db}
	idx.db = db
	local, err := idx.scanEndpointDeleteStatesLocked(path)
	if err != nil || len(local) != 1 || local[path+"/local"] == nil {
		t.Fatalf("local floors=%v err=%v", local, err)
	}
	network, err := idx.scanPathDeleteStatesLocked(path)
	if err != nil || len(network) != 1 || network[path+"/network"] == nil {
		t.Fatalf("network floors=%v err=%v", network, err)
	}
	exact, err := idx.scanEndpointDeleteStatesLocked(exactPath)
	if err != nil || len(exact) != 1 || exact[exactPath] == nil {
		t.Fatalf("exact-key floors=%v err=%v", exact, err)
	}
	if db.globalDeleteScans.Load() != 0 {
		t.Fatal("path query scanned the global delete keyspace")
	}
}
