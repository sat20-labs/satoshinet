package chaincfg

import "testing"

func TestSatoshiNetCanonicalNetworkNames(t *testing.T) {
	if MainNetParams.Name != "mainnet" {
		t.Fatalf("mainnet name = %q", MainNetParams.Name)
	}
	if TestNetParams.Name != "testnet" {
		t.Fatalf("testnet name = %q", TestNetParams.Name)
	}
}
