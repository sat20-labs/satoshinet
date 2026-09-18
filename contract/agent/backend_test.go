package agent

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// The harness keeps actual work/Result outputs across blocks. It never derives
// physical funds from the managed ledger, so a missing output remains a deficit.
type agentTestChain struct {
	t      *testing.T
	store  *RuntimeStore
	utxos  *contractframework.ContractUTXOOverlay
	height int64
}

func newAgentTestChain(t *testing.T) *agentTestChain {
	t.Helper()
	return &agentTestChain{
		t: t, store: NewRuntimeStore(),
		utxos: contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
			Prefix: TestnetContractPrefix, ContractType: ContractTypeAgent,
		}),
	}
}

func agentTestScript(output ResultOutput) ([]byte, error) {
	if addr, err := DecodeContractAddress(output.To); err == nil {
		return ContractPkScript(addr)
	}
	return []byte(output.To), nil
}

func (c *agentTestChain) build(height, blockTime int64, actors map[string]string, txs ...*wire.MsgTx) (BlockResultBuildResult, error) {
	c.t.Helper()
	parent := c.store.Clone()
	built, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: txs, Store: c.store, ContractPrefix: TestnetContractPrefix,
		RuntimeConfig: testRuntimeConfig(), GasConfig: DefaultGasConfig(),
		BlockHeight: height, BlockTime: blockTime, ContractUTXOs: c.utxos.Provider,
		ResolveInvoker: testInvokerResolver(actors), ResolveScript: agentTestScript,
		ResolveOutput: testResultOutputResolver,
	})
	if err != nil {
		require.Equal(c.t, parent.StateRoot(), c.store.StateRoot(), "failed candidate must not mutate its parent")
		return BlockResultBuildResult{}, err
	}
	// Independent replay must produce the same complete post-state, including
	// immutable policy, user liabilities and managed quantities.
	replayed, err := ExecuteBlock(BlockExecutionRequest{
		Txs: txs, Store: parent, ContractPrefix: TestnetContractPrefix,
		RuntimeConfig: testRuntimeConfig(), GasConfig: DefaultGasConfig(),
		BlockHeight: height, BlockTime: blockTime, ContractUTXOs: c.utxos.Provider,
		ResolveInvoker: testInvokerResolver(actors),
	})
	require.NoError(c.t, err)
	require.Equal(c.t, built.Execution.StateRoot, replayed.StateRoot)
	require.Equal(c.t, built.Execution.ResultPlans, replayed.ResultPlans)
	for _, tx := range txs {
		require.NoError(c.t, c.utxos.ApplyTx(tx, height))
	}
	for _, tx := range built.ResultTxs {
		require.NoError(c.t, c.utxos.ApplyTx(tx, height))
	}
	encoded, err := c.store.MarshalBinary()
	require.NoError(c.t, err)
	decoded, err := DecodeRuntimeStore(encoded)
	require.NoError(c.t, err)
	require.Equal(c.t, c.store.StateRoot(), decoded.StateRoot())
	c.store = decoded
	c.height = height
	return built, nil
}

func (c *agentTestChain) run(blockTime int64, actors map[string]string, txs ...*wire.MsgTx) BlockResultBuildResult {
	c.t.Helper()
	built, err := c.build(c.height+1, blockTime, actors, txs...)
	require.NoError(c.t, err)
	return built
}

func (c *agentTestChain) runtime(addr ContractAddress) *Runtime {
	c.t.Helper()
	runtime, ok := c.store.Get(addr)
	require.True(c.t, ok)
	return runtime
}

func fundedAgentDeploy(t *testing.T, prediction PredictionContract, nonce uint64, flags contractcommon.ContractFlags) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	content, err := prediction.Encode()
	require.NoError(t, err)
	payload := DeployPayload{
		Type: ContractTypeAgent, SubType: SubtypePrediction, Version: CurrentAgentVersion,
		GasLimit: DefaultGasConfig().DeployBaseGas, DeployNonce: nonce, Flags: flags, ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, payload.SubType, content, "deployer", nonce)
	require.NoError(t, err)
	script, err := DeployNullDataScript(payload)
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{byte(nonce), 1}, Index: 0}})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, testAgentAsset(DefaultGasConfig().GasAssetName, 500), testAgentContractScript(addr)))
	return tx, addr
}

func fundedAgentInvoke(t *testing.T, addr ContractAddress, action string, param []byte, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	assets = assets.Clone()
	gasName := DefaultGasConfig().GasAssetName
	hasGas := false
	for _, asset := range assets {
		hasGas = hasGas || asset.Name.String() == gasName
	}
	if !hasGas {
		require.NoError(t, assets.Merge(testAgentAsset(gasName, 100)))
	}
	return testAgentInvokeTx(t, addr, action, param, value, assets)
}

func startAgentPrediction(t *testing.T, prediction PredictionContract, flags contractcommon.ContractFlags, ready bool) (*agentTestChain, ContractAddress) {
	t.Helper()
	chain := newAgentTestChain(t)
	deploy, addr := fundedAgentDeploy(t, prediction, 7, flags)
	txs := []*wire.MsgTx{deploy}
	actors := map[string]string{}
	if ready {
		call := fundedAgentInvoke(t, addr, InvokeAPIReady, nil, 0, nil)
		txs = append(txs, call)
		actors[call.TxID()] = "core"
	}
	chain.run(prediction.BetDeadline, actors, txs...)
	return chain, addr
}

func agentConfirmParam(t *testing.T, prediction PredictionContract, outcome string) []byte {
	t.Helper()
	encoded, err := (PredictionConfirmParam{
		ResultType: ResultTypeOutcome, OutcomeID: outcome, Result: "final result",
		ResultURL: prediction.SourceURL, ObservedAt: prediction.EventTime + 1,
	}).Encode()
	require.NoError(t, err)
	return encoded
}

func agentOutputAmount(t *testing.T, plans []ResultPlan, to, assetName string) string {
	t.Helper()
	total := scommon.NewDefaultDecimal(0)
	for _, plan := range plans {
		for _, output := range plan.Outputs {
			if output.To != to {
				continue
			}
			if assetName == SatoshiAssetName {
				total = total.AddAlignPrecision(scommon.NewDefaultDecimal(output.Value))
			} else {
				for _, asset := range output.Assets {
					if asset.Name.String() == assetName {
						total = total.AddAlignPrecision(asset.Amount.Clone())
					}
				}
			}
		}
	}
	return total.String()
}

func TestPredictionUnixTimeRejectsMissingBlockTime(t *testing.T) {
	backend := &Backend{BlockHeight: 123}
	prediction := validPredictionContract()
	prediction.TimeBase = TimeBaseUnix
	_, err := backend.predictionTimeValue(prediction)
	require.Error(t, err)
	prediction.TimeBase = TimeBaseHeight
	value, err := backend.predictionTimeValue(prediction)
	require.NoError(t, err)
	require.Equal(t, int64(123), value)
}

func TestBackendPredictionE2EShape(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	alice := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	bob := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000, nil)
	bets := chain.run(prediction.BetDeadline, map[string]string{alice.TxID(): "alice", bob.TxID(): "bob"}, alice, bob)
	require.Len(t, bets.Execution.Records, 2)
	require.Equal(t, int64(100000), chain.runtime(addr).managed.Value)
	confirm := fundedAgentInvoke(t, addr, InvokeAPIConfirm, agentConfirmParam(t, prediction, "a"), 0, nil)
	built := chain.run(prediction.ConfirmAfter+1, map[string]string{confirm.TxID(): "core"}, confirm)
	require.Len(t, built.Execution.SettlementPlans, 1)
	plan := built.Execution.SettlementPlans[0]
	assertTransfer(t, plan, "deployer", "6000", "deployer_fee")
	assertTransfer(t, plan, "agent", "3000", "agent_fee")
	assertTransfer(t, plan, "bootstrap", "1000", "bootstrap_fee")
	assertTransfer(t, plan, "alice", "90000", "winner_payout")
	require.Equal(t, "90000", agentOutputAmount(t, built.Execution.ResultPlans, "alice", SatoshiAssetName))
	runtime := chain.runtime(addr)
	require.Equal(t, StatusCompleted, runtime.State().Status)
	require.False(t, runtime.State().Closed, "business completion is not lifecycle close")
	gas, err := runtime.managed.AssetAmount(DefaultGasConfig().GasAssetName)
	require.NoError(t, err)
	require.Equal(t, "450", gas.String(), "operating capital is retained until explicit close")

	closeTx := fundedAgentInvoke(t, addr, InvokeAPIClose, nil, 0, nil)
	closed := chain.run(prediction.ConfirmAfter+2, map[string]string{closeTx.TxID(): "deployer"}, closeTx)
	require.True(t, chain.runtime(addr).State().Closed)
	require.True(t, chain.runtime(addr).managed.IsZero())
	// 450 operating gas is profit: 315/135. The close call's separate 100 gas
	// contributes only its 50-unit fee and a 50-unit refund to the deployer.
	require.Equal(t, "365", agentOutputAmount(t, closed.Execution.ResultPlans, "deployer", DefaultGasConfig().GasAssetName))
	require.Equal(t, "135", agentOutputAmount(t, closed.Execution.ResultPlans, "bootstrap", DefaultGasConfig().GasAssetName))
}

func TestBackendRefundableFailureMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, action, actor string
		ready               bool
		value               int64
		param               func(*testing.T) []byte
		late                bool
	}{
		{"bet_before_ready", InvokeAPIBet, "alice", false, 60000, func(t *testing.T) []byte { return mustEncodeBet(t, "a") }, false},
		{"empty_bet", InvokeAPIBet, "alice", true, 0, func(t *testing.T) []byte { return mustEncodeBet(t, "a") }, false},
		{"non_core_ready", InvokeAPIReady, "alice", false, 0, nil, false},
		{"non_core_reject", InvokeAPIReject, "alice", false, 0, func(t *testing.T) []byte { return mustEncodeReject(t, "ambiguous event") }, false},
		{"non_core_confirm", InvokeAPIConfirm, "alice", true, 0, func(t *testing.T) []byte { return agentConfirmParam(t, validPredictionContract(), "a") }, true},
		{"late_bet", InvokeAPIBet, "alice", true, 60000, func(t *testing.T) []byte { return mustEncodeBet(t, "a") }, true},
		{"unknown_action", "unknown", "alice", true, 20, nil, false},
		{"non_deployer_close", InvokeAPIClose, "alice", true, 20, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prediction := validPredictionContract()
			chain, addr := startAgentPrediction(t, prediction, 0, tc.ready)
			before := chain.runtime(addr).managed.Clone()
			var param []byte
			if tc.param != nil {
				param = tc.param(t)
			}
			call := fundedAgentInvoke(t, addr, tc.action, param, tc.value, nil)
			at := prediction.BetDeadline
			if tc.late {
				at = prediction.ConfirmAfter + 1
			}
			built := chain.run(at, map[string]string{call.TxID(): tc.actor}, call)
			require.Len(t, built.Execution.Records, 1)
			require.Equal(t, ResultStatusInvalid, built.Execution.Records[0].Status)
			require.True(t, built.Execution.Records[0].RequiresResult)
			require.Equal(t, fmt.Sprint(tc.value), agentOutputAmount(t, built.Execution.ResultPlans, tc.actor, SatoshiAssetName))
			require.Equal(t, "50", agentOutputAmount(t, built.Execution.ResultPlans, tc.actor, DefaultGasConfig().GasAssetName))
			require.Equal(t, before, chain.runtime(addr).managed)
			require.Empty(t, chain.runtime(addr).State().Prediction.Bets)
			require.Empty(t, chain.runtime(addr).State().Prediction.Confirmations)
			require.False(t, chain.runtime(addr).State().Closed)
		})
	}
}

func TestBackendDefaultInvokeRefundsAllAssets(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	before := chain.runtime(addr).managed.Clone()
	assets := testAgentAsset("ordx:f:unexpected", 7)
	require.NoError(t, assets.Merge(testAgentAsset(DefaultGasConfig().GasAssetName, 5)))
	call := testAgentDefaultInvokeTx(t, addr, 20, assets)
	built := chain.run(prediction.BetDeadline, map[string]string{call.TxID(): "alice"}, call)
	require.Len(t, built.Execution.Records, 1)
	require.Equal(t, ResultStatusInvalid, built.Execution.Records[0].Status)
	require.Equal(t, contractframework.ResultFeeModeSatoshiFee, built.Execution.Records[0].ResultFeeMode)
	require.Equal(t, "10", agentOutputAmount(t, built.Execution.ResultPlans, "alice", SatoshiAssetName))
	require.Equal(t, "7", agentOutputAmount(t, built.Execution.ResultPlans, "alice", "ordx:f:unexpected"))
	require.Equal(t, "5", agentOutputAmount(t, built.Execution.ResultPlans, "alice", DefaultGasConfig().GasAssetName))
	require.Equal(t, before, chain.runtime(addr).managed)
}

func TestBackendRejectsInvalidDeploy(t *testing.T) {
	prediction := validPredictionContract()
	tx, addr := fundedAgentDeploy(t, prediction, 7, 0)
	content, err := prediction.Encode()
	require.NoError(t, err)
	script, err := DeployNullDataScript(DeployPayload{
		Type: ContractTypeAgent, SubType: SubtypePrediction, Version: CurrentAgentVersion,
		GasLimit: 0, DeployNonce: 7, ContractContent: content,
	})
	require.NoError(t, err)
	tx.TxOut[0].PkScript = script
	chain := newAgentTestChain(t)
	_, err = chain.build(1, prediction.BetDeadline, nil, tx)
	require.ErrorIs(t, err, contractframework.ErrCallAdmission)
	require.False(t, chain.store.Exists(addr))
}

func TestBackendNonExclusiveDeployAllowsSameConfigAfterClose(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, false)
	second, secondAddr := fundedAgentDeploy(t, prediction, 8, 0)
	chain.run(prediction.BetDeadline, nil, second)
	require.True(t, chain.store.Exists(addr))
	require.True(t, chain.store.Exists(secondAddr))
	closeTx := fundedAgentInvoke(t, addr, InvokeAPIClose, nil, 0, nil)
	chain.run(prediction.BetDeadline, map[string]string{closeTx.TxID(): "deployer"}, closeTx)
	third, thirdAddr := fundedAgentDeploy(t, prediction, 9, 0)
	chain.run(prediction.BetDeadline, nil, third)
	require.True(t, chain.runtime(addr).State().Closed)
	require.True(t, chain.store.Exists(thirdAddr))
}

func TestBackendDuplicateDeployRefundsAndPreservesMetadata(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, contractcommon.ContractFlagNonClosable, false)
	before := chain.runtime(addr).managed.Clone()
	duplicate, _ := fundedAgentDeploy(t, prediction, 7, 0)
	duplicate.LockTime = 1
	built := chain.run(prediction.BetDeadline, nil, duplicate)
	require.Equal(t, ResultStatusInvalid, built.Execution.Records[0].Status)
	require.Equal(t, contractcommon.ContractFlagNonClosable, chain.runtime(addr).DeploymentFlags())
	require.Equal(t, before, chain.runtime(addr).managed)
	require.Equal(t, "450", agentOutputAmount(t, built.Execution.ResultPlans, "deployer", DefaultGasConfig().GasAssetName))
}

func TestAgentCloseAtBetDeadlineRefundsUsersBeforeProfit(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	alice := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	bob := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 40000, nil)
	chain.run(prediction.BetDeadline-1, map[string]string{alice.TxID(): "alice", bob.TxID(): "bob"}, alice, bob)
	closeTx := fundedAgentInvoke(t, addr, InvokeAPIClose, nil, 23, testAgentAsset("ordx:f:close-refund", 9))
	built := chain.run(prediction.BetDeadline, map[string]string{closeTx.TxID(): "deployer"}, closeTx)
	require.Equal(t, ResultStatusSuccess, built.Execution.Records[0].Status)
	require.True(t, built.Execution.Records[0].CloseContract)
	require.Equal(t, "60000", agentOutputAmount(t, built.Execution.ResultPlans, "alice", SatoshiAssetName))
	require.Equal(t, "40000", agentOutputAmount(t, built.Execution.ResultPlans, "bob", SatoshiAssetName))
	require.Equal(t, "23", agentOutputAmount(t, built.Execution.ResultPlans, "deployer", SatoshiAssetName))
	require.Equal(t, "9", agentOutputAmount(t, built.Execution.ResultPlans, "deployer", "ordx:f:close-refund"))
	require.Equal(t, "365", agentOutputAmount(t, built.Execution.ResultPlans, "deployer", DefaultGasConfig().GasAssetName))
	require.Equal(t, "135", agentOutputAmount(t, built.Execution.ResultPlans, "bootstrap", DefaultGasConfig().GasAssetName))
	require.True(t, chain.runtime(addr).managed.IsZero())

	late := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	late.LockTime = 2
	refunded := chain.run(prediction.BetDeadline+2, map[string]string{late.TxID(): "alice"}, late)
	require.Equal(t, ResultStatusInvalid, refunded.Execution.Records[0].Status)
	require.Equal(t, "60000", agentOutputAmount(t, refunded.Execution.ResultPlans, "alice", SatoshiAssetName))
	require.True(t, chain.runtime(addr).managed.IsZero())
}

func TestAgentNonClosablePolicySurvivesSerialization(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, contractcommon.ContractFlagNonClosable, true)
	for _, param := range [][]byte{nil, []byte(`{"flags":0}`)} {
		closeTx := fundedAgentInvoke(t, addr, InvokeAPIClose, param, 0, nil)
		built := chain.run(prediction.ConfirmAfter+1, map[string]string{closeTx.TxID(): "deployer"}, closeTx)
		require.Equal(t, ResultStatusInvalid, built.Execution.Records[0].Status)
		require.False(t, chain.runtime(addr).State().Closed)
		require.Equal(t, contractcommon.ContractFlagNonClosable, chain.runtime(addr).DeploymentFlags())
	}
}

func TestAgentCloseAfterBetDeadlineRejectsAndRefundsCallFunding(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	bet := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	chain.run(prediction.BetDeadline, map[string]string{bet.TxID(): "alice"}, bet)
	before := chain.runtime(addr).managed.Clone()
	closeTx := fundedAgentInvoke(t, addr, InvokeAPIClose, nil, 23, testAgentAsset("ordx:f:close-refund", 9))
	built := chain.run(prediction.BetDeadline+1, map[string]string{closeTx.TxID(): "deployer"}, closeTx)
	require.Equal(t, ResultStatusInvalid, built.Execution.Records[0].Status)
	require.False(t, chain.runtime(addr).State().Closed)
	require.Equal(t, before, chain.runtime(addr).managed)
	require.Equal(t, "0", agentOutputAmount(t, built.Execution.ResultPlans, "alice", SatoshiAssetName))
	require.Equal(t, "23", agentOutputAmount(t, built.Execution.ResultPlans, "deployer", SatoshiAssetName))
	require.Equal(t, "9", agentOutputAmount(t, built.Execution.ResultPlans, "deployer", "ordx:f:close-refund"))
}

func TestBackendPredictionRejectByCore(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, false)
	call := fundedAgentInvoke(t, addr, InvokeAPIReject, mustEncodeReject(t, "ambiguous event"), 0, nil)
	built := chain.run(prediction.BetDeadline, map[string]string{call.TxID(): "core"}, call)
	require.Equal(t, ResultStatusSuccess, built.Execution.Records[0].Status)
	state := chain.runtime(addr).State()
	require.Equal(t, StatusRejected, state.Status)
	require.Equal(t, PredictionStatusRejected, state.Prediction.Status)
	require.Len(t, state.Prediction.Rejections, 1)
	require.Equal(t, "ambiguous event", state.Prediction.Rejections[0].Reason)
	require.False(t, state.Closed)
}

func TestBackendAdvancesPredictionStatusByBlockTime(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	built := chain.run(prediction.BetDeadline+1, nil)
	require.Empty(t, built.Execution.Records)
	require.Equal(t, PredictionStatusClosedForBet, chain.runtime(addr).State().Prediction.Status)
	chain.run(prediction.ConfirmAfter, nil)
	require.Equal(t, PredictionStatusPendingResult, chain.runtime(addr).State().Prediction.Status)
}

func TestBackendUsesBlockHeightForHeightTimeBase(t *testing.T) {
	prediction := validPredictionContract()
	prediction.TimeBase = TimeBaseHeight
	prediction.EventTime, prediction.BetDeadline, prediction.ConfirmAfter = 20, 10, 30
	chain := newAgentTestChain(t)
	deploy, addr := fundedAgentDeploy(t, prediction, 7, 0)
	ready := fundedAgentInvoke(t, addr, InvokeAPIReady, nil, 0, nil)
	bet := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	_, err := chain.build(10, 1_780_306_800, map[string]string{ready.TxID(): "core", bet.TxID(): "alice"}, deploy, ready, bet)
	require.NoError(t, err)
	require.Len(t, chain.runtime(addr).State().Prediction.Bets, 1)
	_, err = chain.build(11, 1_780_306_801, nil)
	require.NoError(t, err)
	require.Equal(t, PredictionStatusClosedForBet, chain.runtime(addr).State().Prediction.Status)
}

func TestBuildBlockResultTxsConfirmUsesSatoshiPrecision(t *testing.T) {
	prediction := validPredictionContract()
	prediction.MinBetUnit = "1"
	prediction.Outcomes = append(prediction.Outcomes, PredictionOutcome{ID: "c", Text: "draw"})
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	var txs []*wire.MsgTx
	actors := map[string]string{}
	for _, bet := range []struct {
		name, outcome string
		amount        int64
	}{
		{"alice", "a", 100}, {"bob", "b", 300}, {"carol", "c", 1000}, {"dave", "b", 100},
	} {
		tx := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, bet.outcome), bet.amount, nil)
		txs = append(txs, tx)
		actors[tx.TxID()] = bet.name
	}
	chain.run(prediction.BetDeadline, actors, txs...)
	confirm := fundedAgentInvoke(t, addr, InvokeAPIConfirm, agentConfirmParam(t, prediction, "b"), 0, nil)
	built := chain.run(prediction.ConfirmAfter+1, map[string]string{confirm.TxID(): "core"}, confirm)
	require.Equal(t, "1013", agentOutputAmount(t, built.Execution.ResultPlans, "bob", SatoshiAssetName))
	require.Equal(t, "337", agentOutputAmount(t, built.Execution.ResultPlans, "dave", SatoshiAssetName))
	require.Equal(t, "90", agentOutputAmount(t, built.Execution.ResultPlans, "deployer", SatoshiAssetName))
	require.Equal(t, "45", agentOutputAmount(t, built.Execution.ResultPlans, "agent", SatoshiAssetName))
	require.Equal(t, "15", agentOutputAmount(t, built.Execution.ResultPlans, "bootstrap", SatoshiAssetName))
}

func TestBuildBlockResultTxsForConfirmWithGasBetAsset(t *testing.T) {
	prediction := validPredictionContract()
	prediction.BetAsset = DefaultGasConfig().GasAssetName
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	alice := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 0, testAgentAsset(prediction.BetAsset, 60050))
	bob := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "b"), 0, testAgentAsset(prediction.BetAsset, 40050))
	chain.run(prediction.BetDeadline, map[string]string{alice.TxID(): "alice", bob.TxID(): "bob"}, alice, bob)
	runtime := chain.runtime(addr)
	require.Len(t, runtime.State().Prediction.Bets, 2)
	require.Equal(t, "100000", runtime.totalBetAmount().String())
	confirm := fundedAgentInvoke(t, addr, InvokeAPIConfirm, agentConfirmParam(t, prediction, "a"), 0, nil)
	built := chain.run(prediction.ConfirmAfter+1, map[string]string{confirm.TxID(): "core"}, confirm)
	require.Equal(t, "90000", agentOutputAmount(t, built.Execution.ResultPlans, "alice", prediction.BetAsset))
	remaining, err := chain.runtime(addr).managed.AssetAmount(prediction.BetAsset)
	require.NoError(t, err)
	require.Equal(t, "450", remaining.String())
	physical, err := chain.utxos.Provider(addr)
	require.NoError(t, err)
	total, err := contractframework.SumUTXOAssetAmount(physical, prediction.BetAsset)
	require.NoError(t, err)
	require.Equal(t, "450", total.String())
	require.Equal(t, "50", agentOutputAmount(t, built.Execution.ResultPlans, "core", prediction.BetAsset))
}

func TestBuildBlockResultTxsRefundsLateGasAssetBet(t *testing.T) {
	prediction := validPredictionContract()
	prediction.BetAsset = DefaultGasConfig().GasAssetName
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	call := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 0, testAgentAsset(prediction.BetAsset, 60050))
	built := chain.run(prediction.BetDeadline+1, map[string]string{call.TxID(): "alice"}, call)
	require.Equal(t, ResultStatusInvalid, built.Execution.Records[0].Status)
	require.Equal(t, "60000", agentOutputAmount(t, built.Execution.ResultPlans, "alice", prediction.BetAsset))
	require.Empty(t, chain.runtime(addr).State().Prediction.Bets)
}

func TestAgentUnfundedCallDoesNotUsePhysicalOrManagedGas(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	donation := testAgentDefaultInvokeTx(t, addr, 0, testAgentAsset(DefaultGasConfig().GasAssetName, 1000))
	require.NoError(t, chain.utxos.AddTxOutputs(donation, 1))
	before := chain.runtime(addr).managed.Clone()
	call := testAgentInvokeTx(t, addr, InvokeAPIConfirm, agentConfirmParam(t, prediction, "a"), 0, nil)
	built := chain.run(prediction.ConfirmAfter+1, map[string]string{call.TxID(): "core"}, call)
	require.Len(t, built.Execution.Records, 1)
	require.Equal(t, ResultStatusInvalid, built.Execution.Records[0].Status)
	require.False(t, built.Execution.Records[0].RequiresResult, "the current call cannot pay for its own refund")
	require.Empty(t, built.ResultTxs)
	require.Equal(t, before, chain.runtime(addr).managed, "historical physical or managed gas must not subsidize the failed call")
	require.Empty(t, chain.runtime(addr).State().Prediction.Confirmations)
}

func TestAgentRetainsUnmanagedUntilClose(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	donation := testAgentDefaultInvokeTx(t, addr, 10000, testAgentAsset("ordx:f:anomaly", 13))
	require.NoError(t, chain.utxos.AddTxOutputs(donation, 1))
	bet := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	built := chain.run(prediction.BetDeadline, map[string]string{bet.TxID(): "alice"}, bet)
	require.Equal(t, "0", agentOutputAmount(t, built.Execution.ResultPlans, "bootstrap", SatoshiAssetName))
	require.Equal(t, "0", agentOutputAmount(t, built.Execution.ResultPlans, "bootstrap", "ordx:f:anomaly"))
	require.Equal(t, int64(60000), chain.runtime(addr).managed.Value)
	require.Equal(t, "60000", chain.runtime(addr).totalBetAmount().String())
	closeTx := fundedAgentInvoke(t, addr, InvokeAPIClose, nil, 0, nil)
	closed := chain.run(prediction.BetDeadline, map[string]string{closeTx.TxID(): "deployer"}, closeTx)
	require.Equal(t, "10000", agentOutputAmount(t, closed.Execution.ResultPlans, "bootstrap", SatoshiAssetName))
	require.Equal(t, "13", agentOutputAmount(t, closed.Execution.ResultPlans, "bootstrap", "ordx:f:anomaly"))
	require.Equal(t, "60000", agentOutputAmount(t, closed.Execution.ResultPlans, "alice", SatoshiAssetName))
	require.True(t, chain.runtime(addr).managed.IsZero())
}

func TestAgentPhysicalDeficitRejectsWholeCandidate(t *testing.T) {
	prediction := validPredictionContract()
	chain, addr := startAgentPrediction(t, prediction, 0, true)
	bet := fundedAgentInvoke(t, addr, InvokeAPIBet, mustEncodeBet(t, "a"), 60000, nil)
	chain.run(prediction.BetDeadline, map[string]string{bet.TxID(): "alice"}, bet)
	before := chain.store.StateRoot()
	confirm := fundedAgentInvoke(t, addr, InvokeAPIConfirm, agentConfirmParam(t, prediction, "a"), 0, nil)
	_, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{confirm}, Store: chain.store, RuntimeConfig: testRuntimeConfig(),
		BlockHeight: 3, BlockTime: prediction.ConfirmAfter + 1,
		ContractUTXOs:  func(ContractAddress) ([]UTXO, error) { return nil, nil },
		ResolveInvoker: testInvokerResolver(map[string]string{confirm.TxID(): "core"}),
		ResolveScript:  agentTestScript, ResolveOutput: testResultOutputResolver,
	})
	require.ErrorIs(t, err, contractframework.ErrAccountingInvariant)
	require.Equal(t, before, chain.store.StateRoot())
}

func TestAgentFinalizeIsIdempotent(t *testing.T) {
	prediction := validPredictionContract()
	deploy, _ := fundedAgentDeploy(t, prediction, 7, 0)
	backend := NewBackend(BlockExecutionRequest{
		RuntimeConfig: testRuntimeConfig(), BlockHeight: 1, BlockTime: prediction.BetDeadline,
		ResolveInvoker: testInvokerResolver(nil),
	})
	require.NoError(t, backend.ExecuteTx(deploy))
	first, err := backend.Finalize()
	require.NoError(t, err)
	second, err := backend.Finalize()
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Error(t, backend.ExecuteTx(deploy))
}

// Low-level transaction fixtures remain explicit. Tests for insufficient gas
// use these builders directly; valid execution fixtures use fundedAgentInvoke.
func testAgentExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	if req.ResolveInvoker == nil {
		req.ResolveInvoker = testInvokerResolver(nil)
	}
	if req.BlockTime == 0 {
		req.BlockTime = req.BlockHeight
	}
	return ExecuteBlock(req)
}

func testAgentDeployTx(t *testing.T) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	return testAgentDeployTxForContract(t, validPredictionContract())
}

func testAgentInvokeTx(t *testing.T, contract ContractAddress, action string, param []byte, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	script, err := InvokeNullDataScript(InvokePayload{
		GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1, Action: action, Param: param,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
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

func testAgentGasFeeAtHeight(t *testing.T, gas, height int64) *scommon.Decimal {
	t.Helper()
	fee, err := contractcommon.GasFeeDecimalAtHeight(gas, uint64(height))
	require.NoError(t, err)
	return fee
}

func mustEncodeBet(t *testing.T, outcomeID string) []byte {
	t.Helper()
	data, err := (PredictionBetParam{OutcomeID: outcomeID}).Encode()
	require.NoError(t, err)
	return data
}

func mustEncodeConfirm(t *testing.T, resultType, outcomeID string) []byte {
	t.Helper()
	prediction := validPredictionContract()
	data, err := (PredictionConfirmParam{
		ResultType: resultType, OutcomeID: outcomeID, Result: "Team A 101, Team B 98",
		ResultURL: "https://example.com/match/result/123", ObservedAt: prediction.EventTime + 1,
	}).Encode()
	require.NoError(t, err)
	return data
}

func mustEncodeReject(t *testing.T, reason string) []byte {
	t.Helper()
	data, err := (PredictionRejectParam{Reason: reason, CheckedAt: validPredictionContract().BetDeadline}).Encode()
	require.NoError(t, err)
	return data
}

func testRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{CoreNodeAddress: "core", AgentAddress: "agent", BootstrapAddress: "bootstrap"}
}

func testInvokerResolver(invokers map[string]string) InvokerResolver {
	return func(tx *wire.MsgTx, call Tx) (string, error) {
		if actor := invokers[tx.TxID()]; actor != "" {
			return actor, nil
		}
		if call.Kind == TxTypeDeploy {
			return "deployer", nil
		}
		return "", nil
	}
}

func testAgentAsset(name string, amount int64) wire.TxAssets {
	return wire.TxAssets{{Name: *wire.NewAssetNameFromString(name), Amount: *scommon.NewDefaultDecimal(amount)}}
}
