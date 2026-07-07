package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	LLMProviderOllama = "ollama"
	LLMProviderOpenAI = "openai"

	DefaultOllamaEndpoint = "http://127.0.0.1:11434"
	DefaultOpenAIEndpoint = "https://api.openai.com/v1"
	DefaultLLMTimeout     = 60 * time.Second
)

type LLMConfig struct {
	Provider    string
	Endpoint    string
	Model       string
	APIKey      string
	Timeout     time.Duration
	KeepAlive   string
	Temperature float64
	MaxTokens   int
}

type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMCompletionRequest struct {
	Messages    []LLMMessage
	Temperature float64
	MaxTokens   int
}

type LLMCompletionResponse struct {
	Content string
	Raw     []byte
}

type LLMClient interface {
	Complete(ctx context.Context, req LLMCompletionRequest) (LLMCompletionResponse, error)
}

type HTTPLLMClient struct {
	cfg        LLMConfig
	httpClient *http.Client
}

func NewLLMClient(cfg LLMConfig) (LLMClient, error) {
	normalized, err := cfg.Normalized()
	if err != nil {
		return nil, err
	}
	if normalized.Provider == "" {
		return nil, nil
	}
	return &HTTPLLMClient{
		cfg: normalized,
		httpClient: &http.Client{
			Timeout: normalized.Timeout,
		},
	}, nil
}

func (c LLMConfig) Normalized() (LLMConfig, error) {
	c.Provider = strings.ToLower(strings.TrimSpace(c.Provider))
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	c.Model = strings.TrimSpace(c.Model)
	c.KeepAlive = strings.TrimSpace(c.KeepAlive)
	if c.Timeout <= 0 {
		c.Timeout = DefaultLLMTimeout
	}
	switch c.Provider {
	case "":
		return c, nil
	case LLMProviderOllama:
		if c.Endpoint == "" {
			c.Endpoint = DefaultOllamaEndpoint
		}
	case LLMProviderOpenAI:
		if c.Endpoint == "" {
			c.Endpoint = DefaultOpenAIEndpoint
		}
	default:
		return LLMConfig{}, fmt.Errorf("unsupported agent llm provider %q", c.Provider)
	}
	if c.Model == "" {
		return LLMConfig{}, fmt.Errorf("agent llm model is empty")
	}
	if c.MaxTokens < 0 {
		return LLMConfig{}, fmt.Errorf("agent llm max tokens is negative")
	}
	return c, nil
}

func (c *HTTPLLMClient) Complete(ctx context.Context, req LLMCompletionRequest) (LLMCompletionResponse, error) {
	if c == nil {
		return LLMCompletionResponse{}, fmt.Errorf("missing llm client")
	}
	if len(req.Messages) == 0 {
		return LLMCompletionResponse{}, fmt.Errorf("missing llm messages")
	}
	switch c.cfg.Provider {
	case LLMProviderOllama:
		return c.completeOllama(ctx, req)
	case LLMProviderOpenAI:
		return c.completeOpenAI(ctx, req)
	default:
		return LLMCompletionResponse{}, fmt.Errorf("unsupported agent llm provider %q", c.cfg.Provider)
	}
}

func (c *HTTPLLMClient) completeOllama(ctx context.Context, req LLMCompletionRequest) (LLMCompletionResponse, error) {
	body := map[string]interface{}{
		"model":    c.cfg.Model,
		"messages": req.Messages,
		"stream":   false,
	}
	if c.cfg.KeepAlive != "" {
		if c.cfg.KeepAlive == "-1" {
			body["keep_alive"] = -1
		} else {
			body["keep_alive"] = c.cfg.KeepAlive
		}
	}
	options := make(map[string]interface{})
	temperature := requestTemperature(req, c.cfg)
	if temperature != 0 {
		options["temperature"] = temperature
	}
	maxTokens := requestMaxTokens(req, c.cfg)
	if maxTokens > 0 {
		options["num_predict"] = maxTokens
	}
	if len(options) != 0 {
		body["options"] = options
	}

	raw, err := c.postJSON(ctx, ollamaChatEndpoint(c.cfg.Endpoint), body)
	if err != nil {
		return LLMCompletionResponse{}, err
	}
	var decoded struct {
		Message LLMMessage `json:"message"`
		Error   string     `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return LLMCompletionResponse{}, fmt.Errorf("decode ollama response: %w", err)
	}
	if decoded.Error != "" {
		return LLMCompletionResponse{}, fmt.Errorf("ollama error: %s", decoded.Error)
	}
	return LLMCompletionResponse{Content: decoded.Message.Content, Raw: raw}, nil
}

func (c *HTTPLLMClient) completeOpenAI(ctx context.Context, req LLMCompletionRequest) (LLMCompletionResponse, error) {
	body := map[string]interface{}{
		"model":    c.cfg.Model,
		"messages": req.Messages,
		"stream":   false,
	}
	if temperature := requestTemperature(req, c.cfg); temperature != 0 {
		body["temperature"] = temperature
	}
	if maxTokens := requestMaxTokens(req, c.cfg); maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}

	raw, err := c.postJSON(ctx, openAIChatEndpoint(c.cfg.Endpoint), body)
	if err != nil {
		return LLMCompletionResponse{}, err
	}
	var decoded struct {
		Choices []struct {
			Message LLMMessage `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return LLMCompletionResponse{}, fmt.Errorf("decode openai-compatible response: %w", err)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return LLMCompletionResponse{}, fmt.Errorf("openai-compatible error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return LLMCompletionResponse{}, fmt.Errorf("openai-compatible response has no choices")
	}
	return LLMCompletionResponse{Content: decoded.Choices[0].Message.Content, Raw: raw}, nil
}

func (c *HTTPLLMClient) postJSON(ctx context.Context, endpoint string, body interface{}) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm api status %d: %s", resp.StatusCode, string(raw))
	}
	return raw, nil
}

func requestTemperature(req LLMCompletionRequest, cfg LLMConfig) float64 {
	if req.Temperature != 0 {
		return req.Temperature
	}
	return cfg.Temperature
}

func requestMaxTokens(req LLMCompletionRequest, cfg LLMConfig) int {
	if req.MaxTokens != 0 {
		return req.MaxTokens
	}
	return cfg.MaxTokens
}

func ollamaChatEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, "/api/chat") {
		return endpoint
	}
	return endpoint + "/api/chat"
}

func openAIChatEndpoint(endpoint string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, "/chat/completions") {
		return endpoint
	}
	if strings.HasSuffix(endpoint, "/v1") {
		return endpoint + "/chat/completions"
	}
	return endpoint + "/v1/chat/completions"
}
