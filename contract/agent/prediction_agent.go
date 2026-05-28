package agent

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
)

const DefaultPredictionResultMaxBytes int64 = 1 << 20

const (
	DefaultPredictionAgentRetryAttempts = 2
	DefaultPredictionAgentRetryBackoff  = 2 * time.Second
	DefaultPredictionAgentMaxCandidates = 8
)

type PredictionAgentAuditEvent struct {
	Stage          string
	ResultURL      string
	FinalURL       string
	ResultType     string
	OutcomeID      string
	Reason         string
	ResultHash     string
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

type PredictionAgent struct {
	Resolver         *PredictionLLMResolver
	Fetcher          PredictionResultFetcher
	Searcher         PredictionResultSearcher
	RetryAttempts    int
	RetryBackoff     time.Duration
	MaxCandidateURLs int
	Audit            PredictionAgentAuditFunc
}

type PredictionResultSearcher interface {
	SearchPredictionResult(ctx context.Context, contract PredictionContract) ([]string, error)
}

type PredictionAgentConfirmRequest struct {
	Contract            PredictionContract
	ContractAddress     ContractAddress
	ResultURL           string
	ObservedAt          int64
	CoreNodeKey         *btcec.PrivateKey
	CoreNodePubKey      []byte
	SignCoreNodeMessage func([]byte) ([]byte, error)
	AgentVersion        string
	ModelVersion        string
}

type PredictionAgentReadyReviewRequest struct {
	Contract  PredictionContract
	CheckedAt int64
}

func NewPredictionAgent(client LLMClient) *PredictionAgent {
	return &PredictionAgent{
		Resolver:         NewPredictionLLMResolver(client),
		Fetcher:          HTTPPredictionResultTextFetcher{},
		RetryAttempts:    DefaultPredictionAgentRetryAttempts,
		RetryBackoff:     DefaultPredictionAgentRetryBackoff,
		MaxCandidateURLs: DefaultPredictionAgentMaxCandidates,
	}
}

func (a *PredictionAgent) ReviewReady(ctx context.Context, req PredictionAgentReadyReviewRequest) (PredictionRejectParam, bool, error) {
	if a == nil || a.Resolver == nil {
		return PredictionRejectParam{}, false, fmt.Errorf("missing prediction agent resolver")
	}
	if err := req.Contract.Check(); err != nil {
		return PredictionRejectParam{}, false, err
	}
	attempts := a.retryAttempts()
	backoff := a.retryBackoff()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		reject, ready, err := a.Resolver.ReviewContract(ctx, PredictionLLMReviewRequest{
			Contract:  req.Contract,
			CheckedAt: req.CheckedAt,
		})
		if err == nil {
			stage := "ready_decision"
			if !ready {
				stage = "ready_reject"
			}
			a.audit(PredictionAgentAuditEvent{Stage: stage, Reason: reject.Reason, Attempt: attempt})
			return reject, ready, nil
		}
		lastErr = err
		a.audit(PredictionAgentAuditEvent{Stage: "ready_error", Attempt: attempt, Error: err.Error()})
		if attempt < attempts {
			if err := sleepWithContext(ctx, backoff*time.Duration(attempt)); err != nil {
				return PredictionRejectParam{}, false, err
			}
		}
	}
	return PredictionRejectParam{}, false, lastErr
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
	if !errors.Is(err, ErrPredictionResultPending) {
		return PredictionConfirmParam{}, err
	}
	candidateURLs := a.candidateResultURLs(ctx, req.Contract, fetched.Links)
	a.audit(PredictionAgentAuditEvent{Stage: "candidate_urls", CandidateCount: len(candidateURLs)})
	for _, candidateURL := range candidateURLs {
		next, fetchErr := a.fetchWithRetry(ctx, fetcher, candidateURL)
		if fetchErr != nil {
			continue
		}
		param, resolveErr := a.resolveFetchedResult(ctx, req, next, candidateURL)
		if resolveErr == nil {
			return param, nil
		}
		if !errors.Is(resolveErr, ErrPredictionResultPending) {
			return PredictionConfirmParam{}, resolveErr
		}
	}
	return PredictionConfirmParam{}, ErrPredictionResultPending
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
	if !ResultURLAllowed(req.Contract.SourceURL, resultURL) {
		return PredictionConfirmParam{}, fmt.Errorf("final result url is outside source site")
	}
	resolveReq := PredictionLLMResolveRequest{
		Contract:   req.Contract,
		SourceURL:  req.Contract.SourceURL,
		ResultURL:  resultURL,
		ResultText: fetched.Text,
		ObservedAt: req.ObservedAt,
	}
	cleanedBytes := len(CleanPredictionResultText(fetched.Text))
	param, decision, err := a.resolveWithRetry(ctx, resolveReq, resultURL, len(fetched.Text), cleanedBytes)
	if err != nil {
		return PredictionConfirmParam{}, err
	}
	param.AgentVersion = req.AgentVersion
	param.ModelVersion = req.ModelVersion
	if req.CoreNodeKey != nil {
		if err := SignPredictionConfirmAttestation(req.ContractAddress, &param, req.CoreNodeKey); err != nil {
			return PredictionConfirmParam{}, err
		}
	} else if req.SignCoreNodeMessage != nil {
		if err := AttachPredictionConfirmAttestation(req.ContractAddress, &param,
			req.CoreNodePubKey, req.SignCoreNodeMessage); err != nil {
			return PredictionConfirmParam{}, err
		}
	}
	a.audit(PredictionAgentAuditEvent{Stage: "llm_decision", ResultURL: resultURL, ResultType: param.ResultType, OutcomeID: param.OutcomeID, Reason: decision.Reason, ResultHash: param.ResultHash, TextBytes: len(fetched.Text), CleanedBytes: cleanedBytes})
	return param, nil
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

func (a *PredictionAgent) candidateResultURLs(ctx context.Context, contract PredictionContract, links []string) []string {
	limit := a.MaxCandidateURLs
	if limit <= 0 {
		limit = DefaultPredictionAgentMaxCandidates
	}
	seen := make(map[string]struct{})
	candidates := make([]string, 0, limit)
	appendURL := func(candidateURL string) {
		if len(candidates) >= limit || !ResultURLAllowed(contract.SourceURL, candidateURL) {
			return
		}
		if _, ok := seen[candidateURL]; ok {
			return
		}
		seen[candidateURL] = struct{}{}
		candidates = append(candidates, candidateURL)
	}
	for _, candidateURL := range links {
		appendURL(candidateURL)
	}
	for _, candidateURL := range a.searchResultURLs(ctx, contract) {
		appendURL(candidateURL)
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
		client = &http.Client{Timeout: 30 * time.Second}
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
	if text == "" {
		return PredictionResultFetchResult{}, fmt.Errorf("result url text is empty")
	}
	return PredictionResultFetchResult{
		FinalURL: resp.Request.URL.String(),
		Text:     text,
		Links:    ExtractPredictionResultLinks(rawText, resp.Request.URL),
	}, nil
}

var (
	htmlScriptPattern = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlStylePattern  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlTagPattern    = regexp.MustCompile(`(?is)<[^>]+>`)
	htmlLinkPattern   = regexp.MustCompile(`(?is)<a\s+[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
)

func ExtractPredictionResultText(raw string) string {
	raw = htmlScriptPattern.ReplaceAllString(raw, " ")
	raw = htmlStylePattern.ReplaceAllString(raw, " ")
	raw = htmlTagPattern.ReplaceAllString(raw, " ")
	raw = html.UnescapeString(raw)
	return strings.Join(strings.Fields(raw), " ")
}

func ExtractPredictionResultLinks(raw string, base *url.URL) []string {
	matches := htmlLinkPattern.FindAllStringSubmatch(raw, -1)
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		href := html.UnescapeString(strings.TrimSpace(match[1]))
		text := strings.ToLower(ExtractPredictionResultText(match[2]))
		if !predictionResultLinkLooksRelevant(href, text) {
			continue
		}
		parsed, err := url.Parse(href)
		if err != nil {
			continue
		}
		if base != nil {
			parsed = base.ResolveReference(parsed)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}
		normalized := parsed.String()
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func predictionResultLinkLooksRelevant(href, text string) bool {
	value := strings.ToLower(href + " " + text)
	for _, token := range []string{"result", "final", "score", "boxscore", "recap", "match", "game"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}
