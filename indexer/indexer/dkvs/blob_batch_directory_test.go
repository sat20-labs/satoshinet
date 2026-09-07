package dkvs

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func signedFreeBlobRecord(t *testing.T, priv *btcec.PrivateKey, name string, seq uint64, value []byte) *wire.DKVSRecord {
	t.Helper()
	accountID := AccountID(priv.PubKey().SerializeCompressed())
	key, err := BlobKey(accountID, name)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewAccountRecord(key, value, RecordOptions{Seq: seq, TTL: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewFreeLocalFeeProof(key, "blob", uint32(RecordSize(record)), RecordExpiryHeight(record))
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

func signedPathRecord(t *testing.T, priv *btcec.PrivateKey, path string, seq uint64, value string) *wire.DKVSRecord {
	t.Helper()
	return signedPersonalRecordWithPath(t, priv, path, seq, value, 0)
}

func signedRelayablePathRecord(t *testing.T, priv *btcec.PrivateKey, path string, seq uint64, value string) *wire.DKVSRecord {
	t.Helper()
	record := signedPathRecord(t, priv, path, seq, value)
	record.FeeProof = nil
	signRecord(t, priv, record)
	return record
}

func putSingleCAS(idx *Indexer, record *wire.DKVSRecord, precondition WritePrecondition) (bool, error) {
	result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: record, Precondition: precondition,
	}}, BatchCASOptions{})
	if err != nil {
		return false, err
	}
	return result != nil && result.Applied == 1, nil
}

func blobTestIndexer(t *testing.T, maxKeys uint64) *Indexer {
	t.Helper()
	return testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{
			Enabled:             true,
			MaxTTL:              120_000,
			MaxRecordsPerSigner: 16,
			MaxBytesPerSigner:   16 * 1024 * 1024,
			MaxTotalRecords:     64,
			MaxTotalBytes:       32 * 1024 * 1024,
		},
		BlobPolicy: BlobPolicy{
			MaxValueSize:              wire.MaxDKVSBlobValueSize,
			MaxFreeLocalKeysPerSigner: maxKeys,
		},
	})
}

func TestSingleRecordFreeLocalBlobLimitsAndQuota(t *testing.T) {
	idx := blobTestIndexer(t, 1)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	exact := signedFreeBlobRecord(t, priv, "primary", 1,
		bytes.Repeat([]byte{0x5a}, wire.MaxDKVSBlobValueSize))
	if updated, err := idx.PutLocal(exact); err != nil || !updated {
		t.Fatalf("put exact-size blob updated=%v err=%v", updated, err)
	}
	stored, err := idx.Get(exact.Key)
	if err != nil || RecordHash(stored) != RecordHash(exact) {
		t.Fatalf("stored blob mismatch err=%v", err)
	}
	second := signedFreeBlobRecord(t, priv, "secondary", 1, []byte("second"))
	if _, err := idx.PutLocal(second); !errors.Is(err, ErrFreeLocalQuotaExceeded) {
		t.Fatalf("second FREE_LOCAL blob key err=%v", err)
	}
	accountID := AccountID(priv.PubKey().SerializeCompressed())
	tooLargeKey, err := BlobKey(accountID, "too-large")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewAccountRecord(tooLargeKey,
		bytes.Repeat([]byte{1}, wire.MaxDKVSBlobValueSize+1), RecordOptions{Seq: 1, TTL: 60_000}); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("oversized blob err=%v", err)
	}
}

func TestBatchCASIsAtomicAndIdempotent(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPathRecord(t, priv, "batch/a", 1, "one")
	second := signedPathRecord(t, priv, "batch/b", 1, "two")
	create := []CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}
	created, err := idx.PutLocalBatchCASResultWithOptions(create, BatchCASOptions{})
	if err != nil || created.Applied != 2 {
		t.Fatalf("create batch result=%#v err=%v", created, err)
	}
	path, err := CollectionPathForKey(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(created.PrefixStates) != 1 || created.PrefixStates[0].Prefix != path ||
		created.PrefixStates[0].Generation == 0 {
		t.Fatalf("create prefix states=%#v", created.PrefixStates)
	}
	createdGeneration := created.PrefixStates[0].Generation
	replayed, err := idx.PutLocalBatchCASResultWithOptions(create, BatchCASOptions{})
	if err != nil || replayed.Applied != 0 {
		t.Fatalf("idempotent retry result=%#v err=%v", replayed, err)
	}
	if len(replayed.PrefixStates) != 1 || replayed.PrefixStates[0].Prefix != path ||
		replayed.PrefixStates[0].Generation != createdGeneration {
		t.Fatalf("idempotent retry prefix states=%#v want generation=%d",
			replayed.PrefixStates, createdGeneration)
	}
	firstHash := RecordHash(first)
	secondHash := RecordHash(second)
	firstUpdate := signedPathRecord(t, priv, "batch/a", 2, "one-v2")
	secondUpdate := signedPathRecord(t, priv, "batch/b", 2, "two-v2")
	wrong := chainhash.DoubleHashH([]byte("wrong"))
	conflict := []CASMutation{
		{Record: firstUpdate, Precondition: WritePrecondition{ExpectedHash: &firstHash}},
		{Record: secondUpdate, Precondition: WritePrecondition{ExpectedHash: &wrong}},
	}
	if result, err := idx.PutLocalBatchCASResultWithOptions(conflict, BatchCASOptions{}); !errors.Is(err, ErrWriteConflict) || result != nil {
		t.Fatalf("conflicting batch result=%#v err=%v", result, err)
	}
	gotFirst, err := idx.Get(first.Key)
	if err != nil || RecordHash(gotFirst) != firstHash {
		t.Fatalf("first key partially updated err=%v", err)
	}
	gotSecond, err := idx.Get(second.Key)
	if err != nil || RecordHash(gotSecond) != secondHash {
		t.Fatalf("second key partially updated err=%v", err)
	}
	valid := []CASMutation{
		{Record: firstUpdate, Precondition: WritePrecondition{ExpectedHash: &firstHash}},
		{Record: secondUpdate, Precondition: WritePrecondition{ExpectedHash: &secondHash}},
	}
	if result, err := idx.PutLocalBatchCASResultWithOptions(valid, BatchCASOptions{}); err != nil || result.Applied != 2 {
		t.Fatalf("valid update batch result=%#v err=%v", result, err)
	}
}

func TestBatchCASRequiresExactNextSequence(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	initial := signedPathRecord(t, priv, "strict-seq", 1, "one")
	if applied, err := putSingleCAS(idx, initial, WritePrecondition{ExpectAbsent: true}); err != nil || !applied {
		t.Fatalf("initial applied=%v err=%v", applied, err)
	}
	hash := RecordHash(initial)
	skipped := signedPathRecord(t, priv, "strict-seq", 3, "three")
	if applied, err := putSingleCAS(idx, skipped, WritePrecondition{ExpectedHash: &hash}); !errors.Is(err, ErrInvalidSequence) || applied {
		t.Fatalf("skipped sequence applied=%v err=%v", applied, err)
	}
	next := signedPathRecord(t, priv, "strict-seq", 2, "two")
	if applied, err := putSingleCAS(idx, next, WritePrecondition{ExpectedHash: &hash}); err != nil || !applied {
		t.Fatalf("next sequence applied=%v err=%v", applied, err)
	}
}

func TestRemoteRecordRequiresPathSnapshot(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{FeeVerifier: testFeeVerifier{}})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	initial := signedRelayablePathRecord(t, priv, "remote-repair", 1, "one")
	if updated, err := idx.PutLocal(initial); err != nil || !updated {
		t.Fatalf("initial local updated=%v err=%v", updated, err)
	}
	if updated, err := idx.AcceptRemoteRecord(initial, "peer-a"); err != nil || updated {
		t.Fatalf("idempotent remote updated=%v err=%v", updated, err)
	}
	later := signedRelayablePathRecord(t, priv, "remote-repair", 2, "two")
	if updated, err := idx.AcceptRemoteRecord(later, "peer-a"); !errors.Is(err, ErrPathDiverged) || updated {
		t.Fatalf("remote repair updated=%v err=%v", updated, err)
	}
	path, err := CollectionPathForKey(initial.Key)
	if err != nil {
		t.Fatal(err)
	}
	status, err := idx.GetPathLocalStatus(path)
	if err != nil || !status.Stale || status.LastSyncPeer != "peer-a" {
		t.Fatalf("path status=%#v err=%v", status, err)
	}
}

func TestKeyCASDoesNotConflictOnUnrelatedPathMutation(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPathRecord(t, priv, "path/a", 1, "a1")
	second := signedPathRecord(t, priv, "path/b", 1, "b1")
	if result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}, BatchCASOptions{}); err != nil || result.Applied != 2 {
		t.Fatalf("create result=%#v err=%v", result, err)
	}
	firstHash := RecordHash(first)
	secondHash := RecordHash(second)
	secondUpdate := signedPathRecord(t, priv, "path/b", 2, "b2")
	if applied, err := putSingleCAS(idx, secondUpdate, WritePrecondition{ExpectedHash: &secondHash}); err != nil || !applied {
		t.Fatalf("unrelated update applied=%v err=%v", applied, err)
	}
	firstUpdate := signedPathRecord(t, priv, "path/a", 2, "a2")
	if applied, err := putSingleCAS(idx, firstUpdate, WritePrecondition{ExpectedHash: &firstHash}); err != nil || !applied {
		t.Fatalf("key CAS was incorrectly coupled to path mutation applied=%v err=%v", applied, err)
	}
}

func TestBatchCASPathMetaGenerationStillAdvancesInternally(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPathRecord(t, priv, "generation/a", 1, "a1")
	second := signedPathRecord(t, priv, "generation/b", 1, "b1")
	path, err := CollectionPathForKey(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}, BatchCASOptions{}); err != nil || result.Applied != 2 {
		t.Fatalf("create result=%#v err=%v", result, err)
	}
	after, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation+2 {
		t.Fatalf("path generation=%d want=%d", after.Generation, before.Generation+2)
	}
}
