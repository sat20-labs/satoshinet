package agent

import "testing"

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
