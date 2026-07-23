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
	if defaults.FullRecordFeePerBlock != "1" {
		t.Fatalf("full record fee per block=%s", defaults.FullRecordFeePerBlock)
	}
}
