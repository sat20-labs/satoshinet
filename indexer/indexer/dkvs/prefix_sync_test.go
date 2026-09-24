package dkvs

import (
	"errors"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
)

type countingPrefixKVDB struct {
	indexercommon.KVDB
	scans int
}

func (db *countingPrefixKVDB) BatchReadV2(prefix, seek []byte, reverse bool, read func(k, v []byte) error) error {
	db.scans++
	return db.KVDB.BatchReadV2(prefix, seek, reverse, read)
}

func TestPrefixStatusAvoidsFullScanBeforeKnownExpiry(t *testing.T) {
	raw := dbpkg.NewKVDB(t.TempDir())
	if raw == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = raw.Close() })
	db := &countingPrefixKVDB{KVDB: raw}
	height := uint64(100)
	idx := New(db, Config{EndpointID: "test-node", AllowFreeLocal: true,
		FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true}, CurrentHeight: func() uint64 { return height }})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/known-expiry"
	record, err := NewSignedRecord(priv, key, []byte("value"), RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	prefix, err := CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := idx.GetPathMeta(prefix)
	if err != nil || meta.MinExpiryHeight <= 101 {
		t.Fatalf("meta=%+v err=%v", meta, err)
	}
	db.scans = 0
	height = 101
	status, err := idx.PrefixStatus(snapshot.EndpointID, []PrefixGeneration{{Prefix: prefix, Generation: snapshot.Generation}})
	if err != nil || len(status.Changed) != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if db.scans != 0 {
		t.Fatalf("unchanged path scanned %d times before known expiry", db.scans)
	}
	db.scans = 0
	height = 1100
	if _, err := idx.PrefixStatus(snapshot.EndpointID, []PrefixGeneration{{Prefix: prefix, Generation: snapshot.Generation}}); err != nil {
		t.Fatal(err)
	}
	if db.scans == 0 {
		t.Fatal("expiry boundary skipped the required path rebuild")
	}
}

func TestPrefixDeltaRequiresFullBaselineForPreIndexGeneration(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithPath(t, priv, "pre-index", 1, "value", 0)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	prefix, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PrefixDelta(prefix, idx.EndpointID(), 0); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("pre-index cursor must reset, got %v", err)
	}
	snapshot, err := idx.PrefixSnapshot(prefix)
	if err != nil || len(snapshot.Records) != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	delta, err := idx.PrefixDelta(prefix, snapshot.EndpointID, snapshot.Generation)
	if err != nil || len(delta.Records) != 0 {
		t.Fatalf("current cursor delta=%+v err=%v", delta, err)
	}
}

func TestPrefixStatusUsesPathMetaGenerationDirectly(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithPath(t, priv, "generation-test", 1, "one", 0)
	key := record.Key
	prefix, err := CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	snapshot, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != meta.EndpointGeneration || len(snapshot.Records) != 1 || snapshot.Records[0].Key != key {
		t.Fatalf("snapshot=%+v meta=%+v", snapshot, meta)
	}
	status, err := idx.PrefixStatus(snapshot.EndpointID, []PrefixGeneration{{
		Prefix: prefix, Generation: snapshot.Generation,
	}})
	if err != nil || len(status.Changed) != 0 {
		t.Fatalf("unchanged status=%+v err=%v", status, err)
	}
	if _, err := idx.PutLocal(signedPersonalRecordWithPath(t, priv, "generation-test", 2, "two", 0)); err != nil {
		t.Fatal(err)
	}
	status, err = idx.PrefixStatus(snapshot.EndpointID, []PrefixGeneration{{
		Prefix: prefix, Generation: snapshot.Generation,
	}})
	if err != nil || len(status.Changed) != 1 {
		t.Fatalf("changed status=%+v err=%v", status, err)
	}
	meta, err = idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if status.Changed[0].Generation != meta.EndpointGeneration || meta.EndpointGeneration == snapshot.Generation {
		t.Fatalf("status=%+v meta=%+v snapshot=%+v", status, meta, snapshot)
	}
}

func TestPrefixDeltaFindsLateAcceptedSignedRecord(t *testing.T) {
	height := uint64(100)
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true, FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return height },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/late"
	record, err := NewSignedRecord(priv, key, []byte("late"), RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	height = 105
	before, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Records) != 0 {
		t.Fatalf("unexpected initial records: %d", len(before.Records))
	}
	if applied, err := idx.PutLocal(record); err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	delta, err := idx.PrefixDelta(prefix, before.EndpointID, before.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Generation <= before.Generation || len(delta.Records) != 1 || delta.Records[0].Key != key || delta.Records[0].IssueHeight != 100 {
		t.Fatalf("late record missing from delta: %+v", delta)
	}
}

func TestPrefixDeltaOmitsUnchangedRecord(t *testing.T) {
	height := uint64(100)
	idx := testIndexerWithConfig(t, Config{AllowFreeLocal: true,
		FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true}, CurrentHeight: func() uint64 { return height }})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/wallet/"
	first, err := NewSignedRecord(priv, base+"first", []byte("first"), RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(first); err != nil {
		t.Fatal(err)
	}
	prefix, err := CollectionPathForKey(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	// Both writes share a block height; the generation still selects one key.
	height = 100
	late, err := NewSignedRecord(priv, base+"late", []byte("late"), RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(late); err != nil {
		t.Fatal(err)
	}
	delta, err := idx.PrefixDelta(prefix, before.EndpointID, before.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 1 || delta.Records[0].Key != late.Key {
		t.Fatalf("before generation=%d delta=%+v", before.Generation, delta)
	}
}

func TestPrefixDeltaReturnsLatestOverwriteWithoutRemovedKey(t *testing.T) {
	height := uint64(10)
	idx := testIndexerWithConfig(t, Config{AllowFreeLocal: true,
		FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true}, CurrentHeight: func() uint64 { return height }})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/wallet/"
	makeRecord := func(key, value string, seq, issued uint64) *Record {
		record, buildErr := NewSignedRecord(priv, key, []byte(value), RecordOptions{Seq: seq, IssueHeight: issued, TTL: 100})
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		return record
	}
	first, removed := base+"first", base+"removed"
	for _, key := range []string{first, removed} {
		if _, err := idx.PutLocal(makeRecord(key, "old", 1, 10)); err != nil {
			t.Fatal(err)
		}
	}
	prefix, err := CollectionPathForKey(first)
	if err != nil {
		t.Fatal(err)
	}
	before, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	height = 11
	if _, err := idx.PutLocal(makeRecord(first, "new", 2, 11)); err != nil {
		t.Fatal(err)
	}
	tombstone, err := NewSignedTombstone(priv, removed, RecordOptions{Seq: 2, IssueHeight: 11})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(tombstone); err != nil {
		t.Fatal(err)
	}
	delta, err := idx.PrefixDelta(prefix, before.EndpointID, before.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 1 || delta.Records[0].Key != first || string(delta.Records[0].Value) != "new" {
		t.Fatalf("overwrite/deletion delta=%+v", delta)
	}
}

func TestPrefixDeltaFindsRecordInstalledByPathRepair(t *testing.T) {
	height := uint64(105)
	config := Config{AllowFreeLocal: true, FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return height }}
	source := testIndexerWithConfig(t, config)
	target := testIndexerWithConfig(t, config)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/repair"
	record, err := NewSignedRecord(priv, key, []byte("old-signed-newly-installed"), RecordOptions{
		Seq: 1, IssueHeight: 100, TTL: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := target.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if applied, err := source.PutLocal(record); err != nil || !applied {
		t.Fatalf("source write applied=%v err=%v", applied, err)
	}
	pathSnapshot, err := source.GetPathSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyPathSnapshot(pathSnapshot); err != nil {
		t.Fatal(err)
	}
	delta, err := target.PrefixDelta(prefix, before.EndpointID, before.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 1 || delta.Records[0].Key != key {
		t.Fatalf("path repair record missing: %+v", delta)
	}
}

func TestFreeLocalAdvancesEndpointGenerationWithoutRelayState(t *testing.T) {
	height := uint64(1)
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true, FreeLocalCache: DefaultFreeLocalCachePolicy(),
		FeeVerifier:   JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return height },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedFreePersonalRecord(t, priv, "free-local-generation", 1, "local", 0)
	prefix, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	after, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if after.EndpointGeneration != before.EndpointGeneration+1 {
		t.Fatalf("endpoint generation before=%d after=%d", before.EndpointGeneration, after.EndpointGeneration)
	}
	if after.Generation != before.Generation || after.StateRoot != before.StateRoot {
		t.Fatalf("FREE_LOCAL changed relay state before=%+v after=%+v", before, after)
	}
	snapshot, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation != after.EndpointGeneration || len(snapshot.Records) != 1 || snapshot.Records[0].Key != record.Key {
		t.Fatalf("FREE_LOCAL endpoint snapshot=%+v meta=%+v", snapshot, after)
	}
	status, err := idx.PrefixStatus(snapshot.EndpointID, []PrefixGeneration{{
		Prefix: prefix, Generation: before.EndpointGeneration,
	}})
	if err != nil || len(status.Changed) != 1 || status.Changed[0].Generation != after.EndpointGeneration {
		t.Fatalf("FREE_LOCAL status=%+v err=%v", status, err)
	}
	if _, err := idx.GetForRelay(record.Key); err == nil {
		t.Fatal("FREE_LOCAL record became relayable")
	}
	updated := signedFreePersonalRecord(t, priv, "free-local-generation", 2, "updated", 0)
	if _, err := idx.PutLocal(updated); err != nil {
		t.Fatal(err)
	}
	latest, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if latest.EndpointGeneration != after.EndpointGeneration+1 ||
		latest.Generation != after.Generation || latest.StateRoot != after.StateRoot {
		t.Fatalf("FREE_LOCAL update meta before=%+v after=%+v", after, latest)
	}
}

func TestPrefixSynchronizationSupportsAccountBoundMailbox(t *testing.T) {
	height := uint64(100)
	idx := newMessageTestIndexer(t, &height)
	recipient := testMessageAccount(t)
	sender := testMessageAccount(t)
	if _, err := idx.PrefixSnapshot("/personal/" + recipient); err == nil {
		t.Fatal("managed snapshot accepted read-only personal aggregate")
	}
	prefix := "/mail/" + recipient
	initial, err := idx.PrefixSnapshot(prefix)
	if err != nil || len(initial.Records) != 0 {
		t.Fatalf("initial mailbox snapshot=%+v err=%v", initial, err)
	}
	record := internalDirectRecord(t, recipient, sender, 0, height, []byte("inner"))
	if _, err := idx.PutInternalMailbox(record); err != nil {
		t.Fatal(err)
	}
	status, err := idx.PrefixStatus(idx.EndpointID(), []PrefixGeneration{{
		Prefix: prefix, Generation: initial.Generation,
	}})
	if err != nil || len(status.Changed) != 1 || status.Changed[0].Prefix != prefix {
		t.Fatalf("mailbox status=%+v err=%v", status, err)
	}
	snapshot, err := idx.PrefixSnapshot(prefix)
	if err != nil || len(snapshot.Records) != 1 || snapshot.Records[0].Key != record.Key {
		t.Fatalf("mailbox snapshot=%+v err=%v", snapshot, err)
	}
	if _, err := idx.GetPathSnapshot(prefix); err == nil {
		t.Fatal("account-bound mailbox entered network path snapshot")
	}
}
