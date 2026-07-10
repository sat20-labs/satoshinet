package agent

import (
	"strconv"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
)

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	contract := validPredictionContract()
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	deployer := "deployer"
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     3,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, deploy.SubType, content, deployer, deploy.DeployNonce)
	if err != nil {
		t.Fatalf("DeriveContractAddress failed: %v", err)
	}
	runtime, err := NewRuntimeWithDeployer(addr, deploy, RuntimeConfig{
		CoreNodeAddress:  "core",
		AgentAddress:     "agent",
		BootstrapAddress: "bootstrap",
	}, deployer)
	if err != nil {
		t.Fatalf("NewRuntime failed: %v", err)
	}
	return runtime
}

func TestRuntimeReadyRequiresCoreNode(t *testing.T) {
	runtime := newTestRuntime(t)
	if err := runtime.ApplyReady(ApplyReadyRequest{Invoker: "alice"}); err == nil {
		t.Fatalf("expected core node auth error")
	}
	if err := runtime.ApplyReady(ApplyReadyRequest{Invoker: "core"}); err != nil {
		t.Fatalf("ApplyReady failed: %v", err)
	}
	state := runtime.State()
	if state.Status != StatusReady || state.Prediction.Status != PredictionStatusBetting {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestRuntimeRejectsPredictionOnMainnet(t *testing.T) {
	contract := validPredictionContract()
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	deployer := "deployer"
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     3,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(MainnetContractPrefix, deploy.SubType, content, deployer, deploy.DeployNonce)
	if err != nil {
		t.Fatalf("DeriveContractAddress failed: %v", err)
	}
	_, err = NewRuntimeWithDeployer(addr, deploy, RuntimeConfig{
		CoreNodeAddress:  "core",
		AgentAddress:     "agent",
		BootstrapAddress: "bootstrap",
		ChainParams:      &chaincfg.MainNetParams,
	}, deployer)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled prediction on mainnet, got %v", err)
	}
}

func TestRuntimeBetAggregatesByAddressAndOutcome(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	for i := 0; i < 2; i++ {
		err := runtime.ApplyBet(ApplyBetRequest{
			Invoker:   "alice",
			Param:     PredictionBetParam{OutcomeID: "a"},
			AssetName: runtime.Contract().BetAsset,
			Amount:    "10000",
			TimeValue: runtime.Contract().BetDeadline,
		})
		if err != nil {
			t.Fatalf("ApplyBet failed: %v", err)
		}
	}
	state := runtime.State()
	bet := state.Prediction.Bets[predictionBetKey("alice", "a")]
	if bet.Amount != "20000" {
		t.Fatalf("aggregated amount mismatch: %s", bet.Amount)
	}
}

func TestRuntimeDeployerCloseAllowedThroughBetDeadline(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "10000")

	plan, err := runtime.ApplyClose(ApplyCloseRequest{
		Invoker:   "deployer",
		TimeValue: runtime.Contract().BetDeadline,
	})
	if err != nil {
		t.Fatalf("ApplyClose failed: %v", err)
	}
	if !plan.Refund || len(plan.Transfers) != 1 || plan.Transfers[0].To != "alice" {
		t.Fatalf("unexpected close refund plan: %#v", plan)
	}
	if runtime.State().Status != StatusCompleted || runtime.State().Prediction.Status != PredictionStatusRefundable {
		t.Fatalf("unexpected close state: %#v", runtime.State())
	}
}

func TestRuntimeConfirmSettlesWinnersAndFees(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")
	requireBet(t, runtime, "bob", "b", "40000")

	plan, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeOutcome,
			OutcomeID:  "a",
			Result:     "Team A 101, Team B 98",
			ResultURL:  "https://example.com/match/result/123",
			ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 100,
	})
	if err != nil {
		t.Fatalf("ApplyConfirm failed: %v", err)
	}
	assertTransfer(t, plan, "deployer", "6000", "deployer_fee")
	assertTransfer(t, plan, "agent", "3000", "agent_fee")
	assertTransfer(t, plan, "bootstrap", "1000", "bootstrap_fee")
	assertTransfer(t, plan, "alice", "90000", "winner_payout")
	if runtime.State().Prediction.Status != PredictionStatusSettled {
		t.Fatalf("runtime not settled")
	}
}

func TestRuntimeConfirmUsesAssetPrecisionForSatoshiPayouts(t *testing.T) {
	runtime := newTestRuntimeForBetAsset(t, SatoshiAssetName, "1")
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "100")
	requireBet(t, runtime, "bob", "b", "300")
	requireBet(t, runtime, "carol", "c", "1000")
	requireBet(t, runtime, "dave", "b", "100")

	plan, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeOutcome,
			OutcomeID:  "b",
			Result:     "Team B wins",
			ResultURL:  "https://example.com/match/result/123",
			ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 100,
	})
	if err != nil {
		t.Fatalf("ApplyConfirm failed: %v", err)
	}
	assertTransfer(t, plan, "deployer", "90", "deployer_fee")
	assertTransfer(t, plan, "agent", "45", "agent_fee")
	assertTransfer(t, plan, "bootstrap", "15", "bootstrap_fee")
	assertTransfer(t, plan, "bob", "1013", "winner_payout")
	assertTransfer(t, plan, "dave", "337", "winner_payout")

	total := int64(0)
	for _, transfer := range plan.Transfers {
		if strings.Contains(transfer.AssetAmt, ".") {
			t.Fatalf("satoshi transfer should use precision 0: %#v", transfer)
		}
		amount, err := strconv.ParseInt(transfer.AssetAmt, 10, 64)
		if err != nil {
			t.Fatalf("satoshi transfer amount should be an integer: %#v", transfer)
		}
		total += amount
	}
	if total != 1500 {
		t.Fatalf("settlement total mismatch: got %d want 1500", total)
	}
}

func TestRuntimeConfirmRequiresConfirmAfter(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")

	_, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeOutcome,
			OutcomeID:  "a",
			Result:     "Team A 101, Team B 98",
			ResultURL:  "https://example.com/match/result/123",
			ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter - 1,
	})
	if err == nil || err.Error() != "prediction confirm_after has not been reached" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeConfirmRefundsWhenNoWinner(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")
	requireBet(t, runtime, "bob", "a", "40000")

	plan, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeOutcome,
			OutcomeID:  "b",
			Result:     "Team A 101, Team B 98",
			ResultURL:  "https://example.com/match/result/123",
			ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 100,
	})
	if err != nil {
		t.Fatalf("ApplyConfirm failed: %v", err)
	}
	if !plan.Refund {
		t.Fatalf("expected refund plan")
	}
	assertTransfer(t, plan, "alice", "60000", "refund")
	assertTransfer(t, plan, "bob", "40000", "refund")
}

func TestRuntimeConfirmRefundsCancelledResult(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")

	plan, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeCancelled,
			Result:     "Team A 101, Team B 98",
			ResultURL:  "https://example.com/match/result/123",
			ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 100,
	})
	if err != nil {
		t.Fatalf("ApplyConfirm failed: %v", err)
	}
	if !plan.Refund {
		t.Fatalf("expected refund plan")
	}
	assertTransfer(t, plan, "alice", "60000", "refund")
}

func requireReady(t *testing.T, runtime *Runtime) {
	t.Helper()
	if err := runtime.ApplyReady(ApplyReadyRequest{Invoker: "core"}); err != nil {
		t.Fatalf("ApplyReady failed: %v", err)
	}
}

func requireBet(t *testing.T, runtime *Runtime, address, outcome, amount string) {
	t.Helper()
	err := runtime.ApplyBet(ApplyBetRequest{
		Invoker:   address,
		Param:     PredictionBetParam{OutcomeID: outcome},
		AssetName: runtime.Contract().BetAsset,
		Amount:    amount,
		TimeValue: runtime.Contract().BetDeadline,
	})
	if err != nil {
		t.Fatalf("ApplyBet failed: %v", err)
	}
}

func assertTransfer(t *testing.T, plan *PredictionSettlementPlan, to, amount, reason string) {
	t.Helper()
	for _, transfer := range plan.Transfers {
		if transfer.To == to && transfer.Reason == reason {
			if transfer.AssetAmt != amount {
				t.Fatalf("transfer %s/%s amount mismatch: got %s want %s", to, reason, transfer.AssetAmt, amount)
			}
			return
		}
	}
	t.Fatalf("missing transfer to=%s reason=%s in %#v", to, reason, plan.Transfers)
}
