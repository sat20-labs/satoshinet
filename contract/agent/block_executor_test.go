package agent

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestBlockExecutorPredictionE2EShape(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	betA := mustEncodeBet(t, "a")
	betBTxParam := mustEncodeBet(t, "b")
	confirm := mustEncodeConfirm(t, ResultTypeOutcome, "a")

	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, betA, 60000, nil)
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, betBTxParam, 40000, nil)
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, confirm, 0, nil)

	invokers := map[string]string{
		readyTx.TxID():    "core",
		aliceBetTx.TxID(): "alice",
		bobBetTx.TxID():   "bob",
		confirmTx.TxID():  "core",
	}
	store := NewRuntimeStore()
	first, err := ExecuteBlock(BlockExecutionRequest{
		Txs:            []*wire.MsgTx{deployTx, readyTx, aliceBetTx, bobBetTx},
		Store:          store,
		BlockHeight:    validPredictionContract().BetDeadline,
		RuntimeConfig:  testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(invokers),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if len(first.SettlementPlans) != 0 {
		t.Fatalf("unexpected first block settlement plans")
	}
	if len(first.ResultPlans) != 2 {
		t.Fatalf("first block result plan count mismatch: %d", len(first.ResultPlans))
	}
	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs:            []*wire.MsgTx{confirmTx},
		Store:          store,
		BlockHeight:    validPredictionContract().ConfirmAfter + 1,
		RuntimeConfig:  testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(invokers),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock confirm failed: %v", err)
	}
	if len(first.Records)+len(result.Records) != 5 {
		t.Fatalf("record count mismatch: first=%d second=%d", len(first.Records), len(result.Records))
	}
	if len(result.SettlementPlans) != 1 {
		t.Fatalf("settlement plan count mismatch: %d", len(result.SettlementPlans))
	}
	plan := result.SettlementPlans[0]
	assertTransfer(t, plan, "deployer", "6000", "deployer_fee")
	assertTransfer(t, plan, "agent", "3000", "agent_fee")
	assertTransfer(t, plan, "bootstrap", "1000", "bootstrap_fee")
	assertTransfer(t, plan, "alice", "90000", "winner_payout")
	if result.StateRoot == [32]byte{} {
		t.Fatalf("missing state root")
	}
}

func TestBlockExecutorRejectsBetBeforeReady(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	betTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	_, err := ExecuteBlock(BlockExecutionRequest{
		Txs:            []*wire.MsgTx{deployTx, betTx},
		BlockHeight:    validPredictionContract().BetDeadline,
		RuntimeConfig:  testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{betTx.TxID(): "alice"}),
	})
	if err == nil || err.Error() != "agent contract is not ready" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlockExecutorRejectsNonCoreConfirm(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	betTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, mustEncodeConfirm(t, ResultTypeOutcome, "a"), 0, nil)

	store := NewRuntimeStore()
	_, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx, betTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID(): "core",
			betTx.TxID():   "alice",
		}),
	})
	if err != nil {
		t.Fatalf("first block failed: %v", err)
	}
	_, err = ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   validPredictionContract().ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			confirmTx.TxID(): "alice",
		}),
	})
	if err == nil || err.Error() != "invoker is not core node" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlockExecutorRejectsNonCoreReady(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)

	_, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx},
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID(): "alice",
		}),
	})
	if err == nil || err.Error() != "invoker is not core node" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlockExecutorPredictionRejectByCore(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	rejectTx := testAgentInvokeTx(t, addr, InvokeAPIReject, mustEncodeReject(t, "ambiguous event"), 0, nil)

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, rejectTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			rejectTx.TxID(): "core",
		}),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock reject failed: %v", err)
	}
	if len(result.SettlementPlans) != 0 {
		t.Fatalf("unexpected settlement plans")
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if state.Status != StatusRejected || state.Prediction.Status != PredictionStatusRejected {
		t.Fatalf("status mismatch: %#v", state)
	}
	if len(state.Prediction.Rejections) != 1 || state.Prediction.Rejections[0].Reason != "ambiguous event" {
		t.Fatalf("rejection mismatch: %#v", state.Prediction.Rejections)
	}
}

func TestBlockExecutorRejectsNonCoreReject(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	rejectTx := testAgentInvokeTx(t, addr, InvokeAPIReject, mustEncodeReject(t, "ambiguous event"), 0, nil)

	_, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, rejectTx},
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			rejectTx.TxID(): "alice",
		}),
	})
	if err == nil || err.Error() != "invoker is not core node" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildBlockResultTxsForConfirm(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000, nil)
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, mustEncodeConfirm(t, ResultTypeOutcome, "a"), 0, nil)

	store := NewRuntimeStore()
	_, err := ExecuteBlock(BlockExecutionRequest{
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

	abnormalTxID := chainhash.Hash{9}.String()
	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   validPredictionContract().ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ContractUTXOs: ContractUTXOProviderWithTxOutputs(func(contract ContractAddress) ([]UTXO, error) {
			return []UTXO{{
				OutPoint: OutPoint{TxID: abnormalTxID, Vout: 0},
				Contract: contract,
				Value:    10000,
			}}, nil
		}, []*wire.MsgTx{aliceBetTx, bobBetTx}, TestnetContractPrefix),
		ResolveScript:  testResultScriptResolver,
		ResolveInvoker: testInvokerResolver(map[string]string{confirmTx.TxID(): "core"}),
	})
	if err != nil {
		t.Fatalf("BuildBlockResultTxs failed: %v", err)
	}
	if len(built.ResultTxs) != 1 {
		t.Fatalf("result tx count mismatch: %d", len(built.ResultTxs))
	}
	if len(built.Execution.ResultPlans) != 1 {
		t.Fatalf("result plan count mismatch: %d", len(built.Execution.ResultPlans))
	}
	if len(built.ResultTxs[0].TxIn) != 4 {
		t.Fatalf("result input count mismatch: %d", len(built.ResultTxs[0].TxIn))
	}
	if len(built.ResultTxs[0].TxOut) != 5 {
		t.Fatalf("result output count mismatch: %d", len(built.ResultTxs[0].TxOut))
	}
	if built.ResultTxs[0].TxOut[0].Value != 6600 || built.ResultTxs[0].TxOut[3].Value != 99000 {
		t.Fatalf("result outputs did not use full contract pool")
	}
}

func TestBuildBlockResultTxsForDeployAndReady(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)

	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{deployTx, readyTx},
		BlockHeight:    validPredictionContract().BetDeadline,
		RuntimeConfig:  testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{readyTx.TxID(): "core"}),
	})
	if err != nil {
		t.Fatalf("BuildBlockResultTxs failed: %v", err)
	}
	if len(built.ResultTxs) != 1 {
		t.Fatalf("result tx count mismatch: %d", len(built.ResultTxs))
	}
	if len(built.Execution.ResultPlans) != 2 {
		t.Fatalf("result plan count mismatch: %d", len(built.Execution.ResultPlans))
	}
	if len(built.ResultTxs[0].TxIn) != 2 {
		t.Fatalf("result input count mismatch: %d", len(built.ResultTxs[0].TxIn))
	}
	if len(built.ResultTxs[0].TxOut) != 1 {
		t.Fatalf("result output count mismatch: %d", len(built.ResultTxs[0].TxOut))
	}
	_, payload, err := contractcommonReadResultPayload(built.ResultTxs[0])
	if err != nil {
		t.Fatalf("read result payload failed: %v", err)
	}
	if payload.ResultCount != 2 {
		t.Fatalf("result count mismatch: %d", payload.ResultCount)
	}
}

func testAgentDeployTx(t *testing.T) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	contract := validPredictionContract()
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

func testAgentInvokeTx(t *testing.T, contract ContractAddress, action string, param []byte, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	invokeScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  1000,
		CallNonce: 1,
		Action:    action,
		Param:     param,
	})
	if err != nil {
		t.Fatalf("InvokeNullDataScript failed: %v", err)
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(value, assets, testAgentContractScript(contract)))
	return tx
}

func mustEncodeBet(t *testing.T, outcomeID string) []byte {
	t.Helper()
	data, err := (PredictionBetParam{OutcomeID: outcomeID}).Encode()
	if err != nil {
		t.Fatalf("PredictionBetParam.Encode failed: %v", err)
	}
	return data
}

func mustEncodeConfirm(t *testing.T, resultType, outcomeID string) []byte {
	t.Helper()
	contract := validPredictionContract()
	data, err := (PredictionConfirmParam{
		ResultType: resultType,
		OutcomeID:  outcomeID,
		SourceURL:  contract.SourceURL,
		ResultURL:  "https://example.com/match/result/123",
		ResultHash: "abc123",
		ObservedAt: contract.EventTime + 1,
	}).Encode()
	if err != nil {
		t.Fatalf("PredictionConfirmParam.Encode failed: %v", err)
	}
	return data
}

func mustEncodeReject(t *testing.T, reason string) []byte {
	t.Helper()
	data, err := (PredictionRejectParam{
		Reason:    reason,
		CheckedAt: validPredictionContract().BetDeadline,
	}).Encode()
	if err != nil {
		t.Fatalf("PredictionRejectParam.Encode failed: %v", err)
	}
	return data
}

func testRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		CoreNodeAddress:  "core",
		AgentAddress:     "agent",
		BootstrapAddress: "bootstrap",
	}
}

func testInvokerResolver(invokers map[string]string) InvokerResolver {
	return func(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
		return invokers[tx.TxID()], nil
	}
}

func testAgentAsset(name string, amount int64) wire.TxAssets {
	return wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(name),
		Amount: *scommon.NewDefaultDecimal(amount),
	}}
}
