package dkvs

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

func TestUnpaidAutopayRecordStopsRelayButRemainsReadable(t *testing.T) {
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

	height = 11
	provider.state.CurrentBlock = int64(height)
	if err := idx.RefreshPaidRetentionAt(height); err != nil {
		t.Fatalf("refresh unpaid retention: %v", err)
	}
	if _, err := idx.Get(record.Key); err != nil {
		t.Fatalf("unpaid record should remain locally readable during grace: %v", err)
	}
	records, _, done, _, err := idx.Sync(nil, 100)
	if err != nil || !done {
		t.Fatalf("sync unpaid record: done=%v err=%v", done, err)
	}
	if len(records) != 0 {
		t.Fatalf("unpaid record was relayed: %d", len(records))
	}

	height = 12
	provider.state.CurrentBlock = int64(height)
	delegate := provider.state.Delegates[payer]
	delegate.LastPayHeight = int64(height)
	delegate.Status = "active"
	provider.state.Delegates[payer] = delegate
	if err := idx.RefreshPaidRetentionAt(height); err != nil {
		t.Fatalf("refresh resumed retention: %v", err)
	}
	records, _, done, _, err = idx.Sync(nil, 100)
	if err != nil || !done {
		t.Fatalf("sync resumed record: done=%v err=%v", done, err)
	}
	if len(records) != 1 || records[0].Key != record.Key {
		t.Fatalf("resumed record not relayed: %+v", records)
	}
}
