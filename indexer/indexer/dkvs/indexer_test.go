package dkvs

import (
	"encoding/hex"
	"errors"
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/wire"
)

func testIndexer(t *testing.T) *Indexer {
	t.Helper()
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, Config{AllowFreeLocal: true, CurrentHeight: func() uint64 { return 1 }})
}

func signedPersonalRecord(t *testing.T, seq uint64, value string, flags uint32) *wire.DKVSRecord {
	t.Helper()
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return signedPersonalRecordWithKey(t, priv, seq, value, flags)
}

func signedPersonalRecordWithKey(t *testing.T, priv *btcec.PrivateKey, seq uint64, value string, flags uint32) *wire.DKVSRecord {
	t.Helper()
	return signedPersonalRecordWithPath(t, priv, "profile", seq, value, flags)
}

func signedPersonalRecordWithPath(t *testing.T, priv *btcec.PrivateKey, path string, seq uint64, value string, flags uint32) *wire.DKVSRecord {
	t.Helper()
	pub := priv.PubKey().SerializeCompressed()
	key := "/personal/" + personalAccountID(pub) + "/" + path
	record := &wire.DKVSRecord{
		Version:      Version,
		Key:          key,
		Value:        []byte(value),
		PubKey:       pub,
		Seq:          seq,
		IssueTime:    currentUnixMilli(),
		TTL:          60_000,
		ExpiryHeight: 100,
		Flags:        flags,
	}
	hash := SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	return record
}

func TestParseKey(t *testing.T) {
	pub := make([]byte, 33)
	account := personalAccountID(pub)
	if _, err := ParseKey("/personal/" + account + "/profile"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, key := range []string{
		"/mail/box/msg/msg-1",
		"/mail/box/share/pkg/share-1",
		"/blob/object/manifest",
		"/blob/object/chunk/0",
		"/tmp/random",
		"/svc/service/path",
		"/name/alice",
	} {
		if _, err := ParseKey(key); err != nil {
			t.Fatalf("valid key %s rejected: %v", key, err)
		}
	}
	if _, err := ParseKey("/bad/abc"); err != ErrInvalidNamespace {
		t.Fatalf("invalid namespace err=%v", err)
	}
	if _, err := ParseKey("/personal/ABC/profile"); err != ErrInvalidKey {
		t.Fatalf("invalid segment err=%v", err)
	}
	for _, key := range []string{
		"/personal/abc/profile",
		"/mail/box/other/msg-1",
		"/mail/box/msg",
		"/mail/box/share/pkg",
		"/blob/object/chunk/0/extra",
		"/blob/object",
		"/tmp/random/extra",
		"/svc/service",
	} {
		if _, err := ParseKey(key); err != ErrInvalidKey {
			t.Fatalf("invalid key %s err=%v", key, err)
		}
	}
	for _, prefix := range []string{
		"/personal/" + account,
		"/mail/box",
		"/blob/object",
		"/svc/service",
	} {
		if _, err := ParsePrefix(prefix); err != nil {
			t.Fatalf("valid prefix %s rejected: %v", prefix, err)
		}
	}
}

func TestPutSelectAndTombstone(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	old := signedPersonalRecordWithKey(t, priv, 1, "old", 0)
	if updated, err := idx.PutLocal(old); err != nil || !updated {
		t.Fatalf("put old updated=%v err=%v", updated, err)
	}
	newer := signedPersonalRecordWithKey(t, priv, 2, "new", 0)
	if updated, err := idx.PutLocal(newer); err != nil || !updated {
		t.Fatalf("put newer updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(old.Key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "new" {
		t.Fatalf("selector kept %q", string(got.Value))
	}
	tombstone := signedPersonalRecordWithKey(t, priv, 3, "", FlagTombstone)
	if updated, err := idx.PutLocal(tombstone); err != nil || !updated {
		t.Fatalf("put tombstone updated=%v err=%v", updated, err)
	}
	got, err = idx.Get(old.Key)
	if err != nil {
		t.Fatalf("get tombstone: %v", err)
	}
	if !IsTombstone(got.Flags) {
		t.Fatalf("expected tombstone")
	}
	badTombstone := signedPersonalRecordWithKey(t, priv, 4, "not-empty", FlagTombstone)
	if _, err := idx.PutLocal(badTombstone); err != ErrInvalidRecord {
		t.Fatalf("bad tombstone err=%v", err)
	}
}

func TestPersonalPermissionAndCheckpoint(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("put updated=%v err=%v", updated, err)
	}
	bad := signedPersonalRecordWithKey(t, priv, 2, "bad", 0)
	bad.Key = "/personal/" + hex.EncodeToString(make([]byte, 32)) + "/profile"
	hash := SigningHash(bad)
	bad.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	if _, err := idx.PutLocal(bad); err != ErrPermissionDenied {
		t.Fatalf("permission err=%v", err)
	}
	cp, err := idx.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if cp.ActiveRecordCount != 1 || cp.ActiveRecordRoot == "" {
		t.Fatalf("bad checkpoint %#v", cp)
	}
}

type testResolver struct {
	nameKey    []byte
	serviceKey []byte
}

func (r testResolver) CanWriteName(_ string, pubKey []byte) error {
	if string(pubKey) != string(r.nameKey) {
		return ErrPermissionDenied
	}
	return nil
}

func (r testResolver) CanWriteService(_ string, pubKey []byte) error {
	if string(pubKey) != string(r.serviceKey) {
		return ErrPermissionDenied
	}
	return nil
}

type testFeeVerifier struct {
	err error
}

func (v testFeeVerifier) VerifyFeeProof([32]byte, string, int, []byte) error {
	return v.err
}

func signedRecordForKey(t *testing.T, priv *btcec.PrivateKey, key string, seq uint64) *wire.DKVSRecord {
	t.Helper()
	record := &wire.DKVSRecord{
		Version:      Version,
		Key:          key,
		Value:        []byte("value"),
		PubKey:       priv.PubKey().SerializeCompressed(),
		Seq:          seq,
		IssueTime:    currentUnixMilli(),
		TTL:          60_000,
		ExpiryHeight: 100,
	}
	hash := SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	return record
}

func TestNameServiceResolverAndFeeVerifier(t *testing.T) {
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	namePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	svcPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	idx := New(db, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		Resolver: testResolver{
			nameKey:    namePriv.PubKey().SerializeCompressed(),
			serviceKey: svcPriv.PubKey().SerializeCompressed(),
		},
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, namePriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("name put updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, svcPriv, "/svc/wallet/config", 1)); err != nil || !updated {
		t.Fatalf("svc put updated=%v err=%v", updated, err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, namePriv, "/svc/wallet/config", 2)); err != ErrPermissionDenied {
		t.Fatalf("svc permission err=%v", err)
	}

	feeErr := errors.New("fee rejected")
	db2 := dbpkg.NewKVDB(t.TempDir())
	if db2 == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db2.Close() })
	feeIdx := New(db2, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		FeeVerifier:    testFeeVerifier{err: feeErr},
	})
	if _, err := feeIdx.PutLocal(signedPersonalRecord(t, 1, "value", 0)); err != feeErr {
		t.Fatalf("fee verifier err=%v", err)
	}
}

func TestSyncPaginationDoesNotSkipCursorRecord(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	for n, path := range []string{"a", "b", "c"} {
		record := signedPersonalRecordWithPath(t, priv, path, uint64(n+1), path, 0)
		if _, err := idx.PutLocal(record); err != nil {
			t.Fatal(err)
		}
	}
	first, cursor, done, _, err := idx.Sync(nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if done || len(first) != 2 || len(cursor) == 0 {
		t.Fatalf("bad first page len=%d done=%v cursor=%x", len(first), done, cursor)
	}
	second, _, done, _, err := idx.Sync(cursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !done || len(second) != 1 {
		t.Fatalf("bad second page len=%d done=%v", len(second), done)
	}
}
