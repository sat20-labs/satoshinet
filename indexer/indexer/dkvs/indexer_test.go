package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func testIndexer(t *testing.T) *Indexer {
	t.Helper()
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, Config{
		EndpointID:     "test-core-node",
		AllowFreeLocal: true,
		FeeVerifier:    JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight:  func() uint64 { return 1 },
	})
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
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/" + path
	record, err := NewSignedRecord(priv, key, []byte(value), RecordOptions{
		Seq: seq, TTL: 100, Flags: flags,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func signedFreePersonalRecord(t *testing.T, priv *btcec.PrivateKey, path string, seq uint64, value string, flags uint32) *wire.DKVSRecord {
	t.Helper()
	record := signedPersonalRecordWithPath(t, priv, path, seq, value, flags)
	parsed, err := ParseKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewFreeLocalFeeProof(record.Key, parsed.Namespace, wire.MaxDKVSRecordSize, RecordExpiryHeight(record))
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

func TestFreeLocalRecordsStayOnAcceptingNode(t *testing.T) {
	var events []*NotifyEvent
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{
			Enabled:             true,
			MaxTTL:              120_000,
			MaxRecordsPerSigner: 2,
			MaxBytesPerSigner:   1 << 20,
			MaxTotalRecords:     10,
			MaxTotalBytes:       1 << 20,
		},
		Notify: func(event *NotifyEvent) { events = append(events, event) },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedFreePersonalRecord(t, priv, "local", 1, "value", 0)
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("put local updated=%v err=%v", updated, err)
	}
	if len(events) != 1 || events[0].Relay {
		t.Fatalf("free local notify must not relay: %#v", events)
	}
	if _, err := idx.Get(record.Key); err != nil {
		t.Fatalf("local get failed: %v", err)
	}
	if _, err := idx.GetForRelay(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("relay get err=%v", err)
	}
	if _, err := idx.GetByHashForRelay(RecordHash(record)); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("relay hash get err=%v", err)
	}
	records, _, done, _, err := idx.Sync(nil, 10)
	if err != nil || !done || len(records) != 0 {
		t.Fatalf("free local sync records=%d done=%v err=%v", len(records), done, err)
	}
	clientRecords, _, clientDone, _, err := idx.SyncFilteredForClient(nil, 10,
		[]Subscription{{Type: SubscriptionKey, Target: record.Key}})
	if err != nil || !clientDone || len(clientRecords) != 1 ||
		RecordHash(clientRecords[0]) != RecordHash(record) {
		t.Fatalf("free local client sync records=%d done=%v err=%v",
			len(clientRecords), clientDone, err)
	}
	checkpoint, err := idx.Checkpoint()
	if err != nil || checkpoint.ActiveRecordCount != 0 {
		t.Fatalf("free local checkpoint=%#v err=%v", checkpoint, err)
	}
	snapshot, err := idx.Snapshot()
	if err != nil || len(snapshot.Records) != 0 {
		t.Fatalf("free local snapshot=%#v err=%v", snapshot, err)
	}
	emptyRoot, err := recordsRoot(nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.ApplyMirror([]Subscription{{Type: SubscriptionKey, Target: record.Key}}, nil, emptyRoot); err != nil {
		t.Fatalf("mirror free-local omission err=%v", err)
	}
	if _, err := idx.Get(record.Key); err != nil {
		t.Fatalf("mirror removed free-local record: %v", err)
	}
	remote := testIndexer(t)
	if _, err := remote.PutRemote(record); !errors.Is(err, ErrFreeLocalNotRelayable) {
		t.Fatalf("remote free local err=%v", err)
	}

	tombstone := signedFreePersonalRecord(t, priv, "local", 2, "", FlagTombstone)
	if updated, err := idx.PutLocal(tombstone); err != nil || !updated {
		t.Fatalf("local tombstone updated=%v err=%v", updated, err)
	}
	if len(events) != 2 || events[1].Relay {
		t.Fatalf("free local tombstone must not relay: %#v", events)
	}
	if _, err := idx.GetForRelay(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("relay tombstone err=%v", err)
	}
	if _, err := remote.PutRemote(tombstone); !errors.Is(err, ErrFreeLocalNotRelayable) {
		t.Fatalf("remote free tombstone err=%v", err)
	}
}

func TestFreeLocalCacheQuota(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{
			Enabled:             true,
			MaxTTL:              120_000,
			MaxRecordsPerSigner: 1,
			MaxBytesPerSigner:   1 << 20,
			MaxTotalRecords:     1,
			MaxTotalBytes:       1 << 20,
		},
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	first := signedFreePersonalRecord(t, priv, "one", 1, "value", 0)
	if _, err := idx.PutLocal(first); err != nil {
		t.Fatal(err)
	}
	second := signedFreePersonalRecord(t, priv, "two", 1, "value", 0)
	if _, err := idx.PutLocal(second); !errors.Is(err, ErrFreeLocalQuotaExceeded) {
		t.Fatalf("per signer quota err=%v", err)
	}
	other, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	third := signedFreePersonalRecord(t, other, "one", 1, "value", 0)
	if _, err := idx.PutLocal(third); !errors.Is(err, ErrFreeLocalQuotaExceeded) {
		t.Fatalf("total quota err=%v", err)
	}

	shortTTL := signedFreePersonalRecord(t, other, "two", 1, "value", 0)
	shortTTL.TTL = 120_001
	SignRecord(other, shortTTL)
	if _, err := idx.PutLocal(shortTTL); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("ttl limit err=%v", err)
	}
}

func TestLocalCacheAutopayVerifierAcceptsOnlyExplicitFreeLocal(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{
			Enabled:             true,
			MaxTTL:              120_000,
			MaxRecordsPerSigner: 2,
			MaxBytesPerSigner:   1 << 20,
			MaxTotalRecords:     10,
			MaxTotalBytes:       1 << 20,
		},
		FeeVerifier: LocalCacheAutopayFeeVerifier{
			AutopayFeeVerifier: AutopayFeeVerifier{FullRecordFeePerBlock: "1"},
			AllowFreeLocal:     true,
		},
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	free := signedFreePersonalRecord(t, priv, "autopay-free", 1, "value", 0)
	if updated, err := idx.PutLocal(free); err != nil || !updated {
		t.Fatalf("free local autopay wrapper updated=%v err=%v", updated, err)
	}
	withoutProof := signedPersonalRecordWithPath(t, priv, "autopay-paid", 1, "value", 0)
	if _, err := idx.PutLocal(withoutProof); !errors.Is(err, ErrFeeProofRequired) {
		t.Fatalf("missing proof err=%v", err)
	}
}

func TestLocalCacheAutopayVerifierAcceptsFreeLocalCAS(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{
			Enabled:             true,
			MaxTTL:              120_000,
			MaxRecordsPerSigner: 4,
			MaxBytesPerSigner:   1 << 20,
			MaxTotalRecords:     10,
			MaxTotalBytes:       1 << 20,
		},
		FeeVerifier: LocalCacheAutopayFeeVerifier{
			AutopayFeeVerifier: AutopayFeeVerifier{FullRecordFeePerBlock: "1"},
			AllowFreeLocal:     true,
		},
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	single := signedFreePersonalRecord(t, priv, "cas/single", 1, "single", 0)
	if updated, err := idx.PutLocalCAS(single, WritePrecondition{ExpectAbsent: true}); err != nil || !updated {
		t.Fatalf("free local single CAS updated=%v err=%v", updated, err)
	}
	first := signedFreePersonalRecord(t, priv, "cas/batch-a", 1, "first", 0)
	second := signedFreePersonalRecord(t, priv, "cas/batch-b", 1, "second", 0)
	applied, err := idx.PutLocalBatchCAS([]CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	})
	if err != nil || applied != 2 {
		t.Fatalf("free local batch CAS applied=%d err=%v", applied, err)
	}
}

func testMailMsgKey(t *testing.T, mailboxPubKey, senderPubKey []byte, msgID string) string {
	t.Helper()
	key, err := MailMsgKey(AccountID(mailboxPubKey), AccountID(senderPubKey), msgID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestParseKey(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	account := AccountID(priv.PubKey().SerializeCompressed())
	senderAccount := AccountID(sender.PubKey().SerializeCompressed())
	if _, err := ParseKey("/personal/" + account + "/profile"); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, key := range []string{
		"/mail/" + account + "/msg/" + senderAccount + "/msg-1",
		"/mail/" + account + "/share/pkg/share-1",
		"/blob/" + account + "/object",
		"/tmp/random",
		"/svc/service/path",
		"/name/alice",
		"/sys/params",
		"/sys/checkpoint/1",
		"/sys/snapshot/1",
		"/sys/miner/miner-1",
		"/sys/pool/pool-1",
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
		"/mail/" + account + "/other/msg-1",
		"/mail/" + account + "/msg/msg-1",
		"/mail/" + account + "/msg/not-an-account/msg-1",
		"/mail/" + account + "/share/pkg",
		"/blob/" + account + "/object/manifest",
		"/blob/" + account + "/object/chunk/0",
		"/blob/not-an-account/object",
		"/tmp/random/extra",
		"/svc/service",
		"/name/alice/profile",
		"/sys",
		"/sys/params/extra",
		"/sys/checkpoint",
		"/sys/checkpoint/1/extra",
		"/sys/unknown/1",
	} {
		if _, err := ParseKey(key); err != ErrInvalidKey {
			t.Fatalf("invalid key %s err=%v", key, err)
		}
	}
	for _, prefix := range []string{
		"/personal/" + account,
		"/mail/" + account,
		"/blob/" + account + "/object",
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
	if _, err := idx.GetByHash(RecordHash(old)); err != ErrRecordNotFound {
		t.Fatalf("old hash should be removed after update, err=%v", err)
	}
	if byHash, err := idx.GetByHash(RecordHash(newer)); err != nil || byHash.Seq != newer.Seq {
		t.Fatalf("new hash lookup record=%#v err=%v", byHash, err)
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
	if _, err := idx.Get(old.Key); err != ErrRecordNotFound {
		t.Fatalf("deleted record should not be readable: %v", err)
	}
	relay, err := idx.GetForRelay(old.Key)
	if err != nil || !IsTombstone(relay.Flags) {
		t.Fatalf("relay tombstone=%#v err=%v", relay, err)
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
	signRecord(t, priv, bad)
	if _, err := idx.PutLocal(bad); err != ErrPermissionDenied {
		t.Fatalf("wrong account signer err=%v", err)
	}
	cp, err := idx.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if cp.ActiveRecordCount != 1 || cp.ActiveRecordRoot == "" {
		t.Fatalf("bad checkpoint %#v", cp)
	}
}

func TestCheckpointCacheIsClonedAndInvalidated(t *testing.T) {
	idx := testIndexer(t)
	checkpoint, err := idx.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.NamespaceRoots["personal"] = "corrupt"
	cached, err := idx.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if cached.NamespaceRoots["personal"] == "corrupt" {
		t.Fatalf("checkpoint cache exposed mutable map")
	}
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(signedPersonalRecordWithKey(t, priv, 1, "value", 0)); err != nil {
		t.Fatal(err)
	}
	updated, err := idx.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if updated.ActiveRecordCount != 1 {
		t.Fatalf("checkpoint cache was not invalidated: count=%d", updated.ActiveRecordCount)
	}
}

func TestSnapshotMatchesCheckpointAndFiltersInactive(t *testing.T) {
	height := uint64(1)
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return height },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	active := signedPersonalRecordWithPath(t, priv, "active", 1, "active", 0)
	expired := signedPersonalRecordWithPath(t, priv, "expired", 1, "expired", 0)
	expired.IssueHeight = 1
	expired.TTL = 1
	signRecord(t, priv, expired)
	if _, err := idx.PutLocal(active); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(expired); err != nil {
		t.Fatal(err)
	}
	height = 2
	cp, err := idx.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := idx.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Checkpoint.ActiveRecordRoot != cp.ActiveRecordRoot ||
		snapshot.Checkpoint.ActiveRecordCount != cp.ActiveRecordCount {
		t.Fatalf("snapshot checkpoint=%#v checkpoint=%#v", snapshot.Checkpoint, cp)
	}
	if len(snapshot.Records) != 1 || snapshot.Records[0].Key != active.Key {
		t.Fatalf("snapshot records=%v", snapshot.Records)
	}
	if snapshot.CreatedAt == 0 {
		t.Fatalf("snapshot created_at missing")
	}
}

func TestApplySnapshot(t *testing.T) {
	source := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	if _, err := source.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	target := testIndexer(t)
	applied, err := target.ApplySnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("applied=%d", applied)
	}
	got, err := target.Get(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "value" {
		t.Fatalf("value=%s", got.Value)
	}
}

func TestApplySnapshotRejectsRootMismatch(t *testing.T) {
	source := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.PutLocal(signedPersonalRecordWithKey(t, priv, 1, "value", 0)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Checkpoint.ActiveRecordRoot = "00"
	target := testIndexer(t)
	if _, err := target.ApplySnapshot(snapshot); err != ErrInvalidSnapshot {
		t.Fatalf("apply bad snapshot err=%v", err)
	}
}

func TestValidateSnapshotRejectsNamespaceRootMismatch(t *testing.T) {
	source := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.PutLocal(signedPersonalRecordWithKey(t, priv, 1, "value", 0)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	delete(snapshot.Checkpoint.NamespaceRoots, "personal")
	if err := ValidateSnapshot(snapshot); err != ErrInvalidSnapshot {
		t.Fatalf("namespace root mismatch err=%v", err)
	}
	target := testIndexer(t)
	if _, err := target.ApplySnapshot(snapshot); err != ErrInvalidSnapshot {
		t.Fatalf("apply missing namespace root err=%v", err)
	}
}

func testIdentity(canonicalName string, signingKeys ...[]byte) DIDIdentity {
	return DIDIdentity{
		CanonicalName: canonicalName,
		NameID:        NormalizeNameID(canonicalName),
		SigningKeys:   signingKeys,
		Active:        true,
	}
}

type testFeeVerifier struct {
	err error
}

func (v testFeeVerifier) VerifyFeeProof(_, _ [32]byte, _ string, _ int, _ uint64, _ []byte) error {
	return v.err
}

type testAutopayStateProvider struct {
	states map[string]*AutopayContractState
	err    error
}

type countingAutopayStateProvider struct {
	state *AutopayContractState
	calls int
}

func (p *countingAutopayStateProvider) GetAutopayState(string) (*AutopayContractState, error) {
	p.calls++
	return cloneAutopayState(p.state), nil
}

func (p testAutopayStateProvider) GetAutopayState(contract string) (*AutopayContractState, error) {
	if p.err != nil {
		return nil, p.err
	}
	state, ok := p.states[contract]
	if !ok {
		return nil, ErrInvalidFeeProof
	}
	copyState := *state
	return &copyState, nil
}

type countingDIDResolver struct {
	names        map[string]DIDIdentity
	services     map[string]DIDIdentity
	nameCalls    int
	serviceCalls int
}

func (r *countingDIDResolver) ResolveName(name string) (DIDIdentity, error) {
	r.nameCalls++
	identity, ok := r.names[name]
	if !ok {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	return identity, nil
}

func (r *countingDIDResolver) ResolveService(serviceName string) (DIDIdentity, error) {
	r.serviceCalls++
	identity, ok := r.services[serviceName]
	if !ok {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	return identity, nil
}

func signedRecordForKey(t *testing.T, priv *btcec.PrivateKey, key string, seq uint64) *wire.DKVSRecord {
	t.Helper()
	return signedRecordWithValue(t, priv, key, seq, []byte("value"), 0)
}

func signedRecordWithValue(t *testing.T, priv *btcec.PrivateKey, key string, seq uint64, value []byte, flags uint32) *wire.DKVSRecord {
	t.Helper()
	record, err := NewSignedRecord(priv, key, value, RecordOptions{
		Seq: seq, TTL: 100, Flags: flags,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func signRecord(t *testing.T, priv *btcec.PrivateKey, record *wire.DKVSRecord) {
	t.Helper()
	SignRecord(priv, record)
	if len(record.Signature) == 0 {
		t.Fatal("record signing failed")
	}
}

func signedRecordWithStructuredFee(t *testing.T, priv *btcec.PrivateKey, key string, seq uint64, proof FeeProof) *wire.DKVSRecord {
	t.Helper()
	record := signedRecordWithValue(t, priv, key, seq, []byte("value"), 0)
	encoded, err := EncodeFeeProof(&proof)
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof = encoded
	signRecord(t, priv, record)
	return record
}

func signedRecordWithAutopayFee(t *testing.T, priv *btcec.PrivateKey, key string, seq uint64, contract string, _ uint64) *wire.DKVSRecord {
	t.Helper()
	record := signedRecordWithValue(t, priv, key, seq, []byte("value"), 0)
	record.TTL = 0
	parsed, err := ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewAutopayFeeProof(key, parsed.Namespace, wire.MaxDKVSRecordSize, 0, contract, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachSignedFeeProof(record, proof, priv); err != nil {
		t.Fatal(err)
	}
	signRecord(t, priv, record)
	return record
}

func testIndexerWithConfig(t *testing.T, cfg Config) *Indexer {
	t.Helper()
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	if cfg.CurrentHeight == nil {
		cfg.CurrentHeight = func() uint64 { return 1 }
	}
	if cfg.EndpointID == "" {
		cfg.EndpointID = "test-core-node"
	}
	if cfg.FeeVerifier == nil && cfg.AllowFreeLocal {
		cfg.FeeVerifier = JSONFeeVerifier{AllowFreeLocal: cfg.AllowFreeLocal}
	}
	return New(db, cfg)
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
		Resolver: StaticDIDResolver{
			Names: map[string]DIDIdentity{
				"alice": testIdentity("alice", namePriv.PubKey().SerializeCompressed()),
			},
			Services: map[string]DIDIdentity{
				"wallet": testIdentity("wallet", svcPriv.PubKey().SerializeCompressed()),
			},
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

func TestParseFeeProof(t *testing.T) {
	encoded, err := EncodeFeeProof(&FeeProof{
		Mode:         "oneshot",
		PoolContract: "pool",
		Payer:        "payer",
		PaymentTxID:  "payment",
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := ParseFeeProof(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Mode != FeeModeOneshot {
		t.Fatalf("mode=%s", proof.Mode)
	}
	if _, err := ParseFeeProof([]byte{1, 99}); err != ErrInvalidFeeProof {
		t.Fatalf("bad mode err=%v", err)
	}
	if _, err := ParseFeeProof(nil); err != ErrFeeProofRequired {
		t.Fatalf("missing proof err=%v", err)
	}
}

func TestFeeProofBuilders(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/profile"
	proof, err := NewOneshotFeeProof(key, "personal", 1024, 100, "pool", "payer", "payment", "10")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	verifier := JSONFeeVerifier{}
	keyHash := KeyHash(key)
	var keyHash32 [32]byte
	copy(keyHash32[:], keyHash[:])
	if err := verifier.VerifyFeeProof([32]byte{}, keyHash32, "personal", 100, 100, encoded); err != nil {
		t.Fatalf("oneshot verify err=%v", err)
	}
	lease, err := NewLeaseFeeProof(key, "personal", 1024, 100, "pool", "lease", "plan")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = EncodeFeeProof(lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.VerifyFeeProof([32]byte{}, keyHash32, "personal", 100, 100, encoded); err != nil {
		t.Fatalf("lease verify err=%v", err)
	}
	free, err := NewFreeLocalFeeProof(key, "personal", 1024, 100)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = EncodeFeeProof(free)
	if err != nil {
		t.Fatal(err)
	}
	freeVerifier := JSONFeeVerifier{AllowFreeLocal: true}
	if err := freeVerifier.VerifyFeeProof([32]byte{}, keyHash32, "personal", 100, 100, encoded); err != nil {
		t.Fatalf("free verify err=%v", err)
	}
	if _, err := NewOneshotFeeProof(key, "personal", 100, 100, "", "payer", "payment", ""); err != ErrInvalidFeeProof {
		t.Fatalf("bad oneshot err=%v", err)
	}
	if _, err := NewLeaseFeeProof(key, "personal", 100, 100, "pool", "", "plan"); err != ErrInvalidFeeProof {
		t.Fatalf("bad lease err=%v", err)
	}
	if _, err := NewFreeLocalFeeProof(key, "mail", 100, 100); err != ErrInvalidNamespace {
		t.Fatalf("mismatched namespace err=%v", err)
	}
	trimmed, err := NewOneshotFeeProof(key, " personal ", 1024, 100, " pool ", " payer ", " payment ", "10")
	if err != nil {
		t.Fatal(err)
	}
	if trimmed.PoolContract != "pool" || trimmed.Payer != "payer" || trimmed.PaymentTxID != "payment" {
		t.Fatalf("proof fields not normalized: %+v", trimmed)
	}
	if _, err := EncodeFeeProof(&FeeProof{Mode: "bad"}); err != ErrInvalidFeeProof {
		t.Fatalf("bad encode err=%v", err)
	}
}

func TestJSONFeeVerifierPutLocal(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/profile"
	record := signedRecordWithStructuredFee(t, priv, key, 1, FeeProof{
		Mode:         FeeModeOneshot,
		PoolContract: "dkvs-pool",
		Payer:        "payer",
		PaymentTxID:  "payment",
		PaidAmount:   "1",
	})
	idx := testIndexerWithConfig(t, Config{
		FeeVerifier: JSONFeeVerifier{},
	})
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("put structured fee updated=%v err=%v", updated, err)
	}
}

func TestDefaultFeeVerifierRejectsMissingProof(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	if _, err := idx.PutLocal(record); err != ErrFeeProofRequired {
		t.Fatalf("missing fee proof err=%v", err)
	}
}

func TestDefaultFeeVerifierRejectsArbitraryProof(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	record.FeeProof = []byte{1}
	signRecord(t, priv, record)
	if _, err := idx.PutLocal(record); err != ErrFeeProofRequired {
		t.Fatalf("arbitrary fee proof err=%v", err)
	}
}

func TestJSONFeeVerifierRejectsMalformedProof(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/profile"
	record := signedRecordWithStructuredFee(t, priv, key, 1, FeeProof{
		Mode:         FeeModeOneshot,
		PoolContract: "dkvs-pool",
		Payer:        "payer",
	})
	idx := testIndexerWithConfig(t, Config{
		FeeVerifier: JSONFeeVerifier{},
	})
	if _, err := idx.PutLocal(record); err != ErrInvalidFeeProof {
		t.Fatalf("malformed proof err=%v", err)
	}
}

func TestFeeProofCoveredByRecordSignature(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/profile"
	proof := FeeProof{
		Mode:         FeeModeOneshot,
		PoolContract: "dkvs-pool",
		Payer:        "payer",
		PaymentTxID:  "payment",
	}
	record := signedRecordWithStructuredFee(t, priv, key, 1, proof)
	tampered := proof
	tampered.PaymentTxID = "other-payment"
	encoded, err := EncodeFeeProof(&tampered)
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof = encoded
	idx := testIndexerWithConfig(t, Config{
		FeeVerifier: JSONFeeVerifier{},
	})
	if _, err := idx.PutLocal(record); err != ErrInvalidSignature {
		t.Fatalf("tampered fee proof signature err=%v", err)
	}
	signRecord(t, priv, record)
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("resigned fee proof put updated=%v err=%v", updated, err)
	}
}

func TestAutopayFeeVerifierCapacity(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	account := AccountID(priv.PubKey().SerializeCompressed())
	contract := "autopay-contract"
	recipient := "dkvs-fee-recipient"
	payer, err := P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndexerWithConfig(t, Config{
		FeeVerifier: AutopayFeeVerifier{
			StateProvider: testAutopayStateProvider{states: map[string]*AutopayContractState{
				contract: {
					TemplateName:      "autopay.tc",
					Deployer:          "deployer",
					Recipient:         recipient,
					FeeAssetName:      "sat",
					MinAmountPerBlock: "1",
					Status:            "active",
					CurrentBlock:      10,
					Delegates: map[string]AutopayDelegateState{
						payer: {
							AmountPerBlock: "2",
							Balance:        "10",
							LastPayHeight:  10,
							Status:         "active",
						},
					},
				},
			}},
			Recipient:             recipient,
			FeeAssetName:          "sat",
			FullRecordFeePerBlock: "1",
			AddressParams:         &chaincfg.TestNetParams,
		},
	})

	key1 := "/personal/" + account + "/profile1"
	key2 := "/personal/" + account + "/profile2"
	key3 := "/personal/" + account + "/profile3"
	if updated, err := idx.PutLocal(signedRecordWithAutopayFee(t, priv, key1, 1, contract, 100)); err != nil || !updated {
		t.Fatalf("autopay put 1 updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutLocal(signedRecordWithAutopayFee(t, priv, key2, 1, contract, 100)); err != nil || !updated {
		t.Fatalf("autopay put 2 updated=%v err=%v", updated, err)
	}
	if _, err := idx.PutLocal(signedRecordWithAutopayFee(t, priv, key3, 1, contract, 100)); err != ErrFeeCapacityExceeded {
		t.Fatalf("capacity err=%v", err)
	}
	if updated, err := idx.PutLocal(signedRecordWithAutopayFee(t, priv, key1, 2, contract, 100)); err != nil || !updated {
		t.Fatalf("autopay replacement updated=%v err=%v", updated, err)
	}
}

func TestAutopayCapacityIsIndependentPerDelegate(t *testing.T) {
	first, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	second, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	contract := "shared-autopay"
	recipient := "dkvs-fee-recipient"
	firstPayer, err := P2TRAddressFromPubKeyBytes(first.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	secondPayer, err := P2TRAddressFromPubKeyBytes(second.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	state := &AutopayContractState{
		TemplateName: "autopay.tc",
		CurrentBlock: 10,
		ServiceName:  "dkvs",
		Recipient:    recipient,
		FeeAssetName: "sat",
		Status:       "active",
		Delegates: map[string]AutopayDelegateState{
			firstPayer:  {AmountPerBlock: "1", Balance: "10", LastPayHeight: 10, Status: "active"},
			secondPayer: {AmountPerBlock: "1", Balance: "10", LastPayHeight: 10, Status: "active"},
		},
	}
	idx := testIndexerWithConfig(t, Config{FeeVerifier: AutopayFeeVerifier{
		StateProvider:         testAutopayStateProvider{states: map[string]*AutopayContractState{contract: state}},
		Contract:              contract,
		ServiceName:           "dkvs",
		Recipient:             recipient,
		FeeAssetName:          "sat",
		FullRecordFeePerBlock: "1",
		AddressParams:         &chaincfg.TestNetParams,
	}})
	firstKey := "/personal/" + AccountID(first.PubKey().SerializeCompressed()) + "/profile"
	secondKey := "/personal/" + AccountID(second.PubKey().SerializeCompressed()) + "/profile"
	if updated, err := idx.PutLocal(signedRecordWithAutopayFee(t, first, firstKey, 1, contract, 100)); err != nil || !updated {
		t.Fatalf("first delegate updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutLocal(signedRecordWithAutopayFee(t, second, secondKey, 1, contract, 100)); err != nil || !updated {
		t.Fatalf("second delegate updated=%v err=%v", updated, err)
	}
}

func TestHeightCachedAutopayStateProvider(t *testing.T) {
	height := uint64(10)
	base := &countingAutopayStateProvider{state: &AutopayContractState{
		TemplateName: "autopay.tc",
		Status:       "active",
		Delegates:    map[string]AutopayDelegateState{"payer": {Status: "active"}},
	}}
	cached := &HeightCachedAutopayStateProvider{Provider: base, CurrentHeight: func() uint64 { return height }}
	first, err := cached.GetAutopayState("contract")
	if err != nil {
		t.Fatal(err)
	}
	first.Status = "mutated"
	second, err := cached.GetAutopayState("contract")
	if err != nil {
		t.Fatal(err)
	}
	if base.calls != 1 || second.Status != "active" {
		t.Fatalf("calls=%d second=%#v", base.calls, second)
	}
	height++
	if _, err := cached.GetAutopayState("contract"); err != nil {
		t.Fatal(err)
	}
	if base.calls != 2 {
		t.Fatalf("calls after height change=%d", base.calls)
	}
}

func TestAutopayFeeVerifierRejectsInvalidStateAndPayer(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	account := AccountID(priv.PubKey().SerializeCompressed())
	key := "/personal/" + account + "/profile"
	contract := "autopay-contract"
	recipient := "dkvs-fee-recipient"
	payer, err := P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	baseState := AutopayContractState{
		TemplateName:      "autopay.tc",
		Deployer:          "deployer",
		Recipient:         recipient,
		FeeAssetName:      "sat",
		MinAmountPerBlock: "1",
		Status:            "active",
		CurrentBlock:      10,
		Delegates: map[string]AutopayDelegateState{
			payer: {
				AmountPerBlock: "1",
				Balance:        "10",
				Status:         "active",
			},
		},
	}
	tests := []struct {
		name   string
		state  AutopayContractState
		record *wire.DKVSRecord
	}{
		{
			name:  "inactive",
			state: func() AutopayContractState { s := baseState; s.Status = "funding"; return s }(),
		},
		{
			name: "missing delegate",
			state: func() AutopayContractState {
				s := baseState
				s.Delegates = nil
				return s
			}(),
		},
		{
			name:  "wrong recipient",
			state: func() AutopayContractState { s := baseState; s.Recipient = "other-recipient"; return s }(),
		},
		{
			name: "delegate funding",
			state: func() AutopayContractState {
				s := baseState
				delegate := s.Delegates[payer]
				delegate.Status = "funding"
				s.Delegates = map[string]AutopayDelegateState{payer: delegate}
				return s
			}(),
		},
		{
			name: "insufficient delegate balance",
			state: func() AutopayContractState {
				s := baseState
				delegate := s.Delegates[payer]
				delegate.Balance = "0.5"
				s.Delegates = map[string]AutopayDelegateState{payer: delegate}
				return s
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := testIndexerWithConfig(t, Config{
				FeeVerifier: AutopayFeeVerifier{
					StateProvider: testAutopayStateProvider{states: map[string]*AutopayContractState{
						contract: &tt.state,
					}},
					Recipient:             recipient,
					FeeAssetName:          "sat",
					FullRecordFeePerBlock: "1",
					AddressParams:         &chaincfg.TestNetParams,
				},
			})
			record := tt.record
			if record == nil {
				record = signedRecordWithAutopayFee(t, priv, key, 1, contract, 100)
			}
			if _, err := idx.PutLocal(record); err != ErrInvalidFeeProof {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestHTTPFeeVerifier(t *testing.T) {
	allow := true
	wrapped := false
	var seen httpFeeVerifyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		if seen.Namespace != "personal" || seen.RecordSize <= 0 || seen.ExpiryHeight != 100 {
			t.Fatalf("request=%#v", seen)
		}
		if _, err := base64.StdEncoding.DecodeString(seen.FeeProofBase64); err != nil {
			t.Fatalf("fee proof base64: %v", err)
		}
		if wrapped {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "ok",
				"data": map[string]interface{}{"valid": allow},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"valid": allow})
	}))
	defer server.Close()

	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/profile"
	proof := FeeProof{
		Mode:         FeeModeOneshot,
		PoolContract: "dkvs-pool",
		Payer:        "payer",
		PaymentTxID:  "payment",
	}
	record := signedRecordWithStructuredFee(t, priv, key, 1, proof)
	idx := testIndexerWithConfig(t, Config{
		FeeVerifier: HTTPFeeVerifier{Endpoint: server.URL},
	})
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("http fee verifier put updated=%v err=%v", updated, err)
	}
	if seen.RecordHash == "" || seen.KeyHash == "" {
		t.Fatalf("hashes missing in request: %#v", seen)
	}

	wrapped = true
	record = signedRecordWithStructuredFee(t, priv, key, 2, proof)
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("wrapped http fee verifier put updated=%v err=%v", updated, err)
	}

	allow = false
	record = signedRecordWithStructuredFee(t, priv, key, 3, proof)
	if _, err := idx.PutLocal(record); err != ErrInvalidFeeProof {
		t.Fatalf("denied http fee verifier err=%v", err)
	}
}

func TestNormalizeNameID(t *testing.T) {
	if got := NormalizeNameID("alice.name"); got != "alice.name" {
		t.Fatalf("safe name id=%s", got)
	}
	if got := NormalizeNameID(" Alice Name "); got != "alice_name" {
		t.Fatalf("name id normalization got=%q", got)
	}
	if got := NormalizeNameID("alice   name"); got != "alice_name" {
		t.Fatalf("repeated whitespace normalization got=%q", got)
	}
	if key, err := NameKey("Alice Name"); err != nil || key != "/name/alice_name" {
		t.Fatalf("normalized explicit name key=%q err=%v", key, err)
	}
	if _, err := NameKey("alice/name"); err != ErrInvalidKey {
		t.Fatalf("illegal name id was not rejected: %v", err)
	}
}

func TestSDKKeyBuildersAndSignedRecord(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.PubKey().SerializeCompressed()
	personalKey, err := PersonalKey(pub, "/profile/main")
	if err != nil {
		t.Fatal(err)
	}
	if personalKey != "/personal/"+AccountID(pub)+"/profile/main" {
		t.Fatalf("personal key=%s", personalKey)
	}
	for _, build := range []func() (string, error){
		func() (string, error) { return NameKey("alice-name") },
		func() (string, error) { return ServiceKey("wallet", "config") },
		func() (string, error) { return MailMsgKey(AccountID(pub), AccountID(pub), "msg-1") },
		func() (string, error) { return MailShareKey(AccountID(pub), "pkg", "share-1") },
		func() (string, error) { return BlobKey(AccountID(pub), "object") },
		func() (string, error) { return TmpKey("random") },
		func() (string, error) { return SystemParamsKey(), nil },
		func() (string, error) { return SystemMinerKey("miner-1") },
		func() (string, error) { return SystemPoolKey("pool-1") },
	} {
		key, err := build()
		if err != nil {
			t.Fatalf("key builder failed: %v", err)
		}
		if _, err := ParseKey(key); err != nil {
			t.Fatalf("built invalid key %s: %v", key, err)
		}
	}
	record, err := NewSignedRecord(priv, personalKey, []byte("value"), RecordOptions{
		Seq: 1,
		TTL: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(record); err != nil {
		t.Fatal(err)
	}
	idx := testIndexer(t)
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("sdk signed put updated=%v err=%v", updated, err)
	}
}

func TestSDKRenewalRecord(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), "recovery")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewSignedRecord(priv, key, []byte("backup"), RecordOptions{Seq: 7, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	renewal, err := NewSignedRenewalRecord(priv, record, RecordOptions{IssueHeight: 1, TTL: 199})
	if err != nil {
		t.Fatal(err)
	}
	if renewal.Key != record.Key || renewal.Seq != record.Seq ||
		string(renewal.Value) != string(record.Value) || RecordExpiryHeight(renewal) != 200 {
		t.Fatalf("renewal=%#v record=%#v", renewal, record)
	}
	if err := VerifySignature(renewal); err != nil {
		t.Fatal(err)
	}
	if updated, err := idx.PutLocal(renewal); err != nil || !updated {
		t.Fatalf("renewal put updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if RecordExpiryHeight(got) != 200 || got.Seq != 7 || string(got.Value) != "backup" {
		t.Fatalf("got renewal=%#v", got)
	}
	if _, err := NewSignedRenewalRecord(priv, record, RecordOptions{IssueHeight: 1, TTL: 99}); err != ErrInvalidRecord {
		t.Fatalf("non-extension renewal err=%v", err)
	}
	if _, err := NewSignedRenewalRecord(otherPriv, record, RecordOptions{IssueHeight: 1, TTL: 199}); err != ErrPermissionDenied {
		t.Fatalf("other signer renewal err=%v", err)
	}
	tombstone, err := NewSignedTombstone(priv, key, RecordOptions{Seq: 8, IssueHeight: 1, TTL: 299})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSignedRenewalRecord(priv, tombstone, RecordOptions{IssueHeight: 1, TTL: 399}); err != ErrInvalidRecord {
		t.Fatalf("tombstone renewal err=%v", err)
	}
}

func TestSDKTombstone(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), "profile")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewSignedTombstone(priv, key, RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !IsTombstone(record.Flags) || len(record.Value) != 0 {
		t.Fatalf("bad tombstone flags=%d value=%x", record.Flags, record.Value)
	}
	if err := VerifySignature(record); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRecordForClient(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), "profile")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewSignedRecord(priv, key, []byte("value"), RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	hash := RecordHash(record)
	if err := VerifyRecordForClient(record, RecordVerificationOptions{ExpectedKey: key, ExpectedHash: hash, CheckHash: true, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecordForClient(record, RecordVerificationOptions{ExpectedKey: "/tmp/other"}); err != ErrInvalidKey {
		t.Fatalf("wrong expected key err=%v", err)
	}
	if err := VerifyRecordForClient(record, RecordVerificationOptions{ExpectedKey: key, ExpectedHash: chainhash.Hash{}, CheckHash: true}); err != ErrInvalidRecord {
		t.Fatalf("wrong hash err=%v", err)
	}
	if err := VerifyRecordForClient(record, RecordVerificationOptions{ExpectedKey: key, Height: RecordExpiryHeight(record)}); err != ErrExpiredRecord {
		t.Fatalf("expired err=%v", err)
	}
	feeErr := errors.New("fee")
	if err := VerifyRecordForClient(record, RecordVerificationOptions{ExpectedKey: key, FeeVerifier: testFeeVerifier{err: feeErr}}); err != feeErr {
		t.Fatalf("fee err=%v", err)
	}
	badTombstone, err := NewSignedRecord(priv, key, []byte("bad"), RecordOptions{Seq: 2, TTL: 100, Flags: FlagTombstone})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecordForClient(badTombstone, RecordVerificationOptions{ExpectedKey: key}); err != ErrInvalidRecord {
		t.Fatalf("bad tombstone err=%v", err)
	}
}

func TestVerifyRecordsForClient(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	recordA, err := NewSignedRecord(priv, prefix+"/profile", []byte("profile"), RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	recordB, err := NewSignedRecord(priv, prefix+"/settings", []byte("settings"), RecordOptions{Seq: 2, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecordsForClient([]*wire.DKVSRecord{recordA, recordB}, prefix+"/", RecordVerificationOptions{Height: 1}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecordsForClient([]*wire.DKVSRecord{recordA, recordB}, prefix, RecordVerificationOptions{CheckHash: true}); err != ErrInvalidRecord {
		t.Fatalf("multi-record hash err=%v", err)
	}
	otherPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewSignedRecord(otherPriv, "/tmp/random", []byte("tmp"), RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecordsForClient([]*wire.DKVSRecord{recordA, other}, prefix, RecordVerificationOptions{}); err != ErrInvalidKey {
		t.Fatalf("out-of-prefix err=%v", err)
	}
}

func TestVerifySubscriptionRecordsForClient(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := AccountID(priv.PubKey().SerializeCompressed())
	msg, err := NewSignedRecord(priv, testMailMsgKey(t, priv.PubKey().SerializeCompressed(), priv.PubKey().SerializeCompressed(), "msg-1"), []byte("message"), RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	share, err := NewSignedRecord(priv, "/mail/"+mailboxID+"/share/pkg/share-1", []byte("share"), RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	sub := Subscription{Type: SubscriptionMailbox, Target: mailboxID}
	if err := VerifySubscriptionRecordsForClient([]*wire.DKVSRecord{msg, share}, sub, RecordVerificationOptions{Height: 1}); err != nil {
		t.Fatal(err)
	}
	other, err := NewSignedRecord(priv, "/tmp/random", []byte("tmp"), RecordOptions{Seq: 1, TTL: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySubscriptionRecordsForClient([]*wire.DKVSRecord{msg, other}, sub, RecordVerificationOptions{}); err != ErrInvalidKey {
		t.Fatalf("out-of-subscription err=%v", err)
	}
	if err := VerifySubscriptionRecordsForClient([]*wire.DKVSRecord{msg}, Subscription{Type: SubscriptionMailbox, Target: "bad/box"}, RecordVerificationOptions{}); err != ErrInvalidKey {
		t.Fatalf("bad subscription err=%v", err)
	}
}

func TestDefaultResolverKeepsNameServiceClosed(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, priv, "/name/alice", 1)); err != ErrDIDResolverUnavailable {
		t.Fatalf("name default resolver err=%v", err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, priv, "/svc/wallet/config", 1)); err != ErrDIDResolverUnavailable {
		t.Fatalf("svc default resolver err=%v", err)
	}
}

func TestHTTPDIDResolver(t *testing.T) {
	namePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	servicePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	active := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/name/alice":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"canonical_name": "alice",
				"name_id":        "alice",
				"signing_keys":   []string{hex.EncodeToString(namePriv.PubKey().SerializeCompressed())},
				"active":         active,
			})
		case "/service/wallet":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "ok",
				"data": map[string]interface{}{
					"canonical_name": "wallet",
					"name_id":        "wallet",
					"signing_keys":   []string{hex.EncodeToString(servicePriv.PubKey().SerializeCompressed())},
					"active":         active,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolver := HTTPDIDResolver{BaseURL: server.URL}
	nameID, err := resolver.ResolveName("alice")
	if err != nil {
		t.Fatal(err)
	}
	if nameID.CanonicalName != "alice" || nameID.NameID != "alice" || !nameID.Active ||
		len(nameID.SigningKeys) != 1 || string(nameID.SigningKeys[0]) != string(namePriv.PubKey().SerializeCompressed()) {
		t.Fatalf("name identity=%#v", nameID)
	}
	serviceID, err := resolver.ResolveService("wallet")
	if err != nil {
		t.Fatal(err)
	}
	if serviceID.CanonicalName != "wallet" || serviceID.NameID != "wallet" || !serviceID.Active ||
		len(serviceID.SigningKeys) != 1 || string(serviceID.SigningKeys[0]) != string(servicePriv.PubKey().SerializeCompressed()) {
		t.Fatalf("service identity=%#v", serviceID)
	}
	if _, err := resolver.ResolveName("missing"); err != ErrDIDResolverUnavailable {
		t.Fatalf("missing identity err=%v", err)
	}

	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Resolver:       resolver,
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, namePriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("http resolver name put updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, servicePriv, "/svc/wallet/config", 1)); err != nil || !updated {
		t.Fatalf("http resolver service put updated=%v err=%v", updated, err)
	}
	otherPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, otherPriv, "/name/alice", 2)); err != ErrPermissionDenied {
		t.Fatalf("wrong signer err=%v", err)
	}
}

func TestL1NSResolverUsesOwnerAddress(t *testing.T) {
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	ownerAddress, err := P2TRAddressFromPubKeyBytes(ownerPriv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		switch r.URL.Path {
		case "/ns/name/alice":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0,
				"msg":  "ok",
				"data": map[string]interface{}{
					"name":    "alice",
					"address": ownerAddress,
					"utxo":    "txid:0",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	resolver := L1NSResolver{
		BaseURL:       server.URL,
		AddressParams: &chaincfg.TestNetParams,
	}
	identity, err := resolver.ResolveName("alice")
	if err != nil {
		t.Fatal(err)
	}
	if seenPath != "/ns/name/alice" || identity.CanonicalName != "alice" ||
		len(identity.OwnerAddresses) != 1 || identity.OwnerAddresses[0] != ownerAddress {
		t.Fatalf("l1 identity=%#v path=%s", identity, seenPath)
	}
	if err := identity.CanSign(ownerPriv.PubKey().SerializeCompressed()); err != nil {
		t.Fatal(err)
	}
	otherPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.CanSign(otherPriv.PubKey().SerializeCompressed()); err != ErrPermissionDenied {
		t.Fatalf("wrong address signer err=%v", err)
	}

	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Resolver:       resolver,
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("l1 owner put updated=%v err=%v", updated, err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, otherPriv, "/name/alice", 2)); err != ErrPermissionDenied {
		t.Fatalf("l1 wrong owner err=%v", err)
	}
}

func TestExistingNameRecordSkipsResolverForSamePubKey(t *testing.T) {
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &countingDIDResolver{
		names: map[string]DIDIdentity{
			"alice": testIdentity("alice", ownerPriv.PubKey().SerializeCompressed()),
		},
	}
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Resolver:       resolver,
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("initial put updated=%v err=%v", updated, err)
	}
	if resolver.nameCalls != 1 {
		t.Fatalf("initial resolver calls=%d", resolver.nameCalls)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 2)); err != nil || !updated {
		t.Fatalf("same owner update updated=%v err=%v", updated, err)
	}
	if resolver.nameCalls != 1 {
		t.Fatalf("same pubkey update should skip resolver calls=%d", resolver.nameCalls)
	}
	resolver.names["alice"] = testIdentity("alice", newPriv.PubKey().SerializeCompressed())
	if updated, err := idx.PutLocal(signedRecordForKey(t, newPriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("new owner replace updated=%v err=%v", updated, err)
	}
	if resolver.nameCalls != 2 {
		t.Fatalf("new pubkey should resolve calls=%d", resolver.nameCalls)
	}
	got, err := idx.Get("/name/alice")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.PubKey, newPriv.PubKey().SerializeCompressed()) || got.Seq != 1 {
		t.Fatalf("unexpected record seq=%d", got.Seq)
	}
}

func TestNameTransferNotifyForcesNextNameResolve(t *testing.T) {
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &countingDIDResolver{
		names: map[string]DIDIdentity{
			"alice": testIdentity("alice", ownerPriv.PubKey().SerializeCompressed()),
		},
	}
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Resolver:       resolver,
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("initial put updated=%v err=%v", updated, err)
	}
	if resolver.nameCalls != 1 {
		t.Fatalf("initial resolver calls=%d", resolver.nameCalls)
	}
	if err := idx.NotifyNameTransfers([]string{"alice", "alice"}); err != nil {
		t.Fatal(err)
	}
	resolver.names["alice"] = testIdentity("alice", newPriv.PubKey().SerializeCompressed())
	if _, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 2)); err != ErrPermissionDenied {
		t.Fatalf("old owner after transfer err=%v", err)
	}
	if resolver.nameCalls != 2 {
		t.Fatalf("transfer should force resolve calls=%d", resolver.nameCalls)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, newPriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("new owner after transfer updated=%v err=%v", updated, err)
	}
	if resolver.nameCalls != 3 {
		t.Fatalf("new owner should resolve calls=%d", resolver.nameCalls)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, newPriv, "/name/alice", 2)); err != nil || !updated {
		t.Fatalf("same new owner update updated=%v err=%v", updated, err)
	}
	if resolver.nameCalls != 3 {
		t.Fatalf("dirty marker should be cleared calls=%d", resolver.nameCalls)
	}
}

func TestNameTransferNotifyPersistsDirtyMarker(t *testing.T) {
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &countingDIDResolver{
		names: map[string]DIDIdentity{
			"alice": testIdentity("alice", ownerPriv.PubKey().SerializeCompressed()),
		},
	}
	idx := New(db, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		Resolver:       resolver,
	})
	if _, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 1)); err != nil {
		t.Fatal(err)
	}
	if err := idx.NotifyNameTransfers([]string{"alice"}); err != nil {
		t.Fatal(err)
	}
	resolver.names["alice"] = testIdentity("alice", newPriv.PubKey().SerializeCompressed())
	restarted := New(db, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		Resolver:       resolver,
	})
	if _, err := restarted.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 2)); err != ErrPermissionDenied {
		t.Fatalf("dirty marker after restart err=%v", err)
	}
}

func TestNameTransferNotifyClearsAfterResolvedNoop(t *testing.T) {
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := &countingDIDResolver{
		names: map[string]DIDIdentity{
			"alice": testIdentity("alice", ownerPriv.PubKey().SerializeCompressed()),
		},
	}
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Resolver:       resolver,
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 10)); err != nil || !updated {
		t.Fatalf("initial put updated=%v err=%v", updated, err)
	}
	if err := idx.NotifyNameTransfers([]string{"alice"}); err != nil {
		t.Fatal(err)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 1)); err != nil || updated {
		t.Fatalf("resolved noop updated=%v err=%v", updated, err)
	}
	if resolver.nameCalls != 2 {
		t.Fatalf("dirty noop should resolve once calls=%d", resolver.nameCalls)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, ownerPriv, "/name/alice", 11)); err != nil || !updated {
		t.Fatalf("post-noop update updated=%v err=%v", updated, err)
	}
	if resolver.nameCalls != 2 {
		t.Fatalf("dirty marker should be cleared after noop calls=%d", resolver.nameCalls)
	}
}

func TestHTTPSystemVerifier(t *testing.T) {
	systemPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	valid := true
	var seenReq httpSystemVerifyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system/verify" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&seenReq); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0,
			"msg":  "ok",
			"data": map[string]interface{}{"valid": valid},
		})
	}))
	defer server.Close()

	verifier := HTTPSystemVerifier{Endpoint: server.URL + "/system/verify"}
	if err := verifier.CanWriteSystem(SystemParamsKey(), systemPriv.PubKey().SerializeCompressed()); err != nil {
		t.Fatal(err)
	}
	if seenReq.Key != SystemParamsKey() ||
		seenReq.PubKeyHex != hex.EncodeToString(systemPriv.PubKey().SerializeCompressed()) ||
		seenReq.PubKeyBase64 != base64.StdEncoding.EncodeToString(systemPriv.PubKey().SerializeCompressed()) {
		t.Fatalf("system verifier request=%#v", seenReq)
	}
	valid = false
	if err := verifier.CanWriteSystem(SystemParamsKey(), systemPriv.PubKey().SerializeCompressed()); err != ErrPermissionDenied {
		t.Fatalf("invalid system auth err=%v", err)
	}

	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		SystemVerifier: verifier,
	})
	valid = true
	record, err := NewSignedRecord(systemPriv, SystemParamsKey(), []byte(`{"epoch":"1"}`), RecordOptions{
		Seq: 1,
		TTL: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("http system verifier put updated=%v err=%v", updated, err)
	}
	valid = false
	record.Seq = 2
	signRecord(t, systemPriv, record)
	if _, err := idx.PutLocal(record); err != ErrPermissionDenied {
		t.Fatalf("denied system put err=%v", err)
	}
}

func TestRuntimeVerifierInjection(t *testing.T) {
	namePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndexerWithConfig(t, Config{AllowFreeLocal: true})
	if _, err := idx.PutLocal(signedRecordForKey(t, namePriv, "/name/alice", 1)); err != ErrDIDResolverUnavailable {
		t.Fatalf("default resolver err=%v", err)
	}
	idx.SetResolver(StaticDIDResolver{
		Names: map[string]DIDIdentity{
			"alice": testIdentity("alice", namePriv.PubKey().SerializeCompressed()),
		},
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, namePriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("injected resolver updated=%v err=%v", updated, err)
	}
	idx.SetResolver(nil)
	if _, err := idx.PutLocal(signedRecordForKey(t, namePriv, "/name/bob", 1)); err != ErrDIDResolverUnavailable {
		t.Fatalf("nil resolver err=%v", err)
	}

	feePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	feeIdx := testIndexerWithConfig(t, Config{})
	feeRecord := signedPersonalRecordWithKey(t, feePriv, 1, "value", 0)
	if _, err := feeIdx.PutLocal(feeRecord); err != ErrFeeProofRequired {
		t.Fatalf("default fee err=%v", err)
	}
	feeIdx.SetFeeVerifier(testFeeVerifier{})
	if updated, err := feeIdx.PutLocal(feeRecord); err != nil || !updated {
		t.Fatalf("injected fee verifier updated=%v err=%v", updated, err)
	}
	feeIdx.SetFeeVerifier(nil)
	feeRecord2 := signedPersonalRecordWithKey(t, feePriv, 2, "value2", 0)
	if _, err := feeIdx.PutLocal(feeRecord2); err != ErrFeeProofRequired {
		t.Fatalf("nil fee verifier err=%v", err)
	}

	systemPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	systemIdx := testIndexer(t)
	systemRecord := signedRecordForKey(t, systemPriv, "/sys/params", 1)
	if _, err := systemIdx.PutLocal(systemRecord); err != ErrPermissionDenied {
		t.Fatalf("default system err=%v", err)
	}
	systemIdx.SetSystemVerifier(StaticSystemVerifier{Keys: [][]byte{systemPriv.PubKey().SerializeCompressed()}})
	if updated, err := systemIdx.PutLocal(systemRecord); err != nil || !updated {
		t.Fatalf("injected system verifier updated=%v err=%v", updated, err)
	}
	systemIdx.SetSystemVerifier(nil)
	systemRecord2 := signedRecordForKey(t, systemPriv, "/sys/params", 2)
	if _, err := systemIdx.PutLocal(systemRecord2); err != ErrPermissionDenied {
		t.Fatalf("nil system verifier err=%v", err)
	}
}

func TestDIDOwnerRotationFiltersAndAllowsReplacement(t *testing.T) {
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })

	oldPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := StaticDIDResolver{
		Names: map[string]DIDIdentity{
			"alice": testIdentity("alice", oldPriv.PubKey().SerializeCompressed()),
		},
	}
	idx := New(db, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		Resolver:       resolver,
	})
	oldRecord := signedRecordForKey(t, oldPriv, "/name/alice", 10)
	if updated, err := idx.PutLocal(oldRecord); err != nil || !updated {
		t.Fatalf("old owner put updated=%v err=%v", updated, err)
	}

	resolver.Names["alice"] = testIdentity("alice", newPriv.PubKey().SerializeCompressed())
	gotOld, err := idx.Get(oldRecord.Key)
	if err != nil {
		t.Fatalf("rotated old owner get err=%v", err)
	}
	if !bytes.Equal(gotOld.PubKey, oldPriv.PubKey().SerializeCompressed()) {
		t.Fatalf("unexpected old record pubkey")
	}
	listed, total, err := idx.ListPrefix("/name/alice", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || total != 1 {
		t.Fatalf("rotated old owner listed len=%d total=%d", len(listed), total)
	}
	synced, _, done, _, err := idx.Sync(nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(synced) != 1 || !done {
		t.Fatalf("rotated old owner synced len=%d done=%v", len(synced), done)
	}
	cp, err := idx.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if cp.ActiveRecordCount != 1 {
		t.Fatalf("rotated old owner checkpoint count=%d", cp.ActiveRecordCount)
	}

	newRecord := signedRecordForKey(t, newPriv, "/name/alice", 1)
	if updated, err := idx.PutLocal(newRecord); err != nil || !updated {
		t.Fatalf("new owner lower seq put updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(newRecord.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.PubKey, newRecord.PubKey) || got.Seq != 1 {
		t.Fatalf("unexpected active record seq=%d", got.Seq)
	}
}

func TestServiceOwnerRotationAllowsLowerSeqReplacement(t *testing.T) {
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })

	oldPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := StaticDIDResolver{
		Services: map[string]DIDIdentity{
			"wallet": testIdentity("wallet", oldPriv.PubKey().SerializeCompressed()),
		},
	}
	idx := New(db, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		Resolver:       resolver,
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, oldPriv, "/svc/wallet/config", 20)); err != nil || !updated {
		t.Fatalf("old svc owner put updated=%v err=%v", updated, err)
	}
	resolver.Services["wallet"] = testIdentity("wallet", newPriv.PubKey().SerializeCompressed())
	if updated, err := idx.PutLocal(signedRecordForKey(t, newPriv, "/svc/wallet/config", 1)); err != nil || !updated {
		t.Fatalf("new svc owner lower seq put updated=%v err=%v", updated, err)
	}
	got, err := idx.Get("/svc/wallet/config")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.PubKey) != string(newPriv.PubKey().SerializeCompressed()) || got.Seq != 1 {
		t.Fatalf("unexpected svc active seq=%d", got.Seq)
	}
}

func TestMailPermissions(t *testing.T) {
	idx := testIndexer(t)
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := AccountID(ownerPriv.PubKey().SerializeCompressed())
	msgKey := testMailMsgKey(t, ownerPriv.PubKey().SerializeCompressed(), senderPriv.PubKey().SerializeCompressed(), "msg-1")

	// Generic DKVS Put must not bypass MessageManager admission for message
	// entries, even when the sender signature itself is otherwise valid.
	if _, err := idx.PutLocal(signedRecordForKey(t, senderPriv, msgKey, 1)); err == nil {
		t.Fatal("generic mailbox message put was accepted")
	}
	if updated, err := idx.PutInternalMailbox(internalMailboxTestRecord(msgKey, []byte("message"), 100)); err != nil || !updated {
		t.Fatalf("internal mail msg put updated=%v err=%v", updated, err)
	}

	shareKey := "/mail/" + mailboxID + "/share/pkg/share-1"
	if _, err := idx.PutLocal(signedAccountRecord(t, senderPriv, shareKey, []byte("share"), 1)); err == nil {
		t.Fatal("mail share non-owner was accepted")
	}
	if updated, err := idx.PutLocal(signedAccountRecord(t, ownerPriv, shareKey, []byte("share"), 1)); err != nil || !updated {
		t.Fatalf("mail share owner put updated=%v err=%v", updated, err)
	}
}
func TestMailboxMessageUpdateAndDeletePermissions(t *testing.T) {
	idx := testIndexer(t)
	owner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := testMailMsgKey(t, owner.PubKey().SerializeCompressed(), sender.PubKey().SerializeCompressed(), "message-1")
	original := internalMailboxTestRecord(key, []byte("message"), 100)
	if updated, err := idx.PutInternalMailbox(original); err != nil || !updated {
		t.Fatalf("initial message updated=%v err=%v", updated, err)
	}
	if _, err := idx.PutLocal(signedRecordWithValue(t, attacker, key, 2, []byte("replace"), 0)); err == nil {
		t.Fatal("attacker generic update was accepted")
	}
	if _, err := idx.DeleteInternalMailbox(signedMailboxOwnerTombstoneForTest(t, attacker, key, 2)); err == nil {
		t.Fatal("attacker tombstone was accepted")
	}
	if updated, err := idx.DeleteInternalMailbox(signedMailboxOwnerTombstoneForTest(t, owner, key, 2)); err != nil || !updated {
		t.Fatalf("owner tombstone updated=%v err=%v", updated, err)
	}
	if _, err := idx.Get(key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("deleted mailbox record err=%v", err)
	}
	state, err := idx.GetKeyState(key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != KeyStateNeverSeen || state.ETag != "" {
		t.Fatalf("mailbox deletion retained state: %+v", state)
	}
	// The DKVS cache itself keeps no permanent replay marker. MessageManager's
	// bounded receiver replay window, rather than a per-key tombstone, prevents
	// a delayed Direct retry from recreating a deleted mailbox item.
	if updated, err := idx.PutInternalMailbox(original); err != nil || !updated {
		t.Fatalf("physical mailbox reinsert updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutInternalMailbox(original); err != nil || updated {
		t.Fatalf("active mailbox duplicate updated=%v err=%v", updated, err)
	}
}
func TestMailboxQuotaAndTombstone(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		MailboxPolicy:  MailboxPolicy{MaxMessages: 1},
	})
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	msg1 := testMailMsgKey(t, ownerPriv.PubKey().SerializeCompressed(), senderPriv.PubKey().SerializeCompressed(), "msg-1")
	msg2 := testMailMsgKey(t, ownerPriv.PubKey().SerializeCompressed(), senderPriv.PubKey().SerializeCompressed(), "msg-2")
	if updated, err := idx.PutInternalMailbox(internalMailboxTestRecord(msg1, []byte("one"), 100)); err != nil || !updated {
		t.Fatalf("msg1 put updated=%v err=%v", updated, err)
	}
	if _, err := idx.PutInternalMailbox(internalMailboxTestRecord(msg2, []byte("two"), 100)); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("msg2 over quota err=%v", err)
	}
	if updated, err := idx.DeleteInternalMailbox(signedMailboxOwnerTombstoneForTest(t, ownerPriv, msg1, 2)); err != nil || !updated {
		t.Fatalf("msg tombstone updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutInternalMailbox(internalMailboxTestRecord(msg2, []byte("two"), 100)); err != nil || !updated {
		t.Fatalf("msg2 after tombstone updated=%v err=%v", updated, err)
	}
}
func TestMailboxSenderIdentityAndQuotaIsolation(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		MailboxPolicy: MailboxPolicy{
			MaxMessages:          2,
			MaxMessagesPerSender: 1,
		},
	})
	owner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	firstSender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	secondSender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	thirdSender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	firstKey := testMailMsgKey(t, owner.PubKey().SerializeCompressed(), firstSender.PubKey().SerializeCompressed(), "same-id")
	if updated, err := idx.PutInternalMailbox(internalMailboxTestRecord(firstKey, []byte("first"), 100)); err != nil || !updated {
		t.Fatalf("first sender put updated=%v err=%v", updated, err)
	}
	forgedKey := testMailMsgKey(t, owner.PubKey().SerializeCompressed(), secondSender.PubKey().SerializeCompressed(), "forged")
	if _, err := idx.PutLocal(signedRecordForKey(t, firstSender, forgedKey, 1)); err == nil {
		t.Fatal("generic forged sender id was accepted")
	}
	firstSecondKey := testMailMsgKey(t, owner.PubKey().SerializeCompressed(), firstSender.PubKey().SerializeCompressed(), "second")
	if _, err := idx.PutInternalMailbox(internalMailboxTestRecord(firstSecondKey, []byte("second"), 100)); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("per-sender quota err=%v", err)
	}
	secondKey := testMailMsgKey(t, owner.PubKey().SerializeCompressed(), secondSender.PubKey().SerializeCompressed(), "same-id")
	if updated, err := idx.PutInternalMailbox(internalMailboxTestRecord(secondKey, []byte("second-sender"), 100)); err != nil || !updated {
		t.Fatalf("second sender same msg id updated=%v err=%v", updated, err)
	}
	thirdKey := testMailMsgKey(t, owner.PubKey().SerializeCompressed(), thirdSender.PubKey().SerializeCompressed(), "third")
	if _, err := idx.PutInternalMailbox(internalMailboxTestRecord(thirdKey, []byte("third"), 100)); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("mailbox-wide quota err=%v", err)
	}

	mailboxPrefix := "/mail/" + AccountID(owner.PubKey().SerializeCompressed()) + "/msg"
	records, total, err := idx.ListPrefix(mailboxPrefix, 0, 10)
	if err != nil || total != 2 || len(records) != 2 {
		t.Fatalf("mailbox records=%d total=%d err=%v", len(records), total, err)
	}
	mailboxPath := "/mail/" + AccountID(owner.PubKey().SerializeCompressed())
	meta, err := idx.GetPathMeta(mailboxPath)
	if err != nil || meta.EndpointGeneration == 0 {
		t.Fatalf("AccountBound mailbox PathMeta missing: meta=%+v err=%v", meta, err)
	}
}
func TestMailboxAutopayChargesSenderAndDeleteReleasesCapacity(t *testing.T) {
	idx := testIndexer(t)
	owner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	msgKey := testMailMsgKey(t, owner.PubKey().SerializeCompressed(), sender.PubKey().SerializeCompressed(), "paid")
	paid := signedRecordWithStructuredFee(t, sender, msgKey, 1, FeeProof{
		Mode:         FeeModeOneshot,
		PoolContract: "message-pool",
		Payer:        "sender",
		PaymentTxID:  "message-payment",
		PaidAmount:   "1",
	})
	// MESSAGE_SEND accounting belongs to MessageManager. Attaching any DKVS fee
	// proof to a generic /mail message record must not bypass that boundary.
	if _, err := idx.PutLocal(paid); err == nil {
		t.Fatal("paid generic mailbox put bypassed MessageManager")
	}
	if _, err := idx.Get(msgKey); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("generic paid mailbox record became visible: %v", err)
	}
}
func TestNotifyEventTypes(t *testing.T) {
	var events []uint8
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		SystemVerifier: StaticSystemVerifier{
			Keys: [][]byte{priv.PubKey().SerializeCompressed()},
		},
		Notify: func(event *NotifyEvent) {
			events = append(events, event.EventType)
			if _, err := RecordFromNotifyEvent(event); err != nil {
				t.Errorf("invalid notify event: %v", err)
			}
		},
	})
	record := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	record = signedPersonalRecordWithKey(t, priv, 2, "value2", 0)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	renewal := signedPersonalRecordWithKey(t, priv, 2, "value2", 0)
	renewal.IssueHeight = 1
	renewal.TTL = 199
	signRecord(t, priv, renewal)
	if updated, err := idx.PutLocal(renewal); err != nil || !updated {
		t.Fatalf("renewal updated=%v err=%v", updated, err)
	}

	// MessageManager internal mailbox commits notify wallet subscriptions, not
	// the ordinary P2P DKVS Notify callback.
	mailMsg := testMailMsgKey(t, priv.PubKey().SerializeCompressed(), priv.PubKey().SerializeCompressed(), "msg-1")
	beforeMail := len(events)
	if _, err := idx.PutInternalMailbox(internalMailboxTestRecord(mailMsg, []byte("message"), 100)); err != nil {
		t.Fatal(err)
	}
	if len(events) != beforeMail {
		t.Fatalf("AccountBound mailbox emitted ordinary notify events=%v", events)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, priv, "/sys/checkpoint/1", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, priv, "/sys/snapshot/1", 1)); err != nil {
		t.Fatal(err)
	}
	want := []uint8{
		EventRecordPut,
		EventRecordUpdate,
		EventRenewal,
		EventCheckpointReady,
		EventSnapshotReady,
	}
	if len(events) != len(want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	for n := range want {
		if events[n] != want[n] {
			t.Fatalf("events=%v want=%v", events, want)
		}
	}
}
func TestNotifyEventEncoding(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 7, "value", 0)
	event, err := NewNotifyEvent(EventRecordPut, record)
	if err != nil {
		t.Fatal(err)
	}
	if event.EventType != EventRecordPut || len(event.Data) != RecordSize(record) {
		t.Fatalf("bad notify event: %#v", event)
	}
	notifyRecord, err := RecordFromNotifyEvent(event)
	if err != nil || RecordHash(notifyRecord) != RecordHash(record) {
		t.Fatalf("notify record=%#v err=%v", notifyRecord, err)
	}
	encoded, err := MarshalNotifyEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalNotifyEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.EventType != event.EventType || !bytes.Equal(decoded.Data, event.Data) {
		t.Fatalf("decoded=%#v want=%#v", decoded, event)
	}
	if _, err := NewNotifyEvent(EventRecordPut, nil); err != ErrInvalidRecord {
		t.Fatalf("nil notify record err=%v", err)
	}
	if _, err := MarshalNotifyEvent(nil); err != ErrInvalidRecord {
		t.Fatalf("nil notify event err=%v", err)
	}
	if _, err := UnmarshalNotifyEvent([]byte(`{"event_type":1}`)); err != ErrInvalidRecord {
		t.Fatalf("bad notify event err=%v", err)
	}
	if _, err := NewNotifyEvent(EventRecordTombstone, record); err != ErrInvalidRecord {
		t.Fatalf("mismatched notify event err=%v", err)
	}
}

func TestMailboxTTLAndSizePolicy(t *testing.T) {
	ttlIdx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		MailboxPolicy:  MailboxPolicy{MaxMsgTTL: 10},
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	tooLongKey := testMailMsgKey(t, priv.PubKey().SerializeCompressed(), priv.PubKey().SerializeCompressed(), "msg-1")
	tooLong := internalMailboxTestRecord(tooLongKey, []byte("value"), 11)
	if _, err := ttlIdx.PutInternalMailbox(tooLong); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("mail ttl err=%v", err)
	}

	sizeIdx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		MailboxPolicy:  MailboxPolicy{MaxMsgSize: 1},
	})
	tooLargeKey := testMailMsgKey(t, priv.PubKey().SerializeCompressed(), priv.PubKey().SerializeCompressed(), "msg-2")
	tooLarge := internalMailboxTestRecord(tooLargeKey, []byte("0123456789abcdef0123456789abcdef0123456789abcdef"), 100)
	if _, err := sizeIdx.PutInternalMailbox(tooLarge); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("mail size err=%v", err)
	}
}
func TestTmpPolicy(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		TmpPolicy:      TmpPolicy{MaxTTL: 10, MaxSize: 4},
	})

	valid := signedRecordWithValue(t, priv, "/tmp/random", 1, []byte("data"), 0)
	valid.TTL = 10
	signRecord(t, priv, valid)
	if updated, err := idx.PutLocal(valid); err != nil || !updated {
		t.Fatalf("tmp valid updated=%v err=%v", updated, err)
	}

	zeroTTL := signedRecordWithValue(t, priv, "/tmp/no-ttl", 1, []byte("data"), 0)
	zeroTTL.TTL = 0
	signRecord(t, priv, zeroTTL)
	if _, err := idx.PutLocal(zeroTTL); err != ErrInvalidRecord {
		t.Fatalf("tmp zero ttl err=%v", err)
	}

	tooLong := signedRecordWithValue(t, priv, "/tmp/too-long", 1, []byte("data"), 0)
	tooLong.TTL = 11
	signRecord(t, priv, tooLong)
	if _, err := idx.PutLocal(tooLong); err != ErrInvalidRecord {
		t.Fatalf("tmp long ttl err=%v", err)
	}

	tooLarge := signedRecordWithValue(t, priv, "/tmp/too-large", 1, []byte("large"), 0)
	tooLarge.TTL = 10
	signRecord(t, priv, tooLarge)
	if _, err := idx.PutLocal(tooLarge); err != ErrRecordTooLarge {
		t.Fatalf("tmp large err=%v", err)
	}
}

func TestSysWriteDenied(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, priv, "/sys/params", 1)); err != ErrPermissionDenied {
		t.Fatalf("sys write err=%v", err)
	}
}

func TestSysAuthorizedWrite(t *testing.T) {
	systemPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		SystemVerifier: StaticSystemVerifier{
			Keys: [][]byte{systemPriv.PubKey().SerializeCompressed()},
		},
	})
	if updated, err := idx.PutLocal(signedRecordForKey(t, systemPriv, "/sys/params", 1)); err != nil || !updated {
		t.Fatalf("sys authorized put updated=%v err=%v", updated, err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, otherPriv, "/sys/params", 2)); err != ErrPermissionDenied {
		t.Fatalf("sys unauthorized err=%v", err)
	}
}

func TestNameTombstonePermissionAllowsExistingOwnerAndNewOwnerReplace(t *testing.T) {
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })

	oldPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := StaticDIDResolver{
		Names: map[string]DIDIdentity{
			"alice": testIdentity("alice", oldPriv.PubKey().SerializeCompressed()),
		},
	}
	idx := New(db, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return 1 },
		Resolver:       resolver,
	})
	if _, err := idx.PutLocal(signedRecordForKey(t, oldPriv, "/name/alice", 1)); err != nil {
		t.Fatal(err)
	}
	resolver.Names["alice"] = testIdentity("alice", newPriv.PubKey().SerializeCompressed())
	oldTombstone := signedRecordForKey(t, oldPriv, "/name/alice", 2)
	oldTombstone.Value = nil
	oldTombstone.Flags = FlagTombstone
	hash := SigningHash(oldTombstone)
	oldTombstone.Signature = ecdsa.Sign(oldPriv, hash[:]).Serialize()
	if updated, err := idx.PutLocal(oldTombstone); err != nil || !updated {
		t.Fatalf("old owner tombstone err=%v", err)
	}
	newTombstone := signedRecordForKey(t, newPriv, "/name/alice", 1)
	newTombstone.Value = nil
	newTombstone.Flags = FlagTombstone
	hash = SigningHash(newTombstone)
	newTombstone.Signature = ecdsa.Sign(newPriv, hash[:]).Serialize()
	if updated, err := idx.PutLocal(newTombstone); err != nil || updated {
		t.Fatalf("new owner tombstone updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, newPriv, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("new owner recreate updated=%v err=%v", updated, err)
	}
}

func TestNameMultipleCurrentKeysUseNormalSelector(t *testing.T) {
	oldKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := StaticDIDResolver{Names: map[string]DIDIdentity{
		"alice": {
			CanonicalName: "alice",
			NameID:        "alice",
			SigningKeys: [][]byte{
				oldKey.PubKey().SerializeCompressed(),
				newKey.PubKey().SerializeCompressed(),
			},
			Active: true,
		},
	}}
	idx := testIndexerWithConfig(t, Config{AllowFreeLocal: true, Resolver: resolver})
	if updated, err := idx.PutLocal(signedRecordForKey(t, oldKey, "/name/alice", 10)); err != nil || !updated {
		t.Fatalf("old key put updated=%v err=%v", updated, err)
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, newKey, "/name/alice", 1)); err != nil || updated {
		t.Fatalf("second current key low seq updated=%v err=%v", updated, err)
	}
	resolver.Names["alice"] = DIDIdentity{
		CanonicalName: "alice",
		NameID:        "alice",
		SigningKeys:   [][]byte{newKey.PubKey().SerializeCompressed()},
		Active:        true,
	}
	if updated, err := idx.PutLocal(signedRecordForKey(t, newKey, "/name/alice", 1)); err != nil || !updated {
		t.Fatalf("new owner low seq updated=%v err=%v", updated, err)
	}
}

func TestRejectsUnknownFlagsAndFutureIssueHeight(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	unknownFlag := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	unknownFlag.Flags = 1 << 10
	signRecord(t, priv, unknownFlag)
	if _, err := idx.PutLocal(unknownFlag); err != ErrInvalidRecord {
		t.Fatalf("unknown flag err=%v", err)
	}
	future := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	future.IssueHeight = 2
	signRecord(t, priv, future)
	if _, err := idx.PutLocal(future); err != ErrInvalidRecord {
		t.Fatalf("future issue height err=%v", err)
	}
}

func TestPruneExpiredRecords(t *testing.T) {
	height := uint64(1)
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return height },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	record.IssueHeight = 1
	record.TTL = 1
	signRecord(t, priv, record)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	height = 2
	if _, err := idx.Get(record.Key); err != ErrRecordNotFound {
		t.Fatalf("expired get err=%v", err)
	}
	pruned, err := idx.PruneExpired()
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned=%d", pruned)
	}
	if _, err := idx.getRaw(record.Key); err != ErrRecordNotFound {
		t.Fatalf("raw after prune err=%v", err)
	}
}

func TestPruneExpiredKeepsPaidRecords(t *testing.T) {
	height := uint64(1)
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return height },
		FeeVerifier: JSONFeeVerifier{
			AllowFreeLocal: true,
		},
	})
	freePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	paidPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	freeRecord := signedPersonalRecordWithKey(t, freePriv, 1, "free", 0)
	freeRecord.IssueHeight = 1
	freeRecord.TTL = 1
	signRecord(t, freePriv, freeRecord)
	paidRecord := signedPersonalRecordWithKey(t, paidPriv, 1, "paid", 0)
	paidRecord.IssueHeight = 1
	paidRecord.TTL = 1
	parsed, err := ParseKey(paidRecord.Key)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewOneshotFeeProof(paidRecord.Key, parsed.Namespace, wire.MaxDKVSRecordSize, RecordExpiryHeight(paidRecord), "pool", "payer", "txid", "100")
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachSignedFeeProof(paidRecord, proof, paidPriv); err != nil {
		t.Fatal(err)
	}
	signRecord(t, paidPriv, paidRecord)
	if _, err := idx.PutLocal(freeRecord); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(paidRecord); err != nil {
		t.Fatal(err)
	}

	height = 2
	pruned, err := idx.PruneExpired()
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned=%d", pruned)
	}
	if _, err := idx.getRaw(freeRecord.Key); err != ErrRecordNotFound {
		t.Fatalf("free raw after prune err=%v", err)
	}
	if _, err := idx.Get(paidRecord.Key); err != ErrRecordNotFound {
		t.Fatalf("paid active get after expiry err=%v", err)
	}
	if _, err := idx.getRaw(paidRecord.Key); err != nil {
		t.Fatalf("paid raw after prune err=%v", err)
	}
}

func TestPruneExpiredDoesNotBroadcastExpiredRecord(t *testing.T) {
	height := uint64(1)
	var events []uint8
	var eventKeys []string
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return height },
		Notify: func(event *NotifyEvent) {
			events = append(events, event.EventType)
			notified, err := RecordFromNotifyEvent(event)
			if err != nil {
				t.Errorf("invalid notify event: %v", err)
				return
			}
			eventKeys = append(eventKeys, notified.Key)
		},
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedPersonalRecordWithKey(t, priv, 1, "value", 0)
	record.IssueHeight = 1
	record.TTL = 1
	signRecord(t, priv, record)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	height = 2
	if pruned, err := idx.PruneExpired(); err != nil || pruned != 1 {
		t.Fatalf("pruned=%d err=%v", pruned, err)
	}
	want := []uint8{EventRecordPut}
	if len(events) != len(want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
	for n := range want {
		if events[n] != want[n] || eventKeys[n] != record.Key {
			t.Fatalf("events=%v keys=%v", events, eventKeys)
		}
	}
}

func TestPruneExpiredDoesNotDeletePermissionInvalidRecord(t *testing.T) {
	oldPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := StaticDIDResolver{
		Names: map[string]DIDIdentity{
			"alice": testIdentity("alice", oldPriv.PubKey().SerializeCompressed()),
		},
	}
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Resolver:       resolver,
	})
	record := signedRecordForKey(t, oldPriv, "/name/alice", 1)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	resolver.Names["alice"] = testIdentity("alice", newPriv.PubKey().SerializeCompressed())
	pruned, err := idx.PruneExpired()
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Fatalf("pruned permission-invalid record=%d", pruned)
	}
	if _, err := idx.getRaw(record.Key); err != nil {
		t.Fatalf("raw permission-invalid record err=%v", err)
	}
}

func TestSubscriptionValidationAndMatching(t *testing.T) {
	mailboxID := strings.Repeat("1", sha256.Size*2)
	senderID := strings.Repeat("2", sha256.Size*2)
	otherMailboxID := strings.Repeat("3", sha256.Size*2)
	tests := []struct {
		sub     Subscription
		matches []string
		misses  []string
	}{
		{
			sub:     Subscription{Type: SubscriptionKey, Target: "/tmp/random"},
			matches: []string{"/tmp/random"},
			misses:  []string{"/tmp/other"},
		},
		{
			sub:     Subscription{Type: SubscriptionPrefix, Target: "/blob/" + mailboxID},
			matches: []string{"/blob/" + mailboxID + "/object"},
			misses:  []string{"/blob/" + otherMailboxID + "/object"},
		},
		{
			sub:     Subscription{Type: SubscriptionMailbox, Target: mailboxID},
			matches: []string{"/mail/" + mailboxID + "/msg/" + senderID + "/msg-1", "/mail/" + mailboxID + "/share/pkg/share-1"},
			misses:  []string{"/mail/" + otherMailboxID + "/msg/" + senderID + "/msg-1"},
		},
		{
			sub:     Subscription{Type: SubscriptionService, Target: "/svc/wallet"},
			matches: []string{"/svc/wallet/config"},
			misses:  []string{"/svc/market/config"},
		},
	}
	for _, test := range tests {
		sub, err := validateSubscription(test.sub)
		if err != nil {
			t.Fatalf("validate %v: %v", test.sub, err)
		}
		for _, key := range test.matches {
			if !subscriptionMatchesKey(sub, key) {
				t.Fatalf("subscription %v should match %s", sub, key)
			}
		}
		for _, key := range test.misses {
			if subscriptionMatchesKey(sub, key) {
				t.Fatalf("subscription %v should not match %s", sub, key)
			}
		}
	}
	if _, err := validateSubscription(Subscription{Type: SubscriptionMailbox, Target: "bad/box"}); err != ErrInvalidKey {
		t.Fatalf("bad mailbox subscription err=%v", err)
	}
}

func TestSubscribePullsCurrentRecordsAndUnsubscribe(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	recordA := signedPersonalRecordWithPath(t, priv, "a", 1, "a", 0)
	recordB := signedPersonalRecordWithPath(t, priv, "b", 2, "b", 0)
	if _, err := idx.PutLocal(recordA); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(recordB); err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	records, total, err := idx.Subscribe(Subscription{Type: SubscriptionPrefix, Target: prefix})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(records) != 2 {
		t.Fatalf("subscribe records len=%d total=%d", len(records), total)
	}
	if !idx.IsSubscribed(recordA.Key) || !idx.IsSubscribed(recordB.Key) {
		t.Fatalf("expected personal records to match subscription")
	}
	subs := idx.Subscriptions()
	if len(subs) != 1 {
		t.Fatalf("subscriptions len=%d", len(subs))
	}
	if err := idx.Unsubscribe(Subscription{Type: SubscriptionPrefix, Target: prefix}); err != nil {
		t.Fatal(err)
	}
	if idx.IsSubscribed(recordA.Key) || len(idx.Subscriptions()) != 0 {
		t.Fatalf("subscription still active")
	}
}

func TestSubscribeKeyMissingReturnsEmptyInitialState(t *testing.T) {
	idx := testIndexer(t)
	records, total, err := idx.Subscribe(Subscription{Type: SubscriptionKey, Target: "/tmp/missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 || total != 0 {
		t.Fatalf("missing key records len=%d total=%d", len(records), total)
	}
	if !idx.IsSubscribed("/tmp/missing") {
		t.Fatalf("missing key should still be subscribed")
	}
}

func TestSubscribeNotifiesCallback(t *testing.T) {
	var notified []Subscription
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Subscription: func(sub Subscription) {
			notified = append(notified, sub)
		},
	})
	records, total, err := idx.Subscribe(Subscription{Type: SubscriptionKey, Target: "/tmp/missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 || total != 0 {
		t.Fatalf("records len=%d total=%d", len(records), total)
	}
	if len(notified) != 1 || notified[0].Type != SubscriptionKey || notified[0].Target != "/tmp/missing" {
		t.Fatalf("notified=%v", notified)
	}
	if records, total, err := idx.Subscribe(Subscription{Type: SubscriptionKey, Target: "/tmp/missing"}); err != nil || len(records) != 0 || total != 0 {
		t.Fatalf("duplicate subscribe records=%d total=%d err=%v", len(records), total, err)
	}
	if len(notified) != 1 {
		t.Fatalf("duplicate subscription should not notify: %v", notified)
	}
	if _, _, err := idx.Subscribe(Subscription{Type: SubscriptionMailbox, Target: "bad/box"}); err != ErrInvalidKey {
		t.Fatalf("bad subscription err=%v", err)
	}
	if len(notified) != 1 {
		t.Fatalf("invalid subscription should not notify: %v", notified)
	}
}

func TestSubscriptionLimit(t *testing.T) {
	idx := testIndexer(t)
	for n := 0; n < wire.MaxDKVSSyncFilters; n++ {
		key := fmt.Sprintf("/tmp/sub-%d", n)
		if _, _, err := idx.Subscribe(Subscription{Type: SubscriptionKey, Target: key}); err != nil {
			t.Fatalf("subscribe %d: %v", n, err)
		}
	}
	if _, _, err := idx.Subscribe(Subscription{Type: SubscriptionKey, Target: "/tmp/overflow"}); err != ErrTooManySubscriptions {
		t.Fatalf("overflow err=%v want=%v", err, ErrTooManySubscriptions)
	}
	if _, _, err := idx.Subscribe(Subscription{Type: SubscriptionKey, Target: "/tmp/sub-0"}); err != nil {
		t.Fatalf("duplicate subscription at limit: %v", err)
	}
}

func TestBlobAccountIDMustBeSHA256Hex(t *testing.T) {
	if _, err := ParseKey("/blob/" + strings.Repeat("g", 64) + "/photo.jpg"); err != ErrInvalidKey {
		t.Fatalf("non-hex blob account id err=%v", err)
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

func TestSyncFiltered(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	for n, path := range []string{"a", "b"} {
		record := signedPersonalRecordWithPath(t, priv, path, uint64(n+1), path, 0)
		if _, err := idx.PutLocal(record); err != nil {
			t.Fatal(err)
		}
	}
	tmpPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := idx.PutLocal(signedRecordForKey(t, tmpPriv, "/tmp/other", 1)); err != nil {
		t.Fatal(err)
	}

	first, cursor, done, _, err := idx.SyncFiltered(nil, 1, []Subscription{{Type: SubscriptionPrefix, Target: prefix}})
	if err != nil {
		t.Fatal(err)
	}
	if done || len(first) != 1 || !strings.HasPrefix(first[0].Key, prefix+"/") {
		t.Fatalf("bad filtered first page records=%v done=%v", first, done)
	}
	second, cursor, done, _, err := idx.SyncFiltered(cursor, 1, []Subscription{{Type: SubscriptionPrefix, Target: prefix}})
	if err != nil {
		t.Fatal(err)
	}
	if done || len(second) != 1 || !strings.HasPrefix(second[0].Key, prefix+"/") {
		t.Fatalf("bad filtered second page records=%v done=%v", second, done)
	}
	third, _, done, _, err := idx.SyncFiltered(cursor, 1, []Subscription{{Type: SubscriptionPrefix, Target: prefix}})
	if err != nil {
		t.Fatal(err)
	}
	if !done || len(third) != 0 {
		t.Fatalf("bad filtered third page records=%v done=%v", third, done)
	}
	if _, _, _, _, err := idx.SyncFiltered(nil, 1, []Subscription{{Type: SubscriptionMailbox, Target: "bad/box"}}); err != ErrInvalidKey {
		t.Fatalf("bad filter err=%v", err)
	}
}

func TestUsageFiltersActiveRecordsByPrefix(t *testing.T) {
	height := uint64(1)
	idx := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return height },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	activeA := signedPersonalRecordWithPath(t, priv, "a", 1, "a", 0)
	activeB := signedPersonalRecordWithPath(t, priv, "b", 2, "bb", 0)
	expired := signedPersonalRecordWithPath(t, priv, "expired", 3, "expired", 0)
	expired.IssueHeight = 1
	expired.TTL = 1
	signRecord(t, priv, expired)
	for _, record := range []*wire.DKVSRecord{activeA, activeB, expired} {
		if _, err := idx.PutLocal(record); err != nil {
			t.Fatal(err)
		}
	}
	height = 2
	prefix := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	usage, err := idx.Usage(prefix + "/")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Prefix != prefix || usage.ActiveRecords != 2 {
		t.Fatalf("usage=%#v", usage)
	}
	wantSize := uint64(RecordSize(activeA) + RecordSize(activeB))
	if usage.ActiveTotalSize != wantSize {
		t.Fatalf("usage size=%d want=%d", usage.ActiveTotalSize, wantSize)
	}
	if _, err := idx.Usage("bad"); err != ErrInvalidKey {
		t.Fatalf("bad usage prefix err=%v", err)
	}
}

func TestListPrefixUsesSegmentBoundaryAndExactTotal(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	base := "/personal/" + AccountID(priv.PubKey().SerializeCompressed())
	for seq, key := range []string{base + "/a", base + "/ab", base + "/ac"} {
		if _, err := idx.PutLocal(signedRecordWithValue(t, priv, key, uint64(seq+1), []byte(key), 0)); err != nil {
			t.Fatal(err)
		}
	}
	exact, total, err := idx.ListPrefix(base+"/a", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(exact) != 1 || exact[0].Key != base+"/a" {
		t.Fatalf("exact records=%v total=%d", exact, total)
	}
	all, total, err := idx.ListPrefix(base, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(all) != 1 {
		t.Fatalf("paged records=%d total=%d", len(all), total)
	}
}

func TestExpiredTombstoneFloorPreventsOfflineRecordResurrection(t *testing.T) {
	height := uint64(1)
	newIndexer := func(t *testing.T) *Indexer {
		database := dbpkg.NewKVDB(t.TempDir())
		if database == nil {
			t.Fatal("NewKVDB failed")
		}
		t.Cleanup(func() { _ = database.Close() })
		return New(database, Config{AllowFreeLocal: true, CurrentHeight: func() uint64 { return height }})
	}
	source := newIndexer(t)
	offline := newIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	old := signedPersonalRecordWithKey(t, priv, 1, "old", 0)
	signRecord(t, priv, old)
	if _, err := source.PutLocal(old); err != nil {
		t.Fatal(err)
	}
	if _, err := offline.PutLocal(old); err != nil {
		t.Fatal(err)
	}
	tombstone := signedPersonalRecordWithKey(t, priv, 2, "", FlagTombstone)
	tombstone.IssueHeight = 1
	tombstone.TTL = 1
	signRecord(t, priv, tombstone)
	if _, err := source.PutLocal(tombstone); err != nil {
		t.Fatal(err)
	}
	height = 3
	records, _, done, _, err := source.Sync(nil, 10)
	if err != nil || !done || len(records) != 1 || !IsTombstone(records[0].Flags) {
		t.Fatalf("expired tombstone sync records=%d done=%v err=%v", len(records), done, err)
	}
	if updated, err := offline.PutRemote(records[0]); err != nil || !updated {
		t.Fatalf("apply tombstone floor updated=%v err=%v", updated, err)
	}
	if updated, err := offline.PutRemote(old); err != nil || updated {
		t.Fatalf("replayed old record updated=%v err=%v", updated, err)
	}
	if _, err := offline.Get(old.Key); err != ErrRecordNotFound {
		t.Fatalf("deleted record should be absent: %v", err)
	}
	stored, err := offline.GetForRelay(old.Key)
	if err != nil || !IsTombstone(stored.Flags) {
		t.Fatalf("stored delete command=%#v err=%v", stored, err)
	}
	if _, err := source.PutLocal(tombstone); err != ErrExpiredRecord {
		t.Fatalf("local expired tombstone err=%v", err)
	}
}
