package agent

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
)

func testAgentExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	if req.ResolveInvoker == nil {
		req.ResolveInvoker = testInvokerResolver(nil)
	}
	return ExecuteBlock(req)
}

func TestBackendPredictionE2EShape(t *testing.T) {
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
	first, err := testAgentExecuteBlock(BlockExecutionRequest{
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
	result, err := testAgentExecuteBlock(BlockExecutionRequest{
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
	lastRecord := result.Records[len(result.Records)-1]
	if len(lastRecord.AssetIntents) != len(plan.Transfers) {
		t.Fatalf("settlement asset intents were not recorded: records=%+v plan=%+v", result.Records, plan)
	}
	if !lastRecord.AssetIntents[0].From.Equal(addr) {
		t.Fatalf("settlement asset intent source mismatch")
	}
	if result.StateRoot == [32]byte{} {
		t.Fatalf("missing state root")
	}
}

func TestBackendDefaultInvokeNoOp(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	defaultTx := testAgentDefaultInvokeTx(t, addr, 0, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(DefaultGasConfig().GasAssetName),
		Amount: *testAgentGasFee(t, DefaultGasConfig().InvokeBaseGas),
	}})

	result, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, defaultTx},
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Type != TxTypeDeploy {
		t.Fatalf("default invoke should be no-op, records=%+v", result.Records)
	}
	if len(result.SettlementPlans) != 0 {
		t.Fatalf("default invoke should not produce settlements: %+v", result.SettlementPlans)
	}
}

func TestBackendIgnoresInvalidDeploy(t *testing.T) {
	contract := validPredictionContract()
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	deployer := "deployer"
	deploy := DeployPayload{
		GasLimit:        0,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     7,
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

	store := NewRuntimeStore()
	result, err := testAgentExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Store: store})
	if err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if len(result.Records) != 0 || len(result.ResultPlans) != 0 {
		t.Fatalf("invalid deploy should be no-op, records=%+v plans=%+v", result.Records, result.ResultPlans)
	}
	if store.Exists(addr) {
		t.Fatalf("invalid deploy created runtime")
	}
}

func TestBackendIgnoresBetBeforeReady(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	betTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	store := NewRuntimeStore()
	result, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:            []*wire.MsgTx{deployTx, betTx},
		Store:          store,
		BlockHeight:    validPredictionContract().BetDeadline,
		RuntimeConfig:  testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{betTx.TxID(): "alice"}),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Type != TxTypeDeploy {
		t.Fatalf("invalid bet should be no-op, records=%+v", result.Records)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if state.Status != StatusPendingReady || len(state.Prediction.Bets) != 0 {
		t.Fatalf("invalid bet changed state: %#v", state)
	}
}

func TestBackendIgnoresBetWithoutFundingAmount(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	betTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 0, nil)

	store := NewRuntimeStore()
	result, err := testAgentExecuteBlock(BlockExecutionRequest{
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
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if len(result.Records) != 2 {
		t.Fatalf("invalid bet should not add an execution record, records=%+v", result.Records)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if state.Status != StatusReady || len(state.Prediction.Bets) != 0 {
		t.Fatalf("invalid bet changed state: %#v", state)
	}
}

func TestBackendAdvancesPredictionStatusByBlockTime(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID(): "core",
		}),
	})
	if err != nil {
		t.Fatalf("ready block failed: %v", err)
	}

	result, err := testAgentExecuteBlock(BlockExecutionRequest{
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline + 1,
		RuntimeConfig: testRuntimeConfig(),
	})
	if err != nil {
		t.Fatalf("closed-for-bet advance failed: %v", err)
	}
	if len(result.Records) != 0 {
		t.Fatalf("status advance should not add records: %+v", result.Records)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	if runtime.State().Prediction.Status != PredictionStatusClosedForBet {
		t.Fatalf("status mismatch after deadline: %#v", runtime.State())
	}

	_, err = testAgentExecuteBlock(BlockExecutionRequest{
		Store:         store,
		BlockHeight:   validPredictionContract().ConfirmAfter,
		RuntimeConfig: testRuntimeConfig(),
	})
	if err != nil {
		t.Fatalf("pending-result advance failed: %v", err)
	}
	if runtime.State().Prediction.Status != PredictionStatusPendingResult {
		t.Fatalf("status mismatch after confirm_after: %#v", runtime.State())
	}
}

func TestBackendUsesBlockTimeForUnixTimeBase(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	betTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm,
		mustEncodeConfirm(t, ResultTypeOutcome, "a"), 0, nil)
	contract := validPredictionContract()

	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx, betTx},
		Store:         store,
		BlockHeight:   1,
		BlockTime:     contract.BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID(): "core",
			betTx.TxID():   "alice",
		}),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock bet failed: %v", err)
	}
	_, err = testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   2,
		BlockTime:     contract.ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			confirmTx.TxID(): "core",
		}),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock confirm failed: %v", err)
	}
}

func TestBackendUsesBlockHeightForHeightTimeBase(t *testing.T) {
	contract := validPredictionContract()
	contract.TimeBase = TimeBaseHeight
	contract.EventTime = 20
	contract.BetDeadline = 10
	contract.ConfirmAfter = 30
	deployTx, addr := testAgentDeployTxForContract(t, contract)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	betTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)

	_, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx, betTx},
		BlockHeight:   contract.BetDeadline,
		BlockTime:     1_780_306_800,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID(): "core",
			betTx.TxID():   "alice",
		}),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock height-base bet failed: %v", err)
	}
}

func TestBackendIgnoresNonCoreConfirm(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	betTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, mustEncodeConfirm(t, ResultTypeOutcome, "a"), 0, nil)

	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
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
	result, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   validPredictionContract().ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			confirmTx.TxID(): "alice",
		}),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock non-core confirm failed: %v", err)
	}
	if len(result.Records) != 0 || len(result.SettlementPlans) != 0 {
		t.Fatalf("invalid confirm should be no-op, records=%+v settlements=%+v", result.Records, result.SettlementPlans)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if state.Status != StatusReady || state.Prediction.Status != PredictionStatusPendingResult {
		t.Fatalf("invalid confirm changed state: %#v", state)
	}
}

func TestBackendIgnoresNonCoreReady(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)

	store := NewRuntimeStore()
	result, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID(): "alice",
		}),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Type != TxTypeDeploy {
		t.Fatalf("invalid ready should be no-op, records=%+v", result.Records)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if state.Status != StatusPendingReady || state.Prediction.Status != PredictionStatusPendingResult {
		t.Fatalf("invalid ready changed state: %#v", state)
	}
}

func TestBackendPredictionRejectByCore(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	rejectTx := testAgentInvokeTx(t, addr, InvokeAPIReject, mustEncodeReject(t, "ambiguous event"), 0, nil)

	store := NewRuntimeStore()
	result, err := testAgentExecuteBlock(BlockExecutionRequest{
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

func TestBackendIgnoresNonCoreReject(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	rejectTx := testAgentInvokeTx(t, addr, InvokeAPIReject, mustEncodeReject(t, "ambiguous event"), 0, nil)

	store := NewRuntimeStore()
	result, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, rejectTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			rejectTx.TxID(): "alice",
		}),
	})
	if err != nil {
		t.Fatalf("ExecuteBlock failed: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Type != TxTypeDeploy {
		t.Fatalf("invalid reject should be no-op, records=%+v", result.Records)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if state.Status != StatusPendingReady || len(state.Prediction.Rejections) != 0 {
		t.Fatalf("invalid reject changed state: %#v", state)
	}
}

func TestBuildBlockResultTxsForConfirm(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	resultGas := testAgentGasFee(t, DefaultGasConfig().ResultBaseGas).Int64()
	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000,
		testAgentAsset(DefaultGasConfig().GasAssetName, resultGas))
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000,
		testAgentAsset(DefaultGasConfig().GasAssetName, resultGas))
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, mustEncodeConfirm(t, ResultTypeOutcome, "a"), 0,
		testAgentAsset(DefaultGasConfig().GasAssetName, 100))

	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
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

	abnormalTxID := chainhash.Hash{9}.String()
	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   2,
		BlockTime:     validPredictionContract().ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(func(contract ContractAddress) ([]UTXO, error) {
			return []UTXO{contractframework.UTXOFromTxOutput(OutPoint{TxID: abnormalTxID, Vout: 0},
				contract, 0, &wire.TxOut{Value: 10000})}, nil
		}, []*wire.MsgTx{aliceBetTx, bobBetTx, confirmTx}, TestnetContractPrefix, ContractTypeAgent),
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
	if len(built.ResultTxs[0].TxOut) != 7 {
		t.Fatalf("result output count mismatch: %d", len(built.ResultTxs[0].TxOut))
	}
	if built.ResultTxs[0].TxOut[0].Value != 6000 || built.ResultTxs[0].TxOut[3].Value != 90000 {
		t.Fatalf("result outputs used more than settlement pool")
	}
}

func TestAgentCloseResult(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	resultGas := testAgentGasFee(t, DefaultGasConfig().ResultBaseGas).Int64()
	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000,
		testAgentAsset(DefaultGasConfig().GasAssetName, resultGas))
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000,
		testAgentAsset(DefaultGasConfig().GasAssetName, resultGas))
	closeTx := testAgentInvokeTx(t, addr, InvokeAPIClose, nil, 0,
		testAgentAsset(DefaultGasConfig().GasAssetName, resultGas*2))

	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
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
		t.Fatalf("initial block failed: %v", err)
	}
	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{closeTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline + 1,
		RuntimeConfig: testRuntimeConfig(),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(nil,
			[]*wire.MsgTx{aliceBetTx, bobBetTx, closeTx}, TestnetContractPrefix, ContractTypeAgent),
		ResolveScript:  testResultScriptResolver,
		ResolveInvoker: testInvokerResolver(map[string]string{closeTx.TxID(): "deployer"}),
	})
	if err != nil {
		t.Fatalf("BuildBlockResultTxs failed: %v", err)
	}
	if len(built.ResultTxs) != 1 {
		t.Fatalf("result tx count mismatch: %d", len(built.ResultTxs))
	}
	if len(built.Execution.Records) != 1 || !built.Execution.Records[0].CloseContract {
		t.Fatalf("close record mismatch: %+v", built.Execution.Records)
	}
	outputs := built.Execution.ResultPlans[0].Outputs
	satOutputs := outputsByRecipientAndReason(outputs, SatoshiAssetName)
	assertOutputAmount(t, satOutputs, "alice/refund", "60000")
	assertOutputAmount(t, satOutputs, "bob/refund", "40000")
	gasOutputs := outputsByRecipientAndReason(outputs, DefaultGasConfig().GasAssetName)
	assertOutputAmount(t, gasOutputs, "deployer/", "59.997")
	assertOutputAmount(t, gasOutputs, "bootstrap/", "139.998")
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	if runtime.State().Status != StatusCompleted {
		t.Fatalf("close did not complete runtime: %#v", runtime.State())
	}
}

func TestBuildBlockResultTxsForConfirmWithGasBetAsset(t *testing.T) {
	contract := validPredictionContract()
	contract.BetAsset = DefaultGasConfig().GasAssetName
	contract.TimeBase = TimeBaseHeight
	contract.BetDeadline = 10
	contract.EventTime = 11
	contract.ConfirmAfter = 12
	deployTx, addr := testAgentDeployTxForContract(t, contract)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	resultGas := testAgentGasFeeAtHeight(t, DefaultGasConfig().ResultBaseGas, contract.BetDeadline).Int64()
	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 0,
		testAgentAsset(DefaultGasConfig().GasAssetName, 60000+resultGas))
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 0,
		testAgentAsset(DefaultGasConfig().GasAssetName, 40000+resultGas))
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, mustEncodeConfirm(t, ResultTypeOutcome, "a"), 0,
		testAgentAsset(DefaultGasConfig().GasAssetName, 100))

	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx},
		Store:         store,
		BlockHeight:   contract.BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID(): "core",
		}),
	})
	if err != nil {
		t.Fatalf("ready block failed: %v", err)
	}
	parsedAlice, err := ParseTx(aliceBetTx, StandardContractScriptResolver(TestnetContractPrefix))
	if err != nil {
		t.Fatalf("parse alice bet failed: %v", err)
	}
	validatedAlice, err := ValidateParsedInvokeTxBasic(parsedAlice, store.Exists, DefaultGasConfig())
	if err != nil {
		t.Fatalf("validate alice bet failed: %v", err)
	}
	amount, gasAmount, err := betAndGasFundingAmount(validatedAlice.FundingOutput,
		contract.BetAsset, DefaultGasConfig().GasAssetName, testAgentGasFee(t, DefaultGasConfig().ResultBaseGas))
	if err != nil {
		t.Fatalf("split alice bet funding failed: %v", err)
	}
	if amount != "60000" || gasAmount != "50" {
		t.Fatalf("alice funding split mismatch: amount=%s gas=%s", amount, gasAmount)
	}
	_, err = testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{aliceBetTx, bobBetTx},
		Store:         store,
		BlockHeight:   contract.BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			aliceBetTx.TxID(): "alice",
			bobBetTx.TxID():   "bob",
		}),
	})
	if err != nil {
		t.Fatalf("bet block failed: %v", err)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if len(state.Prediction.Bets) != 2 {
		t.Fatalf("gas-as-bet calls were not recorded: %#v", state.Prediction)
	}
	if state.Prediction.GasBalance != "100" {
		t.Fatalf("gas balance mismatch: %s", state.Prediction.GasBalance)
	}

	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   contract.ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(func(contract ContractAddress) ([]UTXO, error) {
			return nil, nil
		}, []*wire.MsgTx{aliceBetTx, bobBetTx, confirmTx}, TestnetContractPrefix, ContractTypeAgent),
		ResolveScript:  testResultScriptResolver,
		ResolveInvoker: testInvokerResolver(map[string]string{confirmTx.TxID(): "core"}),
	})
	if err != nil {
		t.Fatalf("BuildBlockResultTxs failed: %v", err)
	}
	if len(built.ResultTxs) != 1 {
		t.Fatalf("result tx count mismatch: %d", len(built.ResultTxs))
	}
	if len(built.ResultTxs[0].TxOut) != 7 {
		t.Fatalf("result output count mismatch: %d", len(built.ResultTxs[0].TxOut))
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
	if len(built.ResultTxs[0].TxIn) != 3 {
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

func TestBuildBlockResultTxsIgnoresBetWithoutFundingAmount(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	betTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 0, nil)

	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, readyTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			readyTx.TxID(): "core",
		}),
	})
	if err != nil {
		t.Fatalf("initial block failed: %v", err)
	}

	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{betTx},
		Store:         store,
		BlockHeight:   validPredictionContract().BetDeadline,
		RuntimeConfig: testRuntimeConfig(),
		ResolveInvoker: testInvokerResolver(map[string]string{
			betTx.TxID(): "alice",
		}),
	})
	if err != nil {
		t.Fatalf("BuildBlockResultTxs should ignore invalid bet: %v", err)
	}
	if len(built.ResultTxs) != 0 || len(built.Execution.Records) != 0 || len(built.Execution.ResultPlans) != 0 {
		t.Fatalf("invalid bet should be no-op, resultTxs=%d records=%+v plans=%+v", len(built.ResultTxs), built.Execution.Records, built.Execution.ResultPlans)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	if len(runtime.State().Prediction.Bets) != 0 {
		t.Fatalf("invalid bet changed state: %#v", runtime.State())
	}
}

func TestBuildBlockResultTxsIgnoresUnfundedConfirm(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000, nil)
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, mustEncodeConfirm(t, ResultTypeOutcome, "a"), 0, nil)

	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
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
		t.Fatalf("initial block failed: %v", err)
	}

	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   validPredictionContract().ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ContractUTXOs: func(contract ContractAddress) ([]UTXO, error) {
			return nil, nil
		},
		ResolveScript:  testResultScriptResolver,
		ResolveInvoker: testInvokerResolver(map[string]string{confirmTx.TxID(): "core"}),
	})
	if err != nil {
		t.Fatalf("BuildBlockResultTxs should ignore unfunded confirm: %v", err)
	}
	if len(built.ResultTxs) != 0 || len(built.Execution.Records) != 0 || len(built.Execution.ResultPlans) != 0 {
		t.Fatalf("unfunded confirm should be no-op, resultTxs=%d records=%+v plans=%+v", len(built.ResultTxs), built.Execution.Records, built.Execution.ResultPlans)
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if state.Status != StatusReady || state.Prediction.Status == PredictionStatusSettled || len(state.Prediction.Confirmations) != 0 {
		t.Fatalf("unfunded confirm changed settlement state: %#v", state)
	}
}

func TestBuildBlockResultTxsUsesPhysicalGasWhenManagedGasMissing(t *testing.T) {
	deployTx, addr := testAgentDeployTx(t)
	readyTx := testAgentInvokeTx(t, addr, InvokeAPIReady, nil, 0, nil)
	aliceBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	bobBetTx := testAgentInvokeTx(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000, nil)
	confirmTx := testAgentInvokeTx(t, addr, InvokeAPIConfirm, mustEncodeConfirm(t, ResultTypeOutcome, "a"), 0, nil)

	store := NewRuntimeStore()
	_, err := testAgentExecuteBlock(BlockExecutionRequest{
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
		t.Fatalf("initial block failed: %v", err)
	}

	resultGas := testAgentGasFee(t, DefaultGasConfig().ResultBaseGas).Int64()
	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{confirmTx},
		Store:         store,
		BlockHeight:   validPredictionContract().ConfirmAfter + 1,
		RuntimeConfig: testRuntimeConfig(),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(func(contract ContractAddress) ([]UTXO, error) {
			outpoint := OutPoint{TxID: chainhash.Hash{7}.String(), Vout: 0}
			return []UTXO{contractframework.UTXOFromTxOutput(outpoint,
				contract, 0, &wire.TxOut{Assets: testAgentAsset(DefaultGasConfig().GasAssetName, resultGas)})}, nil
		}, []*wire.MsgTx{aliceBetTx, bobBetTx}, TestnetContractPrefix, ContractTypeAgent),
		ResolveScript:  testResultScriptResolver,
		ResolveInvoker: testInvokerResolver(map[string]string{confirmTx.TxID(): "core"}),
	})
	if err != nil {
		t.Fatalf("BuildBlockResultTxs failed: %v", err)
	}
	if len(built.ResultTxs) != 1 {
		t.Fatalf("result tx count mismatch: %d", len(built.ResultTxs))
	}
	runtime, ok := store.Get(addr)
	if !ok {
		t.Fatalf("missing runtime")
	}
	state := runtime.State()
	if state.Prediction.GasBalance != "" {
		t.Fatalf("managed gas should still be empty, got %q", state.Prediction.GasBalance)
	}
	if state.Prediction.Status != PredictionStatusSettled {
		t.Fatalf("confirm did not settle prediction: %#v", state.Prediction)
	}
}

func testAgentDeployTx(t *testing.T) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	return testAgentDeployTxForContract(t, validPredictionContract())
}

func testAgentInvokeTx(t *testing.T, contract ContractAddress, action string, param []byte, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	invokeScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
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

func testAgentDefaultInvokeTx(t *testing.T, contract ContractAddress, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(value, assets, testAgentContractScript(contract)))
	return tx
}

func testAgentGasFee(t *testing.T, gas int64) *scommon.Decimal {
	t.Helper()
	return testAgentGasFeeAtHeight(t, gas, 0)
}

func testAgentGasFeeAtHeight(t *testing.T, gas int64, height int64) *scommon.Decimal {
	t.Helper()
	fee, err := contractcommon.GasFeeDecimalAtHeight(gas, uint64(height))
	if err != nil {
		t.Fatalf("gas fee failed: %v", err)
	}
	return fee
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
		Result:     "Team A 101, Team B 98",
		ResultURL:  "https://example.com/match/result/123",
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
	return func(tx *wire.MsgTx, contractTx Tx) (string, error) {
		if invoker := invokers[tx.TxID()]; invoker != "" {
			return invoker, nil
		}
		if contractTx.Kind == TxTypeDeploy {
			return "deployer", nil
		}
		return "", nil
	}
}

func testAgentAsset(name string, amount int64) wire.TxAssets {
	return wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(name),
		Amount: *scommon.NewDefaultDecimal(amount),
	}}
}
