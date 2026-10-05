package dkvs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func signedCurrentDelete(t *testing.T, priv *btcec.PrivateKey, current *wire.DKVSRecord, height uint64) *wire.DKVSRecord {
	t.Helper()
	command, err := DeleteCommand(current, height)
	if err != nil { t.Fatal(err) }
	signRecord(t, priv, command)
	return command
}

func assertNoDeleteRows(t *testing.T, idx *Indexer) {
	t.Helper()
	count := 0
	if err := idx.db.BatchRead([]byte("dkvs:delete:"), false, func(_, _ []byte) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 0 { t.Fatalf("physical deletion retained %d history rows", count) }
}

func TestWaitFilteredForClientObservesRootChange(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	first := signedPersonalRecordWithKey(t, priv, 1, "one", 0)
	if _, err := idx.PutLocal(first); err != nil { t.Fatal(err) }
	filter := []Subscription{{Type: SubscriptionKey, Target: first.Key}}
	_, _, _, root, err := idx.SyncFilteredForClient(nil, 10, filter)
	if err != nil { t.Fatal(err) }
	second := signedPersonalRecordWithKey(t, priv, 2, "two", 0)
	go func() { time.Sleep(50 * time.Millisecond); _, _ = idx.PutLocal(second) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	next, changed, err := idx.WaitFilteredForClient(ctx, filter, root)
	if err != nil { t.Fatal(err) }
	if !changed || next == root { t.Fatalf("changed=%v root=%s next=%s", changed, root, next) }
}

func TestAbsentDeleteIsNoOpWithoutRetainedHistory(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	old := signedPersonalRecordWithKey(t, priv, 1, "never-stored-here", 0)
	command := signedCurrentDelete(t, priv, old, 1)
	hash := RecordHash(old)
	updated, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &hash})
	if err != nil || updated { t.Fatalf("absent delete updated=%v err=%v", updated, err) }
	state, err := idx.GetKeyState(old.Key)
	if err != nil || state.Status != KeyStateNeverSeen { t.Fatalf("absent key state=%+v err=%v", state, err) }
	if _, err := idx.GetForRelay(old.Key); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("absent delete became relayable: %v", err) }
	assertNoDeleteRows(t, idx)
}

func TestPhysicalDeleteAndSameBlockRecreate(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	original := signedRecordWithValue(t, priv, base+"/profile", 1, []byte("old"), 0)
	if updated, err := idx.PutLocal(original); err != nil || !updated { t.Fatalf("put original updated=%v err=%v", updated, err) }
	command := signedCurrentDelete(t, priv, original, 1)
	oldHash := RecordHash(original)
	if updated, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &oldHash}); err != nil || !updated {
		t.Fatalf("delete updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(original.Key); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("deleted key readable: %v", err) }
	if _, err := idx.GetByHash(oldHash); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("old hash remains indexed: %v", err) }
	if _, err := idx.GetForRelay(original.Key); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("delete retained for relay: %v", err) }
	fresh := signedRecordWithValue(t, priv, original.Key, 1, []byte("new-same-block-incarnation"), 0)
	if updated, err := idx.PutLocalCAS(fresh, WritePrecondition{ExpectAbsent: true}); err != nil || !updated {
		t.Fatalf("new incarnation updated=%v err=%v", updated, err)
	}
	// Equal IssueHeight and Seq are insufficient to identify a delete target.
	if _, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &oldHash}); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("delayed old delete removed the recreation: %v", err)
	}
	got, err := idx.Get(fresh.Key)
	if err != nil || RecordHash(got) != RecordHash(fresh) { t.Fatalf("recreated record=%+v err=%v", got, err) }
	meta, err := idx.GetPathMeta(base)
	if err != nil || meta.ActiveRecords != 1 || meta.ActiveTotalSize != uint64(RecordSize(fresh)) { t.Fatalf("path meta=%+v err=%v", meta, err) }
	assertNoDeleteRows(t, idx)
}

func TestRepeatedDeletesDoNotAccumulateHistory(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	for n := 0; n < 32; n++ {
		record := signedRecordWithValue(t, priv, fmt.Sprintf("%s/churn/%d", base, n), 1, []byte("value"), 0)
		if _, err := idx.PutLocalCAS(record, WritePrecondition{ExpectAbsent: true}); err != nil { t.Fatal(err) }
		hash := RecordHash(record)
		command := signedCurrentDelete(t, priv, record, 1)
		if _, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &hash}); err != nil { t.Fatal(err) }
		if updated, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &hash}); err != nil || updated {
			t.Fatalf("delete retry updated=%v err=%v", updated, err)
		}
	}
	assertNoDeleteRows(t, idx)
	// Generations belong to the canonical collection, not the read-only
	// account aggregate. Every create/delete commits once; retries do not.
	path := base + "/churn"
	meta, err := idx.GetPathMeta(path)
	if err != nil || meta.ActiveRecords != 0 || meta.StateRoot != (chainhash.Hash{}) { t.Fatalf("empty current state=%+v err=%v", meta, err) }
	if meta.Generation != 64 || meta.EndpointGeneration != 64 { t.Fatalf("empty collection lost or inflated its synchronization position: %+v", meta) }
	snapshot, err := idx.GetPathSnapshot(path)
	if err != nil || len(snapshot.Records) != 0 { t.Fatalf("empty current snapshot=%+v err=%v", snapshot, err) }
}

func TestPathMetaTracksPutUpdateDelete(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	first := signedRecordWithValue(t, priv, base+"/a", 1, []byte("a"), 0)
	second := signedRecordWithValue(t, priv, base+"/b", 1, []byte("bbbb"), 0)
	for _, record := range []*wire.DKVSRecord{first, second} {
		if _, err := idx.PutLocal(record); err != nil { t.Fatal(err) }
	}
	check := func(records ...*wire.DKVSRecord) {
		t.Helper()
		meta, err := idx.GetPathMeta(base)
		if err != nil { t.Fatal(err) }
		wantRoot, wantBytes := chainhash.Hash{}, uint64(0)
		for _, record := range records { xorPathMetaRoot(&wantRoot, record); wantBytes += uint64(RecordSize(record)) }
		if meta.ActiveRecords != uint64(len(records)) || meta.ActiveTotalSize != wantBytes || meta.StateRoot != wantRoot {
			t.Fatalf("meta=%+v want count=%d bytes=%d root=%s", meta, len(records), wantBytes, wantRoot)
		}
	}
	check(first, second)
	updatedFirst := signedRecordWithValue(t, priv, first.Key, 2, []byte("updated-value"), 0)
	if _, err := idx.PutLocal(updatedFirst); err != nil { t.Fatal(err) }
	check(updatedFirst, second)
	command := signedCurrentDelete(t, priv, second, 1)
	hash := RecordHash(second)
	if _, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &hash}); err != nil { t.Fatal(err) }
	check(updatedFirst)
	usage, err := idx.Usage(base)
	if err != nil || usage.ActiveRecords != 1 || usage.ActiveTotalSize != uint64(RecordSize(updatedFirst)) { t.Fatalf("usage=%+v err=%v", usage, err) }
	page, total, err := idx.ListPrefix(base, 0, 1)
	if err != nil || total != 1 || len(page) != 1 || page[0].Key != first.Key { t.Fatalf("page=%v total=%d err=%v", page, total, err) }
	assertNoDeleteRows(t, idx)
}

func TestMailboxZeroTTLReservedForPaidInternalRetention(t *testing.T) {
	idx := testIndexer(t)
	owner, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	sender, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	record := &wire.DKVSRecord{Version: Version,
		Key: testMailMsgKey(t, owner.PubKey().SerializeCompressed(), sender.PubKey().SerializeCompressed(), "m1"),
		Value: []byte("message"), Seq: 1, IssueHeight: 1}
	if updated, err := idx.PutInternalMailbox(record); err != nil || !updated { t.Fatalf("paid mailbox updated=%v err=%v", updated, err) }
	freeZero := cloneRecord(record)
	freeZero.Key = testMailMsgKey(t, owner.PubKey().SerializeCompressed(), sender.PubKey().SerializeCompressed(), "m2")
	freeZero.FeeProof, err = EncodeFeeProof(&FeeProof{Mode: FeeModeFreeLocal})
	if err != nil { t.Fatal(err) }
	if _, err := idx.PutInternalMailbox(freeZero); !errors.Is(err, ErrInvalidRecord) { t.Fatalf("FREE_LOCAL zero TTL err=%v", err) }
}

func TestFilteredSyncIncludesOnlyCurrentRecords(t *testing.T) {
	source := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	active := signedRecordWithValue(t, priv, base+"/sync/active", 1, []byte("active"), 0)
	deleted := signedRecordWithValue(t, priv, base+"/sync/deleted", 1, []byte("old"), 0)
	outside := signedRecordWithValue(t, priv, base+"/other", 1, []byte("outside"), 0)
	for _, record := range []*wire.DKVSRecord{active, deleted, outside} {
		if _, err := source.PutLocal(record); err != nil { t.Fatal(err) }
	}
	hash := RecordHash(deleted)
	if _, err := source.PutLocalCAS(signedCurrentDelete(t, priv, deleted, 1), WritePrecondition{ExpectedHash: &hash}); err != nil { t.Fatal(err) }
	filters := []Subscription{{Type: SubscriptionPrefix, Target: base+"/sync"}}
	var cursor []byte
	var collected []*wire.DKVSRecord
	for pages := 0; ; pages++ {
		if pages > 8 { t.Fatal("filtered current sync did not terminate") }
		records, next, done, _, err := source.SyncFiltered(cursor, 1, filters)
		if err != nil { t.Fatal(err) }
		for _, record := range records {
			if !SubscriptionMatchesKey(filters[0], record.Key) || IsTombstone(record.Flags) || record.Key == deleted.Key {
				t.Fatalf("current sync returned a deleted/out-of-scope record: %+v", record)
			}
			collected = append(collected, record)
		}
		if done { break }
		if len(next) == 0 { t.Fatal("sync did not advance cursor") }
		cursor = next
	}
	if len(collected) != 1 || RecordHash(collected[0]) != RecordHash(active) { t.Fatalf("current filtered set=%v", collected) }
	assertNoDeleteRows(t, source)
}

func TestMirrorDeleteDoesNotCreateHistory(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	record := signedPersonalRecordWithKey(t, priv, 5, "value", 0)
	if _, err := idx.PutLocal(record); err != nil { t.Fatal(err) }
	deleted, err := idx.DeleteMirrorKeys([]string{record.Key})
	if err != nil || deleted != 1 { t.Fatalf("mirror delete count=%d err=%v", deleted, err) }
	if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("mirror-deleted record readable: %v", err) }
	if _, err := idx.GetForRelay(record.Key); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("synthetic mirror delete relayable: %v", err) }
	assertNoDeleteRows(t, idx)
	fresh := signedPersonalRecordWithKey(t, priv, 1, "new-life", 0)
	if updated, err := idx.PutLocalCAS(fresh, WritePrecondition{ExpectAbsent: true}); err != nil || !updated { t.Fatalf("fresh recreate updated=%v err=%v", updated, err) }
}

type blockingDIDResolver struct {
	started chan struct{}
	release chan struct{}
	once sync.Once
	mutex sync.Mutex
	calls int
	identity DIDIdentity
}

func (r *blockingDIDResolver) resolve() (DIDIdentity, error) {
	r.mutex.Lock()
	r.calls++
	first := r.calls == 1
	r.mutex.Unlock()
	r.once.Do(func() { close(r.started) })
	// Delay only the request under test. A second authorized writer must be
	// able to finish; it also resolves the current owner under the new policy.
	if first { <-r.release }
	return r.identity, nil
}
func (r *blockingDIDResolver) callCount() int { r.mutex.Lock(); defer r.mutex.Unlock(); return r.calls }
func (r *blockingDIDResolver) ResolveName(string) (DIDIdentity, error) { return r.resolve() }
func (r *blockingDIDResolver) ResolveService(string) (DIDIdentity, error) { return r.resolve() }

func TestResolverDoesNotHoldIndexerWriteLock(t *testing.T) {
	personalKey, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	nameKey, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	resolver := &blockingDIDResolver{started: make(chan struct{}), release: make(chan struct{}),
		identity: DIDIdentity{CanonicalName: "alice", NameID: "alice", SigningKeys: [][]byte{nameKey.PubKey().SerializeCompressed()}, Active: true}}
	var release sync.Once
	defer release.Do(func() { close(resolver.release) })
	idx := testIndexerWithConfig(t, Config{AllowFreeLocal: true, Resolver: resolver})
	personal := signedPersonalRecordWithKey(t, personalKey, 1, "value", 0)
	if _, err := idx.PutLocal(personal); err != nil { t.Fatal(err) }
	name := signedRecordForKey(t, nameKey, "/name/alice", 1)
	putDone := make(chan error, 1)
	go func() { _, err := idx.PutLocal(name); putDone <- err }()
	select { case <-resolver.started: case <-time.After(2*time.Second): t.Fatal("resolver was not called") }
	getDone := make(chan error, 1)
	go func() { _, err := idx.Get(personal.Key); getDone <- err }()
	select {
	case err := <-getDone: if err != nil { t.Fatalf("unrelated get failed: %v", err) }
	case <-time.After(500*time.Millisecond): t.Fatal("resolver held the indexer write lock")
	}
	release.Do(func() { close(resolver.release) })
	select { case err := <-putDone: if err != nil { t.Fatal(err) }; case <-time.After(2*time.Second): t.Fatal("name put did not finish") }
}

func TestResolverResultRecheckedAgainstRecordSequence(t *testing.T) {
	oldOwner, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	newOwner, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	idx := testIndexerWithConfig(t, Config{AllowFreeLocal: true, Resolver: StaticDIDResolver{Names: map[string]DIDIdentity{
		"alice": {CanonicalName: "alice", NameID: "alice", SigningKeys: [][]byte{oldOwner.PubKey().SerializeCompressed()}, Active: true},
	}}})
	original := signedRecordForKey(t, oldOwner, "/name/alice", 1)
	if _, err := idx.PutLocal(original); err != nil { t.Fatal(err) }
	resolver := &blockingDIDResolver{started: make(chan struct{}), release: make(chan struct{}),
		identity: DIDIdentity{CanonicalName: "alice", NameID: "alice", SigningKeys: [][]byte{newOwner.PubKey().SerializeCompressed()}, Active: true}}
	var release sync.Once
	defer release.Do(func() { close(resolver.release) })
	idx.SetResolver(resolver)
	delayed := signedRecordForKey(t, newOwner, "/name/alice", 1)
	done := make(chan error, 1)
	go func() { _, err := idx.PutLocal(delayed); done <- err }()
	select { case <-resolver.started: case <-time.After(2*time.Second): t.Fatal("resolver was not called") }
	// The old owner is no longer authorized. The concurrent mutation must be
	// signed by the actual successor, not exploit the removed signer shortcut.
	concurrent := signedRecordForKey(t, newOwner, "/name/alice", 2)
	if _, err := idx.PutLocal(concurrent); err != nil { t.Fatalf("authorized concurrent write: %v", err) }
	release.Do(func() { close(resolver.release) })
	select {
	case err := <-done: if err != nil { t.Fatalf("delayed write failed: %v", err) }
	case <-time.After(2*time.Second): t.Fatal("delayed write did not finish")
	}
	if resolver.callCount() < 3 { t.Fatalf("resolver calls=%d; delayed request did not retry after the state change", resolver.callCount()) }
	got, err := idx.Get("/name/alice")
	if err != nil || RecordHash(got) != RecordHash(concurrent) { t.Fatalf("newer authorized state rolled back: record=%+v err=%v", got, err) }
}

func TestApplySnapshotPrevalidatesBeforeMutation(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	first := signedPersonalRecordWithPath(t, priv, "a", 1, "a", 0)
	second := signedPersonalRecordWithPath(t, priv, "b", 1, "b", 0)
	second.Signature = []byte{1, 2, 3}
	records := []*wire.DKVSRecord{first, second}
	checkpoint, err := checkpointFromRecords(records, 1)
	if err != nil { t.Fatal(err) }
	if _, err := target.ApplySnapshot(&Snapshot{Checkpoint: checkpoint, Records: records, CreatedAt: currentUnixMilli()}); err == nil { t.Fatal("invalid trailing record accepted") }
	if _, err := target.Get(first.Key); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("snapshot partially applied: %v", err) }
}

func TestApplyMirrorAtomicallyReplacesOmissions(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	keepOld := signedRecordWithValue(t, priv, base+"/keep", 1, []byte("old"), 0)
	omitted := signedRecordWithValue(t, priv, base+"/omitted", 1, []byte("remove"), 0)
	for _, record := range []*wire.DKVSRecord{keepOld, omitted} { if _, err := target.PutLocal(record); err != nil { t.Fatal(err) } }
	keepNew := signedRecordWithValue(t, priv, keepOld.Key, 2, []byte("new"), 0)
	added := signedRecordWithValue(t, priv, base+"/added", 1, []byte("added"), 0)
	records := []*wire.DKVSRecord{added, keepNew}
	root, err := recordsRoot(records, 1)
	if err != nil { t.Fatal(err) }
	if _, err := target.ApplyMirror([]Subscription{{Type: SubscriptionPrefix, Target: base}}, records, root); err != nil { t.Fatal(err) }
	for _, want := range records {
		if got, err := target.Get(want.Key); err != nil || RecordHash(got) != RecordHash(want) { t.Fatalf("record=%+v err=%v", got, err) }
	}
	if _, err := target.Get(omitted.Key); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("omitted record remains: %v", err) }
	meta, err := target.GetPathMeta(base)
	if err != nil || meta.ActiveRecords != 2 { t.Fatalf("mirror path meta=%+v err=%v", meta, err) }
}

func TestApplyMirrorRootMismatchLeavesStateUnchanged(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	record := signedPersonalRecordWithPath(t, priv, "keep", 1, "value", 0)
	if _, err := target.PutLocal(record); err != nil { t.Fatal(err) }
	filter := []Subscription{{Type: SubscriptionPrefix, Target: "/personal/"+AccountID(priv.PubKey().SerializeCompressed())}}
	if _, err := target.ApplyMirror(filter, nil, chainhash.DoubleHashH([]byte("wrong"))); !errors.Is(err, ErrInvalidSnapshot) { t.Fatalf("root mismatch err=%v", err) }
	if got, err := target.Get(record.Key); err != nil || RecordHash(got) != RecordHash(record) { t.Fatalf("root mismatch mutated record=%+v err=%v", got, err) }
}

func TestApplyMirrorPreservesExpiredPaidRecord(t *testing.T) {
	height := uint64(1)
	target := testIndexerWithConfig(t, Config{CurrentHeight: func() uint64 { return height }, FeeVerifier: testFeeVerifier{}})
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	record := signedPersonalRecordWithPath(t, priv, "paid", 1, "value", 0)
	record.IssueHeight, record.TTL, record.FeeProof = 1, 1, []byte{1}
	signRecord(t, priv, record)
	if _, err := target.PutLocal(record); err != nil { t.Fatal(err) }
	height = 3
	root, err := recordsRoot(nil, height)
	if err != nil { t.Fatal(err) }
	filter := []Subscription{{Type: SubscriptionPrefix, Target: "/personal/"+AccountID(priv.PubKey().SerializeCompressed())}}
	if _, err := target.ApplyMirror(filter, nil, root); err != nil { t.Fatal(err) }
	if _, err := target.Get(record.Key); !errors.Is(err, ErrRecordNotFound) { t.Fatalf("expired record active: %v", err) }
	target.mutex.RLock()
	stored, err := target.getRaw(record.Key)
	target.mutex.RUnlock()
	if err != nil || RecordHash(stored) != RecordHash(record) { t.Fatalf("paid expired record removed=%+v err=%v", stored, err) }
}

func TestApplySnapshotDoesNotRollbackNewerRecord(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	newer := signedPersonalRecordWithPath(t, priv, "profile", 2, "new", 0)
	if _, err := target.PutLocal(newer); err != nil { t.Fatal(err) }
	older := signedPersonalRecordWithPath(t, priv, "profile", 1, "old", 0)
	checkpoint, err := checkpointFromRecords([]*wire.DKVSRecord{older}, 1)
	if err != nil { t.Fatal(err) }
	applied, err := target.ApplySnapshot(&Snapshot{Checkpoint: checkpoint, Records: []*wire.DKVSRecord{older}, CreatedAt: currentUnixMilli()})
	if err != nil || applied != 0 { t.Fatalf("older snapshot applied=%d err=%v", applied, err) }
	if got, err := target.Get(newer.Key); err != nil || RecordHash(got) != RecordHash(newer) { t.Fatalf("newer record rolled back=%+v err=%v", got, err) }
}
