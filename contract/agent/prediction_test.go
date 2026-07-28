package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
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

func TestPredictionCodecUsesSharedCompactScriptFormat(t *testing.T) {
	contract := validPredictionContract()
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if json.Valid(content) {
		t.Fatalf("prediction contract content must not use JSON")
	}
	commonContent, err := contract.commonContract().Encode()
	if err != nil {
		t.Fatalf("common Encode failed: %v", err)
	}
	if !reflect.DeepEqual(content, commonContent) {
		t.Fatalf("agent runtime codec differs from shared contract codec")
	}
	decoded, err := DecodePredictionContract(content)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if !reflect.DeepEqual(contract, decoded) {
		t.Fatalf("decoded contract mismatch: %#v", decoded)
	}

	confirm := validPredictionConfirmParam()
	confirm.AgentVersion = CurrentAgentVersion
	confirm.ModelVersion = "model-v1"
	param, err := confirm.Encode()
	if err != nil {
		t.Fatalf("confirm Encode failed: %v", err)
	}
	commonParam, err := (contractcommon.AgentPredictionConfirmParam(confirm)).Encode()
	if err != nil {
		t.Fatalf("common confirm Encode failed: %v", err)
	}
	if !reflect.DeepEqual(param, commonParam) {
		t.Fatalf("agent runtime param codec differs from shared contract codec")
	}
	decodedConfirm, err := DecodePredictionConfirmParam(param)
	if err != nil {
		t.Fatalf("confirm Decode failed: %v", err)
	}
	if !reflect.DeepEqual(confirm, decodedConfirm) {
		t.Fatalf("decoded confirm mismatch: %#v", decodedConfirm)
	}
}

func TestPredictionCodecRejectsLegacyJSON(t *testing.T) {
	if _, err := DecodePredictionContract([]byte(`{"subtype":"prediction"}`)); err == nil {
		t.Fatalf("expected legacy JSON contract content to be rejected")
	}
	if _, err := DecodePredictionBetParam([]byte(`{"outcome_id":"a"}`)); err == nil {
		t.Fatalf("expected legacy JSON param to be rejected")
	}
}

func TestPredictionConfirmCheckAllowsResultURLOnSourceSite(t *testing.T) {
	contract := validPredictionContract()
	param := PredictionConfirmParam{
		ResultType: ResultTypeOutcome,
		OutcomeID:  "a",
		Result:     "Team A 101, Team B 98",
		ResultURL:  "https://example.com/match/result/123",
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
		Result:     "match cancelled",
		ResultURL:  "https://example.com/match/result/123",
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

func validPredictionConfirmParam() PredictionConfirmParam {
	contract := validPredictionContract()
	return PredictionConfirmParam{
		ResultType: ResultTypeOutcome,
		OutcomeID:  "a",
		Result:     "Team A 101, Team B 98",
		ResultURL:  "https://example.com/match/result/123",
		ObservedAt: contract.EventTime + 1,
	}
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
