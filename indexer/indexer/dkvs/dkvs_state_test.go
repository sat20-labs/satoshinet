package dkvs

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestWaitFilteredForClientObservesRootChange(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordWithKey(t, priv, 1, "one", 0)
	if _, err := idx.PutLocal(first); err != nil {
		t.Fatal(err)
	}
	filter := []Subscription{{Type: SubscriptionKey, Target: first.Key}}
	_, _, _, root, err := idx.SyncFilteredForClient(nil, 10, filter)
	if err != nil {
		t.Fatal(err)
	}
	second := signedPersonalRecordWithKey(t, priv, 2, "two", 0)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = idx.PutLocal(second)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	next, changed, err := idx.WaitFilteredForClient(ctx, filter, root)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || next == root {
		t.Fatalf("changed=%v root=%s next=%s", changed, root, next)
	}
}

func TestMissingDeleteCommandIsRetainedOnlyForRelay(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	tombstone := signedPersonalRecordWithKey(t, priv, 1, "", FlagTombstone)
	updated, err := idx.PutRemote(tombstone)
	if err != nil || !updated {
		t.Fatalf("missing tombstone updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(tombstone.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing tombstone created active record: %v", err)
	}
	idx.mutex.RLock()
	_, stateErr := idx.getDeleteStateLocked(tombstone.Key)
	idx.mutex.RUnlock()
	if stateErr != nil {
		t.Fatalf("missing delete command not retained for relay: %v", stateErr)
	}
	if relay, err := idx.GetForRelay(tombstone.Key); err != nil || RecordHash(relay) != RecordHash(tombstone) {
		t.Fatalf("missing delete command relay=%#v err=%v", relay, err)
	}
}

func TestPhysicalDeleteFloorAndRecreate(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	key := base + "/profile"
	original := signedRecordWithValue(t, priv, key, 1, []byte("old"), 0)
	if updated, err := idx.PutLocal(original); err != nil || !updated {
		t.Fatalf("put original updated=%v err=%v", updated, err)
	}
	tombstone := signedRecordWithValue(t, priv, key, 2, nil, FlagTombstone)
	if updated, err := idx.PutLocal(tombstone); err != nil || !updated {
		t.Fatalf("delete updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("deleted key remains readable: %v", err)
	}
	if _, err := idx.GetByHash(RecordHash(original)); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("deleted record hash remains indexed: %v", err)
	}
	relay, err := idx.GetForRelay(key)
	if err != nil || !IsTombstone(relay.Flags) || relay.Seq != 2 {
		t.Fatalf("relay delete=%#v err=%v", relay, err)
	}
	stale := signedRecordWithValue(t, priv, key, 2, []byte("stale"), 0)
	if updated, err := idx.PutRemote(stale); err != nil || updated {
		t.Fatalf("stale replay updated=%v err=%v", updated, err)
	}
	fresh := signedRecordWithValue(t, priv, key, 3, []byte("fresh"), 0)
	if updated, err := idx.PutRemote(fresh); err != nil || !updated {
		t.Fatalf("fresh recreate updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(key)
	if err != nil || string(got.Value) != "fresh" {
		t.Fatalf("fresh record=%#v err=%v", got, err)
	}
	idx.mutex.RLock()
	_, stateErr := idx.getDeleteStateLocked(key)
	idx.mutex.RUnlock()
	if !errors.Is(stateErr, ErrRecordNotFound) {
		t.Fatalf("delete state not cleared after recreate: %v", stateErr)
	}
	meta, err := idx.GetPathMeta(base)
	if err != nil || meta.ActiveRecords != 1 || meta.ActiveTotalSize != uint64(RecordSize(fresh)) {
		t.Fatalf("path meta=%#v err=%v", meta, err)
	}
}

func TestDeleteCommandCompactionPreservesFloor(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	tombstone := signedPersonalRecordWithKey(t, priv, 2, "", FlagTombstone)
	if _, err := idx.PutLocal(tombstone); err != nil {
		t.Fatal(err)
	}

	idx.mutex.Lock()
	state, err := idx.getDeleteStateLocked(record.Key)
	if err != nil {
		idx.mutex.Unlock()
		t.Fatal(err)
	}
	state.RelayUntil = currentUnixMilli() - 1
	batch := idx.db.NewWriteBatch()
	if err := putDeleteStateBatch(batch, record.Key, state); err == nil {
		err = batch.Flush()
	}
	batch.Close()
	idx.mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := idx.PruneExpiredAt(1); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.GetForRelay(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expired delete command still relayable: %v", err)
	}
	idx.mutex.RLock()
	state, err = idx.getDeleteStateLocked(record.Key)
	idx.mutex.RUnlock()
	if err != nil || state == nil || state.FloorSeq != tombstone.Seq || state.Record != nil || state.RelayUntil != 0 {
		t.Fatalf("compacted journal state=%#v err=%v", state, err)
	}
	if updated, err := idx.PutRemote(record); err != nil || updated {
		t.Fatalf("compacted delete floor allowed stale resurrection updated=%v err=%v", updated, err)
	}
	fresh := signedPersonalRecordWithKey(t, priv, 3, "fresh", 0)
	if updated, err := idx.PutRemote(fresh); err != nil || !updated {
		t.Fatalf("higher sequence did not clear compacted floor updated=%v err=%v", updated, err)
	}
}

func TestPathMetaTracksPutUpdateDelete(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	first := signedRecordWithValue(t, priv, base+"/a", 1, []byte("a"), 0)
	second := signedRecordWithValue(t, priv, base+"/b", 1, []byte("bbbb"), 0)
	if _, err := idx.PutLocal(first); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(second); err != nil {
		t.Fatal(err)
	}
	meta, err := idx.GetPathMeta(base)
	wantBytes := uint64(RecordSize(first) + RecordSize(second))
	if err != nil || meta.ActiveRecords != 2 || meta.ActiveTotalSize != wantBytes {
		t.Fatalf("after puts meta=%#v wantBytes=%d err=%v", meta, wantBytes, err)
	}
	wantRoot := chainhash.Hash{}
	xorPathMetaRoot(&wantRoot, first)
	xorPathMetaRoot(&wantRoot, second)
	if meta.StateRoot != wantRoot {
		t.Fatalf("after puts root=%s want=%s", meta.StateRoot, wantRoot)
	}

	updatedFirst := signedRecordWithValue(t, priv, first.Key, 2, []byte("updated-value"), 0)
	if _, err := idx.PutLocal(updatedFirst); err != nil {
		t.Fatal(err)
	}
	meta, err = idx.GetPathMeta(base)
	wantBytes = uint64(RecordSize(updatedFirst) + RecordSize(second))
	if err != nil || meta.ActiveRecords != 2 || meta.ActiveTotalSize != wantBytes {
		t.Fatalf("after update meta=%#v wantBytes=%d err=%v", meta, wantBytes, err)
	}
	wantRoot = chainhash.Hash{}
	xorPathMetaRoot(&wantRoot, updatedFirst)
	xorPathMetaRoot(&wantRoot, second)
	if meta.StateRoot != wantRoot {
		t.Fatalf("after update root=%s want=%s", meta.StateRoot, wantRoot)
	}

	deleteSecond := signedRecordWithValue(t, priv, second.Key, 2, nil, FlagTombstone)
	if _, err := idx.PutLocal(deleteSecond); err != nil {
		t.Fatal(err)
	}
	meta, err = idx.GetPathMeta(base)
	if err != nil || meta.ActiveRecords != 1 || meta.ActiveTotalSize != uint64(RecordSize(updatedFirst)) {
		t.Fatalf("after delete meta=%#v err=%v", meta, err)
	}
	idx.mutex.RLock()
	floor, floorErr := idx.getDeleteStateLocked(second.Key)
	idx.mutex.RUnlock()
	if floorErr != nil {
		t.Fatal(floorErr)
	}
	wantRoot = chainhash.Hash{}
	xorPathMetaRoot(&wantRoot, updatedFirst)
	xorDeleteFloorRoot(&wantRoot, second.Key, floor)
	if meta.StateRoot != wantRoot {
		t.Fatalf("after delete root=%s want=%s", meta.StateRoot, wantRoot)
	}
	usage, err := idx.Usage(base)
	if err != nil || usage.ActiveRecords != meta.ActiveRecords || usage.ActiveTotalSize != meta.ActiveTotalSize {
		t.Fatalf("usage=%#v meta=%#v err=%v", usage, meta, err)
	}
	page, total, err := idx.ListPrefix(base, 0, 1)
	if err != nil || total != 1 || len(page) != 1 || page[0].Key != first.Key {
		t.Fatalf("page=%v total=%d err=%v", page, total, err)
	}
}

func TestMailboxRejectsZeroTTL(t *testing.T) {
	idx := testIndexer(t)
	owner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedRecordWithValue(t, sender, testMailMsgKey(t, owner.PubKey().SerializeCompressed(), sender.PubKey().SerializeCompressed(), "m1"), 1, []byte("message"), 0)
	record.TTL = 0
	signRecord(t, sender, record)
	if _, err := idx.PutLocal(record); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("zero TTL mailbox record err=%v", err)
	}
}

func TestFilteredSyncIncludesDeleteCommands(t *testing.T) {
	source := testIndexer(t)
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	active := signedRecordWithValue(t, priv, base+"/sync/active", 1, []byte("active"), 0)
	deleted := signedRecordWithValue(t, priv, base+"/sync/deleted", 1, []byte("old"), 0)
	outside := signedRecordWithValue(t, priv, base+"/other", 1, []byte("outside"), 0)
	for _, record := range []*wire.DKVSRecord{active, deleted, outside} {
		if _, err := source.PutLocal(record); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := target.PutLocal(deleted); err != nil {
		t.Fatal(err)
	}
	tombstone := signedRecordWithValue(t, priv, deleted.Key, 2, nil, FlagTombstone)
	if _, err := source.PutLocal(tombstone); err != nil {
		t.Fatal(err)
	}

	filters := []Subscription{{Type: SubscriptionPrefix, Target: base + "/sync"}}
	var cursor []byte
	var gotActive, gotDelete bool
	for {
		records, next, done, _, err := source.SyncFiltered(cursor, 1, filters)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if !SubscriptionMatchesKey(filters[0], record.Key) {
				t.Fatalf("sync returned outside key %s", record.Key)
			}
			if record.Key == active.Key && !IsTombstone(record.Flags) {
				gotActive = true
			}
			if record.Key == deleted.Key && IsTombstone(record.Flags) {
				gotDelete = true
			}
			if _, err := target.PutRemote(record); err != nil {
				t.Fatalf("apply %s: %v", record.Key, err)
			}
		}
		if done {
			break
		}
		if len(next) == 0 {
			t.Fatal("sync did not advance cursor")
		}
		cursor = next
	}
	if !gotActive || !gotDelete {
		t.Fatalf("gotActive=%v gotDelete=%v", gotActive, gotDelete)
	}
	if _, err := target.Get(deleted.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("delete command did not remove target key: %v", err)
	}
	if _, err := target.Get(outside.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("filtered sync copied outside key: %v", err)
	}
}

func TestMirrorDeleteDoesNotCreateFloor(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 5, "value", 0)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	deleted, err := idx.DeleteMirrorKeys([]string{record.Key})
	if err != nil || deleted != 1 {
		t.Fatalf("mirror delete count=%d err=%v", deleted, err)
	}
	if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("mirror-deleted record readable: %v", err)
	}
	if _, err := idx.GetForRelay(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("synthetic mirror delete became relayable: %v", err)
	}
	if updated, err := idx.PutRemote(record); err != nil || !updated {
		t.Fatalf("mirror omission prevented later restoration updated=%v err=%v", updated, err)
	}
	fresh := signedPersonalRecordWithKey(t, priv, 6, "fresh", 0)
	if updated, err := idx.PutRemote(fresh); err != nil || !updated {
		t.Fatalf("fresh mirror recreate updated=%v err=%v", updated, err)
	}
}

type blockingDIDResolver struct {
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
	mutex    sync.Mutex
	calls    int
	identity DIDIdentity
}

func (r *blockingDIDResolver) resolve() (DIDIdentity, error) {
	r.mutex.Lock()
	r.calls++
	r.mutex.Unlock()
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.identity, nil
}

func (r *blockingDIDResolver) callCount() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.calls
}

func (r *blockingDIDResolver) ResolveName(string) (DIDIdentity, error) {
	return r.resolve()
}

func (r *blockingDIDResolver) ResolveService(string) (DIDIdentity, error) {
	return r.resolve()
}

func TestResolverDoesNotHoldIndexerWriteLock(t *testing.T) {
	personalKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	nameKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &blockingDIDResolver{
		started: make(chan struct{}),
		release: make(chan struct{}),
		identity: DIDIdentity{
			CanonicalName: "alice",
			NameID:        "alice",
			SigningKeys:   [][]byte{nameKey.PubKey().SerializeCompressed()},
			Active:        true,
		},
	}
	idx := testIndexerWithConfig(t, Config{AllowFreeLocal: true, Resolver: resolver})
	personal := signedPersonalRecordWithKey(t, personalKey, 1, "value", 0)
	if _, err := idx.PutLocal(personal); err != nil {
		t.Fatal(err)
	}
	name := signedRecordForKey(t, nameKey, "/name/alice", 1)
	putDone := make(chan error, 1)
	go func() {
		_, err := idx.PutLocal(name)
		putDone <- err
	}()
	select {
	case <-resolver.started:
	case <-time.After(2 * time.Second):
		t.Fatal("resolver was not called")
	}

	getDone := make(chan error, 1)
	go func() {
		_, err := idx.Get(personal.Key)
		getDone <- err
	}()
	select {
	case err := <-getDone:
		if err != nil {
			t.Fatalf("unrelated get failed: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("resolver call held the indexer write lock")
	}
	close(resolver.release)
	select {
	case err := <-putDone:
		if err != nil {
			t.Fatalf("name put failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("name put did not finish")
	}
}

func TestResolverResultRecheckedAgainstRecordSequence(t *testing.T) {
	oldOwner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newOwner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	oldResolver := StaticDIDResolver{Names: map[string]DIDIdentity{
		"alice": {
			CanonicalName: "alice", NameID: "alice",
			SigningKeys: [][]byte{oldOwner.PubKey().SerializeCompressed()}, Active: true,
		},
	}}
	idx := testIndexerWithConfig(t, Config{AllowFreeLocal: true, Resolver: oldResolver})
	original := signedRecordForKey(t, oldOwner, "/name/alice", 1)
	if _, err := idx.PutLocal(original); err != nil {
		t.Fatal(err)
	}

	resolver := &blockingDIDResolver{
		started: make(chan struct{}), release: make(chan struct{}),
		identity: DIDIdentity{
			CanonicalName: "alice", NameID: "alice",
			SigningKeys: [][]byte{newOwner.PubKey().SerializeCompressed()}, Active: true,
		},
	}
	idx.SetResolver(resolver)
	rotated := signedRecordForKey(t, newOwner, "/name/alice", 1)
	done := make(chan error, 1)
	go func() {
		_, err := idx.PutLocal(rotated)
		done <- err
	}()
	select {
	case <-resolver.started:
	case <-time.After(2 * time.Second):
		t.Fatal("resolver was not called")
	}

	concurrent := signedRecordForKey(t, oldOwner, "/name/alice", 2)
	if _, err := idx.PutLocal(concurrent); err != nil {
		t.Fatalf("concurrent higher-seq update failed: %v", err)
	}
	close(resolver.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("owner rotation failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner rotation did not finish")
	}
	if resolver.callCount() < 2 {
		t.Fatalf("resolver calls=%d want retry after seq change", resolver.callCount())
	}
	got, err := idx.Get("/name/alice")
	if err != nil || !bytes.Equal(got.PubKey, rotated.PubKey) {
		t.Fatalf("rotated record=%#v err=%v", got, err)
	}
}

func TestApplySnapshotPrevalidatesBeforeMutation(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordWithPath(t, priv, "a", 1, "a", 0)
	second := signedPersonalRecordWithPath(t, priv, "b", 1, "b", 0)
	second.Signature = []byte{1, 2, 3}
	records := []*wire.DKVSRecord{first, second}
	checkpoint, err := checkpointFromRecords(records, 1)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &Snapshot{Checkpoint: checkpoint, Records: records, CreatedAt: currentUnixMilli()}
	if _, err := target.ApplySnapshot(snapshot); err == nil {
		t.Fatal("snapshot with invalid trailing record was accepted")
	}
	if _, err := target.Get(first.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("snapshot partially applied before validation: %v", err)
	}
}

func TestApplyMirrorAtomicallyReplacesOmissions(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	keepOld := signedRecordWithValue(t, priv, base+"/keep", 1, []byte("old"), 0)
	omitted := signedRecordWithValue(t, priv, base+"/omitted", 1, []byte("remove"), 0)
	if _, err := target.PutLocal(keepOld); err != nil {
		t.Fatal(err)
	}
	if _, err := target.PutLocal(omitted); err != nil {
		t.Fatal(err)
	}
	keepNew := signedRecordWithValue(t, priv, keepOld.Key, 2, []byte("new"), 0)
	added := signedRecordWithValue(t, priv, base+"/added", 1, []byte("added"), 0)
	records := []*wire.DKVSRecord{added, keepNew}
	root, err := recordsRoot(records, 1)
	if err != nil {
		t.Fatal(err)
	}
	filters := []Subscription{{Type: SubscriptionPrefix, Target: base}}
	if _, err := target.ApplyMirror(filters, records, root); err != nil {
		t.Fatal(err)
	}
	if got, err := target.Get(keepOld.Key); err != nil || string(got.Value) != "new" {
		t.Fatalf("kept record=%#v err=%v", got, err)
	}
	if _, err := target.Get(omitted.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("omitted record remains: %v", err)
	}
	if got, err := target.Get(added.Key); err != nil || string(got.Value) != "added" {
		t.Fatalf("added record=%#v err=%v", got, err)
	}
	meta, err := target.GetPathMeta(base)
	if err != nil || meta.ActiveRecords != 2 {
		t.Fatalf("mirror path meta=%#v err=%v", meta, err)
	}
}

func TestApplyMirrorRootMismatchLeavesStateUnchanged(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithPath(t, priv, "keep", 1, "value", 0)
	if _, err := target.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	badRoot := chainhash.DoubleHashH([]byte("wrong"))
	filter := []Subscription{{Type: SubscriptionPrefix, Target: "/personal/" + AccountID(priv.PubKey().SerializeCompressed())}}
	if _, err := target.ApplyMirror(filter, nil, badRoot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("root mismatch err=%v", err)
	}
	if got, err := target.Get(record.Key); err != nil || RecordHash(got) != RecordHash(record) {
		t.Fatalf("root mismatch mutated record=%#v err=%v", got, err)
	}
}

func TestApplyMirrorPreservesExpiredPaidRecord(t *testing.T) {
	height := uint64(1)
	target := testIndexerWithConfig(t, Config{
		CurrentHeight: func() uint64 { return height },
		FeeVerifier:   testFeeVerifier{},
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithPath(t, priv, "paid", 1, "value", 0)
	record.IssueHeight = 1
	record.TTL = 1
	record.FeeProof = []byte{1}
	signRecord(t, priv, record)
	if _, err := target.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	height = 3
	root, err := recordsRoot(nil, height)
	if err != nil {
		t.Fatal(err)
	}
	filter := []Subscription{{Type: SubscriptionPrefix, Target: "/personal/" + AccountID(priv.PubKey().SerializeCompressed())}}
	if _, err := target.ApplyMirror(filter, nil, root); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("expired record remains active: %v", err)
	}
	target.mutex.RLock()
	stored, err := target.getRaw(record.Key)
	target.mutex.RUnlock()
	if err != nil || RecordHash(stored) != RecordHash(record) {
		t.Fatalf("mirror physically removed paid expired record=%#v err=%v", stored, err)
	}
}

func TestApplySnapshotDoesNotRollbackNewerRecord(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newer := signedPersonalRecordWithPath(t, priv, "profile", 2, "new", 0)
	if _, err := target.PutLocal(newer); err != nil {
		t.Fatal(err)
	}
	older := signedPersonalRecordWithPath(t, priv, "profile", 1, "old", 0)
	checkpoint, err := checkpointFromRecords([]*wire.DKVSRecord{older}, 1)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := target.ApplySnapshot(&Snapshot{
		Checkpoint: checkpoint,
		Records:    []*wire.DKVSRecord{older},
		CreatedAt:  currentUnixMilli(),
	})
	if err != nil || applied != 0 {
		t.Fatalf("older snapshot applied=%d err=%v", applied, err)
	}
	got, err := target.Get(newer.Key)
	if err != nil || got.Seq != newer.Seq || string(got.Value) != "new" {
		t.Fatalf("newer record rolled back: record=%#v err=%v", got, err)
	}
}
