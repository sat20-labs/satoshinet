package dkvs

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

// A triggered contract can retain its last user-invoke currentBlock while
// per-block payment results advance LastPayHeight. Retention must compare
// payment with the indexer's current height, not that stale contract field.
func TestAutopayRetentionWithStaleContractHeight(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	payer, err := P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		height    uint64
		rawHeight int64
		lastPaid  int64
		accept    bool
	}{
		{"paid_current_block_control", 41, 12, 41, true},
		{"unpaid_next_block_red", 42, 12, 41, false},
		{"resumed_current_block_control", 42, 12, 42, true},
		{"rpc_ahead_must_not_reduce_required_height", 42, 43, 42, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &mutableAutopayStateProvider{state: &AutopayContractState{
				TemplateName: autopayTemplateName, CurrentBlock: tc.rawHeight, ServiceName: "dkvs",
				Recipient: "recipient", FeeAssetName: "sgas", Status: "funding",
				Delegates: map[string]AutopayDelegateState{
					payer: {AmountPerBlock: "10", Balance: "9680", LastPayHeight: tc.lastPaid, Status: "active"},
				},
			}}
			cached := &HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return tc.height }}
			verifier := LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: AutopayFeeVerifier{
				StateProvider: cached, Contract: "autopay", ServiceName: "dkvs", Recipient: "recipient",
				FeeAssetName: "sgas", FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
			}, AllowFreeLocal: true}
			record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
			parsed, err := ParseKey(record.Key)
			if err != nil {
				t.Fatal(err)
			}
			err = verifier.VerifyRecordFeeProof(record, parsed)
			if (err == nil) != tc.accept {
				t.Fatalf("indexed height=%d last paid=%d raw currentBlock=%d: accepted=%v, want %v, err=%v", tc.height, tc.lastPaid, tc.rawHeight, err == nil, tc.accept, err)
			}
		})
	}
}
