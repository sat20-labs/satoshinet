package dkvs

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

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
	proof, err := NewFreeLocalFeeProof(key, "blob", uint32(RecordSize(record)), 0)
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
	first := signedPersonalRecordWithPath(t, priv, "batch/a", 1, "one", 0)
	second := signedPersonalRecordWithPath(t, priv, "batch/b", 1, "two", 0)
	create := []CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}
	if applied, err := idx.PutLocalBatchCAS(create); err != nil || applied != 2 {
		t.Fatalf("create batch applied=%d err=%v", applied, err)
	}
	if applied, err := idx.PutLocalBatchCAS(create); err != nil || applied != 0 {
		t.Fatalf("idempotent retry applied=%d err=%v", applied, err)
	}
	firstHash := RecordHash(first)
	secondHash := RecordHash(second)
	firstUpdate := signedPersonalRecordWithPath(t, priv, "batch/a", 2, "one-v2", 0)
	secondUpdate := signedPersonalRecordWithPath(t, priv, "batch/b", 2, "two-v2", 0)
	wrong := chainhash.DoubleHashH([]byte("wrong"))
	conflict := []CASMutation{
		{Record: firstUpdate, Precondition: WritePrecondition{ExpectedHash: &firstHash}},
		{Record: secondUpdate, Precondition: WritePrecondition{ExpectedHash: &wrong}},
	}
	if applied, err := idx.PutLocalBatchCAS(conflict); !errors.Is(err, ErrWriteConflict) || applied != 0 {
		t.Fatalf("conflicting batch applied=%d err=%v", applied, err)
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
	if applied, err := idx.PutLocalBatchCAS(valid); err != nil || applied != 2 {
		t.Fatalf("valid update batch applied=%d err=%v", applied, err)
	}
}

func TestBatchCASRequiresExactNextSequence(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	initial := signedPersonalRecordWithPath(t, priv, "strict-seq", 1, "one", 0)
	if applied, err := idx.PutLocalCAS(initial, WritePrecondition{ExpectAbsent: true}); err != nil || !applied {
		t.Fatalf("initial applied=%v err=%v", applied, err)
	}
	hash := RecordHash(initial)
	skipped := signedPersonalRecordWithPath(t, priv, "strict-seq", 3, "three", 0)
	if applied, err := idx.PutLocalCAS(skipped, WritePrecondition{ExpectedHash: &hash}); !errors.Is(err, ErrWriteConflict) || applied {
		t.Fatalf("skipped sequence applied=%v err=%v", applied, err)
	}
	next := signedPersonalRecordWithPath(t, priv, "strict-seq", 2, "two", 0)
	if applied, err := idx.PutLocalCAS(next, WritePrecondition{ExpectedHash: &hash}); err != nil || !applied {
		t.Fatalf("next sequence applied=%v err=%v", applied, err)
	}
}

func TestRemoteReplicationAllowsSequenceGap(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	initial := signedPersonalRecordWithPath(t, priv, "remote-gap", 1, "one", 0)
	if updated, err := idx.PutRemote(initial); err != nil || !updated {
		t.Fatalf("initial remote updated=%v err=%v", updated, err)
	}
	later := signedPersonalRecordWithPath(t, priv, "remote-gap", 5, "five", 0)
	if updated, err := idx.PutRemote(later); err != nil || !updated {
		t.Fatalf("gapped remote updated=%v err=%v", updated, err)
	}
}

func TestBatchCASPathPreconditionRejectsStaleDirectory(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordWithPath(t, priv, "path/a", 1, "a1", 0)
	second := signedPersonalRecordWithPath(t, priv, "path/b", 1, "b1", 0)
	if applied, err := idx.PutLocalBatchCAS([]CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}); err != nil || applied != 2 {
		t.Fatalf("create applied=%d err=%v", applied, err)
	}
	path, err := CollectionPathForKey(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	secondHash := RecordHash(second)
	secondUpdate := signedPersonalRecordWithPath(t, priv, "path/b", 2, "b2", 0)
	if applied, err := idx.PutLocalCAS(secondUpdate, WritePrecondition{ExpectedHash: &secondHash}); err != nil || !applied {
		t.Fatalf("concurrent update applied=%v err=%v", applied, err)
	}
	firstHash := RecordHash(first)
	firstUpdate := signedPersonalRecordWithPath(t, priv, "path/a", 2, "a2", 0)
	applied, err := idx.PutLocalBatchCASWithOptions([]CASMutation{{
		Record: firstUpdate, Precondition: WritePrecondition{ExpectedHash: &firstHash},
	}}, BatchCASOptions{PathPreconditions: []PathWritePrecondition{{
		Path: path, ExpectedRoot: stale.ActiveRoot, ExpectedGeneration: stale.Generation,
	}}})
	if !errors.Is(err, ErrWriteConflict) || applied != 0 {
		t.Fatalf("stale path applied=%d err=%v", applied, err)
	}
	got, err := idx.Get(first.Key)
	if err != nil || RecordHash(got) != firstHash {
		t.Fatalf("stale path partially updated err=%v", err)
	}
}

func TestBatchCASPathGenerationAdvancesOnce(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordWithPath(t, priv, "generation/a", 1, "a1", 0)
	second := signedPersonalRecordWithPath(t, priv, "generation/b", 1, "b1", 0)
	if applied, err := idx.PutLocalBatchCAS([]CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}); err != nil || applied != 2 {
		t.Fatalf("create applied=%d err=%v", applied, err)
	}
	path, err := CollectionPathForKey(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	firstHash := RecordHash(first)
	secondHash := RecordHash(second)
	firstUpdate := signedPersonalRecordWithPath(t, priv, "generation/a", 2, "a2", 0)
	secondUpdate := signedPersonalRecordWithPath(t, priv, "generation/b", 2, "b2", 0)
	applied, err := idx.PutLocalBatchCASWithOptions([]CASMutation{
		{Record: firstUpdate, Precondition: WritePrecondition{ExpectedHash: &firstHash}},
		{Record: secondUpdate, Precondition: WritePrecondition{ExpectedHash: &secondHash}},
	}, BatchCASOptions{PathPreconditions: []PathWritePrecondition{{
		Path: path, ExpectedRoot: before.ActiveRoot, ExpectedGeneration: before.Generation,
	}}})
	if err != nil || applied != 2 {
		t.Fatalf("path update applied=%d err=%v", applied, err)
	}
	after, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation+1 {
		t.Fatalf("path generation=%d want=%d", after.Generation, before.Generation+1)
	}
}

func TestDirectorySyncIsByteBoundedAndVerifiable(t *testing.T) {
	idx := blobTestIndexer(t, 4)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	accountID := AccountID(priv.PubKey().SerializeCompressed())
	prefix := "/blob/" + accountID
	for n := 0; n < 4; n++ {
		record := signedFreeBlobRecord(t, priv, string(rune('a'+n)), 1,
			bytes.Repeat([]byte{byte(n + 1)}, wire.MaxDKVSBlobValueSize))
		if _, err := idx.PutLocal(record); err != nil {
			t.Fatalf("put blob %d: %v", n, err)
		}
	}
	var cursor []byte
	var root chainhash.Hash
	all := make([]*wire.DKVSRecord, 0, 4)
	pages := 0
	for {
		records, next, done, pageRoot, err := idx.SyncDirectory(prefix, cursor, wire.MaxDKVSRecordsPerMsg)
		if err != nil {
			t.Fatal(err)
		}
		if pages == 0 {
			root = pageRoot
		} else if pageRoot != root {
			t.Fatalf("directory root changed between pages")
		}
		msg := &wire.MsgDKVSSyncResponse{SessionID: 1, Records: records, NextCursor: next, Done: done, CheckpointRoot: pageRoot}
		var encoded bytes.Buffer
		if err := msg.BtcEncode(&encoded, wire.ProtocolVersion, wire.BaseEncoding); err != nil {
			t.Fatalf("page %d exceeds wire bounds: %v", pages, err)
		}
		all = append(all, records...)
		pages++
		if done {
			break
		}
		if len(next) == 0 || bytes.Equal(next, cursor) {
			t.Fatalf("non-progressing cursor on page %d", pages)
		}
		cursor = append(cursor[:0], next...)
	}
	if pages < 2 || len(all) != 4 {
		t.Fatalf("pages=%d records=%d", pages, len(all))
	}
	computed, err := DirectoryRootFromRecords(all, 1)
	if err != nil || computed != root {
		t.Fatalf("directory root computed=%s want=%s err=%v", computed, root, err)
	}
}

func TestDirectoryRootIncludesTombstoneWhileP2PRootIsActiveOnly(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithPath(t, priv, "deleted/item", 1, "value", 0)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/deleted"
	_, _, _, before, err := idx.SyncDirectory(prefix, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	tombstone, err := NewSignedTombstone(priv, record.Key, RecordOptions{Seq: 2, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(tombstone); err != nil {
		t.Fatal(err)
	}
	directoryRecords, _, done, directoryRoot, err := idx.SyncDirectory(prefix, nil, 10)
	if err != nil || !done || len(directoryRecords) != 1 || !IsTombstone(directoryRecords[0].Flags) {
		t.Fatalf("directory tombstone records=%d done=%v err=%v", len(directoryRecords), done, err)
	}
	if directoryRoot == before {
		t.Fatal("directory root did not change after delete")
	}
	_, _, _, p2pRoot, err := idx.SyncFiltered(nil, 10,
		[]Subscription{{Type: SubscriptionPrefix, Target: prefix}})
	if err != nil {
		t.Fatal(err)
	}
	emptyRoot, err := recordsRoot(nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if p2pRoot != emptyRoot || directoryRoot == p2pRoot {
		t.Fatalf("p2p root=%s directory root=%s empty=%s", p2pRoot, directoryRoot, emptyRoot)
	}
}

func TestDirectoryWatchObservesTTLExpiryWithoutMutation(t *testing.T) {
	idx := blobTestIndexer(t, 1)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), "watch/ttl")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewSignedRecord(priv, key, []byte("temporary"), RecordOptions{
		Seq: 1, TTL: 250,
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewFreeLocalFeeProof(record.Key, "personal", uint32(RecordSize(record)), 0)
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	SignRecord(priv, record)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/watch"
	_, _, _, root, err := idx.SyncDirectory(prefix, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	next, changed, err := idx.WaitDirectory(ctx, prefix, root)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || next == root {
		t.Fatalf("TTL expiry not observed changed=%v root=%s next=%s", changed, root, next)
	}
}
