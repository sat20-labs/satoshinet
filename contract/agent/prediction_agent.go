package agent

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
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
	resultURL := req.ResultURL
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
	text := ExtractPredictionResultText(string(raw))
	if text == "" {
		return PredictionResultFetchResult{}, fmt.Errorf("result url text is empty")
	}
	return PredictionResultFetchResult{
		FinalURL: resp.Request.URL.String(),
		Text:     text,
	}, nil
}

var (
	htmlScriptPattern = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	htmlStylePattern  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	htmlTagPattern    = regexp.MustCompile(`(?is)<[^>]+>`)
)

func ExtractPredictionResultText(raw string) string {
	raw = htmlScriptPattern.ReplaceAllString(raw, " ")
	raw = htmlStylePattern.ReplaceAllString(raw, " ")
	raw = htmlTagPattern.ReplaceAllString(raw, " ")
	raw = html.UnescapeString(raw)
	return strings.Join(strings.Fields(raw), " ")
}
