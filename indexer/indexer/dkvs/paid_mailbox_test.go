package dkvs

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

func TestPaidMailboxShareUsesContinuousRetention(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubKey := priv.PubKey().SerializeCompressed()
	accountID := AccountID(pubKey)
	payer, err := P2TRAddressFromPubKeyBytes(pubKey, &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	height := uint64(10)
	provider := &mutableAutopayStateProvider{state: &AutopayContractState{
		TemplateName: autopayTemplateName,
		CurrentBlock: int64(height),
		ServiceName:  "dkvs",
		FeeAssetName: "sgas",
		Status:       "active",
		Delegates: map[string]AutopayDelegateState{
			payer: {AmountPerBlock: "1", Balance: "100", LastPayHeight: int64(height), Status: "active"},
		},
	}}
	cached := &HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return height }}
	verifier := LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: AutopayFeeVerifier{
		StateProvider: cached, Contract: "autopay", ServiceName: "dkvs",
		FeeAssetName: "sgas", FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
	}, AllowFreeLocal: true}
	idx := New(database, Config{
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{Enabled: true, MaxTTL: 60_000, MaxRecordsPerSigner: 10,
			MaxBytesPerSigner: 1 << 20, MaxTotalRecords: 100, MaxTotalBytes: 1 << 20},
		MailboxPolicy: MailboxPolicy{MaxShareBytes: 1 << 20, MaxShares: 10, MaxShareSize: 4096, MaxShareTTL: 60_000},
		FeeVerifier:   verifier,
		CurrentHeight: func() uint64 { return height },
	})
	key, err := MailShareKey(accountID, "package", "share")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewAccountRecord(key, []byte("encrypted-share"), RecordOptions{Seq: 1})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewAutopayFeeProof(key, "mail", uint32(RecordSize(record)), 0, "autopay", "")
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	hash := SigningHash(record)
	sig, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = sig.Serialize()
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatalf("put zero-TTL AUTOPAY mailbox share: %v", err)
	}
	stored, err := idx.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TTL != 0 || stored.ExpiryHeight != 0 {
		t.Fatalf("paid mailbox share carried record lease: ttl=%d expiry=%d", stored.TTL, stored.ExpiryHeight)
	}
}
