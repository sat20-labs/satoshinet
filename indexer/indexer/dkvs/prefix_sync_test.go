package dkvs

import (
	"context"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func TestActiveIncrementalFindsLateAcceptedSignedRecord(t *testing.T) {
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
	before, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Records) != 0 {
		t.Fatalf("unexpected initial records: %d", len(before.Records))
	}
	if applied, err := idx.PutLocal(record); err != nil || !applied {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	delta, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: before.Meta.EndpointID, After: before.Meta.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if delta.Meta.Generation <= before.Meta.Generation || len(delta.Records) != 1 || delta.Records[0].Key != key || delta.Records[0].IssueHeight != 100 {
		t.Fatalf("late record missing from delta: %+v", delta)
	}
}

func TestActiveIncrementalOmitsUnchangedRecord(t *testing.T) {
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
	before, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true})
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
	delta, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: before.Meta.EndpointID, After: before.Meta.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 1 || delta.Records[0].Key != late.Key {
		t.Fatalf("before generation=%d delta=%+v", before.Meta.Generation, delta)
	}
}

func TestActiveIncrementalReturnsLatestOverwriteWithoutRemovedKey(t *testing.T) {
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
	before, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true})
	if err != nil {
		t.Fatal(err)
	}
	height = 11
	if _, err := idx.PutLocal(makeRecord(first, "new", 2, 11)); err != nil {
		t.Fatal(err)
	}
	current, err := idx.Get(removed)
	if err != nil {
		t.Fatal(err)
	}
	command := signedCurrentDelete(t, priv, current, 11)
	if _, err := idx.PutLocal(command); err != nil {
		t.Fatal(err)
	}
	delta, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: before.Meta.EndpointID, After: before.Meta.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 1 || delta.Records[0].Key != first || string(delta.Records[0].Value) != "new" {
		t.Fatalf("overwrite/deletion delta=%+v", delta)
	}
	assertNoDeleteRows(t, idx)
	assertNoDeleteRows(t, idx)
}

func TestActiveIncrementalFindsRecordInstalledByPathRepair(t *testing.T) {
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
	before, err := target.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: target.EndpointID(), Full: true})
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
	delta, err := target.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: before.Meta.EndpointID, After: before.Meta.Generation})
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
	snapshot, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Meta.Generation != after.EndpointGeneration || len(snapshot.Records) != 1 || snapshot.Records[0].Key != record.Key {
		t.Fatalf("FREE_LOCAL endpoint snapshot=%+v meta=%+v", snapshot, after)
	}
	meta, err := idx.ActiveMetadata(context.Background(), ActiveScope{Prefix: prefix})
	if err != nil || meta.Generation != after.EndpointGeneration {
		t.Fatalf("meta=%+v err=%v", meta, err)
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

func TestActiveSynchronizationSupportsAccountBoundMailbox(t *testing.T) {
	height := uint64(100)
	idx := newMessageTestIndexer(t, &height)
	recipient := testMessageAccount(t)
	sender := testMessageAccount(t)
	if _, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: "/personal/" + recipient}, EndpointID: idx.EndpointID(), Full: true}); err == nil {
		t.Fatal("managed snapshot accepted read-only personal aggregate")
	}
	prefix := "/mail/" + recipient
	initial, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true})
	if err != nil || len(initial.Records) != 0 {
		t.Fatalf("initial mailbox snapshot=%+v err=%v", initial, err)
	}
	record := internalDirectRecord(t, recipient, sender, 0, height, []byte("inner"))
	if _, err := idx.PutInternalMailbox(record); err != nil {
		t.Fatal(err)
	}
	meta, err := idx.ActiveMetadata(context.Background(), ActiveScope{Prefix: prefix})
	if err != nil || meta.Generation <= initial.Meta.Generation {
		t.Fatalf("meta=%+v err=%v", meta, err)
	}
	snapshot, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true})
	if err != nil || len(snapshot.Records) != 1 || snapshot.Records[0].Key != record.Key {
		t.Fatalf("mailbox snapshot=%+v err=%v", snapshot, err)
	}
	if _, err := idx.GetPathSnapshot(prefix); err == nil {
		t.Fatal("account-bound mailbox entered network path snapshot")
	}
}

func TestActiveInitialZeroGenerationAndExpiry(t *testing.T) {
	height := uint64(100)
	idx := testIndexerWithConfig(t, Config{EndpointID: "active-expiry", AllowFreeLocal: true, FreeLocalCache: DefaultFreeLocalCachePolicy(), FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true}, CurrentHeight: func() uint64 { return height }})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedFreeTTLPersonalRecord(t, priv, "expiry/value", 1, 100, 10, "value")
	prefix, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	scope := ActiveScope{Prefix: prefix}
	empty, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: scope, EndpointID: idx.EndpointID(), Full: true})
	if err != nil || len(empty.Records) != 0 || empty.Meta.Generation != 0 {
		t.Fatalf("initial=%+v err=%v", empty, err)
	}
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	current, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: scope, EndpointID: idx.EndpointID(), After: 0})
	if err != nil || len(current.Records) != 1 {
		t.Fatalf("initial incremental=%+v err=%v", current, err)
	}
	unchanged, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: scope, EndpointID: idx.EndpointID(), After: current.Meta.Generation})
	if err != nil || len(unchanged.Records) != 0 {
		t.Fatalf("unchanged=%+v err=%v", unchanged, err)
	}
	pathMeta, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if current.Meta.Generation != pathMeta.EndpointGeneration {
		t.Fatal("source generation differs from path metadata")
	}
	height = 110
	expired, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: scope, EndpointID: idx.EndpointID(), Full: true})
	if err != nil || len(expired.Records) != 0 || expired.Meta.Root == current.Meta.Root {
		t.Fatalf("expiry=%+v err=%v", expired, err)
	}
}
