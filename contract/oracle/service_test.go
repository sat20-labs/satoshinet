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
