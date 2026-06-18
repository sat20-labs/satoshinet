package agent

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestBuildResultTxFromPredictionSettlementPlan(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")
	requireBet(t, runtime, "bob", "b", "40000")
	settlement, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeOutcome,
			OutcomeID:  "a",
			SourceURL:  runtime.Contract().SourceURL,
			ResultURL:  "https://example.com/match/result/123",
			ResultHash: "abc123",
			ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("ApplyConfirm failed: %v", err)
	}
	plans, err := contractframework.BuildSettlementResultPlans(
		[]*PredictionSettlementPlan{settlement}, agentSettlementResultOptions())
	if err != nil {
		t.Fatalf("BuildSettlementResultPlans failed: %v", err)
	}
	contract := runtime.Address()
	txidA := chainhash.Hash{1}
	txidB := chainhash.Hash{2}
	plans, err = AugmentResultPlans(plans, func(contract ContractAddress) ([]UTXO, error) {
		return []UTXO{
			{OutPoint: OutPoint{TxID: txidB.String(), Vout: 1}, Contract: contract, Value: 40000, Height: 11},
			{OutPoint: OutPoint{TxID: txidA.String(), Vout: 0}, Contract: contract, Value: 60000, Height: 10},
		}, nil
	})
	if err != nil {
		t.Fatalf("AugmentResultPlans failed: %v", err)
	}
	if len(plans) != 1 || len(plans[0].Inputs) != 2 {
		t.Fatalf("unexpected result plans: %#v", plans)
	}
	if plans[0].Inputs[0].TxID != txidA.String() {
		t.Fatalf("inputs not sorted canonically: %#v", plans[0].Inputs)
	}

	resultTx, err := contractframework.BuildResultTx(contractframework.ResultTxBuildRequest{
		Status:        ResultStatusSuccess,
		Plans:         plans,
		ResolveScript: testResultScriptResolver,
	}, contractframework.ResultTxBuildOptions{})
	if err != nil {
		t.Fatalf("BuildResultTx failed: %v", err)
	}
	if len(resultTx.TxIn) != 2 {
		t.Fatalf("input count mismatch: %d", len(resultTx.TxIn))
	}
	if len(resultTx.TxOut) != 5 {
		t.Fatalf("output count mismatch: %d", len(resultTx.TxOut))
	}
	if resultTx.TxOut[0].Value != 6000 || resultTx.TxOut[3].Value != 90000 {
		t.Fatalf("unexpected result output values")
	}
	if _, _, err := contractcommonReadResultPayload(resultTx); err != nil {
		t.Fatalf("missing result payload: %v", err)
	}
	if err := contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
		Label:        "agent",
		ResultTx:     resultTx,
		Status:       ResultStatusSuccess,
		Plans:        plans,
		CheckPayload: true,
	}); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	_ = contract
}

func testResultScriptResolver(output ResultOutput) ([]byte, error) {
	return txscript.NewScriptBuilder().AddOp(txscript.OP_TRUE).Script()
}

func contractcommonReadResultPayload(tx *wire.MsgTx) (int, ResultPayload, error) {
	for i, out := range tx.TxOut {
		payload, err := contractcommon.ReadResultNullDataScript(out.PkScript)
		if err == nil {
			return i, payload, nil
		}
	}
	return -1, ResultPayload{}, fmt.Errorf("missing result payload")
}
