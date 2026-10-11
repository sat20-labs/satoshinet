package contract_e2e

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	localwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Reuse the channel-funded, two-node fixture. These scenarios use signed BIP86
// inputs rather than the legacy OP_DROP/OP_TRUE scripts, including all rejects.
func newSignedTemplateFixture(t *testing.T, assets map[string]int64) *templateNetworkFixture {
	t.Helper()
	f := newTemplateNetworkFixture(t, assets)
	f.traderAActor = signedTemplateActor(t, f.traderA)
	f.traderBActor = signedTemplateActor(t, f.traderB)
	f.splitAssetTo(t, f.gasAnchor, tmplcontract.DefaultGasConfig().GasAssetName,
		[]int64{40000000, 40000000}, []int64{50000, 50000}, f.traderA,
		[]*templateNetworkActor{f.traderAActor, f.traderBActor})
	for _, name := range sortedTemplateAssets(assets) {
		f.splitAssetTo(t, f.assetAnchors[name], name,
			[]int64{assets[name] / 2, assets[name] / 2}, []int64{50000, 50000}, f.traderA,
			[]*templateNetworkActor{f.traderAActor, f.traderBActor})
	}
	return f
}

func signedTemplateActor(t *testing.T, key *btcec.PrivateKey) *templateNetworkActor {
	t.Helper()
	actor := &templateNetworkActor{key: key, address: testTaprootAddress(t, key)}
	address, err := btcutil.DecodeAddress(actor.address, &chaincfg.TestNetParams)
	require.NoError(t, err)
	actor.pkScript, err = txscript.PayToAddrScript(address)
	require.NoError(t, err)
	return actor
}

func scenarioFunding(t *testing.T, value int64, assets map[string]int64) wire.TxOut {
	t.Helper()
	funding := wire.TxOut{Value: value}
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	if _, exists := assets[gas]; !exists {
		funding.Assets = append(funding.Assets, networkTemplateFunding(t, gas, 1000))
	}
	for _, name := range sortedTemplateAssets(assets) {
		if assets[name] > 0 {
			require.NoError(t, funding.Assets.Add(assetInfoPtr(networkTemplateFunding(t, name, assets[name]))))
		}
	}
	return funding
}

func assetInfoPtr(asset wire.AssetInfo) *wire.AssetInfo { return &asset }

func scenarioSelectFunding(t *testing.T, f *templateNetworkFixture, actor *templateNetworkActor,
	funding wire.TxOut, baseGas int64) []wire.OutPoint {
	t.Helper()
	required := funding
	required.Assets = funding.Assets.Clone()
	height, err := f.bootstrapNode.Client.GetBlockCount()
	require.NoError(t, err)
	fee, err := contractcommon.GasFeeDecimalAtHeight(baseGas, uint64(height+1))
	require.NoError(t, err)
	gas := networkTemplateFunding(t, tmplcontract.DefaultGasConfig().GasAssetName, 1)
	gas.Amount = *fee
	require.NoError(t, required.Assets.Add(&gas))
	return f.selectFundingOutPoints(t, actor, required)
}

func scenarioDeploy(t *testing.T, f *templateNetworkFixture, contract tmplcontract.Contract,
	seed string, flags contractcommon.ContractFlags, funding wire.TxOut) tmplcontract.ContractAddress {
	t.Helper()
	inputs := scenarioSelectFunding(t, f, f.traderAActor, funding, contractcommon.DeployBaseGas)
	tx, address, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: tmplcontract.TestnetContractPrefix, Contract: contract,
		Deployer: f.traderAActor.address, DeployNonce: deployNonceFromBytes([]byte(seed)),
		Flags: flags, GasLimit: networkTemplateDeployGasLimit(), Inputs: inputs, Funding: funding,
	})
	require.NoError(t, err)
	signScenarioInputsWithChange(t, f, tx, f.traderA, contractcommon.DeployBaseGas, 0)
	f.sendAndWaitTx(t, tx)
	requireScenarioResult(t, f, tx, address)
	return address
}

func scenarioInvoke(t *testing.T, f *templateNetworkFixture, signer *btcec.PrivateKey,
	address tmplcontract.ContractAddress, action string, param []byte, funding wire.TxOut) []tmplcontract.ResultOutput {
	t.Helper()
	actor := f.actorForSigner(t, signer)
	inputs := scenarioSelectFunding(t, f, actor, funding, contractcommon.InvokeBaseGas)
	var tx *wire.MsgTx
	var err error
	if action == "" {
		pkScript, scriptErr := contractcommon.ContractPkScript(address)
		require.NoError(t, scriptErr)
		funding.PkScript = pkScript
		tx = wire.NewMsgTx(wire.TxVersion)
		for _, input := range inputs {
			tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
		}
		tx.AddTxOut(&funding)
	} else {
		tx, err = tmplcontract.BuildInvokeTx(tmplcontract.InvokeTxBuildRequest{
			Contract: address, GasLimit: networkTemplateInvokeGasLimit(), CallNonce: uint64(time.Now().UnixNano()),
			Action: action, Param: param, Inputs: inputs, Funding: funding,
		})
		require.NoError(t, err)
	}
	signScenarioInputsWithChange(t, f, tx, signer, contractcommon.InvokeBaseGas, 0)
	f.sendAndWaitTx(t, tx)
	return requireScenarioResult(t, f, tx, address)
}

// Preserve input assets and satoshis in change after fees, sign each input, and
// verify its script before broadcasting. Plain transfers also pay a relay fee.
func signScenarioInputsWithChange(t *testing.T, f *templateNetworkFixture, tx *wire.MsgTx,
	signer *btcec.PrivateKey, baseGas, feeSats int64) {
	t.Helper()
	actor := f.actorForSigner(t, signer)
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	var assets wire.TxAssets
	var value int64
	for _, input := range tx.TxIn {
		previous, err := f.bootstrapNode.Client.GetRawTransaction(&input.PreviousOutPoint.Hash)
		require.NoError(t, err)
		output := previous.MsgTx().TxOut[input.PreviousOutPoint.Index]
		require.Equal(t, actor.pkScript, output.PkScript, "scenario input must belong to the signer")
		fetcher.AddPrevOut(input.PreviousOutPoint, output)
		value += output.Value
		require.NoError(t, assets.Merge(output.Assets))
	}
	for _, output := range tx.TxOut {
		value -= output.Value
		require.NoError(t, assets.Split(output.Assets))
	}
	height, err := f.bootstrapNode.Client.GetBlockCount()
	require.NoError(t, err)
	fee, err := contractcommon.GasFeeDecimalAtHeight(baseGas, uint64(height+1))
	require.NoError(t, err)
	gasAsset := networkTemplateFunding(t, tmplcontract.DefaultGasConfig().GasAssetName, 1)
	gasAsset.Amount = *fee
	require.NoError(t, assets.Subtract(&gasAsset))
	require.GreaterOrEqual(t, feeSats, int64(0))
	value -= feeSats
	require.GreaterOrEqual(t, value, int64(0))
	if value > 0 || !assets.IsZero() {
		tx.AddTxOut(wire.NewTxOut(value, assets, actor.pkScript))
	}
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	for i, input := range tx.TxIn {
		previous := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		input.SignatureScript = nil
		input.Witness, err = txscript.TaprootWitnessSignature(tx, hashes, i, previous.Value, previous.Assets,
			previous.PkScript, txscript.SigHashDefault, signer)
		require.NoError(t, err)
		engine, err := txscript.NewEngine(previous.PkScript, tx, i, txscript.StandardVerifyFlags,
			nil, hashes, previous.Value, previous.Assets, fetcher)
		require.NoError(t, err)
		require.NoError(t, engine.Execute(), "scenario input %d must have a valid signature", i)
	}
}

func requireScenarioResult(t *testing.T, f *templateNetworkFixture, tx *wire.MsgTx,
	address tmplcontract.ContractAddress) []tmplcontract.ResultOutput {
	t.Helper()
	result := requireTemplateResultForTx(t, f.bootstrapNode, tx, address)
	payload, err := contractframework.ResultPayloadFromTx(result, "template")
	require.NoError(t, err)
	require.Equal(t, tmplcontract.ResultStatusSuccess, payload.Status, "block settlement must succeed; action acceptance is asserted separately")
	require.Positive(t, payload.ResultCount)
	return templateResultOutputsForTx(t, f.bootstrapNode, tx, address)
}

func scenarioState(t *testing.T, node *rpctest.Harness, address tmplcontract.ContractAddress) map[string]interface{} {
	t.Helper()
	param, err := json.Marshal(address.MustEncode())
	require.NoError(t, err)
	raw, err := node.Client.RawRequest("getcontractstate", []json.RawMessage{param})
	require.NoError(t, err)
	var response struct {
		State   map[string]interface{} `json:"state"`
		Details map[string]interface{} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(raw, &response))
	require.NotEmpty(t, response.State)
	if exists, ok := response.Details["exists"].(bool); ok {
		require.True(t, exists)
	}
	return response.State
}

func scenarioAMMView(t *testing.T, f *templateNetworkFixture, address tmplcontract.ContractAddress) tmplcontract.AMMStateView {
	t.Helper()
	raw, err := json.Marshal(scenarioState(t, f.bootstrapNode, address))
	require.NoError(t, err)
	var view tmplcontract.AMMStateView
	require.NoError(t, json.Unmarshal(raw, &view))
	return view
}

func scenarioAutopayView(t *testing.T, node *rpctest.Harness, address tmplcontract.ContractAddress) tmplcontract.AutopayStateView {
	t.Helper()
	raw, err := json.Marshal(scenarioState(t, node, address))
	require.NoError(t, err)
	var view tmplcontract.AutopayStateView
	require.NoError(t, json.Unmarshal(raw, &view))
	return view
}

func requireScenarioQueries(t *testing.T, f *templateNetworkFixture, address tmplcontract.ContractAddress, name string) {
	t.Helper()
	base, err := f.bootstrapNode.IndexerURL("testnet")
	require.NoError(t, err)
	var list localwire.ContractListResp
	require.NoError(t, getIndexerJSON(base+"/v3/contracts", &list))
	require.Zero(t, list.Code)
	found := false
	for _, row := range list.Data {
		if row.Address == address.MustEncode() {
			found = true
			require.Equal(t, name, row.Subtype)
		}
	}
	require.True(t, found, "template must be discoverable through the contract list")
	var history localwire.ContractHistoryResp
	require.NoError(t, getIndexerJSON(base+"/v3/contracts/"+url.PathEscape(address.MustEncode())+"/history", &history))
	require.Zero(t, history.Code)
	var deploys, invokes int
	for _, row := range history.Data {
		require.Equal(t, address.MustEncode(), row.Contract)
		if row.Kind == "deploy" {
			deploys++
		}
		if row.Kind == "invoke" {
			invokes++
		}
	}
	require.Equal(t, 1, deploys)
	require.Positive(t, invokes, fmt.Sprintf("history=%+v", history.Data))
	state := scenarioState(t, f.bootstrapNode, address)
	require.Equal(t, name, state["templateName"])
	require.Equal(t, f.traderAActor.address, state["deployer"])
}

// POS deliberately does not mine an empty mempool. Advance height with ordinary
// signed transactions so deferred settlement and autopay execute in real blocks.
func scenarioMineBlocks(t *testing.T, f *templateNetworkFixture, count int) {
	t.Helper()
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	for i := 0; i < count; i++ {
		inputs := f.selectFundingOutPoints(t, f.traderAActor, scenarioFunding(t, 1000, map[string]int64{gas: 2000}))
		tx := wire.NewMsgTx(wire.TxVersion)
		for _, input := range inputs {
			tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
		}
		tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{networkTemplateFunding(t, gas, 1000)}, f.traderAActor.pkScript))
		signScenarioInputsWithChange(t, f, tx, f.traderA, 1000000, 1000)
		f.sendAndWaitTx(t, tx)
	}
}

// Read the restarted process before reconnecting, so peer synchronization
// cannot hide incomplete recovery of its local contract snapshot.
func requireContractStateAfterCoreRestart(t *testing.T, core, bootstrap *rpctest.Harness, nodes []*rpctest.Harness, addresses ...string) {
	t.Helper()
	before := make([]json.RawMessage, len(addresses))
	for i, address := range addresses {
		param, err := json.Marshal(address)
		require.NoError(t, err)
		before[i], err = core.Client.RawRequest("getcontractstate", []json.RawMessage{param})
		require.NoError(t, err)
	}
	require.NoError(t, core.Restart(false))
	for i, address := range addresses {
		param, err := json.Marshal(address)
		require.NoError(t, err)
		after, err := core.Client.RawRequest("getcontractstate", []json.RawMessage{param})
		require.NoError(t, err)
		require.JSONEq(t, string(before[i]), string(after), "cold core state: %s", address)
	}
	require.NoError(t, rpctest.ConnectNode(core, bootstrap))
	require.NoError(t, rpctest.JoinNodes(nodes, rpctest.Blocks))
}
