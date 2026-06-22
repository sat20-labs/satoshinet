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

	"golang.org/x/net/publicsuffix"
)

const DefaultPredictionResultMaxBytes int64 = 1 << 20

const (
	DefaultPredictionAgentRetryAttempts = 2
	DefaultPredictionAgentRetryBackoff  = 2 * time.Second
	DefaultPredictionAgentMaxCandidates = 8
	DefaultPredictionSearchMaxResults   = 8
	DefaultPredictionSearchMaxBytes     = 1 << 20
	DefaultPredictionSearchEndpoint     = "https://www.google.com/search"
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
	Client     *http.Client
	Endpoint   string
	MaxBytes   int64
	MaxResults int
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

func NewPredictionAgent(client LLMClient) *PredictionAgent {
	return &PredictionAgent{
		Resolver:         NewPredictionLLMResolver(client),
		Fetcher:          HTTPPredictionResultTextFetcher{},
		Searcher:         HTTPPredictionResultSearcher{},
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
	candidateURLs := a.candidateResultURLs(ctx, req.Contract, fetched.Links)
	a.audit(PredictionAgentAuditEvent{Stage: "candidate_urls", CandidateCount: len(candidateURLs)})
	lastErr := err
	for _, candidateURL := range candidateURLs {
		next, fetchErr := a.fetchWithRetry(ctx, fetcher, candidateURL)
		if fetchErr != nil {
			lastErr = fetchErr
			continue
		}
		param, resolveErr := a.resolveFetchedResult(ctx, req, next, candidateURL)
		if resolveErr == nil {
			return param, nil
		}
		lastErr = resolveErr
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
	a.audit(PredictionAgentAuditEvent{Stage: "llm_decision", ResultURL: resultURL, ResultType: param.ResultType, OutcomeID: param.OutcomeID, Result: param.Result, Reason: decision.Reason, TextBytes: len(fetched.Text), CleanedBytes: cleanedBytes})
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
	endpoint := strings.TrimSpace(s.Endpoint)
	if endpoint == "" {
		endpoint = DefaultPredictionSearchEndpoint
	}
	searchURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	query := searchURL.Query()
	query.Set("q", buildPredictionSearchQuery(searchDomain, contract))
	if query.Get("num") == "" {
		query.Set("num", fmt.Sprintf("%d", s.maxResults()))
	}
	if query.Get("hl") == "" {
		query.Set("hl", "zh-CN")
	}
	searchURL.RawQuery = query.Encode()

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SatoshiNetPredictionAgent/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prediction search status %d", resp.StatusCode)
	}
	maxBytes := s.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultPredictionSearchMaxBytes
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("prediction search text exceeds max bytes %d", maxBytes)
	}
	return extractPredictionSearchURLs(string(raw), contract.SourceURL, s.maxResults()), nil
}

func (s HTTPPredictionResultSearcher) maxResults() int {
	if s.MaxResults > 0 {
		return s.MaxResults
	}
	return DefaultPredictionSearchMaxResults
}

var (
	htmlScriptPattern     = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlJSONScriptPattern = regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	htmlStylePattern      = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlTagPattern        = regexp.MustCompile(`(?is)<[^>]+>`)
	htmlURLAttrPattern    = regexp.MustCompile(`(?is)<(a|iframe|frame|link)\b[^>]*(href|src)=["']([^"']+)["'][^>]*>`)
	searchHrefPattern     = regexp.MustCompile(`(?is)href=["']([^"']+)["']`)
	rawHTTPURLPattern     = regexp.MustCompile(`https?://[^\s"'<>\\]+`)
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
	matches := htmlURLAttrPattern.FindAllStringSubmatch(raw, -1)
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, match := range matches {
		tag, rawURL, linkText := predictionURLAttrMatch(match)
		if rawURL == "" {
			continue
		}
		text := strings.ToLower(ExtractPredictionResultText(linkText))
		if !predictionResultURLLooksRelevant(tag, rawURL, text) {
			continue
		}
		parsed, err := url.Parse(rawURL)
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

func predictionScriptContainsData(attrs string) bool {
	attrs = strings.ToLower(attrs)
	return strings.Contains(attrs, "application/json") ||
		strings.Contains(attrs, "application/ld+json") ||
		strings.Contains(attrs, "__next_data__") ||
		strings.Contains(attrs, "__nuxt_data__")
}

func predictionURLAttrMatch(match []string) (string, string, string) {
	if len(match) >= 4 && match[1] != "" {
		return strings.ToLower(match[1]), html.UnescapeString(strings.TrimSpace(match[3])), ""
	}
	return "", "", ""
}

func predictionResultURLLooksRelevant(tag, rawURL, text string) bool {
	if tag == "iframe" || tag == "frame" {
		return true
	}
	value := strings.ToLower(rawURL + " " + text)
	for _, token := range []string{"result", "final", "score", "boxscore", "recap", "match", "game"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func buildPredictionSearchQuery(domain string, contract PredictionContract) string {
	parts := []string{"site:" + domain, contract.Title, contract.Description}
	for _, outcome := range contract.Outcomes {
		parts = append(parts, outcome.Text)
	}
	return strings.Join(nonEmptyStrings(parts), " ")
}

func extractPredictionSearchURLs(raw, sourceURL string, limit int) []string {
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
		if normalized == "" || !ResultURLAllowed(sourceURL, normalized) {
			return
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
