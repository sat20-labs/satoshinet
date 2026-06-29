package node

import (
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/agent"
	"github.com/sat20-labs/satoshinet/contract/evm"
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
