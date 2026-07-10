package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

var ErrPredictionEvidenceUnavailable = errors.New("prediction evidence is unavailable")

const DefaultPredictionResultMaxBytes int64 = 1 << 20

const (
	DefaultPredictionAgentRetryAttempts = 2
	DefaultPredictionAgentRetryBackoff  = 2 * time.Second
	DefaultPredictionAgentMaxCandidates = 8
	DefaultPredictionSearchMaxResults   = 8
	DefaultPredictionSearchMaxBytes     = 1 << 20
	DefaultPredictionSearchEndpoint     = "https://www.google.com/search"
	DefaultPredictionSearchFallback     = "https://www.bing.com/search"
)

type PredictionAgentAuditEvent struct {
	Stage          string
	ResultURL      string
	FinalURL       string
	ResultType     string
	OutcomeID      string
	Result         string
	Reason         string
	TextBytes      int
	CleanedBytes   int
	Attempt        int
	CandidateCount int
	Error          string
}

type PredictionAgentAuditFunc func(PredictionAgentAuditEvent)

type PredictionResultTextFetcher interface {
	FetchResultText(ctx context.Context, resultURL string) (string, error)
}

var ErrPredictionStructuredEvidenceUnresolved = errors.New("structured prediction evidence could not be matched to an outcome")

type PredictionResultFetchResult struct {
	FinalURL string
	Text     string
	Links    []string
}

type PredictionResultFetcher interface {
	FetchPredictionResult(ctx context.Context, resultURL string) (PredictionResultFetchResult, error)
}

type HTTPPredictionResultTextFetcher struct {
	Client   *http.Client
	MaxBytes int64
}

type HTTPPredictionResultSearcher struct {
	Client         *http.Client
	Endpoint       string
	MaxBytes       int64
	MaxResults     int
	TrustedSources []TrustedEvidenceSource
}

type PredictionAgent struct {
	Resolver         *PredictionLLMResolver
	Fetcher          PredictionResultFetcher
	Searcher         PredictionResultSearcher
	TrustedSources   []TrustedEvidenceSource
	RetryAttempts    int
	RetryBackoff     time.Duration
	MaxCandidateURLs int
	Audit            PredictionAgentAuditFunc
}

type PredictionResultSearcher interface {
	SearchPredictionResult(ctx context.Context, contract PredictionContract) ([]string, error)
}

type PredictionAgentConfirmRequest struct {
	Contract     PredictionContract
	ResultURL    string
	ObservedAt   int64
	AgentVersion uint32
	ModelVersion string
}

type PredictionAgentReadyReviewRequest struct {
	Contract  PredictionContract
	CheckedAt int64
}

type PredictionAgentReadyReviewResult struct {
	Ready        bool                  `json:"ready"`
	Reject       PredictionRejectParam `json:"reject,omitempty"`
	Reason       string                `json:"reason,omitempty"`
	URLReachable bool                  `json:"urlReachable"`
	SourceURL    string                `json:"sourceUrl,omitempty"`
	FinalURL     string                `json:"finalUrl,omitempty"`
	TextBytes    int                   `json:"textBytes,omitempty"`
	CleanedBytes int                   `json:"cleanedBytes,omitempty"`
}

type TrustedEvidenceSource struct {
	Domain     string `json:"domain"`
	PathPrefix string `json:"pathPrefix,omitempty"`
	Name       string `json:"name,omitempty"`
	Tier       int    `json:"tier,omitempty"`
}

func DefaultTrustedEvidenceSources() []TrustedEvidenceSource {
	return []TrustedEvidenceSource{
		{Name: "Reuters", Domain: "reuters.com", Tier: 1},
		{Name: "Associated Press", Domain: "apnews.com", Tier: 1},
		{Name: "AFP", Domain: "afp.com", Tier: 1},
		{Name: "United Nations", Domain: "un.org", Tier: 1},
		{Name: "World Health Organization", Domain: "who.int", Tier: 1},
		{Name: "World Bank", Domain: "worldbank.org", Tier: 1},
		{Name: "International Monetary Fund", Domain: "imf.org", Tier: 1},
		{Name: "OECD", Domain: "oecd.org", Tier: 1},
		{Name: "U.S. SEC", Domain: "sec.gov", Tier: 1},
		{Name: "U.S. Federal Reserve", Domain: "federalreserve.gov", Tier: 1},
		{Name: "U.S. Treasury", Domain: "treasury.gov", Tier: 1},
		{Name: "U.S. Bureau of Labor Statistics", Domain: "bls.gov", Tier: 1},
		{Name: "U.S. Bureau of Economic Analysis", Domain: "bea.gov", Tier: 1},
		{Name: "NOAA", Domain: "noaa.gov", Tier: 1},
		{Name: "National Weather Service", Domain: "weather.gov", Tier: 1},
		{Name: "U.S. Geological Survey", Domain: "usgs.gov", Tier: 1},
		{Name: "World Meteorological Organization", Domain: "wmo.int", Tier: 1},
		{Name: "U.S. CDC", Domain: "cdc.gov", Tier: 1},
		{Name: "U.S. NIH", Domain: "nih.gov", Tier: 1},
		{Name: "U.S. FDA", Domain: "fda.gov", Tier: 1},
		{Name: "U.S. FEC", Domain: "fec.gov", Tier: 1},
		{Name: "U.S. Congress", Domain: "congress.gov", Tier: 1},
		{Name: "UK Government", Domain: "gov.uk", Tier: 1},
		{Name: "European Union", Domain: "europa.eu", Tier: 1},
		{Name: "ESPN Sports", Domain: "espn.com", Tier: 2},
		{Name: "BBC", Domain: "bbc.com", Tier: 2},
		{Name: "The Guardian", Domain: "theguardian.com", Tier: 2},
		{Name: "Al Jazeera", Domain: "aljazeera.com", Tier: 2},
		{Name: "Nature", Domain: "nature.com", Tier: 2},
		{Name: "Science", Domain: "science.org", Tier: 2},
	}
}

func ParseTrustedEvidenceSources(values []string) ([]TrustedEvidenceSource, error) {
	out := make([]TrustedEvidenceSource, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		source, err := ParseTrustedEvidenceSource(value)
		if err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	return out, nil
}

func ParseTrustedEvidenceSource(value string) (TrustedEvidenceSource, error) {
	original := strings.TrimSpace(value)
	if original == "" {
		return TrustedEvidenceSource{}, fmt.Errorf("trusted evidence source is empty")
	}
	if !strings.Contains(original, "://") {
		original = "https://" + original
	}
	parsed, err := url.Parse(original)
	if err != nil {
		return TrustedEvidenceSource{}, err
	}
	domain := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if domain == "" {
		return TrustedEvidenceSource{}, fmt.Errorf("trusted evidence source domain is empty")
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "" {
		path = strings.TrimRight(parsed.Path, "/")
	}
	return TrustedEvidenceSource{Domain: domain, PathPrefix: path}, nil
}

func NewPredictionAgent(client LLMClient) *PredictionAgent {
	trustedSources := DefaultTrustedEvidenceSources()
	return &PredictionAgent{
		Resolver:         NewPredictionLLMResolver(client),
		Fetcher:          HTTPPredictionResultTextFetcher{},
		Searcher:         HTTPPredictionResultSearcher{TrustedSources: trustedSources},
		TrustedSources:   trustedSources,
		RetryAttempts:    DefaultPredictionAgentRetryAttempts,
		RetryBackoff:     DefaultPredictionAgentRetryBackoff,
		MaxCandidateURLs: DefaultPredictionAgentMaxCandidates,
	}
}

func (a *PredictionAgent) ReviewReady(ctx context.Context, req PredictionAgentReadyReviewRequest) (PredictionRejectParam, bool, error) {
	result, err := a.ReviewReadyResult(ctx, req)
	return result.Reject, result.Ready, err
}

func (a *PredictionAgent) ReviewReadyResult(ctx context.Context, req PredictionAgentReadyReviewRequest) (PredictionAgentReadyReviewResult, error) {
	if a == nil || a.Resolver == nil {
		return PredictionAgentReadyReviewResult{}, fmt.Errorf("missing prediction agent resolver")
	}
	if err := req.Contract.Check(); err != nil {
		return PredictionAgentReadyReviewResult{}, err
	}
	if req.CheckedAt <= 0 {
		return PredictionAgentReadyReviewResult{}, fmt.Errorf("invalid checked_at %d", req.CheckedAt)
	}

	fetcher := a.Fetcher
	if fetcher == nil {
		fetcher = HTTPPredictionResultTextFetcher{}
	}
	fetched, err := a.fetchWithRetry(ctx, fetcher, req.Contract.SourceURL)
	if err != nil {
		reject := PredictionRejectParam{
			Reason:    "source url is not reachable: " + err.Error(),
			CheckedAt: req.CheckedAt,
		}
		return PredictionAgentReadyReviewResult{
			Ready:        false,
			Reject:       reject,
			Reason:       reject.Reason,
			URLReachable: false,
			SourceURL:    req.Contract.SourceURL,
		}, nil
	}
	cleaned := CleanPredictionResultText(fetched.Text)
	if cleaned == "" {
		reject := PredictionRejectParam{
			Reason:    "source url text is empty",
			CheckedAt: req.CheckedAt,
		}
		return PredictionAgentReadyReviewResult{
			Ready:        false,
			Reject:       reject,
			Reason:       reject.Reason,
			URLReachable: true,
			SourceURL:    req.Contract.SourceURL,
			FinalURL:     fetched.FinalURL,
			TextBytes:    len(fetched.Text),
		}, nil
	}
	attempts := a.retryAttempts()
	backoff := a.retryBackoff()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		reject, ready, err := a.Resolver.ReviewContract(ctx, PredictionLLMReviewRequest{
			Contract:   req.Contract,
			CheckedAt:  req.CheckedAt,
			SourceURL:  fetched.FinalURL,
			SourceText: cleaned,
		})
		if err == nil {
			stage := "ready_decision"
			if !ready {
				stage = "ready_reject"
			}
			a.audit(PredictionAgentAuditEvent{
				Stage:        stage,
				Reason:       reject.Reason,
				ResultURL:    req.Contract.SourceURL,
				FinalURL:     fetched.FinalURL,
				Attempt:      attempt,
				TextBytes:    len(fetched.Text),
				CleanedBytes: len(cleaned),
			})
			reason := strings.TrimSpace(reject.Reason)
			return PredictionAgentReadyReviewResult{
				Ready:        ready,
				Reject:       reject,
				Reason:       reason,
				URLReachable: true,
				SourceURL:    req.Contract.SourceURL,
				FinalURL:     fetched.FinalURL,
				TextBytes:    len(fetched.Text),
				CleanedBytes: len(cleaned),
			}, nil
		}
		lastErr = err
		a.audit(PredictionAgentAuditEvent{Stage: "ready_error", Attempt: attempt, Error: err.Error()})
		if attempt < attempts {
			if err := sleepWithContext(ctx, backoff*time.Duration(attempt)); err != nil {
				return PredictionAgentReadyReviewResult{}, err
			}
		}
	}
	return PredictionAgentReadyReviewResult{}, lastErr
}

func (a *PredictionAgent) BuildConfirmParam(ctx context.Context, req PredictionAgentConfirmRequest) (PredictionConfirmParam, error) {
	if a == nil || a.Resolver == nil {
		return PredictionConfirmParam{}, fmt.Errorf("missing prediction agent resolver")
	}
	if err := req.Contract.Check(); err != nil {
		return PredictionConfirmParam{}, err
	}
	if !ResultURLAllowed(req.Contract.SourceURL, req.ResultURL) {
		return PredictionConfirmParam{}, fmt.Errorf("result url is outside source site")
	}
	fetcher := a.Fetcher
	if fetcher == nil {
		fetcher = HTTPPredictionResultTextFetcher{}
	}
	fetched, err := a.fetchWithRetry(ctx, fetcher, req.ResultURL)
	if err != nil {
		return PredictionConfirmParam{}, err
	}
	param, err := a.resolveFetchedResult(ctx, req, fetched, req.ResultURL)
	if err == nil {
		return param, nil
	}
	if errors.Is(err, ErrPredictionStructuredEvidenceUnresolved) {
		return PredictionConfirmParam{}, err
	}
	candidateURLs := a.candidateResultURLs(req.Contract, fetched.Links)
	a.audit(PredictionAgentAuditEvent{Stage: "candidate_urls", CandidateCount: len(candidateURLs)})
	lastErr := err
	searched := false
	if len(candidateURLs) == 0 {
		candidateURLs = a.appendSearchResultURLs(ctx, req.Contract, candidateURLs)
		searched = true
	}
	maxCandidateFetches := a.maxCandidateURLs()
	if a.Searcher != nil {
		maxCandidateFetches += a.maxCandidateURLs()
	}
	for i, fetchedCandidates := 0, 0; i < len(candidateURLs) && fetchedCandidates < maxCandidateFetches; i++ {
		candidateURL := candidateURLs[i]
		next, fetchErr := a.fetchWithRetry(ctx, fetcher, candidateURL)
		fetchedCandidates++
		if fetchErr != nil {
			lastErr = fetchErr
			continue
		}
		param, resolveErr := a.resolveFetchedResult(ctx, req, next, candidateURL)
		if resolveErr == nil {
			return param, nil
		}
		if errors.Is(resolveErr, ErrPredictionStructuredEvidenceUnresolved) {
			return PredictionConfirmParam{}, resolveErr
		}
		lastErr = resolveErr
		candidateURLs = appendCandidateResultURLs(candidateURLs, next.Links, req.Contract.SourceURL, a.trustedSources(), a.maxCandidateURLs())
		if i+1 >= len(candidateURLs) && !searched {
			candidateURLs = a.appendSearchResultURLs(ctx, req.Contract, candidateURLs)
			searched = true
		}
	}
	return PredictionConfirmParam{}, lastErr
}

func (a *PredictionAgent) fetchWithRetry(ctx context.Context, fetcher PredictionResultFetcher, resultURL string) (PredictionResultFetchResult, error) {
	attempts := a.retryAttempts()
	backoff := a.retryBackoff()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		fetched, err := fetcher.FetchPredictionResult(ctx, resultURL)
		if err == nil {
			a.audit(PredictionAgentAuditEvent{Stage: "fetch_ok", ResultURL: resultURL, FinalURL: fetched.FinalURL, TextBytes: len(fetched.Text), Attempt: attempt})
			return fetched, nil
		}
		lastErr = err
		a.audit(PredictionAgentAuditEvent{Stage: "fetch_error", ResultURL: resultURL, Attempt: attempt, Error: err.Error()})
		if attempt < attempts {
			if err := sleepWithContext(ctx, backoff*time.Duration(attempt)); err != nil {
				return PredictionResultFetchResult{}, err
			}
		}
	}
	return PredictionResultFetchResult{}, lastErr
}

func (a *PredictionAgent) resolveFetchedResult(ctx context.Context, req PredictionAgentConfirmRequest,
	fetched PredictionResultFetchResult, requestedURL string) (PredictionConfirmParam, error) {

	resultURL := req.ResultURL
	if requestedURL != "" {
		resultURL = requestedURL
	}
	if fetched.FinalURL != "" {
		resultURL = fetched.FinalURL
	}
	trustedSources := a.trustedSources()
	if !EvidenceURLAllowed(req.Contract.SourceURL, resultURL, trustedSources) {
		return PredictionConfirmParam{}, fmt.Errorf("final result url is outside source site")
	}
	paramResultURL := resultURL
	if !ResultURLAllowed(req.Contract.SourceURL, paramResultURL) {
		paramResultURL = req.Contract.SourceURL
	}
	resultText := fetched.Text
	var structuredScore predictionStructuredScore
	hasStructuredScore := false
	if structuredText, score, ok := buildStructuredScoreResultText(req.Contract, fetched.Text); ok {
		resultText = structuredText
		structuredScore = score
		hasStructuredScore = true
		a.audit(PredictionAgentAuditEvent{
			Stage:        "structured_evidence",
			ResultURL:    resultURL,
			Result:       structuredText,
			TextBytes:    len(fetched.Text),
			CleanedBytes: len(CleanPredictionResultText(structuredText)),
		})
	}
	if !hasStructuredScore && !predictionEvidenceLooksRelevant(req.Contract, fetched.Text) {
		a.audit(PredictionAgentAuditEvent{
			Stage:        "evidence_irrelevant",
			ResultURL:    resultURL,
			TextBytes:    len(fetched.Text),
			CleanedBytes: len(CleanPredictionResultText(fetched.Text)),
		})
		return PredictionConfirmParam{}, ErrPredictionEvidenceUnavailable
	}
	resolveReq := PredictionLLMResolveRequest{
		Contract:   req.Contract,
		SourceURL:  req.Contract.SourceURL,
		ResultURL:  paramResultURL,
		ResultText: resultText,
		ObservedAt: req.ObservedAt,
	}
	cleanedBytes := len(CleanPredictionResultText(resultText))
	param, decision, err := a.resolveWithRetry(ctx, resolveReq, resultURL, len(resultText), cleanedBytes)
	if err != nil {
		if resultText != fetched.Text {
			return PredictionConfirmParam{}, fmt.Errorf("%w: %v", ErrPredictionStructuredEvidenceUnresolved, err)
		}
		return PredictionConfirmParam{}, err
	}
	if hasStructuredScore {
		param, decision, err = a.ensureStructuredOutcomeConsistent(ctx, resolveReq, structuredScore, param, decision, resultURL, len(resultText), cleanedBytes)
		if err != nil {
			return PredictionConfirmParam{}, err
		}
	}
	param.AgentVersion = req.AgentVersion
	param.ModelVersion = req.ModelVersion
	a.audit(PredictionAgentAuditEvent{Stage: "llm_decision", ResultURL: resultURL, ResultType: param.ResultType, OutcomeID: param.OutcomeID, Result: param.Result, Reason: decision.Reason, TextBytes: len(fetched.Text), CleanedBytes: cleanedBytes})
	return param, nil
}

func (a *PredictionAgent) ensureStructuredOutcomeConsistent(ctx context.Context, req PredictionLLMResolveRequest, score predictionStructuredScore,
	param PredictionConfirmParam, decision predictionLLMDecision, resultURL string, textBytes, cleanedBytes int) (PredictionConfirmParam, predictionLLMDecision, error) {

	if structuredScoreOutcomeConsistent(req.Contract, score, param.OutcomeID) {
		return param, decision, nil
	}
	attempts := a.retryAttempts()
	if attempts < 3 {
		attempts = 3
	}
	lastParam := param
	lastDecision := decision
	lastOutcome := param.OutcomeID
	for attempt := 2; attempt <= attempts; attempt++ {
		retryReq := req
		retryReq.ResultText = fmt.Sprintf("%s\n\n%s",
			req.ResultText, structuredScoreRetryGuidance(req.Contract, score, lastOutcome))
		nextParam, nextDecision, err := a.Resolver.ResolveDecision(ctx, retryReq)
		if err != nil {
			a.audit(PredictionAgentAuditEvent{
				Stage:        "llm_error",
				ResultURL:    resultURL,
				ResultType:   nextDecision.ResultType,
				OutcomeID:    nextDecision.OutcomeID,
				Reason:       nextDecision.Reason,
				TextBytes:    textBytes,
				CleanedBytes: cleanedBytes,
				Attempt:      attempt,
				Error:        err.Error(),
			})
			lastDecision = nextDecision
			continue
		}
		lastParam = nextParam
		lastDecision = nextDecision
		lastOutcome = nextParam.OutcomeID
		if structuredScoreOutcomeConsistent(req.Contract, score, nextParam.OutcomeID) {
			return nextParam, nextDecision, nil
		}
		a.audit(PredictionAgentAuditEvent{
			Stage:        "llm_inconsistent",
			ResultURL:    resultURL,
			ResultType:   nextParam.ResultType,
			OutcomeID:    nextParam.OutcomeID,
			Reason:       nextDecision.Reason,
			TextBytes:    textBytes,
			CleanedBytes: cleanedBytes,
			Attempt:      attempt,
			Error:        "structured score does not match outcome",
		})
	}
	return PredictionConfirmParam{}, lastDecision, fmt.Errorf("%w: structured score %s does not match outcome %s",
		ErrPredictionStructuredEvidenceUnresolved, req.ResultText, lastParam.OutcomeID)
}

func structuredScoreRetryGuidance(contract PredictionContract, score predictionStructuredScore, lastOutcome string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "上一轮返回的 outcome_id=%q 与结构化最终比分矛盾。\n", lastOutcome)
	fmt.Fprintf(&b, "结构化最终比分事实：%s %s-%s %s。%s。\n",
		score.HomeName, score.HomeScore, score.GuestScore, score.GuestName, structuredScoreFact(score))
	b.WriteString("请只根据这个比分事实重新匹配 allowed outcomes。逐项事实如下：\n")
	for _, outcome := range contract.Outcomes {
		fmt.Fprintf(&b, "- outcome_id=%s, text=%s: %s\n",
			outcome.ID, outcome.Text, structuredScoreOutcomeGuidance(score, outcome.Text))
	}
	b.WriteString("必须返回紧凑 JSON：result_type=\"outcome\"，outcome_id 必须是上面 allowed outcomes 中与比分事实匹配的那个 id。")
	return b.String()
}

func (a *PredictionAgent) resolveWithRetry(ctx context.Context, req PredictionLLMResolveRequest,
	resultURL string, textBytes, cleanedBytes int) (PredictionConfirmParam, predictionLLMDecision, error) {

	attempts := a.retryAttempts()
	backoff := a.retryBackoff()
	var lastErr error
	var lastDecision predictionLLMDecision
	for attempt := 1; attempt <= attempts; attempt++ {
		param, decision, err := a.Resolver.ResolveDecision(ctx, req)
		if err == nil {
			return param, decision, nil
		}
		lastErr = err
		lastDecision = decision
		stage := "llm_error"
		if errors.Is(err, ErrPredictionResultPending) {
			stage = "llm_pending"
		}
		a.audit(PredictionAgentAuditEvent{
			Stage:        stage,
			ResultURL:    resultURL,
			ResultType:   decision.ResultType,
			OutcomeID:    decision.OutcomeID,
			Reason:       decision.Reason,
			TextBytes:    textBytes,
			CleanedBytes: cleanedBytes,
			Attempt:      attempt,
			Error:        err.Error(),
		})
		if errors.Is(err, ErrPredictionResultPending) {
			return PredictionConfirmParam{}, decision, err
		}
		if attempt < attempts {
			if err := sleepWithContext(ctx, backoff*time.Duration(attempt)); err != nil {
				return PredictionConfirmParam{}, decision, err
			}
		}
	}
	return PredictionConfirmParam{}, lastDecision, lastErr
}

type predictionStructuredScore struct {
	HomeName   string
	GuestName  string
	HomeScore  string
	GuestScore string
	Done       bool
}

func buildStructuredScoreResultText(contract PredictionContract, text string) (string, predictionStructuredScore, bool) {
	score, ok := extractPredictionStructuredScore(contract, text)
	if !ok {
		return "", predictionStructuredScore{}, false
	}
	result := fmt.Sprintf("Verified final score facts:\n- %s %s-%s %s\n- %s scored %s\n- %s scored %s\n- %s\nUse only these final score facts to choose the matching allowed outcome_id.",
		score.HomeName, score.HomeScore, score.GuestScore, score.GuestName,
		score.HomeName, score.HomeScore,
		score.GuestName, score.GuestScore,
		structuredScoreFact(score))
	return result, score, true
}

func extractPredictionStructuredScore(contract PredictionContract, text string) (predictionStructuredScore, bool) {
	var data interface{}
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return predictionStructuredScore{}, false
	}
	contractText := normalizePredictionEvidenceText(contract.Title + " " + contract.Description + " " + predictionOutcomeText(contract))
	return walkPredictionStructuredScore(data, contractText, contract.EventTime)
}

func walkPredictionStructuredScore(value interface{}, contractText string, eventTime int64) (predictionStructuredScore, bool) {
	switch typed := value.(type) {
	case []interface{}:
		for _, item := range typed {
			if score, ok := walkPredictionStructuredScore(item, contractText, eventTime); ok {
				return score, true
			}
		}
	case map[string]interface{}:
		if score, ok := predictionStructuredScoreFromMap(typed, contractText, eventTime); ok {
			return score, true
		}
		for _, item := range typed {
			if score, ok := walkPredictionStructuredScore(item, contractText, eventTime); ok {
				return score, true
			}
		}
	}
	return predictionStructuredScore{}, false
}

func predictionStructuredScoreFromMap(item map[string]interface{}, contractText string, eventTime int64) (predictionStructuredScore, bool) {
	homeName := predictionStructuredString(item, "homeName", "home_name", "hostName", "teamA", "home")
	guestName := predictionStructuredString(item, "guestName", "guest_name", "awayName", "teamB", "guest", "away")
	homeScore, homeOK := predictionStructuredScoreValue(item, "homeScore", "home_score", "hostScore", "scoreA")
	guestScore, guestOK := predictionStructuredScoreValue(item, "guestScore", "guest_score", "awayScore", "scoreB")
	if homeName == "" || guestName == "" || !homeOK || !guestOK {
		return predictionStructuredScore{}, false
	}
	if !predictionStructuredMatchDone(item) {
		return predictionStructuredScore{}, false
	}
	homeNorm := normalizePredictionEvidenceText(homeName)
	guestNorm := normalizePredictionEvidenceText(guestName)
	nameMatched := homeNorm != "" && guestNorm != "" &&
		strings.Contains(contractText, homeNorm) && strings.Contains(contractText, guestNorm)
	if !nameMatched && !predictionStructuredEventTimeMatches(item, eventTime) {
		return predictionStructuredScore{}, false
	}
	return predictionStructuredScore{
		HomeName:   homeName,
		GuestName:  guestName,
		HomeScore:  homeScore,
		GuestScore: guestScore,
		Done:       true,
	}, true
}

// A source can use a different language for participant names.  For a completed
// structured record, the contract event time is an independent, language-neutral
// way to select the exact event before the LLM normalizes the factual result.
func predictionStructuredEventTimeMatches(item map[string]interface{}, eventTime int64) bool {
	if eventTime <= 0 {
		return false
	}
	for _, key := range []string{"startTime", "start_time", "eventTime", "event_time", "beginTime", "begin_time"} {
		value, ok := item[key]
		if !ok {
			continue
		}
		candidate, ok := predictionStructuredUnixTime(value)
		if !ok {
			continue
		}
		if delta := candidate - eventTime; delta >= -5*60 && delta <= 5*60 {
			return true
		}
	}
	return false
}

func predictionStructuredUnixTime(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		unix, err := typed.Int64()
		return unix, err == nil
	case float64:
		return int64(typed), true
	case string:
		value := strings.TrimSpace(typed)
		if unix, err := strconv.ParseInt(value, 10, 64); err == nil {
			return unix, true
		}
		for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
			parsed, err := time.ParseInLocation(layout, value, time.Local)
			if err == nil {
				return parsed.Unix(), true
			}
		}
	}
	return 0, false
}

func predictionStructuredString(item map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		value, ok := item[key]
		if !ok {
			continue
		}
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func predictionStructuredScoreValue(item map[string]interface{}, keys ...string) (string, bool) {
	for _, key := range keys {
		value, ok := item[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case json.Number:
			return typed.String(), true
		case string:
			typed = strings.TrimSpace(typed)
			return typed, typed != ""
		case float64:
			return fmt.Sprintf("%.0f", typed), true
		}
	}
	return "", false
}

func predictionStructuredMatchDone(item map[string]interface{}) bool {
	status := normalizePredictionEvidenceText(predictionStructuredString(item,
		"statusDesc", "status", "gameStatus", "matchStatus", "state", "period"))
	if status == "" {
		return true
	}
	for _, token := range []string{"已结束", "完场", "结束", "final", "fulltime", "ft", "ended", "complete", "completed"} {
		if strings.Contains(status, normalizePredictionEvidenceText(token)) {
			return true
		}
	}
	for _, token := range []string{"未开始", "未赛", "待赛", "赛前", "upcoming", "scheduled", "pending"} {
		if strings.Contains(status, normalizePredictionEvidenceText(token)) {
			return false
		}
	}
	return true
}

func predictionOutcomeText(contract PredictionContract) string {
	parts := make([]string, 0, len(contract.Outcomes))
	for _, outcome := range contract.Outcomes {
		parts = append(parts, outcome.Text)
	}
	return strings.Join(parts, " ")
}

func structuredScoreFact(score predictionStructuredScore) string {
	homeCmp, homeOK := parseStructuredScoreInt(score.HomeScore)
	guestCmp, guestOK := parseStructuredScoreInt(score.GuestScore)
	if !homeOK || !guestOK {
		return "最终比分已公布"
	}
	if homeCmp == guestCmp {
		return "双方战平"
	}
	if homeCmp > guestCmp {
		return score.HomeName + "获胜"
	}
	return score.GuestName + "获胜"
}

func structuredScoreOutcomeGuidance(score predictionStructuredScore, outcomeText string) string {
	homeCmp, homeOK := parseStructuredScoreInt(score.HomeScore)
	guestCmp, guestOK := parseStructuredScoreInt(score.GuestScore)
	if !homeOK || !guestOK {
		return "比分已公布，但比分格式无法做数值比较，请结合选项文本判断"
	}
	if homeCmp == guestCmp {
		if predictionOutcomeTextIndicatesDraw(outcomeText) {
			return "该选项表示平局，且比分相等"
		}
		return "该选项不是平局，但比分相等"
	}
	winner := score.HomeName
	loser := score.GuestName
	if guestCmp > homeCmp {
		winner = score.GuestName
		loser = score.HomeName
	}
	text := normalizePredictionEvidenceText(outcomeText)
	winnerText := normalizePredictionEvidenceText(winner)
	loserText := normalizePredictionEvidenceText(loser)
	if predictionOutcomeTextIndicatesDraw(outcomeText) {
		return "该选项表示平局，但比分不是平局"
	}
	if loserText != "" && strings.Contains(text, loserText) && predictionOutcomeTextHasWinWord(outcomeText) {
		return loser + "没有获胜，该选项不匹配"
	}
	if winnerText != "" && strings.Contains(text, winnerText) && predictionOutcomeTextHasWinWord(outcomeText) {
		return winner + "获胜，该选项匹配"
	}
	if winnerText != "" && strings.Contains(text, winnerText) && (loserText == "" || !strings.Contains(text, loserText)) {
		return winner + "获胜，该选项匹配"
	}
	return "该选项没有明确表达胜者或平局，需要结合比分事实判断"
}

func predictionOutcomeTextHasWinWord(outcomeText string) bool {
	text := normalizePredictionEvidenceText(outcomeText)
	for _, token := range []string{"win", "wins", "won", "beat", "beats", "胜", "获胜", "赢", "胜出"} {
		if strings.Contains(text, normalizePredictionEvidenceText(token)) {
			return true
		}
	}
	return false
}

func structuredScoreOutcomeConsistent(contract PredictionContract, score predictionStructuredScore, outcomeID string) bool {
	outcomeText := ""
	for _, outcome := range contract.Outcomes {
		if strings.EqualFold(strings.TrimSpace(outcome.ID), strings.TrimSpace(outcomeID)) {
			outcomeText = outcome.Text
			break
		}
	}
	if strings.TrimSpace(outcomeText) == "" {
		return false
	}
	homeCmp, homeOK := parseStructuredScoreInt(score.HomeScore)
	guestCmp, guestOK := parseStructuredScoreInt(score.GuestScore)
	if !homeOK || !guestOK {
		return true
	}
	if homeCmp == guestCmp {
		if _, hasDraw := predictionDrawOutcomeID(contract); hasDraw {
			return predictionOutcomeTextIndicatesDraw(outcomeText)
		}
		return true
	}
	if predictionOutcomeTextIndicatesDraw(outcomeText) {
		return false
	}
	winner := score.HomeName
	loser := score.GuestName
	if guestCmp > homeCmp {
		winner = score.GuestName
		loser = score.HomeName
	}
	return structuredScoreOutcomeTextAllowsWinner(outcomeText, winner, loser)
}

func parseStructuredScoreInt(raw string) (int, bool) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	return value, err == nil
}

func structuredScoreOutcomeTextAllowsWinner(outcomeText, winner, loser string) bool {
	text := normalizePredictionEvidenceText(outcomeText)
	winner = normalizePredictionEvidenceText(winner)
	loser = normalizePredictionEvidenceText(loser)
	hasWinner := winner != "" && strings.Contains(text, winner)
	hasLoser := loser != "" && strings.Contains(text, loser)
	hasWinWord := false
	for _, token := range []string{"win", "wins", "won", "beat", "beats", "胜", "获胜", "赢", "胜出"} {
		if strings.Contains(text, normalizePredictionEvidenceText(token)) {
			hasWinWord = true
			break
		}
	}
	if hasWinner && !hasLoser {
		return true
	}
	if hasWinner && hasWinWord {
		return true
	}
	if hasLoser && hasWinWord {
		return false
	}
	return true
}

func (a *PredictionAgent) candidateResultURLs(contract PredictionContract, links []string) []string {
	limit := a.maxCandidateURLs()
	candidates := appendCandidateResultURLs(nil, predictionKnownDataURLs(contract.SourceURL), contract.SourceURL, a.trustedSources(), limit)
	return appendCandidateResultURLs(candidates, links, contract.SourceURL, a.trustedSources(), limit)
}

func (a *PredictionAgent) appendSearchResultURLs(ctx context.Context, contract PredictionContract, candidates []string) []string {
	limit := len(candidates) + a.maxCandidateURLs()
	for _, candidateURL := range a.searchResultURLs(ctx, contract) {
		candidates = appendCandidateResultURLs(candidates, []string{candidateURL}, contract.SourceURL, a.trustedSources(), limit)
	}
	return candidates
}

func (a *PredictionAgent) searchResultURLs(ctx context.Context, contract PredictionContract) []string {
	if a == nil || a.Searcher == nil {
		return nil
	}
	urls, err := a.Searcher.SearchPredictionResult(ctx, contract)
	if err != nil {
		a.audit(PredictionAgentAuditEvent{Stage: "search_error", Error: err.Error()})
		return nil
	}
	a.audit(PredictionAgentAuditEvent{Stage: "search_results", CandidateCount: len(urls)})
	return urls
}

func (a *PredictionAgent) retryAttempts() int {
	if a == nil || a.RetryAttempts <= 0 {
		return DefaultPredictionAgentRetryAttempts
	}
	return a.RetryAttempts
}

func (a *PredictionAgent) retryBackoff() time.Duration {
	if a == nil || a.RetryBackoff <= 0 {
		return DefaultPredictionAgentRetryBackoff
	}
	return a.RetryBackoff
}

func (a *PredictionAgent) maxCandidateURLs() int {
	if a == nil || a.MaxCandidateURLs <= 0 {
		return DefaultPredictionAgentMaxCandidates
	}
	return a.MaxCandidateURLs
}

func (a *PredictionAgent) trustedSources() []TrustedEvidenceSource {
	if a == nil || len(a.TrustedSources) == 0 {
		return DefaultTrustedEvidenceSources()
	}
	return a.TrustedSources
}

func (a *PredictionAgent) audit(event PredictionAgentAuditEvent) {
	if a != nil && a.Audit != nil {
		a.Audit(event)
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f HTTPPredictionResultTextFetcher) FetchResultText(ctx context.Context, resultURL string) (string, error) {
	fetched, err := f.FetchPredictionResult(ctx, resultURL)
	if err != nil {
		return "", err
	}
	return fetched.Text, nil
}

func (f HTTPPredictionResultTextFetcher) FetchPredictionResult(ctx context.Context, resultURL string) (PredictionResultFetchResult, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	maxBytes := f.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultPredictionResultMaxBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resultURL, nil)
	if err != nil {
		return PredictionResultFetchResult{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return PredictionResultFetchResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return PredictionResultFetchResult{}, fmt.Errorf("result url status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return PredictionResultFetchResult{}, err
	}
	if int64(len(raw)) > maxBytes {
		return PredictionResultFetchResult{}, fmt.Errorf("result url text exceeds max bytes %d", maxBytes)
	}
	rawText := string(raw)
	text := ExtractPredictionResultText(rawText)
	links := ExtractPredictionResultLinks(rawText, resp.Request.URL)
	if text == "" && len(links) == 0 {
		return PredictionResultFetchResult{}, fmt.Errorf("result url text is empty")
	}
	return PredictionResultFetchResult{
		FinalURL: resp.Request.URL.String(),
		Text:     text,
		Links:    links,
	}, nil
}

func (s HTTPPredictionResultSearcher) SearchPredictionResult(ctx context.Context, contract PredictionContract) ([]string, error) {
	if err := contract.Check(); err != nil {
		return nil, err
	}
	source, err := parseHTTPURL(contract.SourceURL)
	if err != nil {
		return nil, err
	}
	searchDomain, err := siteSearchDomain(source.Hostname())
	if err != nil {
		return nil, err
	}
	searchDomains := predictionSearchDomains(searchDomain, s.trustedSources())
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	maxBytes := s.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultPredictionSearchMaxBytes
	}

	endpoints := s.endpoints()
	if len(endpoints) == 1 {
		urls, _, err := s.searchPredictionResultEndpoint(ctx, client, maxBytes, searchDomains, contract, endpoints[0])
		return urls, err
	}

	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type endpointResult struct {
		urls     []string
		searchOK bool
		err      error
	}
	results := make(chan endpointResult, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint := endpoint
		go func() {
			urls, searchOK, err := s.searchPredictionResultEndpoint(searchCtx, client, maxBytes, searchDomains, contract, endpoint)
			results <- endpointResult{urls: urls, searchOK: searchOK, err: err}
		}()
	}

	var lastErr error
	searchOK := false
	for range endpoints {
		result := <-results
		if len(result.urls) > 0 {
			cancel()
			return result.urls, nil
		}
		if result.searchOK {
			searchOK = true
		}
		if result.err != nil {
			lastErr = result.err
		}
	}
	if searchOK {
		return nil, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func (s HTTPPredictionResultSearcher) searchPredictionResultEndpoint(ctx context.Context,
	client *http.Client, maxBytes int64, searchDomains []string, contract PredictionContract,
	endpoint string) ([]string, bool, error) {

	var lastErr error
	searchOK := false
	for _, searchQuery := range buildPredictionSearchQueries(searchDomains, contract) {
		searchURL, err := url.Parse(endpoint)
		if err != nil {
			lastErr = err
			continue
		}
		query := searchURL.Query()
		query.Set("q", searchQuery)
		if query.Get("num") == "" {
			query.Set("num", fmt.Sprintf("%d", s.maxResults()))
		}
		if query.Get("count") == "" {
			query.Set("count", fmt.Sprintf("%d", s.maxResults()))
		}
		if query.Get("hl") == "" {
			query.Set("hl", "zh-CN")
		}
		searchURL.RawQuery = query.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SatoshiNetPredictionAgent/1.0)")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		raw, readErr := readLimitedAndClose(resp, maxBytes)
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("prediction search status %d", resp.StatusCode)
			continue
		}
		searchOK = true
		urls := extractPredictionSearchURLs(string(raw), contract.SourceURL, s.trustedSources(), s.maxResults())
		if len(urls) > 0 {
			return urls, true, nil
		}
	}
	return nil, searchOK, lastErr
}

func (s HTTPPredictionResultSearcher) maxResults() int {
	if s.MaxResults > 0 {
		return s.MaxResults
	}
	return DefaultPredictionSearchMaxResults
}

func (s HTTPPredictionResultSearcher) trustedSources() []TrustedEvidenceSource {
	if len(s.TrustedSources) == 0 {
		return DefaultTrustedEvidenceSources()
	}
	return s.TrustedSources
}

func (s HTTPPredictionResultSearcher) endpoints() []string {
	if endpoint := strings.TrimSpace(s.Endpoint); endpoint != "" {
		return []string{endpoint}
	}
	return []string{DefaultPredictionSearchEndpoint, DefaultPredictionSearchFallback}
}

func appendCandidateResultURLs(candidates []string, urls []string, sourceURL string,
	trustedSources []TrustedEvidenceSource, limit int) []string {

	if limit <= 0 {
		limit = DefaultPredictionAgentMaxCandidates
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		seen[candidate] = struct{}{}
	}
	for _, candidateURL := range urls {
		if !EvidenceURLAllowed(sourceURL, candidateURL, trustedSources) {
			continue
		}
		if _, ok := seen[candidateURL]; ok {
			continue
		}
		seen[candidateURL] = struct{}{}
		if !predictionCandidateHighPriority(candidateURL) {
			if len(candidates) >= limit {
				continue
			}
			candidates = append(candidates, candidateURL)
			continue
		}
		insertAt := len(candidates)
		for i, existing := range candidates {
			if !predictionCandidateHighPriority(existing) {
				insertAt = i
				break
			}
		}
		candidates = append(candidates, "")
		copy(candidates[insertAt+1:], candidates[insertAt:])
		candidates[insertAt] = candidateURL
		if len(candidates) > limit {
			delete(seen, candidates[len(candidates)-1])
			candidates = candidates[:limit]
		}
	}
	return candidates
}

func predictionCandidateHighPriority(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	query := strings.ToLower(parsed.RawQuery)
	if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".json") {
		return true
	}
	for _, token := range []string{"season_game_list", "game_status_list", "/api/", "/game/", "/match/",
		"/score", "/result", "/schedule", "/fixture"} {
		if strings.Contains(path, token) || strings.Contains(query, token) {
			return true
		}
	}
	return false
}

func predictionKnownDataURLs(sourceURL string) []string {
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.ToLower(parsed.Path)
	if strings.Contains(host, "cctv.com") && strings.Contains(path, "/2026/") {
		return []string{"https://cbs-u.sports.cctv.com/pc/game/season_game_list?leagueId=3400&season=2026&client=pc"}
	}
	return nil
}

func EvidenceURLAllowed(sourceURL, resultURL string, trustedSources []TrustedEvidenceSource) bool {
	if ResultURLAllowed(sourceURL, resultURL) {
		return true
	}
	return TrustedEvidenceURLAllowed(resultURL, trustedSources)
}

func TrustedEvidenceURLAllowed(resultURL string, trustedSources []TrustedEvidenceSource) bool {
	result, err := parseHTTPURL(resultURL)
	if err != nil || result.Scheme != "https" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(result.Hostname()), ".")
	for _, source := range trustedSources {
		domain := strings.TrimSuffix(strings.ToLower(source.Domain), ".")
		if domain == "" || !hostInDomainScope(host, domain) {
			continue
		}
		pathPrefix := strings.TrimRight(source.PathPrefix, "/")
		if pathPrefix == "" || strings.HasPrefix(result.EscapedPath(), pathPrefix) ||
			strings.HasPrefix(result.Path, pathPrefix) {
			return true
		}
	}
	return false
}

func hostInDomainScope(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func predictionSearchDomains(sourceDomain string, trustedSources []TrustedEvidenceSource) []string {
	out := make([]string, 0, len(trustedSources)+1)
	seen := make(map[string]struct{})
	appendDomain := func(domain string) {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain == "" {
			return
		}
		if _, ok := seen[domain]; ok {
			return
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	appendDomain(sourceDomain)
	for _, source := range trustedSources {
		appendDomain(source.Domain)
	}
	return out
}

func readLimitedAndClose(resp *http.Response, maxBytes int64) ([]byte, error) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("prediction search text exceeds max bytes %d", maxBytes)
	}
	return raw, nil
}

var (
	htmlScriptPattern     = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlJSONScriptPattern = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	htmlStylePattern      = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlTagPattern        = regexp.MustCompile(`(?is)<[^>]+>`)
	htmlAnchorPattern     = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
	htmlScriptSrcPattern  = regexp.MustCompile(`(?is)<script\b([^>]*)>`)
	htmlFramePattern      = regexp.MustCompile(`(?is)<(iframe|frame)\b([^>]*)>`)
	htmlAttrPattern       = regexp.MustCompile(`(?is)\b(href|src)=["']([^"']+)["']`)
	searchHrefPattern     = regexp.MustCompile(`(?is)href=["']([^"']+)["']`)
	rawHTTPURLPattern     = regexp.MustCompile(`https?://[^\s"'<>\\]+`)
	rawSchemeURLPattern   = regexp.MustCompile(`(?m)(^|[^:])//[A-Za-z0-9.-]+/[^\s"'<>\\]+`)
	rawPathURLPattern     = regexp.MustCompile(`["'](/[^"']*(?:api|game|match|score|result|schedule|fixture|season_game)[^"']*)["']`)
)

func ExtractPredictionResultText(raw string) string {
	dataText := ExtractPredictionEmbeddedDataText(raw)
	raw = htmlScriptPattern.ReplaceAllString(raw, " ")
	raw = htmlStylePattern.ReplaceAllString(raw, " ")
	raw = htmlTagPattern.ReplaceAllString(raw, " ")
	raw = html.UnescapeString(raw)
	visibleText := strings.Join(strings.Fields(raw), " ")
	if dataText == "" {
		return visibleText
	}
	if visibleText == "" {
		return dataText
	}
	return visibleText + "\nEmbedded data:\n" + dataText
}

func ExtractPredictionEmbeddedDataText(raw string) string {
	matches := htmlJSONScriptPattern.FindAllStringSubmatch(raw, -1)
	parts := make([]string, 0)
	for _, match := range matches {
		if len(match) < 3 || !predictionScriptContainsData(match[1]) {
			continue
		}
		text := html.UnescapeString(strings.TrimSpace(match[2]))
		if text == "" {
			continue
		}
		parts = append(parts, strings.Join(strings.Fields(text), " "))
	}
	return truncateUTF8Bytes(strings.Join(parts, "\n"), 16000)
}

func ExtractPredictionResultLinks(raw string, base *url.URL) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	appendURL := func(rawURL string) {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return
		}
		if base != nil {
			parsed = base.ResolveReference(parsed)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return
		}
		normalized := parsed.String()
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	for _, match := range htmlFramePattern.FindAllStringSubmatch(raw, -1) {
		if len(match) < 3 {
			continue
		}
		if rawURL := predictionURLAttr(match[2], "src"); rawURL != "" {
			appendURL(rawURL)
		}
	}
	for _, match := range htmlScriptSrcPattern.FindAllStringSubmatch(raw, -1) {
		if len(match) < 2 {
			continue
		}
		rawURL := predictionURLAttr(match[1], "src")
		if rawURL == "" || !predictionScriptURLLooksRelevant(rawURL) {
			continue
		}
		appendURL(rawURL)
	}
	for _, match := range htmlAnchorPattern.FindAllStringSubmatch(raw, -1) {
		if len(match) < 3 {
			continue
		}
		rawURL := predictionURLAttr(match[1], "href")
		text := strings.ToLower(ExtractPredictionResultText(match[2]))
		if rawURL == "" || !predictionResultURLLooksRelevant(rawURL, text) {
			continue
		}
		appendURL(rawURL)
	}
	for _, rawURL := range rawHTTPURLPattern.FindAllString(raw, -1) {
		rawURL = html.UnescapeString(strings.TrimRight(rawURL, ".,);"))
		if !predictionResultURLLooksRelevant(rawURL, "") {
			continue
		}
		appendURL(rawURL)
	}
	for _, rawURL := range extractPredictionDataURLsFromScript(raw, base) {
		appendURL(rawURL)
	}
	return out
}

func predictionScriptContainsData(attrs string) bool {
	attrs = strings.ToLower(attrs)
	return strings.Contains(attrs, "application/json") ||
		strings.Contains(attrs, "application/ld+json") ||
		strings.Contains(attrs, "__next_data__") ||
		strings.Contains(attrs, "__nuxt_data__")
}

func predictionURLAttr(attrs, name string) string {
	name = strings.ToLower(name)
	for _, match := range htmlAttrPattern.FindAllStringSubmatch(attrs, -1) {
		if len(match) >= 3 && strings.ToLower(match[1]) == name {
			return html.UnescapeString(strings.TrimSpace(match[2]))
		}
	}
	return ""
}

func predictionResultURLLooksRelevant(rawURL, text string) bool {
	if predictionURLLooksStaticAsset(rawURL) {
		parsed, err := url.Parse(rawURL)
		if err != nil || !strings.HasSuffix(strings.ToLower(parsed.Path), ".js") ||
			!predictionScriptURLLooksRelevant(rawURL) {
			return false
		}
	}
	value := strings.ToLower(rawURL + " " + text)
	for _, token := range []string{"/match/", "/game/", "result", "final", "boxscore", "recap",
		"score", "schedule", "fixture", "season_game", "赛程", "赛果", "比分", "战报", "集锦"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	for _, token := range []string{"match", "game"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func predictionScriptURLLooksRelevant(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	value := strings.ToLower(parsed.Path + " " + parsed.RawQuery)
	for _, token := range []string{"match", "game", "score", "result", "schedule", "fixture", "season_game", "worldcup"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func predictionURLLooksStaticAsset(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	for _, suffix := range []string{".css", ".js", ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico",
		".woff", ".woff2", ".ttf", ".eot", ".map"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func extractPredictionDataURLsFromScript(raw string, base *url.URL) []string {
	out := make([]string, 0)
	appendRaw := func(rawURL string) {
		rawURL = html.UnescapeString(strings.TrimRight(strings.TrimSpace(rawURL), ".,);"))
		if rawURL == "" {
			return
		}
		if strings.HasPrefix(rawURL, "//") && base != nil {
			rawURL = base.Scheme + ":" + rawURL
		}
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return
		}
		if base != nil {
			parsed = base.ResolveReference(parsed)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return
		}
		if !predictionDataURLLooksRelevant(parsed.String()) {
			return
		}
		out = append(out, parsed.String())
	}
	for _, rawURL := range rawHTTPURLPattern.FindAllString(raw, -1) {
		appendRaw(rawURL)
	}
	for _, match := range rawSchemeURLPattern.FindAllStringSubmatch(raw, -1) {
		if len(match) >= 1 {
			rawURL := strings.TrimSpace(match[0])
			if strings.HasPrefix(rawURL, "//") {
				appendRaw(rawURL)
			} else if idx := strings.Index(rawURL, "//"); idx >= 0 {
				appendRaw(rawURL[idx:])
			}
		}
	}
	for _, match := range rawPathURLPattern.FindAllStringSubmatch(raw, -1) {
		if len(match) >= 2 {
			appendRaw(match[1])
		}
	}
	if base != nil && strings.Contains(raw, "season_game_list") && strings.Contains(base.Hostname(), "cctv.com") {
		appendRaw("https://cbs-u.sports.cctv.com/pc/game/season_game_list?leagueId=3400&season=2026&client=pc")
	}
	if base != nil && strings.Contains(raw, `BASE_URL_PC+"/game/season_game_list`) && strings.Contains(base.Hostname(), "cctv.com") {
		appendRaw("https://cbs-u.sports.cctv.com/pc/game/season_game_list?leagueId=3400&season=2026&client=pc")
	}
	if base != nil && strings.Contains(raw, "game_status_list") && strings.Contains(base.Hostname(), "cctv.com") {
		appendRaw("https://cbs-u.sports.cctv.com/pc/game/game_status_list?client=pc")
	}
	return out
}

func predictionDataURLLooksRelevant(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	if predictionURLLooksStaticAsset(rawURL) && !strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".json") {
		return false
	}
	value := strings.ToLower(parsed.Path + " " + parsed.RawQuery)
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".json") {
		return true
	}
	for _, token := range []string{"/api/", "/game/", "/match/", "/score", "/result", "/schedule", "/fixture", "season_game"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func predictionEvidenceLooksRelevant(contract PredictionContract, text string) bool {
	normalizedText := normalizePredictionEvidenceText(text)
	if normalizedText == "" {
		return false
	}
	for _, phrase := range []string{contract.Title, contract.Description} {
		normalizedPhrase := normalizePredictionEvidenceText(phrase)
		if normalizedPhrase != "" && strings.Contains(normalizedText, normalizedPhrase) {
			return true
		}
	}
	terms := predictionEvidenceTerms(contract)
	if len(terms) == 0 {
		return true
	}
	matched := 0
	for _, term := range terms {
		if strings.Contains(normalizedText, normalizePredictionEvidenceText(term)) {
			matched++
		}
	}
	if len(terms) == 1 {
		return matched == 1
	}
	return matched >= 2
}

func predictionEvidenceTerms(contract PredictionContract) []string {
	terms := make([]string, 0)
	for _, source := range []string{contract.Title, contract.Description} {
		terms = appendPredictionEvidenceTerms(terms, predictionTitleTerms(source)...)
	}
	for _, outcome := range contract.Outcomes {
		terms = appendPredictionEvidenceTerms(terms, predictionTitleTerms(outcome.Text)...)
	}
	return terms
}

func appendPredictionEvidenceTerms(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, value := range dst {
		seen[normalizePredictionEvidenceText(value)] = struct{}{}
	}
	for _, value := range values {
		normalized := normalizePredictionEvidenceText(value)
		if normalized == "" || predictionWeakEvidenceTerm(normalized) {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func predictionWeakEvidenceTerm(term string) bool {
	if len([]rune(term)) <= 1 {
		return true
	}
	allDigits := true
	for _, r := range term {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return true
	}
	switch term {
	case "vs", "v", "win", "wins", "lose", "loses", "draw", "result", "test",
		"world", "cup", "worldcup", "round", "final", "finals", "match", "game",
		"predict", "prediction", "home", "away",
		"世界杯", "决赛", "比赛", "结果", "投注", "预测", "主场", "客场", "赢", "输", "平":
		return true
	}
	return false
}

func normalizePredictionEvidenceText(value string) string {
	value = strings.ToLower(value)
	return strings.Join(strings.Fields(value), "")
}

func predictionTitleTerms(title string) []string {
	replacer := strings.NewReplacer(
		" vs ", " ", " VS ", " ", " Vs ", " ",
		"vs", " ", "VS", " ", "Vs", " ",
		" v ", " ", " V ", " ",
		"对阵", " ", "对", " ",
		"：", " ", ":", " ", "-", " ", "_", " ", "/", " ",
	)
	title = replacer.Replace(title)
	terms := make([]string, 0)
	seen := make(map[string]struct{})
	for _, term := range strings.Fields(title) {
		normalized := normalizePredictionEvidenceText(term)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}

func buildPredictionSearchQueries(domains []string, contract PredictionContract) []string {
	title := strings.TrimSpace(contract.Title)
	description := strings.TrimSpace(contract.Description)
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}
		variants := [][]string{
			{"site:" + domain, title, description},
		}
		if compactTitle := strings.ReplaceAll(title, " ", ""); compactTitle != title {
			variants = append(variants,
				[]string{"site:" + domain, compactTitle, description},
				[]string{"site:" + domain, compactTitle},
			)
		}
		variants = append(variants, []string{"site:" + domain, title})
		for _, parts := range variants {
			query := strings.Join(nonEmptyStrings(parts), " ")
			if query == "" {
				continue
			}
			if _, ok := seen[query]; ok {
				continue
			}
			seen[query] = struct{}{}
			out = append(out, query)
		}
	}
	return out
}

func extractPredictionSearchURLs(raw, sourceURL string, trustedSources []TrustedEvidenceSource, limit int) []string {
	if limit <= 0 {
		limit = DefaultPredictionSearchMaxResults
	}
	out := make([]string, 0, limit)
	seen := make(map[string]struct{})
	appendURL := func(candidate string) {
		if len(out) >= limit {
			return
		}
		normalized := normalizePredictionSearchURL(candidate)
		if normalized == "" {
			return
		}
		if !EvidenceURLAllowed(sourceURL, normalized, trustedSources) {
			normalized = mirrorPredictionCandidateToSourceSite(sourceURL, normalized)
			if normalized == "" || !EvidenceURLAllowed(sourceURL, normalized, trustedSources) {
				return
			}
		}
		if _, ok := seen[normalized]; ok {
			return
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	for _, match := range searchHrefPattern.FindAllStringSubmatch(raw, -1) {
		if len(match) >= 2 {
			appendURL(html.UnescapeString(match[1]))
		}
	}
	for _, match := range rawHTTPURLPattern.FindAllString(raw, -1) {
		appendURL(html.UnescapeString(match))
	}
	return out
}

func normalizePredictionSearchURL(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	if strings.HasPrefix(candidate, "/url?") || strings.HasPrefix(candidate, "https://www.google.com/url?") ||
		strings.HasPrefix(candidate, "http://www.google.com/url?") {
		parsed, err := url.Parse(candidate)
		if err == nil {
			if target := parsed.Query().Get("q"); target != "" {
				candidate = target
			} else if target := parsed.Query().Get("url"); target != "" {
				candidate = target
			}
		}
	}
	parsed, err := url.Parse(candidate)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	parsed.Fragment = ""
	return parsed.String()
}

func mirrorPredictionCandidateToSourceSite(sourceURL, candidateURL string) string {
	source, err := parseHTTPURL(sourceURL)
	if err != nil {
		return ""
	}
	candidate, err := parseHTTPURL(candidateURL)
	if err != nil || !strings.EqualFold(source.Scheme, candidate.Scheme) {
		return ""
	}
	sourceHost := strings.TrimSuffix(strings.ToLower(source.Hostname()), ".")
	candidateHost := strings.TrimSuffix(strings.ToLower(candidate.Hostname()), ".")
	sourceDomain, err := siteSearchDomain(sourceHost)
	if err != nil {
		return ""
	}
	candidateDomain, err := siteSearchDomain(candidateHost)
	if err != nil {
		return ""
	}
	sourceLabel := registrableDomainLabel(sourceDomain)
	if sourceLabel == "" || sourceLabel != registrableDomainLabel(candidateDomain) {
		return ""
	}
	if hostPrefixBeforeDomain(sourceHost, sourceDomain) != hostPrefixBeforeDomain(candidateHost, candidateDomain) {
		return ""
	}
	candidate.Host = source.Host
	return candidate.String()
}

func registrableDomainLabel(domain string) string {
	if i := strings.Index(domain, "."); i > 0 {
		return domain[:i]
	}
	return domain
}

func hostPrefixBeforeDomain(host, domain string) string {
	host = strings.TrimSuffix(host, "."+domain)
	if host == domain {
		return ""
	}
	return host
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func siteSearchDomain(sourceHost string) (string, error) {
	sourceHost = strings.TrimSuffix(strings.ToLower(sourceHost), ".")
	if sourceHost == "" {
		return "", fmt.Errorf("source host is empty")
	}
	if domain, err := publicsuffix.EffectiveTLDPlusOne(sourceHost); err == nil {
		return domain, nil
	}
	return sourceHost, nil
}
