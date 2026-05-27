package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestPredictionAgentE2EConfirmAndSettle(t *testing.T) {
	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/match/result/123" {
			t.Fatalf("unexpected result path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Final</h1><p>Team A 101, Team B 98.</p></body></html>`))
	}))
	defer resultServer.Close()

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path: %s", r.URL.Path)
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode llm request: %v", err)
		}
		messages := req["messages"].([]interface{})
		last := messages[len(messages)-1].(map[string]interface{})
		if got := last["content"].(string); got == "" {
			t.Fatalf("missing prompt content")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"result_type\":\"outcome\",\"outcome_id\":\"a\",\"reason\":\"Team A won\"}"}}]}`))
	}))
	defer llmServer.Close()

	llmClient, err := NewLLMClient(LLMConfig{
		Provider: LLMProviderOpenAI,
		Endpoint: llmServer.URL,
		Model:    "local-model",
	})
	if err != nil {
		t.Fatalf("NewLLMClient failed: %v", err)
	}

	deployTx, addr := testAgentDeployTxForContract(t, predictionContractForResultServer(resultServer.URL))
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000, nil)

	store := NewRuntimeStore()
	_, err = ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx, aliceBetTx, bobBetTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID():    "core",
			aliceBetTx.TxID(): "alice",
			bobBetTx.TxID():   "bob",
		}),
	})
	if err != nil {
		t.Fatalf("first block failed: %v", err)
	}

	contract := predictionContractForResultServer(resultServer.URL)
	corenodeAgent := NewPredictionAgent(llmClient)
	confirmParam, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  resultServer.URL + "/match/result/123",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("BuildConfirmParam failed: %v", err)
	}
	if confirmParam.ResultHash != PredictionResultTextHash("Final Team A 101, Team B 98.") {
		t.Fatalf("result hash mismatch: %s", confirmParam.ResultHash)
	}
	encodedConfirm, err := confirmParam.Encode()
	if err != nil {
		t.Fatalf("confirm encode failed: %v", err)
	}
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, encodedConfirm, 0, nil)

	abnormalTxID := chainhash.Hash{9}.String()
	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   contract.ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ContractUTXOs: ContractUTXOProviderWithTxOutputs(func(contract ContractAddress) ([]UTXO, error) {
			return []UTXO{{
				OutPoint: OutPoint{TxID: abnormalTxID, Vout: 0},
				Contract: contract,
				Value:    10000,
			}}, nil
		}, []*wire.MsgTx{aliceBetTx, bobBetTx}, TestnetContractPrefix),
		ResolveInvoker: testInvokerResolver(map[string]string{confirmTx.TxID(): "core"}),
		ResolveScript:  testResultScriptResolver,
	})
	if err != nil {
		t.Fatalf("BuildBlockResultTxs failed: %v", err)
	}
	if len(built.ResultTxs) != 1 {
		t.Fatalf("result tx count mismatch: %d", len(built.ResultTxs))
	}
	if len(built.ResultTxs[0].TxIn) != 4 {
		t.Fatalf("result input count mismatch: %d", len(built.ResultTxs[0].TxIn))
	}
	if got := built.ResultTxs[0].TxOut[0].Value; got != 6600 {
		t.Fatalf("deployer fee mismatch: %d", got)
	}
	if got := built.ResultTxs[0].TxOut[3].Value; got != 99000 {
		t.Fatalf("winner payout mismatch: %d", got)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	if runtime.State().Status != StatusCompleted {
		t.Fatalf("runtime status mismatch: %s", runtime.State().Status)
	}
}

func TestPredictionAgentUsesFinalRedirectURL(t *testing.T) {
	var resultServer *httptest.Server
	resultServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/match/preview/123":
			http.Redirect(w, r, resultServer.URL+"/match/result/123", http.StatusFound)
		case "/match/result/123":
			_, _ = w.Write([]byte(`<html><body>Final: Team A wins.</body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer resultServer.Close()

	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"a"}`}
	contract := predictionContractForResultServer(resultServer.URL)
	corenodeAgent := NewPredictionAgent(client)
	param, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  contract.SourceURL,
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("BuildConfirmParam failed: %v", err)
	}
	if param.ResultURL != resultServer.URL+"/match/result/123" {
		t.Fatalf("result url mismatch: %s", param.ResultURL)
	}
}

func TestPredictionAgentSearchesSameSiteResultLinkWhenSourcePending(t *testing.T) {
	var resultServer *httptest.Server
	resultServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/match/preview/123":
			_, _ = w.Write([]byte(`<html><body>
				<h1>Upcoming game</h1>
				<a href="/match/result/123">Final score</a>
			</body></html>`))
		case "/match/result/123":
			_, _ = w.Write([]byte(`<html><body>Final: Team A 101, Team B 98.</body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer resultServer.Close()

	client := &sequenceLLMClient{responses: []string{
		`{"result_type":"pending","reason":"preview page has no final score"}`,
		`{"result_type":"outcome","outcome_id":"a","reason":"Team A won"}`,
	}}
	contract := predictionContractForResultServer(resultServer.URL)
	corenodeAgent := NewPredictionAgent(client)
	param, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  contract.SourceURL,
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("BuildConfirmParam failed: %v", err)
	}
	if param.ResultURL != resultServer.URL+"/match/result/123" {
		t.Fatalf("result url mismatch: %s", param.ResultURL)
	}
	if param.ResultType != ResultTypeOutcome || param.OutcomeID != "a" {
		t.Fatalf("decision mismatch: %#v", param)
	}
	if client.calls != 2 {
		t.Fatalf("llm call count mismatch: %d", client.calls)
	}
}

type sequenceLLMClient struct {
	responses []string
	calls     int
}

func (c *sequenceLLMClient) Complete(ctx context.Context, req LLMCompletionRequest) (LLMCompletionResponse, error) {
	if c.calls >= len(c.responses) {
		return LLMCompletionResponse{}, ErrPredictionResultPending
	}
	response := c.responses[c.calls]
	c.calls++
	return LLMCompletionResponse{Content: response}, nil
}

func predictionContractForResultServer(serverURL string) PredictionContract {
	contract := validPredictionContract()
	contract.SourceURL = serverURL + "/match/preview/123"
	return contract
}

func testAgentDeployTxForContract(t *testing.T, contract PredictionContract) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	deploy := DeployPayload{
		GasLimit:        1000,
		Subtype:         SubtypePrediction,
		AgentVersion:    CurrentAgentVersion,
		Deployer:        "deployer",
		Random:          []byte("random"),
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, deploy.Subtype, deploy.ContractContent, deploy.Deployer, deploy.Random)
	if err != nil {
		t.Fatalf("DeriveContractAddress failed: %v", err)
	}
	script, err := DeployNullDataScript(deploy)
	if err != nil {
		t.Fatalf("DeployNullDataScript failed: %v", err)
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, nil, testAgentContractScript(addr)))
	return tx, addr
}
