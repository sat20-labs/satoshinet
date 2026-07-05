package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestPredictionAgentE2EConfirmAndSettle(t *testing.T) {
	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/match/result/123" {
			t.Fatalf("unexpected result path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Team A vs Team B Final</h1><p>Team A 101, Team B 98.</p></body></html>`))
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
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"result_type\":\"outcome\",\"outcome_id\":\"a\",\"result\":\"Team A 101, Team B 98\",\"reason\":\"Team A won\"}"}}]}`))
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
	resultGas := testAgentGasFee(t, DefaultGasConfig().ResultBaseGas).Int64()
	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000,
		testAgentAsset(DefaultGasConfig().GasAssetName, resultGas))
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000,
		testAgentAsset(DefaultGasConfig().GasAssetName, resultGas))

	store := NewRuntimeStore()
	_, err = ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx, aliceBetTx, bobBetTx},
		Store:         store,
		BlockHeight:   1,
		BlockTime:     validPredictionContract().BetDeadline,
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
	if confirmParam.Result != "Team A 101, Team B 98" {
		t.Fatalf("result mismatch: %s", confirmParam.Result)
	}
	encodedConfirm, err := confirmParam.Encode()
	if err != nil {
		t.Fatalf("confirm encode failed: %v", err)
	}
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, encodedConfirm, 0,
		testAgentAsset(DefaultGasConfig().GasAssetName, 100))

	abnormalTxID := chainhash.Hash{9}.String()
	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   2,
		BlockTime:     contract.ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(func(contract ContractAddress) ([]UTXO, error) {
			return []UTXO{contractframework.UTXOFromTxOutput(OutPoint{TxID: abnormalTxID, Vout: 0},
				contract, 0, &wire.TxOut{Value: 10000})}, nil
		}, []*wire.MsgTx{aliceBetTx, bobBetTx, confirmTx}, TestnetContractPrefix, ContractTypeAgent),
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
	if got := built.ResultTxs[0].TxOut[0].Value; got != 6000 {
		t.Fatalf("deployer fee mismatch: %d", got)
	}
	if got := built.ResultTxs[0].TxOut[3].Value; got != 90000 {
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
			_, _ = w.Write([]byte(`<html><body>Team A vs Team B Final: Team A wins.</body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer resultServer.Close()

	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"a","result":"Team A wins"}`}
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
					<h1>Team A vs Team B upcoming game</h1>
					<a href="/match/result/123">Final score</a>
				</body></html>`))
		case "/match/result/123":
			_, _ = w.Write([]byte(`<html><body>Team A vs Team B Final: Team A 101, Team B 98.</body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer resultServer.Close()

	client := &sequenceLLMClient{responses: []string{
		`{"result_type":"pending","reason":"preview page has no final score"}`,
		`{"result_type":"outcome","outcome_id":"a","result":"Team A 101, Team B 98","reason":"Team A won"}`,
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

func TestPredictionAgentSearchesSiteWhenSourceHasNoResultLink(t *testing.T) {
	var resultServer *httptest.Server
	resultServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/match/preview/123":
			_, _ = w.Write([]byte(`<html><body><h1>Upcoming game</h1></body></html>`))
		case "/match/result/123":
			_, _ = w.Write([]byte(`<html><body>Team A vs Team B Final: Team A 101, Team B 98.</body></html>`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer resultServer.Close()

	client := &sequenceLLMClient{responses: []string{
		`{"result_type":"outcome","outcome_id":"a","result":"Team A 101, Team B 98","reason":"Team A won"}`,
	}}
	contract := predictionContractForResultServer(resultServer.URL)
	corenodeAgent := NewPredictionAgent(client)
	corenodeAgent.RetryAttempts = 1
	corenodeAgent.Searcher = staticPredictionSearcher{urls: []string{resultServer.URL + "/match/result/123"}}
	param, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  contract.SourceURL,
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("BuildConfirmParam failed: %v", err)
	}
	if param.ResultURL != resultServer.URL+"/match/result/123" || param.OutcomeID != "a" {
		t.Fatalf("unexpected confirm param: %#v", param)
	}
	if client.calls != 1 {
		t.Fatalf("llm call count mismatch: %d", client.calls)
	}
}

func TestPredictionAgentReturnsEvidenceUnavailable(t *testing.T) {
	contract := predictionContractForResultServer("https://example.com")
	corenodeAgent := NewPredictionAgent(&fakeLLMClient{response: `{}`})
	corenodeAgent.Fetcher = &retryPredictionFetcher{result: PredictionResultFetchResult{
		FinalURL: contract.SourceURL,
		Text:     "generic sports schedule without contract participants",
	}}
	corenodeAgent.Searcher = staticPredictionSearcher{}
	_, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  contract.SourceURL,
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if !errors.Is(err, ErrPredictionEvidenceUnavailable) {
		t.Fatalf("expected evidence unavailable, got %v", err)
	}
}

func TestPredictionResultURLAllowedSameRegisteredDomain(t *testing.T) {
	source := "https://worldcup.cctv.com/2026/schedule/index.shtml"
	if !ResultURLAllowed(source, "https://cbs-u.sports.cctv.com/pc/game/season_game_list") {
		t.Fatalf("expected same registered domain URL to be allowed")
	}
	if ResultURLAllowed(source, "https://evilcctv.com/pc/game/season_game_list") {
		t.Fatalf("expected lookalike domain to be rejected")
	}
	if ResultURLAllowed(source, "http://cbs-u.sports.cctv.com/pc/game/season_game_list") {
		t.Fatalf("expected scheme downgrade to be rejected")
	}
}

func TestPredictionResultFetcherExtractsIframeAndEmbeddedData(t *testing.T) {
	raw := `<html><body>
		<h1>Fixture shell</h1>
		<iframe src="//cbs.sports.cctv.com/worldcup2026_schedule_tabs.html"></iframe>
		<script type="application/ld+json">{"name":"Team A vs Team B","score":"101-98"}</script>
	</body></html>`
	base, err := url.Parse("https://worldcup.cctv.com/2026/schedule/index.shtml")
	if err != nil {
		t.Fatal(err)
	}
	links := ExtractPredictionResultLinks(raw, base)
	if len(links) != 1 || links[0] != "https://cbs.sports.cctv.com/worldcup2026_schedule_tabs.html" {
		t.Fatalf("iframe links mismatch: %#v", links)
	}
	text := ExtractPredictionResultText(raw)
	if !strings.Contains(text, "Fixture shell") || !strings.Contains(text, `"score":"101-98"`) {
		t.Fatalf("extracted text missing evidence: %s", text)
	}
}

func TestPredictionResultFetcherExtractsScriptCandidateURL(t *testing.T) {
	raw := `<html><head>
		<link rel="stylesheet" href="https://r.img.cctvpic.com/worldcup/2026/schedule/style/style.css">
	</head><body>
		<script>
			var iframe="https://cbs.sports.cctv.com/worldcup2026_schedule_tabs.html";
		</script>
	</body></html>`
	base, err := url.Parse("https://worldcup.cctv.com/2026/schedule/index.shtml")
	if err != nil {
		t.Fatal(err)
	}
	links := ExtractPredictionResultLinks(raw, base)
	if len(links) != 1 || links[0] != "https://cbs.sports.cctv.com/worldcup2026_schedule_tabs.html" {
		t.Fatalf("script candidate links mismatch: %#v", links)
	}
}

func TestPredictionSearchExtractsGoogleResultURLs(t *testing.T) {
	source := "https://worldcup.cctv.com/2026/schedule/index.shtml"
	raw := `<html><body>
		<a href="/url?q=https%3A%2F%2Fworldcup.cctv.com%2F2026%2Fmatch%2F22920322%2Findex.shtml&sa=U">match</a>
		<a href="/url?q=https%3A%2F%2Fworldcup.cctv.cn%2F2026%2Fmatch%2F22920323%2Findex.shtml&sa=U">mirror</a>
		<a href="/url?q=https%3A%2F%2Fevilcctv.com%2Ffake&sa=U">fake</a>
		<a href="https://cbs-u.sports.cctv.com/pc/game/season_game_list?leagueId=3400">api</a>
	</body></html>`
	urls := extractPredictionSearchURLs(raw, source, nil, 5)
	if len(urls) != 3 {
		t.Fatalf("search urls mismatch: %#v", urls)
	}
	if urls[0] != "https://worldcup.cctv.com/2026/match/22920322/index.shtml" {
		t.Fatalf("first search url mismatch: %s", urls[0])
	}
	if urls[1] != "https://worldcup.cctv.com/2026/match/22920323/index.shtml" {
		t.Fatalf("mirrored search url mismatch: %s", urls[1])
	}
	if urls[2] != "https://cbs-u.sports.cctv.com/pc/game/season_game_list?leagueId=3400" {
		t.Fatalf("third search url mismatch: %s", urls[2])
	}
}

func TestPredictionEvidenceUsesDescriptionAndOutcomes(t *testing.T) {
	contract := validPredictionContract()
	contract.Title = "Argentina vs Cabo Verde"
	contract.Description = "2026世界杯1/16决赛 阿根廷 对 佛得角"
	contract.Outcomes = []PredictionOutcome{
		{ID: "a", Text: "阿根廷赢"},
		{ID: "b", Text: "佛得角赢"},
		{ID: "c", Text: "平"},
	}
	text := `{"gameName":"阿根廷vs佛得角","gameRound":"1/16决赛","homeScore":3,"guestScore":2}`
	if !predictionEvidenceLooksRelevant(contract, text) {
		t.Fatalf("expected Chinese description/outcome evidence to be relevant")
	}
}

func TestPredictionAgentFollowsStaticScriptDataURL(t *testing.T) {
	var resultServer *httptest.Server
	resultServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/match/preview/123":
			_, _ = w.Write([]byte(`<html><body>
				<iframe src="/frame/schedule.html"></iframe>
			</body></html>`))
		case "/frame/schedule.html":
			_, _ = w.Write([]byte(`<html><head>
				<script src="/scripts/worldcup2026_schedule.js"></script>
			</head><body>fixture shell</body></html>`))
		case "/scripts/worldcup2026_schedule.js":
			_, _ = w.Write([]byte(`const url="/api/game/season_game_list?leagueId=3400&season=2026&client=pc";`))
		case "/api/game/season_game_list":
			_, _ = w.Write([]byte(`{"gameName":"阿根廷vs佛得角","gameRound":"1/16决赛","homeScore":3,"guestScore":2}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer resultServer.Close()

	client := &sequenceLLMClient{responses: []string{
		`{"result_type":"outcome","outcome_id":"a","result":"阿根廷 3-2 佛得角","reason":"阿根廷获胜"}`,
	}}
	contract := validPredictionContract()
	contract.Title = "Argentina vs Cabo Verde"
	contract.Description = "2026世界杯1/16决赛 阿根廷 对 佛得角"
	contract.SourceURL = resultServer.URL + "/match/preview/123"
	contract.Outcomes = []PredictionOutcome{
		{ID: "a", Text: "阿根廷赢"},
		{ID: "b", Text: "佛得角赢"},
		{ID: "c", Text: "平"},
	}
	corenodeAgent := NewPredictionAgent(client)
	corenodeAgent.RetryAttempts = 1
	corenodeAgent.MaxCandidateURLs = 8
	param, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  contract.SourceURL,
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("BuildConfirmParam failed: %v", err)
	}
	if param.OutcomeID != "a" || param.ResultURL != resultServer.URL+"/api/game/season_game_list?leagueId=3400&season=2026&client=pc" {
		t.Fatalf("unexpected confirm param: %#v", param)
	}
}

func TestPredictionAgentUsesTrustedExternalEvidence(t *testing.T) {
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>Official event page pending.</body></html>`))
	}))
	defer sourceServer.Close()

	evidenceServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>Team A vs Team B final: Team A 101, Team B 98.</body></html>`))
	}))
	defer evidenceServer.Close()

	evidenceURL, err := url.Parse(evidenceServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &sequenceLLMClient{responses: []string{
		`{"result_type":"outcome","outcome_id":"a","result":"Team A 101, Team B 98","reason":"Team A won"}`,
	}}
	contract := predictionContractForResultServer(sourceServer.URL)
	corenodeAgent := NewPredictionAgent(client)
	corenodeAgent.Fetcher = HTTPPredictionResultTextFetcher{Client: evidenceServer.Client()}
	corenodeAgent.Searcher = staticPredictionSearcher{urls: []string{evidenceServer.URL}}
	corenodeAgent.TrustedSources = []TrustedEvidenceSource{{Domain: evidenceURL.Hostname()}}
	corenodeAgent.RetryAttempts = 1

	param, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  contract.SourceURL,
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("BuildConfirmParam failed: %v", err)
	}
	if param.OutcomeID != "a" || param.ResultURL != contract.SourceURL {
		t.Fatalf("trusted external evidence should keep source result url, got %#v", param)
	}
}

func TestHTTPPredictionResultSearcherBuildsSiteQuery(t *testing.T) {
	var gotQuery string
	searchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`<html><body>
			<a href="/url?q=https%3A%2F%2Fworldcup.cctv.com%2F2026%2Fmatch%2F22920322%2Findex.shtml&sa=U">match</a>
			<a href="/url?q=https%3A%2F%2Fexample.com%2Fwrong&sa=U">wrong</a>
		</body></html>`))
	}))
	defer searchServer.Close()

	contract := validPredictionContract()
	contract.Title = "比利时vs伊朗"
	contract.Description = "2026世界杯第二轮"
	contract.SourceURL = "https://worldcup.cctv.com/2026/schedule/index.shtml"
	searcher := HTTPPredictionResultSearcher{Endpoint: searchServer.URL, MaxResults: 5}
	urls, err := searcher.SearchPredictionResult(context.Background(), contract)
	if err != nil {
		t.Fatalf("SearchPredictionResult failed: %v", err)
	}
	if !strings.Contains(gotQuery, "site:cctv.com") || !strings.Contains(gotQuery, contract.Title) ||
		!strings.Contains(gotQuery, contract.Description) {
		t.Fatalf("query missing contract scope: %s", gotQuery)
	}
	if len(urls) != 1 || urls[0] != "https://worldcup.cctv.com/2026/match/22920322/index.shtml" {
		t.Fatalf("search urls mismatch: %#v", urls)
	}
}

func TestHTTPPredictionResultSearcherUsesFirstEndpointWithResults(t *testing.T) {
	contract := validPredictionContract()
	contract.SourceURL = "https://worldcup.cctv.com/2026/schedule/index.shtml"
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `<html><body></body></html>`
			if req.URL.Host == "www.google.com" {
				select {
				case <-time.After(300 * time.Millisecond):
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}
			if req.URL.Host == "www.bing.com" {
				body = `<html><body>
					<a href="https://worldcup.cctv.com/2026/match/22920322/index.shtml">match</a>
				</body></html>`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	start := time.Now()
	searcher := HTTPPredictionResultSearcher{Client: client, MaxResults: 5}
	urls, err := searcher.SearchPredictionResult(context.Background(), contract)
	if err != nil {
		t.Fatalf("SearchPredictionResult failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 200*time.Millisecond {
		t.Fatalf("search fallback was not concurrent, elapsed=%s", elapsed)
	}
	if len(urls) != 1 || urls[0] != "https://worldcup.cctv.com/2026/match/22920322/index.shtml" {
		t.Fatalf("search urls mismatch: %#v", urls)
	}
}

func TestPredictionAgentReadyReviewRejectsAmbiguousContract(t *testing.T) {
	client := &fakeLLMClient{response: `{"ready":false,"reason":"event source is not verifiable"}`}
	contract := predictionContractForResultServer("https://example.com")
	corenodeAgent := NewPredictionAgent(client)
	corenodeAgent.Fetcher = &retryPredictionFetcher{result: PredictionResultFetchResult{
		FinalURL: contract.SourceURL,
		Text:     "England vs Croatia fixture page",
	}}

	reject, ready, err := corenodeAgent.ReviewReady(context.Background(), PredictionAgentReadyReviewRequest{
		Contract:  contract,
		CheckedAt: contract.BetDeadline,
	})
	if err != nil {
		t.Fatalf("ReviewReady failed: %v", err)
	}
	if ready {
		t.Fatalf("expected reject decision")
	}
	if reject.Reason != "event source is not verifiable" || reject.CheckedAt != contract.BetDeadline {
		t.Fatalf("reject mismatch: %#v", reject)
	}
}

func TestPredictionAgentReadyReviewRejectsUnreachableSourceURL(t *testing.T) {
	client := &fakeLLMClient{response: `{"ready":true,"reason":"ok"}`}
	contract := predictionContractForResultServer("https://example.com")
	corenodeAgent := NewPredictionAgent(client)
	corenodeAgent.RetryAttempts = 1
	corenodeAgent.Fetcher = &retryPredictionFetcher{failures: 1}

	result, err := corenodeAgent.ReviewReadyResult(context.Background(), PredictionAgentReadyReviewRequest{
		Contract:  contract,
		CheckedAt: contract.BetDeadline,
	})
	if err != nil {
		t.Fatalf("ReviewReadyResult failed: %v", err)
	}
	if result.Ready || result.URLReachable {
		t.Fatalf("unexpected ready result: %#v", result)
	}
	if !strings.Contains(result.Reason, "source url is not reachable") {
		t.Fatalf("unexpected reject reason: %s", result.Reason)
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

type staticPredictionSearcher struct {
	urls []string
	err  error
}

func (s staticPredictionSearcher) SearchPredictionResult(ctx context.Context, contract PredictionContract) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.urls, nil
}

func TestPredictionAgentRetriesFetchAndAudits(t *testing.T) {
	client := &fakeLLMClient{response: `{"result_type":"outcome","outcome_id":"a","result":"Team A 101, Team B 98","reason":"Team A won"}`}
	contract := predictionContractForResultServer("https://example.com")
	fetcher := &retryPredictionFetcher{failures: 1, result: PredictionResultFetchResult{
		FinalURL: "https://example.com/match/result/123",
		Text:     "Team A vs Team B Final: Team A 101, Team B 98.",
	}}
	events := make([]PredictionAgentAuditEvent, 0)
	corenodeAgent := NewPredictionAgent(client)
	corenodeAgent.Fetcher = fetcher
	corenodeAgent.RetryAttempts = 2
	corenodeAgent.RetryBackoff = time.Nanosecond
	corenodeAgent.Audit = func(event PredictionAgentAuditEvent) {
		events = append(events, event)
	}
	param, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("BuildConfirmParam failed: %v", err)
	}
	if param.OutcomeID != "a" || fetcher.calls != 2 {
		t.Fatalf("unexpected result outcome=%s fetch_calls=%d", param.OutcomeID, fetcher.calls)
	}
	if !auditStageSeen(events, "fetch_error") || !auditStageSeen(events, "fetch_ok") || !auditStageSeen(events, "llm_decision") {
		t.Fatalf("missing audit events: %#v", events)
	}
}

func TestPredictionAgentRetriesLLMAndAudits(t *testing.T) {
	client := &retryLLMClient{
		failures: 1,
		response: `{"result_type":"outcome","outcome_id":"a","result":"Team A 101, Team B 98","reason":"Team A won"}`,
	}
	contract := predictionContractForResultServer("https://example.com")
	corenodeAgent := NewPredictionAgent(client)
	corenodeAgent.Fetcher = &retryPredictionFetcher{result: PredictionResultFetchResult{
		FinalURL: "https://example.com/match/result/123",
		Text:     "Team A vs Team B Final: Team A 101, Team B 98.",
	}}
	corenodeAgent.RetryAttempts = 2
	corenodeAgent.RetryBackoff = time.Nanosecond
	events := make([]PredictionAgentAuditEvent, 0)
	corenodeAgent.Audit = func(event PredictionAgentAuditEvent) {
		events = append(events, event)
	}

	param, err := corenodeAgent.BuildConfirmParam(context.Background(), PredictionAgentConfirmRequest{
		Contract:   contract,
		ResultURL:  "https://example.com/match/result/123",
		ObservedAt: contract.ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("BuildConfirmParam failed: %v", err)
	}
	if param.OutcomeID != "a" || client.calls != 2 {
		t.Fatalf("unexpected result outcome=%s llm_calls=%d", param.OutcomeID, client.calls)
	}
	if !auditStageSeen(events, "llm_error") || !auditStageSeen(events, "llm_decision") {
		t.Fatalf("missing llm audit events: %#v", events)
	}
}

func TestHTTPPredictionResultFetcherRejectsOversizedResult(t *testing.T) {
	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer resultServer.Close()

	fetcher := HTTPPredictionResultTextFetcher{MaxBytes: 3}
	_, err := fetcher.FetchPredictionResult(context.Background(), resultServer.URL)
	if err == nil {
		t.Fatalf("expected oversized result error")
	}
}

type retryPredictionFetcher struct {
	failures int
	calls    int
	result   PredictionResultFetchResult
}

func (f *retryPredictionFetcher) FetchPredictionResult(ctx context.Context, resultURL string) (PredictionResultFetchResult, error) {
	f.calls++
	if f.calls <= f.failures {
		return PredictionResultFetchResult{}, errors.New("temporary fetch failure")
	}
	return f.result, nil
}

type retryLLMClient struct {
	failures int
	calls    int
	response string
}

func (c *retryLLMClient) Complete(ctx context.Context, req LLMCompletionRequest) (LLMCompletionResponse, error) {
	c.calls++
	if c.calls <= c.failures {
		return LLMCompletionResponse{}, errors.New("temporary llm failure")
	}
	return LLMCompletionResponse{Content: c.response}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func auditStageSeen(events []PredictionAgentAuditEvent, stage string) bool {
	for _, event := range events {
		if event.Stage == stage {
			return true
		}
	}
	return false
}

func predictionContractForResultServer(serverURL string) PredictionContract {
	contract := validPredictionContract()
	contract.Title = "Team A vs Team B"
	contract.Description = "Predict the Team A vs Team B result"
	contract.SourceURL = serverURL + "/match/preview/123"
	return contract
}

func testAgentDeployTxForContract(t *testing.T, contract PredictionContract) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	return testAgentDeployTxForContractWithNonce(t, contract, 7)
}

func testAgentDeployTxForContractWithNonce(t *testing.T, contract PredictionContract, nonce uint64) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	deployer := "deployer"
	deploy := DeployPayload{
		GasLimit:        DefaultGasConfig().DeployBaseGas,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     nonce,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, deploy.SubType, deploy.ContractContent, deployer, deploy.DeployNonce)
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
