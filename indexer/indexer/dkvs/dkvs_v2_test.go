package dkvs

import (
	"errors"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestMissingTombstoneIsNotPersisted(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	command := signedPersonalRecordWithPath(t, priv, "missing", 2, "", FlagTombstone)
	updated, err := idx.PutRemote(command)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("delete of a missing key must be an idempotent no-op")
	}
	if _, err := idx.GetForSync(command.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing delete command was persisted: %v", err)
	}
	idx.mutex.RLock()
	_, err = idx.getDeleteStateRaw(command.Key)
	idx.mutex.RUnlock()
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("unexpected delete state for missing key: %v", err)
	}
}

func TestDeletePhysicallyRemovesRecordAndRejectsReplay(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	original := signedPersonalRecordWithPath(t, priv, "profile", 1, "value", 0)
	if updated, err := idx.PutLocal(original); err != nil || !updated {
		t.Fatalf("put original updated=%v err=%v", updated, err)
	}
	command := signedPersonalRecordWithPath(t, priv, "profile", 2, "", FlagTombstone)
	if updated, err := idx.PutLocal(command); err != nil || !updated {
		t.Fatalf("delete updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(original.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("deleted record remains readable: %v", err)
	}
	retained, err := idx.GetForSync(original.Key)
	if err != nil || !IsTombstone(retained.Flags) || retained.Seq != command.Seq {
		t.Fatalf("retained delete command=%#v err=%v", retained, err)
	}
	if _, err := idx.PutRemote(original); !errors.Is(err, ErrStaleRecord) {
		t.Fatalf("old record replay err=%v", err)
	}
	recreated := signedPersonalRecordWithPath(t, priv, "profile", 3, "new", 0)
	if updated, err := idx.PutLocal(recreated); err != nil || !updated {
		t.Fatalf("recreate updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(recreated.Key)
	if err != nil || string(got.Value) != "new" {
		t.Fatalf("recreated record=%#v err=%v", got, err)
	}
}

func TestPathMetaTracksCreateUpdateDelete(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordWithPath(t, priv, "a", 1, "a", 0)
	second := signedPersonalRecordWithPath(t, priv, "b", 1, "bb", 0)
	if _, err := idx.PutLocal(first); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(second); err != nil {
		t.Fatal(err)
	}
	path := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := uint64(RecordSize(first) + RecordSize(second))
	if meta.ActiveCount != 2 || meta.ActiveBytes != wantBytes {
		t.Fatalf("initial meta=%#v want count=2 bytes=%d", meta, wantBytes)
	}

	updatedFirst := signedPersonalRecordWithPath(t, priv, "a", 2, "a-longer-value", 0)
	if _, err := idx.PutLocal(updatedFirst); err != nil {
		t.Fatal(err)
	}
	command := signedPersonalRecordWithPath(t, priv, "b", 2, "", FlagTombstone)
	if _, err := idx.PutLocal(command); err != nil {
		t.Fatal(err)
	}
	meta, err = idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ActiveCount != 1 || meta.ActiveBytes != uint64(RecordSize(updatedFirst)) {
		t.Fatalf("updated meta=%#v", meta)
	}
	usage, err := idx.Usage(path)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ActiveRecords != meta.ActiveCount || usage.ActiveTotalSize != meta.ActiveBytes {
		t.Fatalf("usage=%#v meta=%#v", usage, meta)
	}
}

func TestMailboxRequiresNonZeroTTL(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := &wire.DKVSRecord{
		Version:      Version,
		Key:          "/mail/recipient/msg/forever",
		Value:        []byte("message"),
		PubKey:       priv.PubKey().SerializeCompressed(),
		Seq:          1,
		IssueTime:    currentUnixMilli(),
		TTL:          0,
		ExpiryHeight: 100,
	}
	hash := SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	if _, err := idx.PutLocal(record); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("zero ttl mailbox record err=%v", err)
	}
}

func TestPathSyncFlushesConcurrentChanges(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := signedPersonalRecordWithPath(t, priv, "a", 1, "base", 0)
	if _, err := idx.PutLocal(base); err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	filter := []Subscription{{Type: SubscriptionPrefix, Target: prefix}}
	records, cursor, done, _, err := idx.SyncFilteredSession(99, nil, 1, filter)
	if err != nil || done || len(records) != 1 {
		t.Fatalf("first page records=%d done=%v err=%v", len(records), done, err)
	}

	command := signedPersonalRecordWithPath(t, priv, "a", 2, "", FlagTombstone)
	added := signedPersonalRecordWithPath(t, priv, "b", 1, "added", 0)
	if _, err := idx.PutLocal(command); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(added); err != nil {
		t.Fatal(err)
	}

	var sawDelete, sawAdded bool
	for pages := 0; !done && pages < 20; pages++ {
		records, cursor, done, _, err = idx.SyncFilteredSession(99, cursor, 1, filter)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Key == command.Key && IsTombstone(record.Flags) {
				sawDelete = true
			}
			if record.Key == added.Key && !IsTombstone(record.Flags) {
				sawAdded = true
			}
		}
	}
	if !done || !sawDelete || !sawAdded {
		t.Fatalf("done=%v sawDelete=%v sawAdded=%v", done, sawDelete, sawAdded)
	}
}

func TestMirrorReconcileDeletesOnlyOmittedRecords(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	keep := signedPersonalRecordWithPath(t, priv, "keep", 1, "keep", 0)
	remove := signedPersonalRecordWithPath(t, priv, "remove", 1, "remove", 0)
	if _, err := idx.PutLocal(keep); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(remove); err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	deleted, err := idx.ReconcileMirrorSubscription(
		Subscription{Type: SubscriptionPrefix, Target: prefix},
		map[string]struct{}{keep.Key: {}},
	)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if _, err := idx.Get(keep.Key); err != nil {
		t.Fatalf("kept record missing: %v", err)
	}
	if _, err := idx.Get(remove.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("omitted record remains: %v", err)
	}
}

type blockingResolver struct {
	started chan struct{}
	release chan struct{}
	key     []byte
}

func (r blockingResolver) resolve(name string) (DIDIdentity, error) {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-r.release
	return DIDIdentity{
		CanonicalName: name,
		NameID:        NormalizeNameID(name),
		SigningKeys:   [][]byte{r.key},
		Active:        true,
	}, nil
}

func (r blockingResolver) ResolveName(name string) (DIDIdentity, error) {
	return r.resolve(name)
}

func (r blockingResolver) ResolveService(name string) (DIDIdentity, error) {
	return r.resolve(name)
}

func TestResolverDoesNotHoldIndexerWriteLock(t *testing.T) {
	idx := testIndexer(t)
	personalKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	personal := signedPersonalRecordWithPath(t, personalKey, "profile", 1, "value", 0)
	if _, err := idx.PutLocal(personal); err != nil {
		t.Fatal(err)
	}

	nameKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := blockingResolver{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		key:     nameKey.PubKey().SerializeCompressed(),
	}
	idx.SetResolver(resolver)
	nameRecord := &wire.DKVSRecord{
		Version:      Version,
		Key:          "/name/alice",
		Value:        []byte("value"),
		PubKey:       nameKey.PubKey().SerializeCompressed(),
		Seq:          1,
		IssueTime:    currentUnixMilli(),
		TTL:          60_000,
		ExpiryHeight: 100,
	}
	hash := SigningHash(nameRecord)
	nameRecord.Signature = ecdsa.Sign(nameKey, hash[:]).Serialize()

	putDone := make(chan error, 1)
	go func() {
		_, err := idx.PutLocal(nameRecord)
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
			t.Fatalf("concurrent get: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("resolver call held the indexer write lock")
	}
	close(resolver.release)
	if err := <-putDone; err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotPrevalidationAvoidsPartialApply(t *testing.T) {
	source := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordWithPath(t, priv, "one", 1, "one", 0)
	second := signedPersonalRecordWithPath(t, priv, "two", 1, "two", 0)
	second.Signature[0] ^= 0xff
	checkpoint, err := CheckpointFromRecords([]*wire.DKVSRecord{first, second}, 1)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &Snapshot{
		Checkpoint: checkpoint,
		Records:    []*wire.DKVSRecord{first, second},
		CreatedAt:  currentUnixMilli(),
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("test snapshot root: %v", err)
	}
	target := source
	if applied, err := target.ApplySnapshot(snapshot); err == nil || applied != 0 {
		t.Fatalf("applied=%d err=%v", applied, err)
	}
	if _, err := target.Get(first.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("first record was partially applied: %v", err)
	}
}
