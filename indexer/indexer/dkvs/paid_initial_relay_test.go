package dkvs

import (
	"errors"
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

func newAutopayMirrorIndexer(t *testing.T, lastPayHeight int64) (*Indexer, *btcec.PrivateKey, uint64) {
	t.Helper()
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	payer, err := P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	height := uint64(10)
	provider := &mutableAutopayStateProvider{state: &AutopayContractState{
		TemplateName: autopayTemplateName,
		CurrentBlock: int64(height),
		ServiceName:  "dkvs",
		Recipient:    "recipient",
		FeeAssetName: "sgas",
		Status:       "active",
		Delegates: map[string]AutopayDelegateState{
			payer: {
				AmountPerBlock: "1", Balance: "100", LastPayHeight: lastPayHeight, Status: "active",
			},
		},
	}}
	cached := &HeightCachedAutopayStateProvider{
		Provider: provider, CurrentHeight: func() uint64 { return height },
	}
	verifier := LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: AutopayFeeVerifier{
		StateProvider: cached, Contract: "autopay", ServiceName: "dkvs", Recipient: "recipient",
		FeeAssetName: "sgas", FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
	}, AllowFreeLocal: true}
	idx := New(database, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{
			Enabled: true, MaxTTL: 60_000, MaxRecordsPerSigner: 10,
			MaxBytesPerSigner: 1 << 20, MaxTotalRecords: 100, MaxTotalBytes: 1 << 20,
		},
		FeeVerifier: verifier, CurrentHeight: func() uint64 { return height },
	})
	return idx, priv, height
}

// A write that just proved the current block payment must not wait for the next
// maintenance refresh before it can enter P2P sync. This is the propagation
// counterpart of the restart fail-closed test and covers the local origin of a
// record that remote peers will validate through the same priming path.
func TestVerifiedAutopayRecordRelaysImmediately(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	payer, err := P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	height := uint64(10)
	provider := &mutableAutopayStateProvider{state: &AutopayContractState{
		TemplateName: autopayTemplateName,
		CurrentBlock: int64(height),
		ServiceName:  "dkvs",
		Recipient:    "recipient",
		FeeAssetName: "sgas",
		Status:       "active",
		Delegates: map[string]AutopayDelegateState{
			payer: {AmountPerBlock: "1", Balance: "100", LastPayHeight: int64(height), Status: "active"},
		},
	}}
	cached := &HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return height }}
	verifier := LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: AutopayFeeVerifier{
		StateProvider: cached, Contract: "autopay", ServiceName: "dkvs", Recipient: "recipient",
		FeeAssetName: "sgas", FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
	}, AllowFreeLocal: true}
	idx := New(database, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{Enabled: true, MaxTTL: 60_000, MaxRecordsPerSigner: 10,
			MaxBytesPerSigner: 1 << 20, MaxTotalRecords: 100, MaxTotalBytes: 1 << 20},
		FeeVerifier: verifier, CurrentHeight: func() uint64 { return height },
	})
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatalf("put paid record: %v", err)
	}
	records, _, done, _, err := idx.Sync(nil, 100)
	if err != nil || !done {
		t.Fatalf("sync paid record: done=%v err=%v", done, err)
	}
	if len(records) != 1 || records[0].Key != record.Key {
		t.Fatalf("newly verified paid record did not relay: %+v", records)
	}
}

func TestApplyMirrorAcceptsCurrentlyPaidAutopayRecordWithEmptyCache(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, ok := paidRetentionCacheFor(idx).get(record.Key); ok {
		t.Fatal("new mirror target unexpectedly had a paid-retention cache entry")
	}
	root, err := recordsRoot([]*wire.DKVSRecord{record}, height)
	if err != nil {
		t.Fatal(err)
	}
	filters := []Subscription{{Type: SubscriptionKey, Target: record.Key}}
	applied, err := idx.ApplyMirror(filters, []*wire.DKVSRecord{record}, root)
	if err != nil || applied != 1 {
		t.Fatalf("apply paid AUTOPAY mirror: applied=%d err=%v", applied, err)
	}
	if got, err := idx.Get(record.Key); err != nil || RecordHash(got) != RecordHash(record) {
		t.Fatalf("mirrored paid record=%#v err=%v", got, err)
	}
	retention, ok := paidRetentionCacheFor(idx).get(record.Key)
	if !ok || !paidRetentionCurrent(retention, height) {
		t.Fatalf("paid retention was not committed with mirror: retention=%+v ok=%v", retention, ok)
	}
	records, _, done, _, err := idx.Sync(nil, 100)
	if err != nil || !done || len(records) != 1 || records[0].Key != record.Key {
		t.Fatalf("mirrored paid record did not relay: records=%+v done=%v err=%v", records, done, err)
	}
}

func TestApplyMirrorRejectsUnpaidAutopayRecord(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 9)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	root, err := recordsRoot([]*wire.DKVSRecord{record}, height)
	if err != nil {
		t.Fatal(err)
	}
	filters := []Subscription{{Type: SubscriptionKey, Target: record.Key}}
	if _, err := idx.ApplyMirror(filters, []*wire.DKVSRecord{record}, root); !errors.Is(err, ErrInvalidFeeProof) {
		t.Fatalf("unpaid AUTOPAY mirror err=%v", err)
	}
	if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("unpaid AUTOPAY mirror mutated store: %v", err)
	}
	if _, ok := paidRetentionCacheFor(idx).get(record.Key); ok {
		t.Fatal("unpaid AUTOPAY mirror populated retention cache")
	}
}

func TestApplyMirrorStillRejectsFreeLocalRecord(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	record := signedFreePersonalRecord(t, priv, "free-local-mirror", 1, "value", 0)
	root, err := recordsRoot([]*wire.DKVSRecord{record}, height)
	if err != nil {
		t.Fatal(err)
	}
	filters := []Subscription{{Type: SubscriptionKey, Target: record.Key}}
	if _, err := idx.ApplyMirror(filters, []*wire.DKVSRecord{record}, root); !errors.Is(err, ErrFreeLocalNotRelayable) {
		t.Fatalf("FREE_LOCAL mirror err=%v", err)
	}
	if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("FREE_LOCAL mirror mutated store: %v", err)
	}
}
