package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/stretchr/testify/require"
)

// Exercise authentication, JSON command decoding, the registered handler and
// response encoding through the production RPC listener, using local fixtures.
func TestPredictionReadyRPCPrivateEvidenceHTTP(t *testing.T) {
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
				if r.Method != http.MethodGet || r.URL.Path != "/event" {
					t.Errorf("unexpected evidence request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				_, _ = w.Write([]byte("<html><body>Regtest match: Team A versus Team B. Final score will appear here.</body></html>"))
			}))
			defer source.Close()
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				modelCalls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/chat" {
					t.Errorf("unexpected model request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"ready\":true,\"reason\":\"specific event and two mutually exclusive outcomes\"}"}}`))
			}))
			defer model.Close()
			cfg = &config{RPCMaxClients: 10, AgentLLMProvider: "ollama", AgentLLMEndpoint: model.URL,
				AgentLLMModel: "test-existing-model", AgentLLMTimeout: time.Second, AgentAllowPrivateEvidence: allow}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("prediction-ready-test:local-fixture"))
			rpc := &rpcServer{cfg: rpcserverConfig{Listeners: []net.Listener{listener}}, quit: make(chan int),
				authsha: sha256.Sum256([]byte(authorization)), statusLines: make(map[int]string)}
			rpc.ntfnMgr = newWsNotificationManager(rpc)
			rpc.Start()
			t.Cleanup(func() { require.NoError(t, rpc.Stop()) })
			contract := agent.PredictionContract{Subtype: agent.SubtypePrediction,
				Title: "Regtest evidence option", Description: "Predict the winner of Team A versus Team B in the regtest match.",
				TimeBase: agent.TimeBaseHeight, EventTime: 29, BetDeadline: 10, ConfirmAfter: 30,
				SourceURL: source.URL + "/event", BetAsset: agent.SatoshiAssetName, MinBetUnit: "1000",
				Outcomes: []agent.PredictionOutcome{{ID: "a", Text: "Team A wins"}, {ID: "b", Text: "Team B wins"}}}
			require.NoError(t, contract.Check())
			raw, err := json.Marshal(contract)
			require.NoError(t, err)
			checkedAt := int64(1)
			body, err := btcjson.MarshalCmd(btcjson.RpcVersion1, 1, &btcjson.ReviewPredictionReadyCmd{
				ContractJSON: string(raw), CheckedAt: &checkedAt})
			require.NoError(t, err)
			request, err := http.NewRequest(http.MethodPost, "http://"+listener.Addr().String(), bytes.NewReader(body))
			require.NoError(t, err)
			request.Header.Set("Authorization", authorization)
			request.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 10 * time.Second}
			response, err := client.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
			var reply btcjson.Response
			require.NoError(t, json.NewDecoder(response.Body).Decode(&reply))
			require.Nil(t, reply.Error)
			require.NotNil(t, reply.ID)
			require.Equal(t, float64(1), *reply.ID)
			var result btcjson.PredictionReadyReviewResult
			require.NoError(t, json.Unmarshal(reply.Result, &result))
			require.Equal(t, allow, result.Ready)
			require.Equal(t, allow, result.URLReachable)
			require.Equal(t, checkedAt, result.CheckedAt)
			require.Equal(t, contract.SourceURL, result.SourceURL)
			wantCalls := int32(0)
			if allow {
				wantCalls = 1
				require.Equal(t, contract.SourceURL, result.FinalURL)
				require.Positive(t, result.TextBytes)
				require.Positive(t, result.CleanedBytes)
			}
			require.Equal(t, wantCalls, sourceCalls.Load())
			require.Equal(t, wantCalls, modelCalls.Load())
			t.Logf("HTTP JSON-RPC allow_private=%v ready=%v reachable=%v evidence_calls=%d model_calls=%d",
				allow, result.Ready, result.URLReachable, sourceCalls.Load(), modelCalls.Load())
		})
	}
}
