package dkvs

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
)

func TestDefaultDKVSAutopayPaysCurrentMiner(t *testing.T) {
	defaults := NetworkDefaultsForParams(&chaincfg.TestNetParams)
	if defaults.AutopayRecipient != "" {
		t.Fatalf("DKVS AUTOPAY recipient=%q, want miner fee", defaults.AutopayRecipient)
	}
	if defaults.AutopayMinAmountPerBlock != "1" {
		t.Fatalf("minimum amount per block=%s", defaults.AutopayMinAmountPerBlock)
	}
	if defaults.FullRecordFeePerBlock != "0.1" {
		t.Fatalf("full record fee per block=%s", defaults.FullRecordFeePerBlock)
	}
}

func TestDefaultDKVSAutopayRecordCapacity(t *testing.T) {
	const payer = "tb1ptestpayer"
	defaults := NetworkDefaultsForParams(&chaincfg.TestNetParams)
	verifier := AutopayFeeVerifier{FullRecordFeePerBlock: defaults.FullRecordFeePerBlock}
	state := &AutopayContractState{Delegates: map[string]AutopayDelegateState{
		payer: {AmountPerBlock: "10"},
	}}
	capacity, err := verifier.maxRecordsForState(state, payer, ParsedKey{Namespace: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	if capacity != 100 {
		t.Fatalf("record capacity=%d, want 100", capacity)
	}
}
