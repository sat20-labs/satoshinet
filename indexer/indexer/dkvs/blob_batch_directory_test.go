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

func signedPersonalRecordV1(t *testing.T, priv *btcec.PrivateKey, path string, seq, pathGeneration uint64, value string) *wire.DKVSRecord {
	t.Helper()
	record := signedPersonalRecordWithPath(t, priv, path, seq, value, 0)
	record.PathGeneration = pathGeneration
	signRecord(t, priv, record)
	return record
}

func signedRelayablePersonalRecordV1(t *testing.T, priv *btcec.PrivateKey, path string, seq, pathGeneration uint64, value string) *wire.DKVSRecord {
	t.Helper()
	record := signedPersonalRecordV1(t, priv, path, seq, pathGeneration, value)
	record.TTL = 0
	record.ExpiryHeight = 100
	record.FeeProof = nil
	signRecord(t, priv, record)
	return record
}

func currentPathCondition(t *testing.T, idx *Indexer, key string) PathWritePrecondition {
	t.Helper()
	path, err := CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	return PathWritePrecondition{
		Path:               path,
		ExpectedRoot:       meta.StateRoot,
		ExpectedGeneration: meta.Generation,
	}
}

func putSingleCASV1(idx *Indexer, record *wire.DKVSRecord, precondition WritePrecondition,
	pathPrecondition PathWritePrecondition) (bool, error) {
	result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: record, Precondition: precondition,
	}}, BatchCASOptions{PathPreconditions: []PathWritePrecondition{pathPrecondition}})
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
	first := signedPersonalRecordV1(t, priv, "batch/a", 1, 1, "one")
	second := signedPersonalRecordV1(t, priv, "batch/b", 1, 2, "two")
	create := []CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}
	initialCondition := currentPathCondition(t, idx, first.Key)
	createOptions := BatchCASOptions{PathPreconditions: []PathWritePrecondition{initialCondition}}
	if result, err := idx.PutLocalBatchCASResultWithOptions(create, createOptions); err != nil || result.Applied != 2 {
		t.Fatalf("create batch result=%#v err=%v", result, err)
	}
	if result, err := idx.PutLocalBatchCASResultWithOptions(create, createOptions); err != nil || result.Applied != 0 {
		t.Fatalf("idempotent retry result=%#v err=%v", result, err)
	}
	firstHash := RecordHash(first)
	secondHash := RecordHash(second)
	firstUpdate := signedPersonalRecordV1(t, priv, "batch/a", 2, 3, "one-v2")
	secondUpdate := signedPersonalRecordV1(t, priv, "batch/b", 2, 4, "two-v2")
	wrong := chainhash.DoubleHashH([]byte("wrong"))
	conflict := []CASMutation{
		{Record: firstUpdate, Precondition: WritePrecondition{ExpectedHash: &firstHash}},
		{Record: secondUpdate, Precondition: WritePrecondition{ExpectedHash: &wrong}},
	}
	updateCondition := currentPathCondition(t, idx, first.Key)
	updateOptions := BatchCASOptions{PathPreconditions: []PathWritePrecondition{updateCondition}}
	if result, err := idx.PutLocalBatchCASResultWithOptions(conflict, updateOptions); !errors.Is(err, ErrWriteConflict) || result != nil {
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
	if result, err := idx.PutLocalBatchCASResultWithOptions(valid, updateOptions); err != nil || result.Applied != 2 {
		t.Fatalf("valid update batch result=%#v err=%v", result, err)
	}
}

func TestBatchCASRequiresExactNextSequence(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	initial := signedPersonalRecordV1(t, priv, "strict-seq", 1, 1, "one")
	initialCondition := currentPathCondition(t, idx, initial.Key)
	if applied, err := putSingleCASV1(idx, initial, WritePrecondition{ExpectAbsent: true}, initialCondition); err != nil || !applied {
		t.Fatalf("initial applied=%v err=%v", applied, err)
	}
	hash := RecordHash(initial)
	pathCondition := currentPathCondition(t, idx, initial.Key)
	skipped := signedPersonalRecordV1(t, priv, "strict-seq", 3, 2, "three")
	if applied, err := putSingleCASV1(idx, skipped, WritePrecondition{ExpectedHash: &hash}, pathCondition); !errors.Is(err, ErrInvalidSequence) || applied {
		t.Fatalf("skipped sequence applied=%v err=%v", applied, err)
	}
	next := signedPersonalRecordV1(t, priv, "strict-seq", 2, 2, "two")
	if applied, err := putSingleCASV1(idx, next, WritePrecondition{ExpectedHash: &hash}, pathCondition); err != nil || !applied {
		t.Fatalf("next sequence applied=%v err=%v", applied, err)
	}
}

func TestRemoteReplicationRejectsPathGenerationGap(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{FeeVerifier: testFeeVerifier{}})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	initial := signedRelayablePersonalRecordV1(t, priv, "remote-gap", 1, 1, "one")
	if updated, err := idx.PutRemoteV1(initial, "peer-a"); err != nil || !updated {
		t.Fatalf("initial remote updated=%v err=%v", updated, err)
	}
	later := signedRelayablePersonalRecordV1(t, priv, "remote-gap", 5, 3, "five")
	if updated, err := idx.PutRemoteV1(later, "peer-a"); !errors.Is(err, ErrPathGenerationGap) || updated {
		t.Fatalf("gapped remote updated=%v err=%v", updated, err)
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

func TestBatchCASPathPreconditionRejectsStaleDirectory(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordV1(t, priv, "path/a", 1, 1, "a1")
	second := signedPersonalRecordV1(t, priv, "path/b", 1, 2, "b1")
	createOptions := BatchCASOptions{PathPreconditions: []PathWritePrecondition{currentPathCondition(t, idx, first.Key)}}
	if result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}, createOptions); err != nil || result.Applied != 2 {
		t.Fatalf("create result=%#v err=%v", result, err)
	}
	stale := currentPathCondition(t, idx, first.Key)
	secondHash := RecordHash(second)
	secondUpdate := signedPersonalRecordV1(t, priv, "path/b", 2, 3, "b2")
	if applied, err := putSingleCASV1(idx, secondUpdate, WritePrecondition{ExpectedHash: &secondHash}, stale); err != nil || !applied {
		t.Fatalf("concurrent update applied=%v err=%v", applied, err)
	}
	firstHash := RecordHash(first)
	firstUpdate := signedPersonalRecordV1(t, priv, "path/a", 2, 3, "a2")
	result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: firstUpdate, Precondition: WritePrecondition{ExpectedHash: &firstHash},
	}}, BatchCASOptions{PathPreconditions: []PathWritePrecondition{stale}})
	if !errors.Is(err, ErrStaleGeneration) || result != nil {
		t.Fatalf("stale path result=%#v err=%v", result, err)
	}
	got, err := idx.Get(first.Key)
	if err != nil || RecordHash(got) != firstHash {
		t.Fatalf("stale path partially updated err=%v", err)
	}
}

func TestBatchCASPathGenerationAdvancesPerMutation(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedPersonalRecordV1(t, priv, "generation/a", 1, 1, "a1")
	second := signedPersonalRecordV1(t, priv, "generation/b", 1, 2, "b1")
	if result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}, BatchCASOptions{PathPreconditions: []PathWritePrecondition{currentPathCondition(t, idx, first.Key)}}); err != nil || result.Applied != 2 {
		t.Fatalf("create result=%#v err=%v", result, err)
	}
	beforeCondition := currentPathCondition(t, idx, first.Key)
	firstHash := RecordHash(first)
	secondHash := RecordHash(second)
	firstUpdate := signedPersonalRecordV1(t, priv, "generation/a", 2, beforeCondition.ExpectedGeneration+1, "a2")
	secondUpdate := signedPersonalRecordV1(t, priv, "generation/b", 2, beforeCondition.ExpectedGeneration+2, "b2")
	result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{
		{Record: firstUpdate, Precondition: WritePrecondition{ExpectedHash: &firstHash}},
		{Record: secondUpdate, Precondition: WritePrecondition{ExpectedHash: &secondHash}},
	}, BatchCASOptions{PathPreconditions: []PathWritePrecondition{beforeCondition}})
	if err != nil || result.Applied != 2 {
		t.Fatalf("path update result=%#v err=%v", result, err)
	}
	after, err := idx.GetPathMeta(beforeCondition.Path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != beforeCondition.ExpectedGeneration+2 {
		t.Fatalf("path generation=%d want=%d", after.Generation, beforeCondition.ExpectedGeneration+2)
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
