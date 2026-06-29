package agent

import (
	"context"
	"fmt"
	"testing"
)

type fakeLLMClient struct {
	response  string
	responses []string
	req       LLMCompletionRequest
	reqs      []LLMCompletionRequest
}

func (c *fakeLLMClient) Complete(ctx context.Context, req LLMCompletionRequest) (LLMCompletionResponse, error) {
	c.req = req
	c.reqs = append(c.reqs, req)
	if len(c.responses) > 0 {
		response := c.responses[0]
		c.responses = c.responses[1:]
		return LLMCompletionResponse{Content: response}, nil
	}
	return LLMCompletionResponse{Content: c.response}, nil
}

func TestPredictionLLMResolverBuildsConfirmParam(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"a","result":"Team A 101, Team B 98","reason":"final score matched"}`}
	resolver := NewPredictionLLMResolver(client)
	contract := validPredictionContract()
	param, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: "  Team A 101\nTeam B 98  Final ",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "a" {
		t.Fatalf("decision mismatch: %#v", param)
	}
	if param.Result != "Team A 101, Team B 98" {
		t.Fatalf("result mismatch: %s", param.Result)
	}
	if len(client.req.Messages) != 2 {
		t.Fatalf("message count mismatch: %d", len(client.req.Messages))
	}
}

func TestPredictionLLMResolverTruncatesResultTo128Bytes(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"a","result":"这是一个很长的比赛结果说明，用来测试字节截断不会破坏UTF8字符。这是一个很长的比赛结果说明，用来测试字节截断不会破坏UTF8字符。","reason":"final"}`}
	resolver := NewPredictionLLMResolver(client)
	contract := validPredictionContract()
	param, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: "Team A 101 Team B 98 Final",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if len(param.Result) > MaxPredictionConfirmResultLen {
		t.Fatalf("result too long: %d", len(param.Result))
	}
	if param.Result != string([]rune(param.Result)) {
		t.Fatalf("result is not valid UTF-8: %q", param.Result)
	}
}

func TestPredictionLLMResolverInfersMissingOutcomeID(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","result":"Team A wins","reason":"Official final result report"}`}
	resolver := NewPredictionLLMResolver(client)
	contract := validPredictionContract()
	param, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: "Official final result report. Team A wins the event. This maps to allowed outcome a: Team A wins.",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "a" {
		t.Fatalf("decision mismatch: %#v", param)
	}
}

func TestPredictionLLMResolverInfersDrawOutcomeFromEqualScore(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"0","result":"0-0","reason":"The final score is 0-0."}`}
	resolver := NewPredictionLLMResolver(client)
	contract := validPredictionContract()
	contract.Outcomes = append(contract.Outcomes, PredictionOutcome{ID: "c", Text: "draw"})
	param, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: "Official final score: Team A 0-0 Team B.",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "c" {
		t.Fatalf("decision mismatch: %#v", param)
	}
}

func TestPredictionLLMResolverOverridesWrongOutcomeWhenResultIsDraw(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"a","result":"比利时0-0伊朗","reason":"The final score is 0-0."}`}
	resolver := NewPredictionLLMResolver(client)
	contract := validPredictionContract()
	contract.Outcomes = append(contract.Outcomes, PredictionOutcome{ID: "c", Text: "平"})
	param, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: "2026年世界杯：比利时VS伊朗。最终比分 比利时 0-0 伊朗。",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "c" {
		t.Fatalf("decision mismatch: %#v", param)
	}
}

func TestPredictionLLMResolverSingleStepCanMismatchResultAndOutcome(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"c","result":"England wins","reason":"Official final result: England beat Croatia 2-1."}`}
	resolver := NewPredictionLLMResolver(client)
	contract := predictionOutcomeListFixtureContract()
	param, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: predictionOutcomeListFixtureText(),
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "c" || param.Result != "England wins" {
		t.Fatalf("decision mismatch: %#v", param)
	}
}

func TestPredictionLLMResolverTwoStageMatchesOutcomeFromResult(t *testing.T) {
	client := &fakeLLMClient{responses: []string{
		`{"result_type":"outcome","result":"England wins","reason":"Official final result: England beat Croatia 2-1."}`,
		`{"result_type":"outcome","outcome_id":"a","reason":"The result England wins matches outcome a."}`,
	}}
	contract := predictionOutcomeListFixtureContract()
	param, err := resolvePredictionTwoStageForTest(context.Background(), client, PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: predictionOutcomeListFixtureText(),
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("two-stage resolve failed: %v", err)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "a" || param.Result != "England wins" {
		t.Fatalf("decision mismatch: %#v", param)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("LLM call count mismatch: got %d want 2", len(client.reqs))
	}
}

func TestPredictionLLMResolverParsesLooseDecision(t *testing.T) {
	client := &fakeLLMClient{response: "result_type: outcome, outcome_id: a, result: Team A won, reason: Team A won"}
	resolver := NewPredictionLLMResolver(client)
	contract := validPredictionContract()
	param, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: "Team A won final",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "a" {
		t.Fatalf("decision mismatch: %#v", param)
	}
}

func TestPredictionLLMResolverNormalizesOutcomeIDWithText(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"a:Team A wins","result":"Team A wins","reason":"official final result"}`}
	resolver := NewPredictionLLMResolver(client)
	contract := validPredictionContract()
	param, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: "Team A won final",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "a" {
		t.Fatalf("decision mismatch: %#v", param)
	}
}

func TestPredictionLLMResolverRejectsInvalidDecision(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"z"}`}
	resolver := NewPredictionLLMResolver(client)
	contract := validPredictionContract()
	_, err := resolver.Resolve(context.Background(), PredictionLLMResolveRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ResultText: "final score",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err == nil {
		t.Fatalf("expected invalid decision error")
	}
}

func predictionOutcomeListFixtureContract() PredictionContract {
	contract := validPredictionContract()
	contract.Title = "Prediction payout share live test"
	contract.Description = "England vs Croatia result test. Resolve the final result into England win, Croatia win, or draw."
	contract.Outcomes = []PredictionOutcome{
		{ID: "a", Text: "England wins"},
		{ID: "b", Text: "Croatia wins"},
		{ID: "c", Text: "Draw"},
	}
	return contract
}

func predictionOutcomeListFixtureText() string {
	return "Prediction payout share live test. England vs Croatia result test. Resolve the final result into England win, Croatia win, or draw. Official final result: England beat Croatia 2-1. Outcome id a: England wins."
}

func resolvePredictionTwoStageForTest(ctx context.Context, client LLMClient, req PredictionLLMResolveRequest) (PredictionConfirmParam, error) {
	if err := req.Contract.Check(); err != nil {
		return PredictionConfirmParam{}, err
	}
	cleaned := CleanPredictionResultText(req.ResultText)
	if cleaned == "" {
		return PredictionConfirmParam{}, fmt.Errorf("prediction result text is empty")
	}
	resultResponse, err := client.Complete(ctx, LLMCompletionRequest{
		Messages: []LLMMessage{
			{Role: "system", Content: "Analyze only the factual event result. Return compact JSON with result_type, result, and reason. Do not choose an outcome_id."},
			{Role: "user", Content: cleaned},
		},
	})
	if err != nil {
		return PredictionConfirmParam{}, err
	}
	resultDecision, err := decodePredictionLLMDecision(resultResponse.Content)
	if err != nil {
		return PredictionConfirmParam{}, err
	}
	if resultDecision.ResultType == "" {
		resultDecision.ResultType = ResultTypeOutcome
	}
	if resultDecision.ResultType == "pending" {
		return PredictionConfirmParam{}, ErrPredictionResultPending
	}
	if resultDecision.ResultType != ResultTypeOutcome {
		return PredictionConfirmParam{
			ResultType: resultDecision.ResultType,
			Result:     compactPredictionResult(resultDecision),
			ResultURL:  req.ResultURL,
			ObservedAt: req.ObservedAt,
		}, nil
	}

	matchResponse, err := client.Complete(ctx, LLMCompletionRequest{
		Messages: []LLMMessage{
			{Role: "system", Content: "Match the factual result to exactly one allowed prediction outcome. Return compact JSON with outcome_id and reason."},
			{Role: "user", Content: predictionResolvePrompt(req.Contract, compactPredictionResult(resultDecision))},
		},
	})
	if err != nil {
		return PredictionConfirmParam{}, err
	}
	matchDecision, err := decodePredictionLLMDecision(matchResponse.Content)
	if err != nil {
		return PredictionConfirmParam{}, err
	}
	outcomeID, ok := normalizePredictionOutcomeID(req.Contract, matchDecision.OutcomeID)
	if !ok {
		return PredictionConfirmParam{}, fmt.Errorf("unknown prediction outcome id %s", matchDecision.OutcomeID)
	}
	resultDecision.OutcomeID = outcomeID
	param := PredictionConfirmParam{
		ResultType: resultDecision.ResultType,
		OutcomeID:  resultDecision.OutcomeID,
		Result:     compactPredictionResult(resultDecision),
		ResultURL:  req.ResultURL,
		ObservedAt: req.ObservedAt,
	}
	if err := param.Check(req.Contract); err != nil {
		return PredictionConfirmParam{}, err
	}
	return param, nil
}
