package dkvs

import (
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func TestPrefixDeletionCASAndCompaction(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{EndpointID: "delete-cas",
		AllowFreeLocal: true, FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return 100 },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/delete-cas"
	key := prefix + "/value"
	record, err := NewSignedRecord(priv, key, []byte("live"), RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
	if err != nil { t.Fatal(err) }
	if _, err := idx.PutLocal(record); err != nil { t.Fatal(err) }
	before, err := idx.PrefixSnapshot(prefix)
	if err != nil { t.Fatal(err) }
	tombstone, err := NewSignedTombstone(priv, key, RecordOptions{Seq: 2, IssueHeight: 100})
	if err != nil { t.Fatal(err) }
	hash := RecordHash(record)
	if _, err := idx.PutLocalCAS(tombstone, WritePrecondition{ExpectedHash: &hash}); err != nil { t.Fatal(err) }
	assertDeleted := func() {
		t.Helper()
		delta, err := idx.PrefixDelta(prefix, before.EndpointID, before.Generation)
		if err != nil { t.Fatal(err) }
		if len(delta.Records) != 0 || len(delta.KeyStates) != 1 ||
			delta.KeyStates[0].Key != key || delta.KeyStates[0].Status != KeyStateDeleted ||
			delta.KeyStates[0].Seq != 2 || delta.KeyStates[0].ETag != RecordHash(tombstone).String() {
			t.Fatal("CAS deletion did not produce a compact terminal key-state delta")
		}
	}
	assertDeleted()
	idx.mutex.Lock()
	batch := idx.db.NewWriteBatch()
	_, err = idx.compactExpiredDeleteCommandsLocked(batch, ^uint64(0))
	if err == nil { err = batch.Flush() }
	batch.Close()
	idx.mutex.Unlock()
	if err != nil { t.Fatal(err) }
	assertDeleted()
	snapshot, err := idx.PrefixSnapshot(prefix)
	if err != nil || len(snapshot.KeyStates) != 1 || snapshot.KeyStates[0].Status != KeyStateDeleted {
		t.Fatal("compacting tombstone body discarded snapshot deletion floor")
	}
	if _, err := idx.Get(key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("direct live read after deletion: %v", err)
	}
}

func TestPrefixDeletionLearnedThroughPathRepair(t *testing.T) {
	config := Config{AllowFreeLocal: true, FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return 100 }}
	source := testIndexerWithConfig(t, config)
	target := testIndexerWithConfig(t, config)
	priv, err := btcec.NewPrivateKey()
	if err != nil { t.Fatal(err) }
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/repair-delete/value"
	prefix, err := CollectionPathForKey(key)
	if err != nil { t.Fatal(err) }
	record, err := NewSignedRecord(priv, key, []byte("live"), RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
	if err != nil { t.Fatal(err) }
	for _, idx := range []*Indexer{source, target} {
		if _, err := idx.PutLocal(record); err != nil { t.Fatal(err) }
	}
	before, err := target.PrefixSnapshot(prefix)
	if err != nil { t.Fatal(err) }
	tombstone, err := NewSignedTombstone(priv, key, RecordOptions{Seq: 2, IssueHeight: 100})
	if err != nil { t.Fatal(err) }
	if _, err := source.PutLocal(tombstone); err != nil { t.Fatal(err) }
	repair, err := source.GetPathSnapshot(prefix)
	if err != nil { t.Fatal(err) }
	if _, err := target.ApplyPathSnapshot(repair); err != nil { t.Fatal(err) }
	delta, err := target.PrefixDelta(prefix, before.EndpointID, before.Generation)
	if err != nil { t.Fatal(err) }
	if len(delta.KeyStates) != 1 || len(delta.Records) != 0 ||
		delta.KeyStates[0].Key != key || delta.KeyStates[0].Status != KeyStateDeleted {
		t.Fatal("node repair installed deletion but failed to notify the existing terminal cursor")
	}
}

func TestPrefixDeletionPayloadLimitsIncludeKeyStates(t *testing.T) {
	state := DKVSKeyState{Key: "/personal/test/limit", Status: KeyStateDeleted, Seq: 1, ETag: "hash"}
	states := make([]DKVSKeyState, MaxPrefixReadRecords)
	size := 0
	if err := appendPrefixKeyState(&states, &size, state); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatal("deleted key states bypassed record-count limit")
	}
	states = nil
	size = MaxPrefixReadBytes
	if err := appendPrefixKeyState(&states, &size, state); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatal("deleted key states bypassed payload-byte limit")
	}
}
