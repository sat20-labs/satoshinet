package agent

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
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
			Result:     "Team A 101, Team B 98",
			ResultURL:  "https://example.com/match/result/123",
			ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("ApplyConfirm failed: %v", err)
	}
	plans, err := contractframework.BuildSettlementResultPlans(
		[]*PredictionSettlementPlan{settlement}, defaultAgentSettlementResultOptions())
	if err != nil {
		t.Fatalf("BuildSettlementResultPlans failed: %v", err)
	}
	contract := runtime.Address()
	txidA := chainhash.Hash{1}
	txidB := chainhash.Hash{2}
	plans, err = AugmentResultPlans(plans, func(contract ContractAddress) ([]UTXO, error) {
		return []UTXO{
			contractframework.UTXOFromTxOutput(OutPoint{TxID: txidB.String(), Vout: 1}, contract, 11, &wire.TxOut{Value: 40000}),
			contractframework.UTXOFromTxOutput(OutPoint{TxID: txidA.String(), Vout: 0}, contract, 10, &wire.TxOut{Value: 60000}),
		}, nil
	}, nil, nil, DefaultGasConfig().GasAssetName, "bootstrap")
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

func TestAugmentResultPlansUsesAssetPrecisionForPredictionPayouts(t *testing.T) {
	const assetName = "brc20:f:sgas"
	contract := newTestRuntime(t).Address()
	assetPrecision := func(name string) (int, bool) {
		if name == assetName {
			return 0, true
		}
		return 0, false
	}
	plans, err := contractframework.BuildSettlementResultPlans([]*PredictionSettlementPlan{{
		Contract: contract.MustEncode(),
		Transfers: []contractframework.SettlementTransfer{
			{To: "alice", AssetName: assetName, AssetAmt: "33.3333333333", Reason: "winner_payout"},
			{To: "bob", AssetName: assetName, AssetAmt: "33.3333333333", Reason: "winner_payout"},
			{To: "carol", AssetName: assetName, AssetAmt: "33.3333333333", Reason: "winner_payout"},
		},
	}}, agentSettlementResultOptions(assetPrecision))
	if err != nil {
		t.Fatalf("BuildSettlementResultPlans failed: %v", err)
	}
	assets, err := contractframework.NewAssetSetWithPrecision(assetName, "99", MaxPredictionDecimalPrecision, ErrInvalidAsset)
	if err != nil {
		t.Fatalf("NewAssetSetWithPrecision failed: %v", err)
	}
	augmented, err := AugmentResultPlans(plans, func(contract ContractAddress) ([]UTXO, error) {
		outpoint := OutPoint{TxID: chainhash.Hash{3}.String(), Vout: 0}
		return []UTXO{contractframework.UTXOFromTxOutput(outpoint, contract, 1, &wire.TxOut{Assets: assets})}, nil
	}, nil, assetPrecision, DefaultGasConfig().GasAssetName, "bootstrap")
	if err != nil {
		t.Fatalf("AugmentResultPlans failed: %v", err)
	}
	if len(augmented) != 1 || len(augmented[0].Outputs) != 3 {
		t.Fatalf("unexpected augmented plans: %#v", augmented)
	}
	got := make([]string, 0, len(augmented[0].Outputs))
	total := int64(0)
	for _, output := range augmented[0].Outputs {
		if output.AssetName != assetName || len(output.Assets) != 1 {
			t.Fatalf("unexpected output asset: %#v", output)
		}
		amount := output.Assets[0].Amount
		if amount.Precision != 0 {
			t.Fatalf("asset precision mismatch: got %d want 0 in %#v", amount.Precision, output)
		}
		got = append(got, amount.String())
		total += amount.Int64()
	}
	if fmt.Sprint(got) != "[33 33 33]" {
		t.Fatalf("unexpected payout split: %v", got)
	}
	if total != 99 {
		t.Fatalf("payout total mismatch: got %d want 99", total)
	}
}

func TestAugmentResultPlansUsesManagedPredictionPoolAndGas(t *testing.T) {
	const (
		betAsset = "brc20:f:sgas"
		gasAsset = "brc20:f:sgas"
	)
	runtime := newTestRuntimeForBetAsset(t, betAsset, "1")
	runtime.config.AssetPrecision = func(name string) (int, bool) {
		if name == betAsset {
			return 18, true
		}
		return 0, false
	}
	requireReady(t, runtime)
	for _, bet := range []struct {
		address string
		outcome string
		amount  string
	}{
		{"a10", "a", "10"}, {"a20", "a", "20"}, {"a30", "a", "30"},
		{"b10", "b", "10"}, {"b20", "b", "20"}, {"b30", "b", "30"},
		{"c10", "c", "10"}, {"c20", "c", "20"}, {"c30", "c", "30"},
	} {
		requireBet(t, runtime, bet.address, bet.outcome, bet.amount)
	}
	settlement, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeOutcome,
			OutcomeID:  "a",
			Result:     "home wins",
			ResultURL:  "https://example.com/match/result/123",
			ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 1,
	})
	if err != nil {
		t.Fatalf("ApplyConfirm failed: %v", err)
	}
	plans, err := contractframework.BuildSettlementResultPlans(
		[]*PredictionSettlementPlan{settlement}, agentSettlementResultOptions(runtime.config.AssetPrecision))
	if err != nil {
		t.Fatalf("BuildSettlementResultPlans failed: %v", err)
	}
	runtime.state.Prediction.GasBalance = "200"
	store := NewRuntimeStore()
	store.Add(runtime)
	resultFee := mustDecimal(t, "0.01", 18)
	plans[0].GasFee = resultFee
	physicalAssets := mustAssetSet(t, betAsset, "380", 18)
	augmented, err := AugmentResultPlans(plans, func(contract ContractAddress) ([]UTXO, error) {
		outpoint := OutPoint{TxID: chainhash.Hash{4}.String(), Vout: 0}
		return []UTXO{contractframework.UTXOFromTxOutput(outpoint, contract, 1, &wire.TxOut{Assets: physicalAssets})}, nil
	}, store, runtime.config.AssetPrecision, gasAsset, "bootstrap")
	if err != nil {
		t.Fatalf("AugmentResultPlans failed: %v", err)
	}

	outputs := outputsByRecipientAndReason(augmented[0].Outputs, betAsset)
	assertOutputAmount(t, outputs, "deployer/deployer_fee", "10.8")
	assertOutputAmount(t, outputs, "agent/agent_fee", "5.4")
	assertOutputAmount(t, outputs, "bootstrap/bootstrap_fee", "1.8")
	assertOutputAmount(t, outputs, "a10/winner_payout", "27")
	assertOutputAmount(t, outputs, "a20/winner_payout", "54")
	assertOutputAmount(t, outputs, "a30/winner_payout", "81")
	assertOutputAmount(t, outputs, "deployer/", "119.994")
	assertOutputAmount(t, outputs, "bootstrap/", "79.996")
	if got := sumOutputAmounts(augmented[0].Outputs, betAsset); got != "379.99" {
		t.Fatalf("output total mismatch: got %s want 379.99 in %#v", got, augmented[0].Outputs)
	}
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

func newTestRuntimeForBetAsset(t *testing.T, betAsset, minBetUnit string) *Runtime {
	t.Helper()
	contract := validPredictionContract()
	contract.BetAsset = betAsset
	contract.MinBetUnit = minBetUnit
	contract.Outcomes = append(contract.Outcomes, PredictionOutcome{ID: "c", Text: "draw"})
	content, err := contract.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	deployer := "deployer"
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     9,
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

func mustDecimal(t *testing.T, amount string, precision int) *scommon.Decimal {
	t.Helper()
	decimal, err := scommon.NewDecimalFromString(amount, precision)
	if err != nil {
		t.Fatalf("NewDecimalFromString(%s) failed: %v", amount, err)
	}
	return decimal
}

func mustAssetSet(t *testing.T, assetName, amount string, precision int) wire.TxAssets {
	t.Helper()
	assets, err := contractframework.NewAssetSetWithPrecision(assetName, amount, precision, ErrInvalidAsset)
	if err != nil {
		t.Fatalf("NewAssetSetWithPrecision failed: %v", err)
	}
	return assets
}

func outputsByRecipientAndReason(outputs []ResultOutput, assetName string) map[string]string {
	out := make(map[string]string)
	name := wire.NewAssetNameFromString(assetName)
	for _, output := range outputs {
		key := output.To + "/" + output.Reason
		var amount *scommon.Decimal
		if assetName == SatoshiAssetName && output.Value != 0 {
			amount = scommon.NewDefaultDecimal(output.Value)
		}
		if name != nil {
			if asset, err := output.Assets.Find(name); err == nil && asset != nil {
				amount = asset.Amount.Clone()
			}
		}
		if amount == nil {
			continue
		}
		out[key] = amount.String()
	}
	return out
}

func assertOutputAmount(t *testing.T, outputs map[string]string, key, want string) {
	t.Helper()
	if got := outputs[key]; got != want {
		t.Fatalf("output %s mismatch: got %s want %s in %#v", key, got, want, outputs)
	}
}

func sumOutputAmounts(outputs []ResultOutput, assetName string) string {
	total := scommon.NewDefaultDecimal(0)
	name := wire.NewAssetNameFromString(assetName)
	for _, output := range outputs {
		if assetName == SatoshiAssetName {
			total = scommon.DecimalAdd(total, scommon.NewDefaultDecimal(output.Value))
			continue
		}
		if name == nil {
			continue
		}
		if asset, err := output.Assets.Find(name); err == nil && asset != nil {
			total = total.AddAlignPrecision(asset.Amount.Clone())
		}
	}
	return total.String()
}
