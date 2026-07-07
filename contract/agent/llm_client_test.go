package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLLMClientOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["model"] != "llama3" {
			t.Fatalf("model mismatch: %v", req["model"])
		}
		if req["keep_alive"] != float64(-1) {
			t.Fatalf("keep_alive mismatch: %v", req["keep_alive"])
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"answer-a"},"done":true}`))
	}))
	defer server.Close()

	client, err := NewLLMClient(LLMConfig{
		Provider:  LLMProviderOllama,
		Endpoint:  server.URL,
		Model:     "llama3",
		KeepAlive: "-1",
	})
	if err != nil {
		t.Fatalf("NewLLMClient failed: %v", err)
	}
	resp, err := client.Complete(context.Background(), LLMCompletionRequest{
		Messages: []LLMMessage{{Role: "user", Content: "question"}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Content != "answer-a" {
		t.Fatalf("content mismatch: %s", resp.Content)
	}
}

func TestLLMClientOpenAICompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization mismatch: %q", got)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"answer-b"}}]}`))
	}))
	defer server.Close()

	client, err := NewLLMClient(LLMConfig{
		Provider: LLMProviderOpenAI,
		Endpoint: server.URL,
		Model:    "model-a",
		APIKey:   "secret",
	})
	if err != nil {
		t.Fatalf("NewLLMClient failed: %v", err)
	}
	resp, err := client.Complete(context.Background(), LLMCompletionRequest{
		Messages: []LLMMessage{{Role: "user", Content: "question"}},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Content != "answer-b" {
		t.Fatalf("content mismatch: %s", resp.Content)
	}
}

func TestLLMConfigValidation(t *testing.T) {
	if client, err := NewLLMClient(LLMConfig{}); err != nil || client != nil {
		t.Fatalf("disabled client mismatch: client=%v err=%v", client, err)
	}
	if _, err := NewLLMClient(LLMConfig{Provider: "bad", Model: "x"}); err == nil {
		t.Fatalf("expected unsupported provider error")
	}
	if _, err := NewLLMClient(LLMConfig{Provider: LLMProviderOllama}); err == nil {
		t.Fatalf("expected empty model error")
	}
}
