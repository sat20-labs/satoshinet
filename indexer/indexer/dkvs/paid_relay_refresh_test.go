package dkvs

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

// A paid record is locally readable immediately after admission, but relay is
// fail-closed until the node refreshes the current-block payment state. This
// covers startup/restart and the short interval after a new local write.
func TestAutopayRecordFailsClosedUntilRetentionRefresh(t *testing.T) {
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
		FeeVerifier:   verifier,
		CurrentHeight: func() uint64 { return height },
	})
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatalf("put paid record: %v", err)
	}
	// Model a process restart: persisted records remain, but the derived
	// retention cache is intentionally empty until contract state is refreshed.
	paidRetentionCacheFor(idx).replace(make(map[string]PaidRecordRetention))

	records, _, done, _, err := idx.Sync(nil, 100)
	if err != nil || !done {
		t.Fatalf("sync before refresh: done=%v err=%v", done, err)
	}
	if len(records) != 0 {
		t.Fatalf("unrefreshed paid record was relayed: %d", len(records))
	}
	if _, err := idx.Get(record.Key); err != nil {
		t.Fatalf("unrefreshed paid record should remain locally readable: %v", err)
	}

	if err := idx.RefreshPaidRetentionAt(height); err != nil {
		t.Fatalf("refresh paid retention: %v", err)
	}
	records, _, done, _, err = idx.Sync(nil, 100)
	if err != nil || !done {
		t.Fatalf("sync after refresh: done=%v err=%v", done, err)
	}
	if len(records) != 1 || records[0].Key != record.Key {
		t.Fatalf("refreshed paid record not relayed: %+v", records)
	}
}
