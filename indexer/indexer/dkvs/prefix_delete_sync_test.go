package dkvs

import (
	"context"
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func TestPrefixDeletionCASUsesCurrentSetWithoutHistory(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{EndpointID: "delete-cas",
		AllowFreeLocal: true, FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return 100 },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/delete-cas"
	key := prefix + "/value"
	record, err := NewSignedRecord(priv, key, []byte("live"), RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	before, err := idx.ActiveMetadata(context.Background(), ActiveScope{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	command := signedCurrentDelete(t, priv, record, 100)
	hash := RecordHash(record)
	if updated, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &hash}); err != nil || !updated {
		t.Fatalf("delete updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &hash}); err != nil || updated {
		t.Fatalf("retry updated=%v err=%v", updated, err)
	}
	after, err := idx.ActiveMetadata(context.Background(), ActiveScope{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation <= before.Generation || after.Root == before.Root {
		t.Fatal("physical deletion did not invalidate source metadata")
	}
	snapshot, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true})
	if err != nil || len(snapshot.Records) != 0 {
		t.Fatalf("current empty snapshot=%+v err=%v", snapshot, err)
	}
	state, err := idx.GetKeyState(key)
	if err != nil || state.Status != KeyStateNeverSeen {
		t.Fatalf("deleted key retained state=%+v err=%v", state, err)
	}
	if _, err := idx.Get(key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("deleted value readable: %v", err)
	}
	assertNoDeleteRows(t, idx)
}

func TestPrefixDeletionLearnedThroughSourceBoundRepair(t *testing.T) {
	config := Config{EndpointID: "source", AllowFreeLocal: true, FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight: func() uint64 { return 100 }}
	source := testIndexerWithConfig(t, config)
	config.EndpointID = "target"
	target := testIndexerWithConfig(t, config)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/repair-delete/value"
	prefix, err := CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewSignedRecord(priv, key, []byte("live"), RecordOptions{Seq: 1, IssueHeight: 100, TTL: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	if updated, err := target.AcceptCurrentRecord(record); err != nil || !updated {
		t.Fatalf("initial replication updated=%v err=%v", updated, err)
	}
	before, err := target.ActiveMetadata(context.Background(), ActiveScope{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := target.NetworkSyncBaseline(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.PutLocal(signedCurrentDelete(t, priv, record, 100)); err != nil {
		t.Fatal(err)
	}
	repair, err := source.GetPathSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(repair.Records) != 0 {
		t.Fatal("deleted record leaked into source snapshot")
	}
	if _, err := target.ApplyPathSnapshotFrom(repair, baseline); err != nil {
		t.Fatal(err)
	}
	after, err := target.ActiveMetadata(context.Background(), ActiveScope{Prefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation <= before.Generation || after.Root == before.Root {
		t.Fatal("repair did not invalidate terminal metadata")
	}
	if _, err := target.Get(key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("missing source key not removed: %v", err)
	}
	snapshot, err := target.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: target.EndpointID(), Full: true})
	if err != nil || len(snapshot.Records) != 0 {
		t.Fatalf("target snapshot=%+v err=%v", snapshot, err)
	}
	assertNoDeleteRows(t, target)
}

func TestCurrentKeyStatePayloadLimits(t *testing.T) {
	state := DKVSKeyState{Key: "/personal/test/limit", Status: KeyStateActive, Seq: 1, ETag: "hash"}
	states := make([]DKVSKeyState, MaxPrefixReadRecords)
	size := 0
	if err := appendPrefixKeyState(&states, &size, state); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatal("key states bypassed count limit")
	}
	states = nil
	size = MaxPrefixReadBytes
	if err := appendPrefixKeyState(&states, &size, state); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatal("key states bypassed byte limit")
	}
	state.Status = KeyStateNeverSeen
	states, size = nil, 0
	if err := appendPrefixKeyState(&states, &size, state); !errors.Is(err, ErrInvalidRecord) {
		t.Fatal("absence was serialized as stored key-state history")
	}
}
