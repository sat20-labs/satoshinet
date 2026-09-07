package dkvs

import (
	"sync/atomic"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/wire"
)

func signedFreeTTLPersonalRecord(t *testing.T, priv *btcec.PrivateKey, path string,
	seq, issueHeight, ttl uint64, value string) *wire.DKVSRecord {

	t.Helper()
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), path)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewAccountRecord(key, []byte(value), RecordOptions{
		Seq: seq, IssueHeight: issueHeight, TTL: ttl,
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewFreeLocalFeeProof(
		key, "personal", uint32(RecordSize(record)), RecordExpiryHeight(record),
	)
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	SignRecord(priv, record)
	return record
}

func TestFreeLocalExpiryPhysicallyDeletesWithoutSequenceFloor(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{
			Enabled:             true,
			MaxTTL:              100,
			MaxRecordsPerSigner: 16,
			MaxBytesPerSigner:   1 << 20,
			MaxTotalRecords:     64,
			MaxTotalBytes:       4 << 20,
		},
		CurrentHeight: func() uint64 { return height.Load() },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedFreeTTLPersonalRecord(t, priv, "expiry/item", 1, 1, 2, "one")
	if result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: record, Precondition: WritePrecondition{ExpectAbsent: true},
	}}, BatchCASOptions{EndpointID: idx.EndpointID()}); err != nil || result.Applied != 1 {
		t.Fatalf("put result=%+v err=%v", result, err)
	}
	prefix, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 1 {
		t.Fatalf("snapshot records=%d", len(snapshot.Records))
	}
	beforeMeta, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}

	height.Store(3)
	if pruned, err := idx.PruneExpiredAt(3); err != nil || pruned != 1 {
		t.Fatalf("pruned=%d err=%v", pruned, err)
	}
	state, err := idx.GetKeyState(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != KeyStateNeverSeen || state.Seq != 0 || state.ETag != "" {
		t.Fatalf("expired key state=%+v", state)
	}
	after, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Records) != 0 {
		t.Fatalf("expired record remained in direct read: %+v", after)
	}
	if after.Generation == snapshot.Generation {
		t.Fatalf("FREE_LOCAL expiry did not advance endpoint generation: before=%d after=%d",
			snapshot.Generation, after.Generation)
	}
	afterMeta, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if afterMeta.Generation != beforeMeta.Generation || afterMeta.StateRoot != beforeMeta.StateRoot {
		t.Fatalf("FREE_LOCAL expiry changed canonical path state: before=%+v after=%+v", beforeMeta, afterMeta)
	}

	next := signedFreeTTLPersonalRecord(t, priv, "expiry/item", 1, 3, 2, "two")
	if result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: next, Precondition: WritePrecondition{ExpectAbsent: true},
	}}, BatchCASOptions{EndpointID: idx.EndpointID()}); err != nil || result.Applied != 1 {
		t.Fatalf("recreate result=%+v err=%v", result, err)
	}
}
