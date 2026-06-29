package engine

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
)

func TestSupportedContractsDisableAgentPredictionOnMainnet(t *testing.T) {
	supported := NewQueryServiceForParams(nil, &chaincfg.MainNetParams).SupportedContracts()
	if containsContract(supported, "agent:prediction") {
		t.Fatalf("mainnet should not support agent prediction: %v", supported)
	}
}

func TestSupportedContractsEnableAgentPredictionOnTestnet(t *testing.T) {
	supported := NewQueryServiceForParams(nil, &chaincfg.TestNetParams).SupportedContracts()
	if !containsContract(supported, "agent:prediction") {
		t.Fatalf("testnet should support agent prediction: %v", supported)
	}
}

func containsContract(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
