package agent

import (
	"context"
	"testing"
)

type fakeLLMClient struct {
	response string
	req      LLMCompletionRequest
}

func (c *fakeLLMClient) Complete(ctx context.Context, req LLMCompletionRequest) (LLMCompletionResponse, error) {
	c.req = req
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
