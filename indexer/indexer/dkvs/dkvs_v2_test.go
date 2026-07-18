package dkvs

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestMissingTombstoneIsNoOp(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	tombstone := signedPersonalRecordWithKey(t, priv, 1, "", FlagTombstone)
	updated, err := idx.PutRemote(tombstone)
	if err != nil || updated {
		t.Fatalf("missing tombstone updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(tombstone.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing tombstone created active record: %v", err)
	}
	idx.mutex.RLock()
	_, stateErr := idx.getDeleteStateLocked(tombstone.Key)
	idx.mutex.RUnlock()
	if !errors.Is(stateErr, ErrRecordNotFound) {
		t.Fatalf("missing tombstone created delete state: %v", stateErr)
	}
	if _, err := idx.GetForRelay(tombstone.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing tombstone became relayable: %v", err)
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

func TestDeleteCommandCompactionKeepsFloor(t *testing.T) {
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
	if err != nil || state.Record != nil || state.FloorSeq != 2 {
		t.Fatalf("compacted state=%#v err=%v", state, err)
	}
	if updated, err := idx.PutRemote(record); err != nil || updated {
		t.Fatalf("compacted floor allowed stale replay updated=%v err=%v", updated, err)
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

	updatedFirst := signedRecordWithValue(t, priv, first.Key, 2, []byte("updated-value"), 0)
	if _, err := idx.PutLocal(updatedFirst); err != nil {
		t.Fatal(err)
	}
	meta, err = idx.GetPathMeta(base)
	wantBytes = uint64(RecordSize(updatedFirst) + RecordSize(second))
	if err != nil || meta.ActiveRecords != 2 || meta.ActiveTotalSize != wantBytes {
		t.Fatalf("after update meta=%#v wantBytes=%d err=%v", meta, wantBytes, err)
	}

	deleteSecond := signedRecordWithValue(t, priv, second.Key, 2, nil, FlagTombstone)
	if _, err := idx.PutLocal(deleteSecond); err != nil {
		t.Fatal(err)
	}
	meta, err = idx.GetPathMeta(base)
	if err != nil || meta.ActiveRecords != 1 || meta.ActiveTotalSize != uint64(RecordSize(updatedFirst)) {
		t.Fatalf("after delete meta=%#v err=%v", meta, err)
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
	mailbox := AccountID(owner.PubKey().SerializeCompressed())
	record := signedRecordWithValue(t, sender, "/mail/"+mailbox+"/msg/m1", 1, []byte("message"), 0)
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

func TestMirrorDeleteKeepsCompactFloor(t *testing.T) {
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
	if updated, err := idx.PutRemote(record); err != nil || updated {
		t.Fatalf("mirror floor allowed replay updated=%v err=%v", updated, err)
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
	identity DIDIdentity
}

func (r *blockingDIDResolver) resolve() (DIDIdentity, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.identity, nil
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

func TestApplySnapshotOrdersBlobManifestBeforeChunks(t *testing.T) {
	target := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manifest, chunks, err := BuildSignedBlobRecords(
		priv,
		"object",
		[][]byte{[]byte("hello "), []byte("world")},
		nil,
		RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 100},
	)
	if err != nil {
		t.Fatal(err)
	}
	records := append(append([]*wire.DKVSRecord{}, chunks...), manifest)
	checkpoint, err := checkpointFromRecords(records, 1)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &Snapshot{Checkpoint: checkpoint, Records: records, CreatedAt: currentUnixMilli()}
	applied, err := target.ApplySnapshot(snapshot)
	if err != nil || applied != len(records) {
		t.Fatalf("apply blob snapshot applied=%d err=%v", applied, err)
	}
	manifestRecord, err := target.Get(manifest.Key)
	if err != nil {
		t.Fatal(err)
	}
	chunkRecords := make([]*wire.DKVSRecord, 0, len(chunks))
	for _, chunk := range chunks {
		stored, err := target.Get(chunk.Key)
		if err != nil {
			t.Fatal(err)
		}
		chunkRecords = append(chunkRecords, stored)
	}
	_, content, err := AssembleBlobFromRecords(manifestRecord, chunkRecords, BlobPolicy{})
	if err != nil || string(content) != "hello world" {
		t.Fatalf("assembled content=%q err=%v", content, err)
	}
}

func TestBlobRejectsNonCanonicalChunkIndexAndUnevenChunks(t *testing.T) {
	accountID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := ParseKey("/blob/" + accountID + "/object/chunk/00"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("non-canonical chunk index err=%v", err)
	}
	if _, _, err := BuildBlobManifest(
		[][]byte{[]byte("short"), []byte("longer")}, nil, 60_000, 100,
	); !errors.Is(err, ErrBlobManifestInvalid) {
		t.Fatalf("uneven chunks err=%v", err)
	}
}
