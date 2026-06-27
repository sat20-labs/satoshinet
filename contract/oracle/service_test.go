package oracle

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
)

func TestServiceDisabledOnMainnet(t *testing.T) {
	service, err := NewService(Config{
		ChainParams: &chaincfg.MainNetParams,
		LLM: LLMConfig{
			Provider: "ollama",
			Model:    "test",
		},
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	if service.Enabled() {
		t.Fatalf("mainnet oracle service should be disabled")
	}
}
