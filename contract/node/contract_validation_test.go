package node

import (
	"strings"
	"testing"
	"time"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestCompositeContractBlockActivityRoutesResultByCurrentBlockSpentContractUTXO(t *testing.T) {
	cases := []struct {
		name         string
		contractType byte
		want         contractBlockActivity
	}{
		{name: "template", contractType: contractcommon.ContractTypeTemplate, want: contractBlockActivity{Template: true}},
		{name: "evm", contractType: contractcommon.ContractTypeEVM, want: contractBlockActivity{EVM: true}},
		{name: "agent", contractType: contractcommon.ContractTypeAgent, want: contractBlockActivity{Agent: true}},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prevOut := wire.OutPoint{Hash: chainhash.Hash{byte(i + 1)}, Index: 0}
			view := blockchain.NewUtxoViewpoint()
			view.Entries()[prevOut] = blockchain.NewUtxoEntry(testContractTxOut(t, tc.contractType), 1, false)
			block := btcutil.NewBlock(&wire.MsgBlock{
				Transactions: []*wire.MsgTx{testEVMCoinbaseTx(), testCommonResultTx(t, prevOut)},
			})

			activity, err := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
				ChainParams: &chaincfg.TestNetParams,
			}).blockActivity(block, view)
			if err != nil {
				t.Fatalf("blockActivity failed: %v", err)
			}
			if activity != tc.want {
				t.Fatalf("activity mismatch: got %+v want %+v", activity, tc.want)
			}
		})
	}
}

func TestCompositeContractBlockActivityRejectsResultWithoutContractInput(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{9}, Index: 0}
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[prevOut] = blockchain.NewUtxoEntry(wire.NewTxOut(1, nil, []byte{0x51}), 1, false)
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx(), testCommonResultTx(t, prevOut)},
	})

	_, err := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams: &chaincfg.TestNetParams,
	}).blockActivity(block, view)
	if err == nil || !strings.Contains(err.Error(), "spends no contract UTXO") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompositeContractBlockActivityRoutesResultBySpentContractUTXO(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{8}, Index: 0}
	entry := blockchain.NewUtxoEntry(testContractTxOut(t, contractcommon.ContractTypeTemplate), 1, false)
	entry.Spend()
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[prevOut] = entry
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx(), testCommonResultTx(t, prevOut)},
	})

	activity, err := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams: &chaincfg.TestNetParams,
	}).blockActivity(block, view)
	if err != nil {
		t.Fatalf("blockActivity failed: %v", err)
	}
	if !activity.Template || activity.EVM || activity.Agent {
		t.Fatalf("activity mismatch: got %+v", activity)
	}
}

func TestCompositeContractBlockActivityIncludesDueEVMTrigger(t *testing.T) {
	contract := testContractAddressForBlockchain(t)
	runtime := evm.NewRuntime(nil)
	runtime.SetCode(evm.ContractAddressHash(contract), []byte{0x00})
	if err := runtime.State.RegisterTrigger(evm.Trigger{
		ID:       "height-trigger",
		Contract: contract,
		Kind:     evm.TriggerAtHeight,
		Height:   100,
		GasLimit: evm.DefaultGasConfig().TriggerBaseGas,
	}); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: time.Unix(1710000000, 0)},
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx()},
	})
	block.SetHeight(100)

	activity, err := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams: &chaincfg.TestNetParams,
		EVMValidator: NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{
			NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*evm.Runtime, error) {
				return runtime.Clone(), nil
			},
		}),
	}).blockActivity(block, blockchain.NewUtxoViewpoint())
	if err != nil {
		t.Fatalf("blockActivity failed: %v", err)
	}
	if !activity.EVM || activity.Template || activity.Agent {
		t.Fatalf("activity mismatch: got %+v", activity)
	}
}

func TestCompositeContractBlockActivityIncludesDueAgentHeightTrigger(t *testing.T) {
	store := testReadyAgentRuntimeStore(t, agent.TimeBaseHeight, 10, 30)
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: time.Unix(1710000000, 0)},
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx()},
	})
	block.SetHeight(11)

	activity, err := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		ChainParams: &chaincfg.TestNetParams,
		AgentValidator: NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
			NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error) {
				return store.Clone(), nil
			},
		}),
	}).blockActivity(block, blockchain.NewUtxoViewpoint())
	if err != nil {
		t.Fatalf("blockActivity failed: %v", err)
	}
	if !activity.Agent || activity.Template || activity.EVM {
		t.Fatalf("activity mismatch: got %+v", activity)
	}
}

func TestTemplateBlockActivityIncludesDueAutopayTrigger(t *testing.T) {
	gasConfig := testNodeAutopayGasConfig()
	runtime := testNodeAutopayRuntime(t, "recipient-address", "ordx:f:test", "10")
	contractAddr := runtime.Address()
	if err := runtime.ApplyFunding(testNodeContractOutput("fund", 0, contractAddr, 0,
		testNodeAssets("ordx:f:test", 20, gasConfig.GasAssetName, 100)), gasConfig.GasAssetName); err != nil {
		t.Fatalf("apply autopay funding: %v", err)
	}
	if _, err := runtime.SettleBlockWithGasConfig(100, gasConfig); err != nil {
		t.Fatalf("settle autopay activation: %v", err)
	}
	store := template.NewRuntimeStore()
	store.Add(runtime)
	before := store.StateRoot()

	validator := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{
		ChainParams: &chaincfg.TestNetParams,
		GasConfig:   gasConfig,
		NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*template.RuntimeStore, error) {
			return store.Clone(), nil
		},
	})
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: time.Unix(1710000000, 0)},
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx()},
	})
	block.SetHeight(101)

	active, err := validator.HasContractBlockActivity(block, blockchain.NewUtxoViewpoint())
	if err != nil {
		t.Fatalf("template block activity failed: %v", err)
	}
	if !active {
		t.Fatalf("expected due autopay trigger to mark template active")
	}
	if store.StateRoot() != before {
		t.Fatalf("activity probe mutated parent store")
	}
}

func TestCompositeContractStateRootUsesInactiveParentRoots(t *testing.T) {
	templateRoot := testHashRoot(0x11)
	evmRoot := testHashRoot(0x22)
	agentRoot := testHashRoot(0x33)
	expected := contractcommon.CombineStateRoots(templateRoot, evmRoot, agentRoot)

	coinbase := testEVMCoinbaseTx()
	if err := contractengine.UpsertCoinbaseStateRoot(coinbase, expected); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase},
	})

	validator := NewCompositeContractBlockValidator(CompositeContractBlockValidatorConfig{
		TemplateValidator: testRootValidator{parentRoot: templateRoot},
		EVMValidator:      testRootValidator{postRoot: evmRoot},
		AgentValidator:    testRootValidator{parentRoot: agentRoot},
	})
	if err := validator.verifyCombinedStateRoot(block, blockchain.NewUtxoViewpoint(), false, true, false); err != nil {
		t.Fatalf("verifyCombinedStateRoot failed: %v", err)
	}
}

func TestAgentValidatorRejectsMissingRootForDueHeightTrigger(t *testing.T) {
	store := testReadyAgentRuntimeStore(t, agent.TimeBaseHeight, 10, 30)
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: time.Unix(1710000000, 0)},
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx()},
	})
	block.SetHeight(11)

	err := NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
		ChainParams: &chaincfg.TestNetParams,
		NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error) {
			return store.Clone(), nil
		},
	}).ValidateAgentBlock(block, blockchain.NewUtxoViewpoint())
	if err == nil || !strings.Contains(err.Error(), "missing agent state root commitment") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentValidatorAdvancesDueHeightTriggerWithRoot(t *testing.T) {
	store := testReadyAgentRuntimeStore(t, agent.TimeBaseHeight, 10, 30)
	executed, err := agent.ExecuteBlock(agent.BlockExecutionRequest{
		Store:       store.Clone(),
		BlockHeight: 11,
		BlockTime:   time.Unix(1710000000, 0).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	coinbase := testEVMCoinbaseTx()
	if err := contractengine.UpsertCoinbaseStateRoot(coinbase, executed.StateRoot); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: time.Unix(1710000000, 0)},
		Transactions: []*wire.MsgTx{coinbase},
	})
	block.SetHeight(11)

	validator := NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
		ChainParams: &chaincfg.TestNetParams,
		NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*agent.RuntimeStore, error) {
			return store.Clone(), nil
		},
	})
	if err := validator.ValidateAgentBlock(block, blockchain.NewUtxoViewpoint()); err != nil {
		t.Fatal(err)
	}
	postState, ok := validator.AgentBlockPostState(block.Hash())
	if !ok {
		t.Fatal("expected agent validator to expose post-state")
	}
	snapshots, err := postState.Snapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].State.Prediction.Status != agent.PredictionStatusClosedForBet {
		t.Fatalf("unexpected post-state snapshots: %#v", snapshots)
	}
}

func TestPreviousOutputScriptResolverAllowsCurrentBlockSpentEntry(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{7}, Index: 0}
	txOut := testContractTxOut(t, contractcommon.ContractTypeTemplate)
	entry := blockchain.NewUtxoEntry(txOut, 1, false)
	entry.Spend()
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[prevOut] = entry

	got, ok := previousOutputScriptResolver(view)(prevOut)
	if !ok {
		t.Fatalf("expected spent current-block entry to resolve")
	}
	if string(got) != string(txOut.PkScript) {
		t.Fatalf("script mismatch")
	}
}

func TestTemplateValidatorRejectsUnexpectedResultWithoutPlan(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{3}, Index: 0}
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[prevOut] = blockchain.NewUtxoEntry(testContractTxOut(t, contractcommon.ContractTypeTemplate), 1, false)
	coinbase := testEVMCoinbaseTx()
	if err := contractengine.UpsertCoinbaseStateRoot(coinbase, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase, testCommonResultTx(t, prevOut)},
	})
	block.SetHeight(100)

	err := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{
		ChainParams:         &chaincfg.TestNetParams,
		SkipStateRootVerify: true,
	}).ValidateTemplateBlock(block, view)
	if err == nil || !strings.Contains(err.Error(), "unexpected template RESULT transaction") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAgentValidatorRejectsUnexpectedResultWithoutPlan(t *testing.T) {
	prevOut := wire.OutPoint{Hash: chainhash.Hash{4}, Index: 0}
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[prevOut] = blockchain.NewUtxoEntry(testContractTxOut(t, contractcommon.ContractTypeAgent), 1, false)
	coinbase := testEVMCoinbaseTx()
	if err := contractengine.UpsertCoinbaseStateRoot(coinbase, [32]byte{}); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{coinbase, testCommonResultTx(t, prevOut)},
	})
	block.SetHeight(100)

	err := NewAgentBlockExecutionValidator(AgentBlockExecutionConfig{
		ChainParams:         &chaincfg.TestNetParams,
		SkipStateRootVerify: true,
	}).ValidateAgentBlock(block, view)
	if err == nil || !strings.Contains(err.Error(), "unexpected agent RESULT transaction") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type testRootValidator struct {
	parentRoot [32]byte
	postRoot   [32]byte
}

func (v testRootValidator) ValidateContractModuleBlock(*btcutil.Block, *blockchain.UtxoViewpoint) error {
	return nil
}

func (v testRootValidator) ParentState(*btcutil.Block,
	*blockchain.UtxoViewpoint) (contractframework.RuntimeStore, bool, error) {

	return contractframework.RootEngineState{StateRoot: v.parentRoot}, true, nil
}

func (v testRootValidator) BlockPostState(*chainhash.Hash) (contractframework.RuntimeStore, bool) {
	return contractframework.RootEngineState{StateRoot: v.postRoot}, true
}

func testHashRoot(value byte) [32]byte {
	var root [32]byte
	for i := range root {
		root[i] = value
	}
	return root
}

func testNodeAutopayRuntime(t *testing.T, recipient, feeAsset, minAmount string) *template.ContractRuntime {
	t.Helper()
	contract := template.NewAutopayContract("dkvs", recipient, feeAsset, minAmount)
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("encode autopay contract: %v", err)
	}
	deploy := template.DeployPayload{
		GasLimit:        1000,
		SubType:         template.TemplateAutopay,
		Version:         template.CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := template.DeriveContractAddress(contractcommon.TestnetContractPrefix,
		deploy.ContractContent, "deployer-address", deploy.DeployNonce)
	if err != nil {
		t.Fatalf("derive autopay address: %v", err)
	}
	runtime, err := template.NewRuntimeWithDeployer(addr, deploy, template.NewDefaultRegistry(), "deployer-address")
	if err != nil {
		t.Fatalf("new autopay runtime: %v", err)
	}
	return runtime
}

func testNodeAutopayGasConfig() template.GasConfig {
	cfg := template.DefaultGasConfig()
	cfg.GasAssetName = "ordx:f:gas"
	cfg.DeployBaseGas = 1
	cfg.InvokeBaseGas = 1
	cfg.ResultBaseGas = 1
	cfg.TriggerBaseGas = 1
	return cfg
}

func testNodeAssets(assetNameA string, amountA int64, assetNameB string, amountB int64) wire.TxAssets {
	assets := testNodeAsset(assetNameA, amountA)
	if err := assets.Merge(testNodeAsset(assetNameB, amountB)); err != nil {
		panic(err)
	}
	return assets
}

func testNodeAsset(assetName string, amount int64) wire.TxAssets {
	return wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(assetName),
		Amount: *scommon.NewDefaultDecimal(amount),
	}}
}

func testNodeContractOutput(txid string, vout uint32, contractAddr template.ContractAddress,
	value int64, assets wire.TxAssets) template.ContractOutput {

	return contractframework.ContractOutputFromFunding(contractcommon.FundingOutput{
		OutPoint: contractcommon.TxOutPoint{TxID: txid, Vout: vout},
		Vout:     vout,
		Contract: contractAddr,
		Value:    value,
		Assets:   assets,
	})
}

func testReadyAgentRuntimeStore(t *testing.T, timeBase string, betDeadline, confirmAfter int64) *agent.RuntimeStore {
	t.Helper()
	contract := agent.PredictionContract{
		Subtype:      agent.SubtypePrediction,
		Title:        "height prediction",
		Description:  "test prediction",
		TimeBase:     timeBase,
		EventTime:    confirmAfter - 1,
		BetDeadline:  betDeadline,
		ConfirmAfter: confirmAfter,
		SourceURL:    "https://example.com/match",
		BetAsset:     agent.SatoshiAssetName,
		MinBetUnit:   "1000",
		Outcomes: []agent.PredictionOutcome{
			{ID: "a", Text: "home wins"},
			{ID: "b", Text: "away wins"},
		},
	}
	content, err := contract.Encode()
	if err != nil {
		t.Fatal(err)
	}
	deployer := "deployer"
	deploy := agent.DeployPayload{
		GasLimit:        1000,
		SubType:         agent.SubtypePrediction,
		Version:         agent.CurrentAgentVersion,
		DeployNonce:     3,
		ContractContent: content,
	}
	addr, _, err := agent.DeriveContractAddress(
		agent.TestnetContractPrefix, deploy.SubType, content, deployer, deploy.DeployNonce)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := agent.NewRuntimeWithDeployer(addr, deploy, agent.RuntimeConfig{CoreNodeAddress: "core"}, deployer)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ApplyReady(agent.ApplyReadyRequest{Invoker: "core"}); err != nil {
		t.Fatal(err)
	}
	store := agent.NewRuntimeStore()
	store.Add(runtime)
	return store
}

func testCommonResultTx(t *testing.T, prevOut wire.OutPoint) *wire.MsgTx {
	t.Helper()
	script, err := contractcommon.ResultNullDataScript(contractcommon.ResultPayload{
		Status:      contractcommon.ResultStatusSuccess,
		ResultCount: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&prevOut, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx
}

func testContractTxOut(t *testing.T, contractType byte) *wire.TxOut {
	t.Helper()
	contract, err := contractcommon.NewContractAddressFromHash(
		contractcommon.TestnetContractPrefix, contractcommon.AddressVersionV1,
		contractType, testBytes(20, contractType))
	if err != nil {
		t.Fatal(err)
	}
	switch contractType {
	case contractcommon.ContractTypeTemplate:
		txOut, err := template.NewContractTxOut(1, nil, contract)
		if err != nil {
			t.Fatal(err)
		}
		return txOut
	case contractcommon.ContractTypeAgent:
		txOut, err := agent.NewContractTxOut(1, nil, contract)
		if err != nil {
			t.Fatal(err)
		}
		return txOut
	default:
		script, err := contractcommon.ContractPkScript(contract)
		if err != nil {
			t.Fatal(err)
		}
		return wire.NewTxOut(1, nil, script)
	}
}
