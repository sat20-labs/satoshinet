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

var ErrPredictionResultEvidence = errors.New("prediction result is not supported by source evidence")

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
	resultDecision, err := r.extractPredictionResult(ctx, req.Contract, cleaned)
	if err != nil {
		return PredictionConfirmParam{}, resultDecision, err
	}
	if strings.TrimSpace(resultDecision.ResultType) == "pending" {
		return PredictionConfirmParam{}, resultDecision, ErrPredictionResultPending
	}
	if resultDecision.ResultType == ResultTypeOutcome {
		if err := r.validatePredictionResultEvidence(ctx, cleaned, resultDecision); err != nil {
			return PredictionConfirmParam{}, resultDecision, err
		}
	}
	if resultDecision.ResultType != ResultTypeOutcome {
		param := PredictionConfirmParam{
			ResultType: strings.TrimSpace(resultDecision.ResultType),
			Result:     compactPredictionResult(resultDecision),
			ResultURL:  req.ResultURL,
			ObservedAt: req.ObservedAt,
		}
		if err := param.Check(req.Contract); err != nil {
			return PredictionConfirmParam{}, resultDecision, err
		}
		return param, resultDecision, nil
	}
	factualResult := compactPredictionResult(resultDecision)
	matchDecision, err := r.matchPredictionOutcome(ctx, req.Contract, factualResult, cleaned)
	if err != nil {
		return PredictionConfirmParam{}, matchDecision, err
	}
	matchDecision.ResultType = ResultTypeOutcome
	matchDecision.Result = factualResult
	if strings.TrimSpace(matchDecision.Reason) == "" {
		matchDecision.Reason = strings.TrimSpace(resultDecision.Reason)
	}
	matchDecision = normalizePredictionLLMDecision(req.Contract, factualResult, matchDecision)
	if strings.TrimSpace(matchDecision.OutcomeID) == "" {
		if id, ok := inferPredictionExplicitOutcomeID(req.Contract, cleaned); ok {
			matchDecision.OutcomeID = id
		}
	}
	param := PredictionConfirmParam{
		ResultType: ResultTypeOutcome,
		OutcomeID:  strings.TrimSpace(matchDecision.OutcomeID),
		Result:     compactPredictionResult(matchDecision),
		ResultURL:  req.ResultURL,
		ObservedAt: req.ObservedAt,
	}
	if err := param.Check(req.Contract); err != nil {
		return PredictionConfirmParam{}, matchDecision, err
	}
	return param, matchDecision, nil
}

func (r *PredictionLLMResolver) extractPredictionResult(ctx context.Context, contract PredictionContract, cleaned string) (predictionLLMDecision, error) {
	response, err := r.Client.Complete(ctx, LLMCompletionRequest{
		Messages: []LLMMessage{
			{
				Role: "system",
				Content: "You extract the factual result for a SatoshiNet prediction contract. Return only compact JSON with " +
					"result_type, result, evidence_quote, and reason. result_type must be one of outcome, pending, unverifiable, invalid, or cancelled. " +
					"For outcome, evidence_quote must be a verbatim quote from the evidence text that directly states the final result. " +
					"Do not choose or return an outcome_id. Do not include markdown.",
			},
			{
				Role:    "user",
				Content: "/no_think\n" + predictionResultExtractionPrompt(contract, cleaned),
			},
		},
	})
	if err != nil {
		return predictionLLMDecision{}, err
	}
	decision, err := decodePredictionLLMDecision(response.Content)
	if err != nil {
		return predictionLLMDecision{}, err
	}
	decision = normalizePredictionResultDecision(cleaned, decision)
	return decision, nil
}

func (r *PredictionLLMResolver) matchPredictionOutcome(ctx context.Context, contract PredictionContract, factualResult, evidenceText string) (predictionLLMDecision, error) {
	response, err := r.Client.Complete(ctx, LLMCompletionRequest{
		Messages: []LLMMessage{
			{
				Role: "system",
				Content: "You match a factual result to exactly one allowed prediction outcome. Return only compact JSON with " +
					"result_type, outcome_id, and reason. result_type must be outcome when one outcome clearly matches. " +
					"Use pending, unverifiable, or invalid only when no allowed outcome can be chosen. Do not include markdown.",
			},
			{
				Role:    "user",
				Content: "/no_think\n" + predictionOutcomeMatchPrompt(contract, factualResult, evidenceText),
			},
		},
	})
	if err != nil {
		return predictionLLMDecision{}, err
	}
	decision, err := decodePredictionLLMDecision(response.Content)
	if err != nil {
		return predictionLLMDecision{}, err
	}
	decision = normalizePredictionLLMDecision(contract, factualResult, decision)
	if strings.TrimSpace(decision.OutcomeID) == "" {
		if id, ok := inferPredictionExplicitOutcomeID(contract, evidenceText); ok {
			decision.OutcomeID = id
		}
	}
	if strings.TrimSpace(decision.ResultType) == "pending" {
		return decision, ErrPredictionResultPending
	}
	return decision, nil
}

func normalizePredictionResultDecision(cleaned string, decision predictionLLMDecision) predictionLLMDecision {
	decision.ResultType = strings.TrimSpace(decision.ResultType)
	decision.Result = strings.TrimSpace(decision.Result)
	decision.Reason = strings.TrimSpace(decision.Reason)
	if !predictionLLMResultTypeValid(decision.ResultType) {
		if decision.Result != "" || decision.Reason != "" || decision.OutcomeID != "" || decision.Outcome != "" || decision.ID != "" {
			decision.ResultType = ResultTypeOutcome
		}
	}
	if decision.ResultType == ResultTypeOutcome && decision.Result == "" && decision.Reason == "" {
		decision.Result = cleaned
	}
	return decision
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
	Evidence   string `json:"evidence_quote"`
	Reason     string `json:"reason"`
}

type predictionEvidenceDecision struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason"`
}

func (r *PredictionLLMResolver) validatePredictionResultEvidence(ctx context.Context, cleaned string, decision predictionLLMDecision) error {
	evidence := CleanPredictionResultText(decision.Evidence)
	result := CleanPredictionResultText(decision.Result)
	if evidence == "" {
		if result != "" && strings.Contains(cleaned, result) {
			evidence = result
		}
	}
	if evidence == "" {
		return fmt.Errorf("%w: missing evidence_quote", ErrPredictionResultEvidence)
	}
	if !strings.Contains(cleaned, evidence) {
		return fmt.Errorf("%w: evidence_quote is not present in source text", ErrPredictionResultEvidence)
	}
	if result != "" && strings.Contains(evidence, result) {
		return nil
	}
	response, err := r.Client.Complete(ctx, LLMCompletionRequest{
		Messages: []LLMMessage{
			{
				Role: "system",
				Content: "You verify whether a verbatim source quote directly proves a claimed factual result. " +
					"Return only compact JSON with supported and reason. Do not use outside knowledge or infer from an event description.",
			},
			{
				Role: "user",
				Content: "/no_think\nClaimed factual result:\n" + result +
					"\n\nVerbatim source quote:\n" + evidence +
					"\n\nSet supported=true only when the quote itself directly establishes the claimed final result. " +
					"Participant names, event titles, schedules, or descriptions without a final result are insufficient.",
			},
		},
	})
	if err != nil {
		return err
	}
	var verified predictionEvidenceDecision
	if err := json.Unmarshal([]byte(trimLLMJSON(response.Content)), &verified); err != nil {
		return fmt.Errorf("decode prediction evidence decision: %w", err)
	}
	if !verified.Supported {
		return fmt.Errorf("%w: %s", ErrPredictionResultEvidence, strings.TrimSpace(verified.Reason))
	}
	return nil
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
		if !predictionLLMResultTypeValid(decision.ResultType) {
			decision.ResultType = ResultTypeOutcome
		}
		return decision
	}
	for _, raw := range []string{decision.Outcome, decision.ID} {
		if id, ok := normalizePredictionOutcomeID(contract, raw); ok {
			decision.OutcomeID = id
			if !predictionLLMResultTypeValid(decision.ResultType) {
				decision.ResultType = ResultTypeOutcome
			}
			return decision
		}
	}
	if decision.ResultType != ResultTypeOutcome {
		return decision
	}
	if id, ok := inferPredictionOutcomeID(contract, decision.Result, decision.Reason, decision.Outcome); ok {
		decision.OutcomeID = id
		return decision
	}
	if id, ok := inferPredictionOutcomeID(contract, cleanedText); ok {
		decision.OutcomeID = id
	}
	return decision
}

func predictionLLMResultTypeValid(resultType string) bool {
	switch strings.TrimSpace(resultType) {
	case ResultTypeOutcome, "pending", ResultTypeCancelled, ResultTypeInvalid, ResultTypeUnverifiable:
		return true
	default:
		return false
	}
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
			if outcomeText != "" && predictionOutcomeTextMatchesEvidence(outcomeText, text) {
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

func inferPredictionExplicitOutcomeID(contract PredictionContract, texts ...string) (string, bool) {
	matches := make(map[string]struct{})
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		lowerText := strings.ToLower(text)
		for _, outcome := range contract.Outcomes {
			id := strings.TrimSpace(outcome.ID)
			if id != "" && containsPredictionOutcomeID(lowerText, strings.ToLower(id)) {
				matches[outcome.ID] = struct{}{}
			}
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	for id := range matches {
		return id, true
	}
	return "", false
}

func predictionOutcomeTextMatchesEvidence(outcomeText, evidence string) bool {
	outcome := normalizePredictionEvidenceText(outcomeText)
	evidence = normalizePredictionEvidenceText(evidence)
	if outcome == "" || evidence == "" {
		return false
	}
	if strings.Contains(evidence, outcome) {
		return true
	}
	for _, suffix := range []string{"胜", "wins", "win"} {
		if !strings.HasSuffix(outcome, suffix) {
			continue
		}
		subject := strings.TrimSpace(strings.TrimSuffix(outcome, suffix))
		if subject == "" || !strings.Contains(evidence, subject) {
			continue
		}
		for _, token := range []string{"胜", "获胜", "赢", "胜出", "wins", "won", "beat", "beats"} {
			if strings.Contains(evidence, normalizePredictionEvidenceText(token)) {
				return true
			}
		}
	}
	return false
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

func predictionResultExtractionPrompt(contract PredictionContract, cleanedText string) string {
	var b strings.Builder
	b.WriteString("Description:\n")
	b.WriteString(contract.Description)
	b.WriteString("\n\nEvidence text:\n")
	b.WriteString(cleanedText)
	b.WriteString("\n\nExtract only the factual event result. ")
	b.WriteString("Return result_type \"outcome\" and a short factual result when the final result is clear. ")
	b.WriteString("For outcome, evidence_quote must copy a verbatim passage from Evidence text that directly proves the final result; participant names or event descriptions alone are insufficient. ")
	b.WriteString("If no such passage exists, return result_type \"pending\" with an empty evidence_quote. ")
	b.WriteString("When the evidence uses a different language, normalize participant names and the factual result into the language used by Description where possible; do not add facts. ")
	b.WriteString("Do not choose an outcome_id in this step. ")
	b.WriteString("Set result to a short factual final result, limited to 128 bytes. ")
	b.WriteString("Use result_type \"pending\" when the event result is not available yet. ")
	b.WriteString("Use result_type \"unverifiable\" when the result cannot be verified. ")
	b.WriteString("Use result_type \"invalid\" when the event or market is invalid.")
	return b.String()
}

func predictionOutcomeMatchPrompt(contract PredictionContract, factualResult, evidenceText string) string {
	var b strings.Builder
	b.WriteString("Description:\n")
	b.WriteString(contract.Description)
	b.WriteString("\nAllowed outcomes:\n")
	for _, outcome := range contract.Outcomes {
		b.WriteString("- ")
		b.WriteString(outcome.ID)
		b.WriteString(": ")
		b.WriteString(outcome.Text)
		b.WriteString("\n")
	}
	b.WriteString("\nFactual result:\n")
	b.WriteString(factualResult)
	if evidenceText = strings.TrimSpace(evidenceText); evidenceText != "" && evidenceText != factualResult {
		b.WriteString("\n\nOriginal evidence excerpt for mapping context:\n")
		b.WriteString(truncateUTF8Bytes(evidenceText, 1200))
	}
	b.WriteString("\n\nChoose exactly one allowed outcome when the result is clear. ")
	b.WriteString("The outcome_id must be one of the exact allowed outcome ids listed above; do not return a score, team name, event title, numeric rank, or explanation as outcome_id. ")
	b.WriteString("For sports scores, compare the two final scores first, then map the winner or draw to the allowed outcome text. ")
	b.WriteString("Return result_type outcome and outcome_id equal to the chosen allowed outcome id. ")
	b.WriteString("Use result_type \"pending\" and empty outcome_id when the event result is not available yet. ")
	b.WriteString("Use result_type \"unverifiable\" and empty outcome_id when the result cannot be verified. ")
	b.WriteString("Use result_type \"invalid\" and empty outcome_id when the event or market is invalid.")
	return b.String()
}

func predictionResolvePrompt(contract PredictionContract, cleanedText string) string {
	return predictionOutcomeMatchPrompt(contract, cleanedText, "")
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
	content = strings.TrimSpace(content)
	if jsonText, ok := firstJSONObject(content); ok {
		return jsonText
	}
	return content
}

func firstJSONObject(content string) (string, bool) {
	start := strings.Index(content, "{")
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(content); i++ {
		ch := content[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(content[start : i+1]), true
			}
		}
	}
	return "", false
}
