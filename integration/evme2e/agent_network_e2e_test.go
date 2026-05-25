//go:build rpctest
// +build rpctest

package evme2e

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestNetworkAgentPredictionAutoConfirm(t *testing.T) {
	if os.Getenv("SATOSHINET_AGENT_NETWORK_E2E") != "1" {
		t.Skip("set SATOSHINET_AGENT_NETWORK_E2E=1 to run the Agent prediction network E2E")
	}

	configureFastPOSTimers(t)
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/match/result/123", r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>Final</h1><p>Team A 101, Team B 98.</p></body></html>`))
	}))
	defer resultServer.Close()

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		var req map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"result_type\":\"outcome\",\"outcome_id\":\"a\",\"reason\":\"Team A won\"}"}}]}`))
	}))
	defer llmServer.Close()

	const lockedValue = int64(200000)
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	traderA := keyFromMnemonic(t, bootstrapMnemonic, 1)
	traderB := keyFromMnemonic(t, bootstrapMnemonic, 2)
	spendScript, _, redeemScript, controlBlock := testCallerTaprootScript(t, bootstrapKey)
	coreFundingScript := p2trPkScriptFromKey(t, coreKey)
	bootstrapAddress := p2trAddressFromKey(t, bootstrapKey)
	aliceAddress := p2trAddressFromKey(t, traderA)

	witnessScript, lockedPkScript, err := anchortx.GetP2WSHscript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)

	lockedUtxo := templateLockedOutPoint("agent-gas", 0)
	fakeL1 := startFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()),
		map[string]*indexercommon.AssetsInUtxo{
			lockedUtxo: {
				OutPoint: lockedUtxo,
				Value:    lockedValue,
				PkScript: lockedPkScript,
				Assets: []*indexercommon.DisplayAsset{
					testDisplayAsset(gasAsset, "140000"),
				},
			},
		})
	bootstrapNode, coreNode := startAgentSatoshiNetNetwork(t, fakeL1, llmServer.URL)
	nodes := []*rpctest.Harness{bootstrapNode, coreNode}

	anchorTx := buildNetworkAnchorTx(t, lockedUtxo, lockedValue,
		testWireAsset(gasAsset, 140000), gasAsset+"-140000-0-1",
		witnessScript, bootstrapKey, spendScript)
	sendTx(t, bootstrapNode, anchorTx)
	waitForPOSTx(t, bootstrapNode, nodes, anchorTx)

	splitOutputs := []*wire.TxOut{
		wire.NewTxOut(1000, testWireAsset(gasAsset, 10000), spendScript),
		wire.NewTxOut(1000, testWireAsset(gasAsset, 10000), spendScript),
		wire.NewTxOut(1000, testWireAsset(gasAsset, 60000), spendScript),
		wire.NewTxOut(1000, testWireAsset(gasAsset, 40000), spendScript),
		wire.NewTxOut(20000, nil, coreFundingScript),
		wire.NewTxOut(10000, nil, spendScript),
		wire.NewTxOut(10000, nil, spendScript),
	}
	splitTx := buildTemplateSplitTx(t, bootstrapKey, wire.OutPoint{Hash: anchorTx.TxHash(), Index: 0}, splitOutputs)
	signTemplateTaprootInputs(t, splitTx, bootstrapKey, redeemScript, controlBlock)
	sendTx(t, bootstrapNode, splitTx)
	waitForPOSTx(t, bootstrapNode, nodes, splitTx)
	agentInputs := collectSpendableOutPoints(t, splitTx, splitOutputs)

	_, bestHeight, err := bootstrapNode.Client.GetBestBlock()
	require.NoError(t, err)
	contract := agentcontract.PredictionContract{
		Subtype:      agentcontract.SubtypePrediction,
		Title:        "Agent E2E basketball prediction",
		Description:  "Team A vs Team B final score",
		TimeBase:     agentcontract.TimeBaseHeight,
		BetDeadline:  int64(bestHeight) + 4,
		EventTime:    int64(bestHeight) + 5,
		ConfirmAfter: int64(bestHeight) + 6,
		SourceURL:    resultServer.URL + "/match/result/123",
		BetAsset:     gasAsset,
		MinBetUnit:   "10000",
		Outcomes: []agentcontract.PredictionOutcome{
			{ID: "a", Text: "Team A wins"},
			{ID: "b", Text: "Team B wins"},
		},
	}
	deployTx, agentAddress := buildAgentDeployTx(t, contract, bootstrapAddress, agentInputs[0], splitOutputs[0], spendScript)
	signTemplateTaprootInputs(t, deployTx, bootstrapKey, redeemScript, controlBlock)
	sendTx(t, bootstrapNode, deployTx)
	waitForPOSTx(t, bootstrapNode, nodes, deployTx)

	readyTx := buildAgentInvokeTx(t, agentAddress, agentcontract.InvokeAPIReady, nil,
		agentInputs[1], nil, splitOutputs[1], spendScript)
	signTemplateTaprootInputs(t, readyTx, coreKey, redeemScript, controlBlock)
	sendTx(t, bootstrapNode, readyTx)
	waitForPOSTx(t, bootstrapNode, nodes, readyTx)

	aliceBet := mustAgentBetParam(t, "a")
	aliceTx := buildAgentInvokeTx(t, agentAddress, agentcontract.InvokeAPIBet, aliceBet,
		agentInputs[2], testWireAsset(gasAsset, 60000), nil, spendScript)
	signTemplateTaprootInputs(t, aliceTx, traderA, redeemScript, controlBlock)
	sendTx(t, bootstrapNode, aliceTx)
	waitForPOSTx(t, bootstrapNode, nodes, aliceTx)

	bobBet := mustAgentBetParam(t, "b")
	bobTx := buildAgentInvokeTx(t, agentAddress, agentcontract.InvokeAPIBet, bobBet,
		agentInputs[3], testWireAsset(gasAsset, 40000), nil, spendScript)
	signTemplateTaprootInputs(t, bobTx, traderB, redeemScript, controlBlock)
	sendTx(t, bootstrapNode, bobTx)
	waitForPOSTx(t, bootstrapNode, nodes, bobTx)

	heartbeatTx := buildTemplateSplitTx(t, bootstrapKey, agentInputs[5], []*wire.TxOut{
		wire.NewTxOut(splitOutputs[5].Value-1000, nil, spendScript),
	})
	signTemplateTaprootInputs(t, heartbeatTx, bootstrapKey, redeemScript, controlBlock)
	sendTx(t, bootstrapNode, heartbeatTx)
	waitForPOSTx(t, bootstrapNode, nodes, heartbeatTx)

	heartbeatTx = buildTemplateSplitTx(t, bootstrapKey, agentInputs[6], []*wire.TxOut{
		wire.NewTxOut(splitOutputs[6].Value-1000, nil, spendScript),
	})
	signTemplateTaprootInputs(t, heartbeatTx, bootstrapKey, redeemScript, controlBlock)
	sendTx(t, bootstrapNode, heartbeatTx)
	waitForPOSTx(t, bootstrapNode, nodes, heartbeatTx)

	waitForAgentWinnerPayout(t, bootstrapNode, coreNode, nodes, aliceAddress, gasAsset, "90000", int32(contract.ConfirmAfter))
}

func startAgentSatoshiNetNetwork(t *testing.T, fakeL1 *httptest.Server, llmEndpoint string) (*rpctest.Harness, *rpctest.Harness) {
	t.Helper()
	bootstrapNode := startAgentSatoshiNetNode(t, fakeL1, "bootstrap", bootstrapMnemonic, nil)
	coreNode := startAgentSatoshiNetNode(t, fakeL1, "core", coreMnemonic, []string{
		"--agentllmprovider=openai",
		"--agentllmendpoint=" + llmEndpoint,
		"--agentllmmodel=fake-agent",
		"--agentcheckinterval=1s",
	})
	require.NoError(t, rpctest.ConnectNode(coreNode, bootstrapNode))
	require.NoError(t, rpctest.JoinNodes([]*rpctest.Harness{bootstrapNode, coreNode}, rpctest.Blocks))
	return bootstrapNode, coreNode
}

func startAgentSatoshiNetNode(t *testing.T, fakeL1 *httptest.Server, role, mnemonic string, extraArgs []string) *rpctest.Harness {
	t.Helper()
	nodeKey := keyFromMnemonic(t, mnemonic, 0)
	nodePubKey := hex.EncodeToString(nodeKey.PubKey().SerializeCompressed())
	btcdCfg := []string{
		"--notls",
		"--nocheckpoints",
		"--nodnsseed",
		"--indexerscheme=http",
		"--indexerhost=" + strings.TrimPrefix(fakeL1.URL, "http://"),
		"--indexerproxy=testnet",
		"--generate",
		"--miningpubkey=" + nodePubKey,
	}
	btcdCfg = append(btcdCfg, extraArgs...)
	env := []string{
		"SATOSHINET_RPCTEST_NODE_ROLE=" + role,
		"SATOSHINET_RPCTEST_STP_MNEMONIC=" + mnemonic,
		"SATOSHINET_RPCTEST_STP_PASSWORD=rpctest",
	}
	for _, name := range []string{
		"SATOSHINET_POS_MINER_INTERVAL",
		"SATOSHINET_POS_PREWARNING_INTERVAL",
		"SATOSHINET_POS_CHECKING_INTERVAL",
	} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	r, err := rpctest.NewWithEnv(&chaincfg.TestNetParams, nil, btcdCfg, "", env)
	require.NoError(t, err)
	r.MaxConnRetries = 200
	r.ConnectionRetryTimeout = 100 * time.Millisecond
	require.NoError(t, r.SetUp(false, 0))
	t.Cleanup(func() {
		require.NoError(t, r.TearDown())
	})
	return r
}

func buildAgentDeployTx(t *testing.T, contract agentcontract.PredictionContract, deployer string,
	input wire.OutPoint, inputOut *wire.TxOut, spendScript []byte) (*wire.MsgTx, agentcontract.ContractAddress) {

	t.Helper()
	content, err := contract.Encode()
	require.NoError(t, err)
	tx, address, err := agentcontract.BuildDeployTx(agentcontract.DeployTxBuildRequest{
		ContractPrefix:  agentcontract.TestnetContractPrefix,
		Subtype:         agentcontract.SubtypePrediction,
		AgentVersion:    agentcontract.CurrentAgentVersion,
		Deployer:        deployer,
		Random:          []byte("agent-network-e2e"),
		ContractContent: content,
		GasLimit:        100000,
		Inputs:          []wire.OutPoint{input},
		ChangeOutputs: []*wire.TxOut{
			wire.NewTxOut(inputOut.Value, inputOut.Assets.Clone(), spendScript),
		},
	})
	require.NoError(t, err)
	return tx, address
}

func buildAgentInvokeTx(t *testing.T, contract agentcontract.ContractAddress, action string, param []byte,
	input wire.OutPoint, fundingAssets wire.TxAssets, changeOut *wire.TxOut, spendScript []byte) *wire.MsgTx {

	t.Helper()
	var funding agentcontract.TxFunding
	if fundingAssets != nil {
		funding.Assets = []agentcontract.AssetAmount{{
			AssetName: fundingAssets[0].Name.String(),
			Amount:    fundingAssets[0].Amount.Clone(),
		}}
	}
	changeOutputs := []*wire.TxOut(nil)
	if changeOut != nil {
		changeOutputs = append(changeOutputs, wire.NewTxOut(changeOut.Value, changeOut.Assets.Clone(), spendScript))
	}
	tx, err := agentcontract.BuildInvokeTx(agentcontract.InvokeTxBuildRequest{
		Contract:      contract,
		GasLimit:      100000,
		CallNonce:     uint64(time.Now().UnixNano()),
		Action:        action,
		Param:         param,
		Funding:       funding,
		Inputs:        []wire.OutPoint{input},
		ChangeOutputs: changeOutputs,
	})
	require.NoError(t, err)
	return tx
}

func mustAgentBetParam(t *testing.T, outcome string) []byte {
	t.Helper()
	data, err := (agentcontract.PredictionBetParam{OutcomeID: outcome}).Encode()
	require.NoError(t, err)
	return data
}

func waitForAgentWinnerPayout(t *testing.T, bootstrapNode, coreNode *rpctest.Harness, nodes []*rpctest.Harness,
	address, assetName, amount string, minHeight int32) {

	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		_ = rpctest.JoinNodes(nodes, rpctest.Blocks)
		summary, err := fetchAssetSummary(bootstrapNode, address)
		if err == nil && summary[assetName] == amount {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	summary, err := fetchAssetSummary(bootstrapNode, address)
	require.NoError(t, err)
	_, height, heightErr := coreNode.Client.GetBestBlock()
	require.NoError(t, heightErr)
	require.GreaterOrEqual(t, height, minHeight)
	require.Equal(t, amount, summary[assetName], "address=%s asset=%s summary=%v", address, assetName, summary)
}

func p2trAddressFromKey(t *testing.T, key *btcec.PrivateKey) string {
	t.Helper()
	address, err := sindexercommon.PubKeyBytesToP2TRAddress(key.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	require.NoError(t, err)
	return address
}

func p2trPkScriptFromKey(t *testing.T, key *btcec.PrivateKey) []byte {
	t.Helper()
	tapKey := txscript.ComputeTaprootKeyNoScript(key.PubKey())
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(tapKey), &chaincfg.TestNetParams)
	require.NoError(t, err)
	pkScript, err := txscript.PayToAddrScript(addr)
	require.NoError(t, err)
	return pkScript
}
