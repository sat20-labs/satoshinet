package dkvs

import (
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

func resignPaidLifetimeRecord(t *testing.T, record *Record, priv *btcec.PrivateKey) {
	t.Helper()
	hash := SigningHash(record)
	sig, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = sig.Serialize()
}

func TestAutopayRecordRejectsRecordLevelLifetime(t *testing.T) {
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
	verifier := LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: AutopayFeeVerifier{
		StateProvider: provider, Contract: "autopay", ServiceName: "dkvs", Recipient: "recipient",
		FeeAssetName: "sgas", FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
	}, AllowFreeLocal: true}

	for name, mutate := range map[string]func(*Record){
		"ttl":           func(record *Record) { record.TTL = 60_000 },
		"expiry_height": func(record *Record) { record.ExpiryHeight = height + 100 },
	} {
		t.Run(name, func(t *testing.T) {
			record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
			mutate(record)
			resignPaidLifetimeRecord(t, record, priv)
			_, err := validateParsedCoreWithVerifier(record, height, record.IssueTime, false, true, verifier)
			if !errors.Is(err, ErrInvalidFeeProof) {
				t.Fatalf("expected invalid fee proof, got %v", err)
			}
		})
	}
}
