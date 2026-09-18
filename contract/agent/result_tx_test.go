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

func TestBuildResultTxFromPredictionSettlementPlan(t *testing.T) {
	runtime := newTestRuntime(t)
	requireReady(t, runtime)
	requireBet(t, runtime, "alice", "a", "60000")
	requireBet(t, runtime, "bob", "b", "40000")
	// Business liabilities and framework quantities are separate facts. The
	// fixture explicitly establishes both; a physical UTXO never authorizes itself.
	require.NoError(t, runtime.managed.Credit(100000, nil))
	settlement, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeOutcome, OutcomeID: "a", Result: "Team A 101, Team B 98",
			ResultURL: "https://example.com/match/result/123", ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 1,
	})
	require.NoError(t, err)
	plans, err := contractframework.BuildSettlementResultPlans(
		[]*PredictionSettlementPlan{settlement}, defaultAgentSettlementResultOptions())
	require.NoError(t, err)
	store := NewRuntimeStore()
	store.Add(runtime)
	txidA, txidB := chainhash.Hash{1}, chainhash.Hash{2}
	plans, err = AugmentResultPlans(plans, func(addr ContractAddress) ([]UTXO, error) {
		return []UTXO{
			contractframework.UTXOFromTxOutput(OutPoint{TxID: txidB.String(), Vout: 1}, addr, 11, &wire.TxOut{Value: 40000}),
			contractframework.UTXOFromTxOutput(OutPoint{TxID: txidA.String(), Vout: 0}, addr, 10, &wire.TxOut{Value: 60000}),
		}, nil
	}, store, nil, DefaultGasConfig().GasAssetName, "bootstrap")
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Len(t, plans[0].Inputs, 2)
	require.Equal(t, txidA.String(), plans[0].Inputs[0].TxID)
	resultTx, err := contractframework.BuildResultTx(contractframework.ResultTxBuildRequest{
		Status: ResultStatusSuccess, Plans: plans, ResolveScript: testResultScriptResolver,
	}, contractframework.ResultTxBuildOptions{})
	require.NoError(t, err)
	require.Len(t, resultTx.TxIn, 2)
	require.Len(t, resultTx.TxOut, 5)
	require.Equal(t, int64(6000), resultTx.TxOut[0].Value)
	require.Equal(t, int64(90000), resultTx.TxOut[3].Value)
	_, _, err = contractcommonReadResultPayload(resultTx)
	require.NoError(t, err)
	require.NoError(t, contractframework.VerifyCanonicalResultTx(contractframework.CanonicalResultVerifyRequest{
		Label: "agent", ResultTx: resultTx, Status: ResultStatusSuccess, Plans: plans,
		ResolveScript: testResultScriptResolver, CheckPayload: true,
	}))
}

func TestAugmentResultPlansUsesAssetPrecisionForPredictionPayouts(t *testing.T) {
	const assetName = "brc20:f:sgas"
	runtime := newTestRuntimeForBetAsset(t, assetName, "1")
	addr := runtime.Address()
	precision := func(name string) (int, bool) { return 0, name == assetName }
	plans, err := contractframework.BuildSettlementResultPlans([]*PredictionSettlementPlan{{
		Contract: addr.MustEncode(),
		Transfers: []contractframework.SettlementTransfer{
			{To: "alice", AssetName: assetName, AssetAmt: "33.3333333333", Reason: "winner_payout"},
			{To: "bob", AssetName: assetName, AssetAmt: "33.3333333333", Reason: "winner_payout"},
			{To: "carol", AssetName: assetName, AssetAmt: "33.3333333333", Reason: "winner_payout"},
		},
	}}, agentSettlementResultOptions(precision))
	require.NoError(t, err)
	assets := mustAssetSet(t, assetName, "99", 0)
	require.NoError(t, runtime.managed.Credit(0, assets))
	store := NewRuntimeStore()
	store.Add(runtime)
	augmented, err := AugmentResultPlans(plans, func(addr ContractAddress) ([]UTXO, error) {
		outpoint := OutPoint{TxID: chainhash.Hash{3}.String(), Vout: 0}
		return []UTXO{contractframework.UTXOFromTxOutput(outpoint, addr, 1, &wire.TxOut{Assets: assets})}, nil
	}, store, precision, DefaultGasConfig().GasAssetName, "bootstrap")
	require.NoError(t, err)
	require.Len(t, augmented, 1)
	require.Len(t, augmented[0].Outputs, 3)
	for _, output := range augmented[0].Outputs {
		require.Len(t, output.Assets, 1)
		require.Equal(t, 0, output.Assets[0].Amount.Precision)
		require.Equal(t, "33", output.Assets[0].Amount.String())
	}
	require.Equal(t, "99", sumOutputAmounts(augmented[0].Outputs, assetName))
}

func TestAugmentResultPlansRetainsOperatingGasAfterPredictionCompletes(t *testing.T) {
	const asset = "brc20:f:sgas"
	runtime := newTestRuntimeForBetAsset(t, asset, "1")
	runtime.config.AssetPrecision = func(name string) (int, bool) { return 18, name == asset }
	requireReady(t, runtime)
	for _, bet := range []struct{ address, outcome, amount string }{
		{"a10", "a", "10"}, {"a20", "a", "20"}, {"a30", "a", "30"},
		{"b10", "b", "10"}, {"b20", "b", "20"}, {"b30", "b", "30"},
		{"c10", "c", "10"}, {"c20", "c", "20"}, {"c30", "c", "30"},
	} {
		requireBet(t, runtime, bet.address, bet.outcome, bet.amount)
	}
	settlement, err := runtime.ApplyConfirm(ApplyConfirmRequest{
		Invoker: "core",
		Param: PredictionConfirmParam{
			ResultType: ResultTypeOutcome, OutcomeID: "a", Result: "home wins",
			ResultURL: "https://example.com/match/result/123", ObservedAt: runtime.Contract().EventTime + 1,
		},
		TimeValue: runtime.Contract().ConfirmAfter + 1,
	})
	require.NoError(t, err)
	plans, err := contractframework.BuildSettlementResultPlans(
		[]*PredictionSettlementPlan{settlement}, agentSettlementResultOptions(runtime.config.AssetPrecision))
	require.NoError(t, err)
	physical := mustAssetSet(t, asset, "380", 18)
	require.NoError(t, runtime.managed.Credit(0, physical))
	store := NewRuntimeStore()
	store.Add(runtime)
	plans[0].GasFee = mustDecimal(t, "0.01", 18)
	augmented, err := AugmentResultPlans(plans, func(addr ContractAddress) ([]UTXO, error) {
		outpoint := OutPoint{TxID: chainhash.Hash{4}.String(), Vout: 0}
		return []UTXO{contractframework.UTXOFromTxOutput(outpoint, addr, 1, &wire.TxOut{Assets: physical})}, nil
	}, store, runtime.config.AssetPrecision, asset, "bootstrap")
	require.NoError(t, err)
	outputs := outputsByRecipientAndReason(augmented[0].Outputs, asset)
	assertOutputAmount(t, outputs, "deployer/deployer_fee", "10.8")
	assertOutputAmount(t, outputs, "agent/agent_fee", "5.4")
	assertOutputAmount(t, outputs, "bootstrap/bootstrap_fee", "1.8")
	assertOutputAmount(t, outputs, "a10/winner_payout", "27")
	assertOutputAmount(t, outputs, "a20/winner_payout", "54")
	assertOutputAmount(t, outputs, "a30/winner_payout", "81")
	addr := runtime.Address()
	assertOutputAmount(t, outputs, addr.MustEncode()+"/", "199.99")
	require.NotContains(t, outputs, "deployer/")
	require.Equal(t, "379.99", sumOutputAmounts(augmented[0].Outputs, asset))
	require.False(t, runtime.State().Closed)
}

func TestInvalidRefundUsesOnlyFundingUTXO(t *testing.T) {
	const gasAsset = "brc20:f:sgas"
	runtime := newTestRuntime(t)
	runtime.state.Prediction.GasBalance = "50"
	runtime.addBet("valid-bettor", "a", "1000")
	require.NoError(t, runtime.managed.Credit(1000, mustAssetSet(t, gasAsset, "50", 0)))
	before := runtime.managed.Clone()
	store := NewRuntimeStore()
	store.Add(runtime)
	addr := runtime.Address()
	managed := OutPoint{TxID: chainhash.Hash{5}.String(), Vout: 1}
	invalidFunding := OutPoint{TxID: chainhash.Hash{6}.String(), Vout: 1}
	plans, err := AugmentResultPlans([]ResultPlan{{
		Contract: addr.MustEncode(), InputScope: contractframework.ResultInputScopeExplicit,
		Inputs: []OutPoint{invalidFunding}, GasFee: mustDecimal(t, "50", 0),
		Outputs: []ResultOutput{{To: "invalid-invoker", Value: 200, Reason: "refund"}},
	}}, func(got ContractAddress) ([]UTXO, error) {
		require.True(t, got.Equal(addr))
		return []UTXO{
			contractframework.UTXOFromTxOutput(managed, addr, 10,
				&wire.TxOut{Value: 1000, Assets: mustAssetSet(t, gasAsset, "50", 0)}),
			contractframework.UTXOFromTxOutput(invalidFunding, addr, 11,
				&wire.TxOut{Value: 200, Assets: mustAssetSet(t, gasAsset, "50", 0)}),
		}, nil
	}, store, func(name string) (int, bool) { return 0, name == gasAsset }, gasAsset, "bootstrap")
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Equal(t, []OutPoint{invalidFunding}, plans[0].Inputs)
	require.Len(t, plans[0].Outputs, 1)
	require.Equal(t, "invalid-invoker", plans[0].Outputs[0].To)
	require.Equal(t, int64(200), plans[0].Outputs[0].Value)
	require.Equal(t, before, runtime.managed)
}

func TestAgentManagedQuantitiesParticipateInStateRoot(t *testing.T) {
	runtime := newTestRuntime(t)
	before := runtime.StateRoot()
	require.NoError(t, runtime.managed.Credit(1000, mustAssetSet(t, "brc20:f:sgas", "50", 0)))
	require.NotEqual(t, before, runtime.StateRoot())
	store := NewRuntimeStore()
	store.Add(runtime)
	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "managedFunding")
	decoded, err := DecodeRuntimeStore(encoded)
	require.NoError(t, err)
	require.Equal(t, store.StateRoot(), decoded.StateRoot())
	restored, ok := decoded.Get(runtime.Address())
	require.True(t, ok)
	require.Equal(t, runtime.managed, restored.managed)
}

func testResultScriptResolver(output ResultOutput) ([]byte, error) {
	return []byte(output.To), nil
}

func testResultOutputResolver(tx *wire.MsgTx) ([]ResultOutput, error) {
	return contractframework.ResultOutputsFromTx(tx, TestnetContractPrefix,
		contractcommon.ParseContractPkScript,
		func(script []byte) (string, bool, error) { return string(script), len(script) != 0, nil })
}

func contractcommonReadResultPayload(tx *wire.MsgTx) (int, ResultPayload, error) {
	for i, output := range tx.TxOut {
		payload, err := contractcommon.ReadResultNullDataScript(output.PkScript)
		if err == nil {
			return i, payload, nil
		}
	}
	return -1, ResultPayload{}, fmt.Errorf("missing result payload")
}

func newTestRuntimeForBetAsset(t *testing.T, betAsset, minBetUnit string) *Runtime {
	t.Helper()
	prediction := validPredictionContract()
	prediction.BetAsset = betAsset
	prediction.MinBetUnit = minBetUnit
	prediction.Outcomes = append(prediction.Outcomes, PredictionOutcome{ID: "c", Text: "draw"})
	content, err := prediction.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit: 1000, SubType: SubtypePrediction, Version: CurrentAgentVersion,
		DeployNonce: 9, ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, deploy.SubType, content, "deployer", deploy.DeployNonce)
	require.NoError(t, err)
	runtime, err := NewRuntimeWithDeployer(addr, deploy, testRuntimeConfig(), "deployer")
	require.NoError(t, err)
	return runtime
}

func mustDecimal(t *testing.T, amount string, precision int) *scommon.Decimal {
	t.Helper()
	decimal, err := scommon.NewDecimalFromString(amount, precision)
	require.NoError(t, err)
	return decimal
}

func mustAssetSet(t *testing.T, assetName, amount string, precision int) wire.TxAssets {
	t.Helper()
	assets, err := contractframework.NewAssetSetWithPrecision(assetName, amount, precision, ErrInvalidAsset)
	require.NoError(t, err)
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
		if amount != nil {
			out[key] = amount.String()
		}
	}
	return out
}

func assertOutputAmount(t *testing.T, outputs map[string]string, key, want string) {
	t.Helper()
	require.Equal(t, want, outputs[key], "output %s in %#v", key, outputs)
}

func sumOutputAmounts(outputs []ResultOutput, assetName string) string {
	total := scommon.NewDefaultDecimal(0)
	name := wire.NewAssetNameFromString(assetName)
	for _, output := range outputs {
		if assetName == SatoshiAssetName {
			total = total.AddAlignPrecision(scommon.NewDefaultDecimal(output.Value))
		} else if name != nil {
			if asset, err := output.Assets.Find(name); err == nil && asset != nil {
				total = total.AddAlignPrecision(asset.Amount.Clone())
			}
		}
	}
	return total.String()
}
