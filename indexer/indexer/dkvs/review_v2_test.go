package dkvs

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/wire"
)

func signedReviewRecord(t *testing.T, priv *btcec.PrivateKey, key string, seq uint64,
	value []byte, ttl uint64, flags uint32) *wire.DKVSRecord {

	t.Helper()
	record := &wire.DKVSRecord{
		Version:      Version,
		Key:          key,
		Value:        append([]byte(nil), value...),
		PubKey:       priv.PubKey().SerializeCompressed(),
		Seq:          seq,
		IssueTime:    currentUnixMilli(),
		TTL:          ttl,
		ExpiryHeight: 100,
		Flags:        flags,
	}
	hash := SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	return record
}

func TestDeleteMissingKeyDoesNotPersistState(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	deleted := signedReviewRecord(t, priv, key, 1, nil, 60_000, FlagTombstone)
	updated, err := idx.PutLocal(deleted)
	if err != nil || updated {
		t.Fatalf("missing delete updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing key get err=%v", err)
	}
	idx.mutex.RLock()
	_, err = idx.getDeleteStateLocked(key)
	idx.mutex.RUnlock()
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing key created delete state: %v", err)
	}
}

func TestDeletePhysicallyRemovesAndBlocksStaleReplay(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	original := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	if updated, err := idx.PutLocal(original); err != nil || !updated {
		t.Fatalf("put original updated=%v err=%v", updated, err)
	}
	deleted := signedPersonalRecordWithKey(t, priv, 2, "", FlagTombstone)
	if updated, err := idx.PutLocal(deleted); err != nil || !updated {
		t.Fatalf("delete updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(original.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("deleted key get err=%v", err)
	}
	idx.mutex.RLock()
	_, rawErr := idx.getRaw(original.Key)
	idx.mutex.RUnlock()
	if !errors.Is(rawErr, ErrRecordNotFound) {
		t.Fatalf("active record was not physically removed: %v", rawErr)
	}
	relay, err := idx.GetForSync(original.Key)
	if err != nil || !IsTombstone(relay.Flags) || relay.Seq != deleted.Seq {
		t.Fatalf("relay delete=%#v err=%v", relay, err)
	}
	stale := signedPersonalRecordWithKey(t, priv, 2, "stale", 0)
	if _, err := idx.PutLocal(stale); !errors.Is(err, ErrStaleRecord) {
		t.Fatalf("stale replay err=%v", err)
	}
	fresh := signedPersonalRecordWithKey(t, priv, 3, "fresh", 0)
	if updated, err := idx.PutLocal(fresh); err != nil || !updated {
		t.Fatalf("fresh recreate updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(original.Key)
	if err != nil || string(got.Value) != "fresh" {
		t.Fatalf("fresh record=%#v err=%v", got, err)
	}
}

func TestPathMetaTracksPutUpdateDelete(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordWithPath(t, priv, "profile", 1, "a", 0)
	second := signedPersonalRecordWithPath(t, priv, "settings", 1, "bb", 0)
	if _, err := idx.PutLocal(first); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(second); err != nil {
		t.Fatal(err)
	}
	path := "/personal/" + personalAccountID(priv.PubKey().SerializeCompressed())
	meta, err := idx.PathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := uint64(RecordSize(first) + RecordSize(second))
	if meta.ActiveCount != 2 || meta.ActiveBytes != wantBytes {
		t.Fatalf("meta=%#v want count=2 bytes=%d", meta, wantBytes)
	}
	updated := signedPersonalRecordWithPath(t, priv, "profile", 2, "larger-value", 0)
	if _, err := idx.PutLocal(updated); err != nil {
		t.Fatal(err)
	}
	deleted := signedPersonalRecordWithPath(t, priv, "settings", 2, "", FlagTombstone)
	if _, err := idx.PutLocal(deleted); err != nil {
		t.Fatal(err)
	}
	meta, err = idx.PathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ActiveCount != 1 || meta.ActiveBytes != uint64(RecordSize(updated)) {
		t.Fatalf("updated meta=%#v", meta)
	}
	usage, err := idx.Usage(path)
	if err != nil || usage.ActiveRecords != 1 || usage.ActiveTotalSize != uint64(RecordSize(updated)) {
		t.Fatalf("usage=%#v err=%v", usage, err)
	}
	records, total, err := idx.ListPrefix(path, 0, 1)
	if err != nil || total != 1 || len(records) != 1 || records[0].Key != updated.Key {
		t.Fatalf("records=%v total=%d err=%v", records, total, err)
	}
}

func TestMailboxRequiresNonZeroTTL(t *testing.T) {
	idx := testIndexer(t)
	sender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mailbox := personalAccountID(recipient.PubKey().SerializeCompressed())
	key, err := MailMsgKey(mailbox, "m1")
	if err != nil {
		t.Fatal(err)
	}
	record := signedReviewRecord(t, sender, key, 1, []byte("message"), 0, 0)
	if _, err := idx.PutLocal(record); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("zero ttl mailbox err=%v", err)
	}
}

type blockingReviewResolver struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
	value   DIDIdentity
}

func (r *blockingReviewResolver) resolve() (DIDIdentity, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return r.value, nil
}

func (r *blockingReviewResolver) ResolveName(string) (DIDIdentity, error) {
	return r.resolve()
}

func (r *blockingReviewResolver) ResolveService(string) (DIDIdentity, error) {
	return r.resolve()
}

func TestExternalResolverDoesNotHoldIndexerWriteLock(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &blockingReviewResolver{
		started: make(chan struct{}),
		release: make(chan struct{}),
		value: DIDIdentity{
			CanonicalName: "alice",
			NameID:        "alice",
			SigningKeys:   [][]byte{priv.PubKey().SerializeCompressed()},
			Active:        true,
		},
	}
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		Resolver:       resolver,
	})
	personal := signedPersonalRecordWithKey(t, priv, 1, "local", 0)
	if _, err := idx.PutLocal(personal); err != nil {
		t.Fatal(err)
	}
	nameRecord := signedReviewRecord(t, priv, "/name/alice", 1, []byte("value"), 60_000, 0)
	putDone := make(chan error, 1)
	go func() {
		_, err := idx.PutLocal(nameRecord)
		putDone <- err
	}()
	select {
	case <-resolver.started:
	case <-time.After(time.Second):
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
			t.Fatalf("concurrent get err=%v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Get blocked while external resolver was waiting")
	}
	close(resolver.release)
	if err := <-putDone; err != nil {
		t.Fatalf("name put err=%v", err)
	}
}

func TestPathSyncCoalescesConcurrentChanges(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := "/personal/" + personalAccountID(priv.PubKey().SerializeCompressed())
	sub := Subscription{Type: SubscriptionPrefix, Target: path}
	if err := idx.BeginPathSync("session", sub); err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordWithKey(t, priv, 1, "one", 0)
	second := signedPersonalRecordWithKey(t, priv, 2, "two", 0)
	if _, err := idx.PutLocal(first); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(second); err != nil {
		t.Fatal(err)
	}
	pending, overflow := idx.EndPathSync("session")
	if overflow || len(pending) != 1 || pending[0].Seq != 2 || string(pending[0].Value) != "two" {
		t.Fatalf("pending=%v overflow=%v", pending, overflow)
	}
}

func TestMirrorDeleteRemovesOnlyMissingKeys(t *testing.T) {
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
	path := "/personal/" + personalAccountID(priv.PubKey().SerializeCompressed())
	deleted, err := idx.DeleteMirrorKeys(Subscription{Type: SubscriptionPrefix, Target: path}, []string{remove.Key})
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if _, err := idx.Get(keep.Key); err != nil {
		t.Fatalf("keep err=%v", err)
	}
	if _, err := idx.Get(remove.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("remove err=%v", err)
	}
	meta, err := idx.PathMeta(path)
	if err != nil || meta.ActiveCount != 1 || meta.ActiveBytes != uint64(RecordSize(keep)) {
		t.Fatalf("meta=%#v err=%v", meta, err)
	}
}
