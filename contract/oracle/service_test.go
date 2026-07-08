package oracle

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
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

func TestNoBetConfirmParamIsValid(t *testing.T) {
	contract := testPredictionContract()
	param := noBetConfirmParam(contract, 120, "test-model")
	if err := param.Check(contract); err != nil {
		t.Fatalf("no-bet confirm param should be valid: %v", err)
	}
	if param.ResultType != agentcontract.ResultTypeCancelled || param.OutcomeID != "" ||
		param.Result != "no bets" || param.ResultURL != contract.SourceURL {
		t.Fatalf("unexpected no-bet confirm param: %#v", param)
	}
}

func TestTipContextUsesLocalUnixForUnixPrediction(t *testing.T) {
	tip := TipContext{
		Height:    123,
		Unix:      1000,
		BlockUnix: 1000,
		LocalUnix: 2000,
	}
	contract := testPredictionContract()
	if got := tip.oracleUnix(); got != 2000 {
		t.Fatalf("oracle unix mismatch: got %d want 2000", got)
	}
	if got := tip.observedAt(contract); got != 2000 {
		t.Fatalf("unix prediction observed_at mismatch: got %d want 2000", got)
	}

	contract.TimeBase = agentcontract.TimeBaseHeight
	if got := tip.observedAt(contract); got != 123 {
		t.Fatalf("height prediction observed_at mismatch: got %d want 123", got)
	}
}

func TestTipContextFallsBackToLegacyUnix(t *testing.T) {
	tip := TipContext{
		Height: 123,
		Unix:   1000,
	}
	if got := tip.oracleUnix(); got != 1000 {
		t.Fatalf("legacy unix fallback mismatch: got %d want 1000", got)
	}
}

func testPredictionContract() agentcontract.PredictionContract {
	return agentcontract.PredictionContract{
		Subtype:      agentcontract.SubtypePrediction,
		Title:        "Team A vs Team B",
		Description:  "test match",
		TimeBase:     agentcontract.TimeBaseUnix,
		EventTime:    100,
		BetDeadline:  90,
		ConfirmAfter: 110,
		SourceURL:    "https://example.com/match/123",
		BetAsset:     agentcontract.SatoshiAssetName,
		MinBetUnit:   "1",
		Outcomes: []agentcontract.PredictionOutcome{
			{ID: "a", Text: "Team A wins"},
			{ID: "b", Text: "Team B wins"},
		},
	}
}
