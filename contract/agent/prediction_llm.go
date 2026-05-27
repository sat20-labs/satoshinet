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

type PredictionLLMResolver struct {
	Client LLMClient
}

func NewPredictionLLMResolver(client LLMClient) *PredictionLLMResolver {
	return &PredictionLLMResolver{Client: client}
}

func (r *PredictionLLMResolver) Resolve(ctx context.Context, req PredictionLLMResolveRequest) (PredictionConfirmParam, error) {
	if r == nil || r.Client == nil {
		return PredictionConfirmParam{}, fmt.Errorf("missing prediction llm client")
	}
	if err := req.Contract.Check(); err != nil {
		return PredictionConfirmParam{}, err
	}
	cleaned := CleanPredictionResultText(req.ResultText)
	if cleaned == "" {
		return PredictionConfirmParam{}, fmt.Errorf("prediction result text is empty")
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
		return PredictionConfirmParam{}, err
	}
	decision, err := decodePredictionLLMDecision(response.Content)
	if err != nil {
		return PredictionConfirmParam{}, err
	}
	if strings.TrimSpace(decision.ResultType) == "pending" {
		return PredictionConfirmParam{}, ErrPredictionResultPending
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
		return PredictionConfirmParam{}, err
	}
	return param, nil
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

func decodePredictionLLMDecision(content string) (predictionLLMDecision, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var decision predictionLLMDecision
	if err := json.Unmarshal([]byte(content), &decision); err != nil {
		return predictionLLMDecision{}, fmt.Errorf("decode prediction llm decision: %w", err)
	}
	return decision, nil
}
