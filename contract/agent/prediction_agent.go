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
)

const DefaultPredictionResultMaxBytes int64 = 1 << 20

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
	Resolver *PredictionLLMResolver
	Fetcher  PredictionResultFetcher
	Searcher PredictionResultSearcher
}

type PredictionResultSearcher interface {
	SearchPredictionResult(ctx context.Context, contract PredictionContract) ([]string, error)
}

type PredictionAgentConfirmRequest struct {
	Contract   PredictionContract
	ResultURL  string
	ObservedAt int64
}

func NewPredictionAgent(client LLMClient) *PredictionAgent {
	return &PredictionAgent{
		Resolver: NewPredictionLLMResolver(client),
		Fetcher:  HTTPPredictionResultTextFetcher{},
	}
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
	fetched, err := fetcher.FetchPredictionResult(ctx, req.ResultURL)
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
	for _, candidateURL := range append(fetched.Links, a.searchResultURLs(ctx, req.Contract)...) {
		if !ResultURLAllowed(req.Contract.SourceURL, candidateURL) {
			continue
		}
		next, fetchErr := fetcher.FetchPredictionResult(ctx, candidateURL)
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
	return a.Resolver.Resolve(ctx, PredictionLLMResolveRequest{
		Contract:   req.Contract,
		SourceURL:  req.Contract.SourceURL,
		ResultURL:  resultURL,
		ResultText: fetched.Text,
		ObservedAt: req.ObservedAt,
	})
}

func (a *PredictionAgent) searchResultURLs(ctx context.Context, contract PredictionContract) []string {
	if a == nil || a.Searcher == nil {
		return nil
	}
	urls, err := a.Searcher.SearchPredictionResult(ctx, contract)
	if err != nil {
		return nil
	}
	return urls
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
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return PredictionResultFetchResult{}, err
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
