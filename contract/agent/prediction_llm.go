package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var ErrPredictionResultPending = errors.New("prediction result is pending")

var predictionDrawScorePattern = regexp.MustCompile(`(^|[^\d])(\d{1,2})\s*[-:：]\s*(\d{1,2})([^\d]|$)`)

type PredictionLLMResolveRequest struct {
	Contract   PredictionContract
	SourceURL  string
	ResultURL  string
	ResultText string
	ObservedAt int64
}

type PredictionLLMReviewRequest struct {
	Contract   PredictionContract
	CheckedAt  int64
	SourceURL  string
	SourceText string
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
					"result_type, outcome_id, result, and reason. result_type must be one of outcome, pending, unverifiable, invalid, or cancelled. " +
					"Use result_type outcome and set outcome_id to the chosen allowed outcome id when the result is clear. Do not include markdown.",
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
	decision = normalizePredictionLLMDecision(req.Contract, cleaned, decision)
	if strings.TrimSpace(decision.ResultType) == "pending" {
		return PredictionConfirmParam{}, decision, ErrPredictionResultPending
	}
	param := PredictionConfirmParam{
		ResultType: strings.TrimSpace(decision.ResultType),
		OutcomeID:  strings.TrimSpace(decision.OutcomeID),
		Result:     compactPredictionResult(decision),
		ResultURL:  req.ResultURL,
		ObservedAt: req.ObservedAt,
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
				Content: "You review SatoshiNet prediction contracts before activation. Review the contract definition and source page evidence, not the current event result. Return only compact JSON with " +
					"ready and reason. Do not include markdown.",
			},
			{
				Role:    "user",
				Content: predictionReviewPrompt(req),
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

type predictionLLMDecision struct {
	ResultType string `json:"result_type"`
	OutcomeID  string `json:"outcome_id"`
	Outcome    string `json:"outcome"`
	ID         string `json:"id"`
	Result     string `json:"result"`
	Reason     string `json:"reason"`
}

func compactPredictionResult(decision predictionLLMDecision) string {
	result := strings.TrimSpace(decision.Result)
	if result == "" {
		result = strings.TrimSpace(decision.Reason)
	}
	if len(result) <= MaxPredictionConfirmResultLen {
		return result
	}
	return strings.TrimSpace(truncateUTF8Bytes(result, MaxPredictionConfirmResultLen))
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	end := 0
	for idx := range s {
		if idx > maxBytes {
			break
		}
		end = idx
	}
	if end == 0 {
		return ""
	}
	return s[:end]
}

func normalizePredictionLLMDecision(contract PredictionContract, cleanedText string, decision predictionLLMDecision) predictionLLMDecision {
	decision.ResultType = strings.TrimSpace(decision.ResultType)
	decision.OutcomeID = strings.TrimSpace(decision.OutcomeID)

	if decision.ResultType == ResultTypeOutcome {
		if id, ok := inferPredictionDrawOutcomeID(contract, cleanedText, decision.Result, decision.Reason); ok {
			decision.OutcomeID = id
			return decision
		}
	}
	if id, ok := normalizePredictionOutcomeID(contract, decision.OutcomeID); ok {
		decision.OutcomeID = id
		return decision
	}
	for _, raw := range []string{decision.Outcome, decision.ID} {
		if id, ok := normalizePredictionOutcomeID(contract, raw); ok {
			decision.OutcomeID = id
			return decision
		}
	}
	if decision.ResultType != ResultTypeOutcome {
		return decision
	}
	if id, ok := inferPredictionOutcomeID(contract, cleanedText, decision.Result, decision.Reason, decision.Outcome); ok {
		decision.OutcomeID = id
	}
	return decision
}

func normalizePredictionOutcomeID(contract PredictionContract, raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	candidates := predictionOutcomeIDCandidates(value)
	for _, outcome := range contract.Outcomes {
		for _, candidate := range candidates {
			if strings.EqualFold(candidate, strings.TrimSpace(outcome.ID)) {
				return outcome.ID, true
			}
			if strings.EqualFold(candidate, strings.TrimSpace(outcome.Text)) {
				return outcome.ID, true
			}
		}
	}
	return "", false
}

func predictionOutcomeIDCandidates(value string) []string {
	candidates := []string{value}
	for _, sep := range []string{":", "：", "-", "—", " ", "\t"} {
		before, after, ok := strings.Cut(value, sep)
		if !ok {
			continue
		}
		if before = strings.TrimSpace(before); before != "" {
			candidates = append(candidates, before)
		}
		if after = strings.TrimSpace(after); after != "" {
			candidates = append(candidates, after)
		}
	}
	return candidates
}

func inferPredictionOutcomeID(contract PredictionContract, texts ...string) (string, bool) {
	matches := make(map[string]struct{})
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		lowerText := strings.ToLower(text)
		for _, outcome := range contract.Outcomes {
			id := strings.TrimSpace(outcome.ID)
			outcomeText := strings.TrimSpace(outcome.Text)
			if id != "" && containsPredictionOutcomeID(lowerText, strings.ToLower(id)) {
				matches[outcome.ID] = struct{}{}
			}
			if outcomeText != "" && strings.Contains(lowerText, strings.ToLower(outcomeText)) {
				matches[outcome.ID] = struct{}{}
			}
		}
	}
	if len(matches) != 1 {
		return inferPredictionDrawOutcomeID(contract, texts...)
	}
	for id := range matches {
		return id, true
	}
	return "", false
}

func inferPredictionDrawOutcomeID(contract PredictionContract, texts ...string) (string, bool) {
	drawEvidence := false
	for _, text := range texts {
		if predictionTextIndicatesDraw(text) {
			drawEvidence = true
			break
		}
	}
	if !drawEvidence {
		return "", false
	}
	return predictionDrawOutcomeID(contract)
}

func predictionDrawOutcomeID(contract PredictionContract) (string, bool) {
	var matched string
	for _, outcome := range contract.Outcomes {
		if !predictionOutcomeTextIndicatesDraw(outcome.Text) {
			continue
		}
		if matched != "" {
			return "", false
		}
		matched = outcome.ID
	}
	if strings.TrimSpace(matched) == "" {
		return "", false
	}
	return matched, true
}

func predictionOutcomeTextIndicatesDraw(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	for _, word := range []string{"draw", "tie", "tied", "平", "平局", "战平"} {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func predictionTextIndicatesDraw(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	if predictionOutcomeTextIndicatesDraw(text) {
		return true
	}
	for _, match := range predictionDrawScorePattern.FindAllStringSubmatch(text, -1) {
		if len(match) >= 4 && match[2] == match[3] {
			return true
		}
	}
	return false
}

func containsPredictionOutcomeID(text, id string) bool {
	if text == "" || id == "" {
		return false
	}
	markers := []string{
		"outcome " + id,
		"outcome: " + id,
		"outcome_id " + id,
		"outcome_id: " + id,
		"outcome_id=\"" + id + "\"",
		"outcome_id='" + id + "'",
		"allowed outcome " + id,
	}
	for _, marker := range markers {
		if containsPredictionMarker(text, marker) {
			return true
		}
	}
	return false
}

func containsPredictionMarker(text, marker string) bool {
	for {
		idx := strings.Index(text, marker)
		if idx < 0 {
			return false
		}
		after := idx + len(marker)
		if after >= len(text) || !isPredictionIDByte(text[after]) {
			return true
		}
		text = text[after:]
	}
}

func isPredictionIDByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

type predictionLLMReviewDecision struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason"`
}

func predictionReviewPrompt(req PredictionLLMReviewRequest) string {
	contract := req.Contract
	var b strings.Builder
	b.WriteString("Review this prediction contract definition before betting starts. Do not resolve or predict the event result during this review.\n")
	b.WriteString("Return ready true when the event, allowed outcomes, timing, source URL, asset, and minimum bet unit are understandable, verifiable later, and executable.\n")
	b.WriteString("Do not reject only because the final result is not available yet, or because the source URL has not published the result yet.\n")
	b.WriteString("Reject only when the event is ambiguous, outcomes are unclear or incomplete, source URL is unsuitable for later verification, timing is impossible, or execution rules are inconsistent.\n\n")
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
	if req.SourceURL != "" && req.SourceURL != contract.SourceURL {
		b.WriteString("\nFetched final URL: ")
		b.WriteString(req.SourceURL)
	}
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
	if text := strings.TrimSpace(req.SourceText); text != "" {
		b.WriteString("\nSource page text excerpt:\n")
		b.WriteString(truncateUTF8Bytes(text, 4000))
		b.WriteString("\n")
	}
	b.WriteString("\nReturn {\"ready\":true,\"reason\":\"...\"} when the contract definition is understandable, can be verified later, and is executable. Otherwise return {\"ready\":false,\"reason\":\"...\"}.")
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
	b.WriteString("Return result_type outcome and outcome_id equal to the chosen allowed outcome id. ")
	b.WriteString("Set result to a short factual final result, such as the final score, limited to 128 bytes. ")
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
		if decision, ok := parsePredictionLLMDecisionFields(content); ok {
			return decision, nil
		}
		return predictionLLMDecision{}, fmt.Errorf("decode prediction llm decision: %w", err)
	}
	return decision, nil
}

func parsePredictionLLMDecisionFields(content string) (predictionLLMDecision, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return predictionLLMDecision{}, false
	}
	normalized := strings.NewReplacer("\n", ",", "\r", ",", ";", ",", "|", ",").Replace(content)
	var decision predictionLLMDecision
	for _, part := range strings.Split(normalized, ",") {
		key, value, ok := strings.Cut(part, ":")
		if !ok {
			key, value, ok = strings.Cut(part, "=")
		}
		if !ok {
			continue
		}
		key = strings.ToLower(cleanPredictionLLMField(key))
		value = cleanPredictionLLMField(value)
		switch key {
		case "result_type", "resulttype":
			decision.ResultType = value
		case "outcome_id", "outcomeid":
			decision.OutcomeID = value
		case "outcome":
			decision.Outcome = value
		case "id":
			decision.ID = value
		case "reason":
			decision.Reason = value
		}
	}
	if decision.ResultType == "" && decision.OutcomeID == "" && decision.Outcome == "" && decision.ID == "" {
		return predictionLLMDecision{}, false
	}
	return decision, true
}

func cleanPredictionLLMField(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "{}[] ")
	value = strings.Trim(value, "\"'")
	return strings.TrimSpace(value)
}

func trimLLMJSON(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}
