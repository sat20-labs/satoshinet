package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var ErrPredictionResultPending = errors.New("prediction result is pending")

type PredictionLLMResolveRequest struct {
	Contract   PredictionContract
	SourceURL  string
	ResultURL  string
	ResultText string
	ObservedAt int64
}

type PredictionLLMReviewRequest struct {
	Contract  PredictionContract
	CheckedAt int64
}

type PredictionLLMResolver struct {
	Client LLMClient
}

func NewPredictionLLMResolver(client LLMClient) *PredictionLLMResolver {
	return &PredictionLLMResolver{Client: client}
}

func (r *PredictionLLMResolver) Resolve(ctx context.Context, req PredictionLLMResolveRequest) (PredictionConfirmParam, error) {
	param, _, err := r.ResolveDecision(ctx, req)
	return param, err
}

func (r *PredictionLLMResolver) ResolveDecision(ctx context.Context, req PredictionLLMResolveRequest) (PredictionConfirmParam, predictionLLMDecision, error) {
	if r == nil || r.Client == nil {
		return PredictionConfirmParam{}, predictionLLMDecision{}, fmt.Errorf("missing prediction llm client")
	}
	if err := req.Contract.Check(); err != nil {
		return PredictionConfirmParam{}, predictionLLMDecision{}, err
	}
	cleaned := CleanPredictionResultText(req.ResultText)
	if cleaned == "" {
		return PredictionConfirmParam{}, predictionLLMDecision{}, fmt.Errorf("prediction result text is empty")
	}
	response, err := r.Client.Complete(ctx, LLMCompletionRequest{
		Messages: []LLMMessage{
			{
				Role: "system",
				Content: "You resolve SatoshiNet prediction contracts. Return only compact JSON with " +
					"result_type, outcome_id, and reason. Do not include markdown.",
			},
			{
				Role:    "user",
				Content: predictionResolvePrompt(req.Contract, cleaned),
			},
		},
	})
	if err != nil {
		return PredictionConfirmParam{}, predictionLLMDecision{}, err
	}
	decision, err := decodePredictionLLMDecision(response.Content)
	if err != nil {
		return PredictionConfirmParam{}, predictionLLMDecision{}, err
	}
	if strings.TrimSpace(decision.ResultType) == "pending" {
		return PredictionConfirmParam{}, decision, ErrPredictionResultPending
	}
	param := PredictionConfirmParam{
		ResultType: strings.TrimSpace(decision.ResultType),
		OutcomeID:  strings.TrimSpace(decision.OutcomeID),
		SourceURL:  req.SourceURL,
		ResultURL:  req.ResultURL,
		ResultHash: PredictionResultTextHash(cleaned),
		ObservedAt: req.ObservedAt,
	}
	if param.SourceURL == "" {
		param.SourceURL = req.Contract.SourceURL
	}
	if err := param.Check(req.Contract); err != nil {
		return PredictionConfirmParam{}, decision, err
	}
	return param, decision, nil
}

func (r *PredictionLLMResolver) ReviewContract(ctx context.Context, req PredictionLLMReviewRequest) (PredictionRejectParam, bool, error) {
	if r == nil || r.Client == nil {
		return PredictionRejectParam{}, false, fmt.Errorf("missing prediction llm client")
	}
	if err := req.Contract.Check(); err != nil {
		return PredictionRejectParam{}, false, err
	}
	if req.CheckedAt <= 0 {
		return PredictionRejectParam{}, false, fmt.Errorf("invalid checked_at %d", req.CheckedAt)
	}
	response, err := r.Client.Complete(ctx, LLMCompletionRequest{
		Messages: []LLMMessage{
			{
				Role: "system",
				Content: "You review SatoshiNet prediction contracts before activation. Return only compact JSON with " +
					"ready and reason. Do not include markdown.",
			},
			{
				Role:    "user",
				Content: predictionReviewPrompt(req.Contract),
			},
		},
	})
	if err != nil {
		return PredictionRejectParam{}, false, err
	}
	decision, err := decodePredictionLLMReviewDecision(response.Content)
	if err != nil {
		return PredictionRejectParam{}, false, err
	}
	if decision.Ready {
		return PredictionRejectParam{}, true, nil
	}
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = "agent rejected prediction contract without a reason"
	}
	return PredictionRejectParam{Reason: reason, CheckedAt: req.CheckedAt}, false, nil
}

func CleanPredictionResultText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func PredictionResultTextHash(cleanedText string) string {
	sum := sha256.Sum256([]byte(CleanPredictionResultText(cleanedText)))
	return hex.EncodeToString(sum[:])
}

type predictionLLMDecision struct {
	ResultType string `json:"result_type"`
	OutcomeID  string `json:"outcome_id"`
	Reason     string `json:"reason"`
}

type predictionLLMReviewDecision struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason"`
}

func predictionReviewPrompt(contract PredictionContract) string {
	var b strings.Builder
	b.WriteString("Review this prediction contract. It can enter ready state only if the event, allowed outcomes, timing, source URL, asset, and minimum bet unit are understandable, verifiable, and executable.\n")
	b.WriteString("Reject it when the event is ambiguous, outcomes are unclear or incomplete, source URL is unsuitable for verification, timing is impossible, or execution rules are inconsistent.\n\n")
	b.WriteString("Title: ")
	b.WriteString(contract.Title)
	b.WriteString("\nDescription: ")
	b.WriteString(contract.Description)
	b.WriteString("\nTime base: ")
	b.WriteString(contract.TimeBase)
	b.WriteString("\nEvent time: ")
	b.WriteString(fmt.Sprint(contract.EventTime))
	b.WriteString("\nBet deadline: ")
	b.WriteString(fmt.Sprint(contract.BetDeadline))
	b.WriteString("\nConfirm after: ")
	b.WriteString(fmt.Sprint(contract.ConfirmAfter))
	b.WriteString("\nSource URL: ")
	b.WriteString(contract.SourceURL)
	b.WriteString("\nBet asset: ")
	b.WriteString(contract.BetAsset)
	b.WriteString("\nMin bet unit: ")
	b.WriteString(contract.MinBetUnit)
	b.WriteString("\nOutcomes:\n")
	for _, outcome := range contract.Outcomes {
		b.WriteString("- ")
		b.WriteString(outcome.ID)
		b.WriteString(": ")
		b.WriteString(outcome.Text)
		b.WriteString("\n")
	}
	b.WriteString("\nReturn {\"ready\":true,\"reason\":\"...\"} only when it is understandable, verifiable, and executable. Otherwise return {\"ready\":false,\"reason\":\"...\"}.")
	return b.String()
}

func predictionResolvePrompt(contract PredictionContract, cleanedText string) string {
	var b strings.Builder
	b.WriteString("Contract title: ")
	b.WriteString(contract.Title)
	b.WriteString("\nDescription: ")
	b.WriteString(contract.Description)
	b.WriteString("\nAllowed outcomes:\n")
	for _, outcome := range contract.Outcomes {
		b.WriteString("- ")
		b.WriteString(outcome.ID)
		b.WriteString(": ")
		b.WriteString(outcome.Text)
		b.WriteString("\n")
	}
	b.WriteString("\nResult text:\n")
	b.WriteString(cleanedText)
	b.WriteString("\n\nChoose exactly one allowed outcome when the result is clear. ")
	b.WriteString("Use result_type \"pending\" and empty outcome_id when the event result is not available yet. ")
	b.WriteString("Use result_type \"unverifiable\" and empty outcome_id when the result cannot be verified. ")
	b.WriteString("Use result_type \"invalid\" and empty outcome_id when the event or market is invalid.")
	return b.String()
}

func decodePredictionLLMReviewDecision(content string) (predictionLLMReviewDecision, error) {
	content = trimLLMJSON(content)
	var decision predictionLLMReviewDecision
	if err := json.Unmarshal([]byte(content), &decision); err != nil {
		return predictionLLMReviewDecision{}, fmt.Errorf("decode prediction llm review: %w", err)
	}
	return decision, nil
}

func decodePredictionLLMDecision(content string) (predictionLLMDecision, error) {
	content = trimLLMJSON(content)
	var decision predictionLLMDecision
	if err := json.Unmarshal([]byte(content), &decision); err != nil {
		return predictionLLMDecision{}, fmt.Errorf("decode prediction llm decision: %w", err)
	}
	return decision, nil
}

func trimLLMJSON(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}
