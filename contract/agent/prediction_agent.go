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
	if !predictionEvidenceLooksRelevant(req.Contract, fetched.Text) {
		a.audit(PredictionAgentAuditEvent{
			Stage:        "evidence_irrelevant",
			ResultURL:    resultURL,
			TextBytes:    len(fetched.Text),
			CleanedBytes: len(CleanPredictionResultText(fetched.Text)),
		})
		return PredictionConfirmParam{}, ErrPredictionResultPending
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
		urls, _, err := s.searchPredictionResultEndpoint(ctx, client, maxBytes, searchDomain, contract, endpoints[0])
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
			urls, searchOK, err := s.searchPredictionResultEndpoint(searchCtx, client, maxBytes, searchDomain, contract, endpoint)
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
	client *http.Client, maxBytes int64, searchDomain string, contract PredictionContract,
	endpoint string) ([]string, bool, error) {

	var lastErr error
	searchOK := false
	for _, searchQuery := range buildPredictionSearchQueries(searchDomain, contract) {
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
		urls := extractPredictionSearchURLs(string(raw), contract.SourceURL, s.maxResults())
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

func (s HTTPPredictionResultSearcher) endpoints() []string {
	if endpoint := strings.TrimSpace(s.Endpoint); endpoint != "" {
		return []string{endpoint}
	}
	return []string{DefaultPredictionSearchEndpoint, DefaultPredictionSearchFallback}
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
	htmlFramePattern      = regexp.MustCompile(`(?is)<(iframe|frame)\b([^>]*)>`)
	htmlAttrPattern       = regexp.MustCompile(`(?is)\b(href|src)=["']([^"']+)["']`)
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
	value := strings.ToLower(rawURL + " " + text)
	for _, token := range []string{"/match/", "/game/", "result", "final", "boxscore", "recap", "比分", "赛果", "战报", "集锦"} {
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

func predictionEvidenceLooksRelevant(contract PredictionContract, text string) bool {
	normalizedText := normalizePredictionEvidenceText(text)
	if normalizedText == "" {
		return false
	}
	if title := normalizePredictionEvidenceText(contract.Title); title != "" &&
		strings.Contains(normalizedText, title) {
		return true
	}
	terms := predictionTitleTerms(contract.Title)
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

func buildPredictionSearchQueries(domain string, contract PredictionContract) []string {
	title := strings.TrimSpace(contract.Title)
	description := strings.TrimSpace(contract.Description)
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
	out := make([]string, 0, len(variants))
	seen := make(map[string]struct{})
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
	return out
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
		if normalized == "" {
			return
		}
		if !ResultURLAllowed(sourceURL, normalized) {
			normalized = mirrorPredictionCandidateToSourceSite(sourceURL, normalized)
			if normalized == "" || !ResultURLAllowed(sourceURL, normalized) {
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
