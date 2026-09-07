package dkvs

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

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
