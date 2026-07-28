package agent

import "testing"

func TestDeriveContractAddressUsesAgentType(t *testing.T) {
	content, err := validPredictionContract().Encode()
	if err != nil {
		t.Fatalf("PredictionContract.Encode failed: %v", err)
	}
	addr, hash, err := DeriveContractAddress(
		TestnetContractPrefix,
		SubtypePrediction,
		content,
		"deployer",
		3,
	)
	if err != nil {
		t.Fatalf("DeriveContractAddress failed: %v", err)
	}
	if addr.ContractType() != ContractTypeAgent {
		t.Fatalf("contract type mismatch: got %d", addr.ContractType())
	}
	if len(addr.ContractHashBytes()) != len(hash) {
		t.Fatalf("hash length mismatch: got %d want %d", len(addr.ContractHashBytes()), len(hash))
	}
}
