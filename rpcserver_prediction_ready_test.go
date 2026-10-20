package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/contract/agent"
)

// The isolated-regtest evidence option must apply to the deployment preflight
// as well as the node's Oracle service. Keep the default network guard intact.
func TestPredictionReadyRPCPrivateEvidenceOption(t *testing.T) {
	previous := cfg
	t.Cleanup(func() { cfg = previous })
	for _, allow := range []bool{false, true} {
		name := "default_rejects_private"
		if allow {
			name = "explicit_isolated_option_allows_private"
		}
		t.Run(name, func(t *testing.T) {
			var sourceCalls, modelCalls atomic.Int32
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sourceCalls.Add(1)
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<html><body>Regtest match: Team A versus Team B. Final score will appear here.</body></html>"))
			}))
			defer source.Close()
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				modelCalls.Add(1)
				if r.URL.Path != "/api/chat" {
					t.Errorf("unexpected model route: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"message": map[string]string{"role": "assistant", "content": `{"ready":true,"reason":"specific event and two mutually exclusive outcomes"}`},
				})
			}))
			defer model.Close()
			cfg = &config{AgentLLMProvider: "ollama", AgentLLMEndpoint: model.URL,
				AgentLLMModel: "test-existing-model", AgentLLMTimeout: time.Second,
				AgentAllowPrivateEvidence: allow}
			contract := agent.PredictionContract{Subtype: agent.SubtypePrediction,
				Title: "Regtest evidence option", Description: "Predict the winner of Team A versus Team B in the regtest match.",
				TimeBase: agent.TimeBaseHeight, EventTime: 29, BetDeadline: 10, ConfirmAfter: 30,
				SourceURL: source.URL + "/event", BetAsset: agent.SatoshiAssetName, MinBetUnit: "1000",
				Outcomes: []agent.PredictionOutcome{{ID: "a", Text: "Team A wins"}, {ID: "b", Text: "Team B wins"}}}
			if err := contract.Check(); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(contract)
			if err != nil {
				t.Fatal(err)
			}
			checkedAt := int64(1)
			value, err := handleReviewPredictionReady(&rpcServer{}, &btcjson.ReviewPredictionReadyCmd{
				ContractJSON: string(raw), CheckedAt: &checkedAt}, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, ok := value.(btcjson.PredictionReadyReviewResult)
			if !ok {
				t.Fatalf("unexpected response type: %T", value)
			}
			if result.Ready != allow || result.URLReachable != allow {
				t.Fatalf("allow private=%v: ready=%v reachable=%v source calls=%d model calls=%d reason=%s",
					allow, result.Ready, result.URLReachable, sourceCalls.Load(), modelCalls.Load(), result.Reason)
			}
			if allow && (sourceCalls.Load() != 1 || modelCalls.Load() != 1) {
				t.Fatalf("preflight must fetch the source and review it: source=%d model=%d", sourceCalls.Load(), modelCalls.Load())
			}
			if !allow && (sourceCalls.Load() != 0 || modelCalls.Load() != 0) {
				t.Fatalf("default policy must reject before network access: source=%d model=%d", sourceCalls.Load(), modelCalls.Load())
			}
		})
	}
}
