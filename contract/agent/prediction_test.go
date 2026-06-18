package agent

import (
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func validPredictionContract() PredictionContract {
	return PredictionContract{
		Subtype:      SubtypePrediction,
		Title:        "2026 finals",
		Description:  "Predict the result",
		TimeBase:     TimeBaseUnix,
		EventTime:    1780310400,
		BetDeadline:  1780306800,
		ConfirmAfter: 1780396800,
		SourceURL:    "https://example.com/match/preview",
		BetAsset:     SatoshiAssetName,
		MinBetUnit:   "10000",
		Outcomes: []PredictionOutcome{
			{ID: "a", Text: "home wins"},
			{ID: "b", Text: "away wins"},
		},
	}
}

func TestPredictionContractCheck(t *testing.T) {
	contract := validPredictionContract()
	if err := contract.Check(); err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	contract.Outcomes = append(contract.Outcomes, PredictionOutcome{ID: "a", Text: "duplicate"})
	if err := contract.Check(); err == nil {
		t.Fatalf("expected duplicate outcome error")
	}
}

func TestPredictionConfirmCheckAllowsResultURLOnSourceSite(t *testing.T) {
	contract := validPredictionContract()
	param := PredictionConfirmParam{
		ResultType: ResultTypeOutcome,
		OutcomeID:  "a",
		SourceURL:  contract.SourceURL,
		ResultURL:  "https://example.com/match/result/123",
		ResultHash: "abc123",
		ObservedAt: 1780314000,
	}
	if err := param.Check(contract); err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	param.ResultURL = "https://stats.example.com/match/result/123"
	if err := param.Check(contract); err != nil {
		t.Fatalf("Check should allow source subdomain: %v", err)
	}

	param.ResultURL = "http://example.com/match/result/123"
	if err := param.Check(contract); err == nil {
		t.Fatalf("expected result url scheme error")
	}

	param.ResultURL = "https://evil.example.net/match/result/123"
	if err := param.Check(contract); err == nil {
		t.Fatalf("expected result url scope error")
	}
}

func TestPredictionConfirmRefundResultRequiresEmptyOutcome(t *testing.T) {
	contract := validPredictionContract()
	param := PredictionConfirmParam{
		ResultType: ResultTypeCancelled,
		SourceURL:  contract.SourceURL,
		ResultURL:  "https://example.com/match/result/123",
		ResultHash: "abc123",
		ObservedAt: 1780314000,
	}
	if err := param.Check(contract); err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	param.OutcomeID = "a"
	if err := param.Check(contract); err == nil {
		t.Fatalf("expected outcome id error")
	}
}

func TestPredictionConfirmAttestationVerifiesCoreNodeSignature(t *testing.T) {
	_, contract := testAgentDeployTx(t)
	key, pubKey, address := testCoreNodeKey(t)
	param := validPredictionConfirmParam()
	param.AgentVersion = "agent-v1"
	param.ModelVersion = "model-v1"
	if err := SignPredictionConfirmAttestation(contract, &param, key); err != nil {
		t.Fatalf("SignPredictionConfirmAttestation failed: %v", err)
	}
	if param.CoreNodePubKey != pubKey {
		t.Fatalf("pubkey mismatch: got %s want %s", param.CoreNodePubKey, pubKey)
	}
	if err := VerifyPredictionConfirmAttestation(contract, param, pubKey, address, &chaincfg.TestNetParams); err != nil {
		t.Fatalf("VerifyPredictionConfirmAttestation failed: %v", err)
	}

	param.ResultHash = "tampered"
	err := VerifyPredictionConfirmAttestation(contract, param, pubKey, address, &chaincfg.TestNetParams)
	if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("unexpected tamper error: %v", err)
	}
}

func TestPredictionConfirmAttestationSupportsExternalSigner(t *testing.T) {
	_, contract := testAgentDeployTx(t)
	key, pubKey, address := testCoreNodeKey(t)
	param := validPredictionConfirmParam()
	if err := AttachPredictionConfirmAttestation(contract, &param, key.PubKey().SerializeCompressed(),
		func(msg []byte) ([]byte, error) {
			return ecdsa.Sign(key, chainhash.HashB(msg)).Serialize(), nil
		}); err != nil {
		t.Fatalf("AttachPredictionConfirmAttestation failed: %v", err)
	}
	if param.CoreNodePubKey != pubKey {
		t.Fatalf("pubkey mismatch: got %s want %s", param.CoreNodePubKey, pubKey)
	}
	if err := VerifyPredictionConfirmAttestation(contract, param, pubKey, address, &chaincfg.TestNetParams); err != nil {
		t.Fatalf("VerifyPredictionConfirmAttestation failed: %v", err)
	}
}

func TestRuntimeConfirmRequiresConfiguredAttestation(t *testing.T) {
	deployTx, contract := testAgentDeployTx(t)
	parsed, err := ParseTx(deployTx, StandardContractScriptResolver(TestnetContractPrefix))
	if err != nil {
		t.Fatalf("ParseTx failed: %v", err)
	}
	key, pubKey, address := testCoreNodeKey(t)
	deployPayload := agentDeployPayloadFromFramework(parsed.Deploy)
	runtime, err := NewRuntime(contract, *deployPayload, RuntimeConfig{
		CoreNodeAddress:           address,
		CoreNodePubKey:            pubKey,
		AgentAddress:              "agent",
		BootstrapAddress:          "bootstrap",
		ChainParams:               &chaincfg.TestNetParams,
		RequireConfirmAttestation: true,
	})
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	if err := runtime.ApplyReady(ApplyReadyRequest{Invoker: address}); err != nil {
		t.Fatalf("ApplyReady failed: %v", err)
	}

	param := validPredictionConfirmParam()
	_, err = runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker:   address,
		Param:     param,
		TimeValue: runtime.Contract().ConfirmAfter + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "missing core node pubkey") {
		t.Fatalf("unexpected missing attestation error: %v", err)
	}

	if err := SignPredictionConfirmAttestation(contract, &param, key); err != nil {
		t.Fatalf("SignPredictionConfirmAttestation failed: %v", err)
	}
	if _, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker:   address,
		Param:     param,
		TimeValue: runtime.Contract().ConfirmAfter + 1,
	}); err != nil {
		t.Fatalf("ApplyConfirm with attestation failed: %v", err)
	}
}

func TestCheckBetAmount(t *testing.T) {
	for _, amount := range []string{"10000", "20000", "10000.5"} {
		if err := CheckBetAmount(amount, "0.5"); err != nil {
			t.Fatalf("CheckBetAmount(%s) failed: %v", amount, err)
		}
	}
	for _, amount := range []string{"9999", "10000.25"} {
		if err := CheckBetAmount(amount, "10000"); err == nil {
			t.Fatalf("expected CheckBetAmount(%s) error", amount)
		}
	}
}

func validPredictionConfirmParam() PredictionConfirmParam {
	contract := validPredictionContract()
	return PredictionConfirmParam{
		ResultType: ResultTypeOutcome,
		OutcomeID:  "a",
		SourceURL:  contract.SourceURL,
		ResultURL:  "https://example.com/match/result/123",
		ResultHash: "abc123",
		ObservedAt: contract.EventTime + 1,
	}
}

func testCoreNodeKey(t *testing.T) (*btcec.PrivateKey, string, string) {
	t.Helper()
	privBytes := make([]byte, 32)
	for i := range privBytes {
		privBytes[i] = byte(i + 1)
	}
	priv, pub := btcec.PrivKeyFromBytes(privBytes)
	pubKeyText := strings.ToLower(hexEncode(pub.SerializeCompressed()))
	address, err := contractframework.TaprootAddressFromPubKey(pub.SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatalf("TaprootAddressFromPubKey failed: %v", err)
	}
	return priv, pubKeyText, address
}

func hexEncode(data []byte) string {
	const alphabet = "0123456789abcdef"
	out := make([]byte, len(data)*2)
	for i, b := range data {
		out[i*2] = alphabet[b>>4]
		out[i*2+1] = alphabet[b&0x0f]
	}
	return string(out)
}
