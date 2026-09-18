package framework

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBoundAssetFailureRefundPreservesCarrierSats(t *testing.T) {
	for _, module := range []ModuleType{ModuleTemplate, ModuleEVM, ModuleAgent} {
		for _, fallback := range []bool{false, true} {
			t.Run(fmt.Sprintf("module_%d/fallback_%v", module, fallback), func(t *testing.T) {
				addr := testContractAddress(t, module, 48)
				assets := quantityAssets(t, 20, 5)
				for i := range assets {
					if assets[i].Name.String() == quantityToken {
						assets[i].BindingSat = 1
					}
				}
				funding := newContractOutput("failed", 0, addr, 100, assets, nil)
				fee := int64(2)
				if fallback {
					fee = 9
				}
				outcome, err := FundingFailureOutcome(FundingFailureRequest{
					TxID: "failed", Contract: addr, CallID: "failed-call", Recipient: "caller",
					Funding: []ContractOutput{funding}, GasAsset: quantityGas, GasFee: scommon.NewDefaultDecimal(fee),
				})
				require.NoError(t, err)
				plan, err := FailureResultPlan(outcome.ToRecord())
				require.NoError(t, err)
				if module == ModuleEVM {
					plan.InputScope = ResultInputScopeAllContractUTXOs
					plan.Outputs, err = canonicalIntentOutputs(addr, outcome.AssetIntents, AssetPrecisionPolicy{})
					require.NoError(t, err)
				}
				records := []ExecutionRecord{outcome.ToRecord()}
				plans := AddGasFeesToResultPlans([]ResultPlan{plan}, records)
				plans = AttachCallFunding(plans, records)
				utxo := UTXOFromTxOutput(funding.OutPoint, addr, 1, &wire.TxOut{Value: 100, Assets: assets})
				plan, err = AugmentManagedResultPlan(ManagedResultRequest{
					Plan: plans[0], View: quantityView(t, addr, utxo), GasAssetName: quantityGas,
				})
				require.NoError(t, err)
				require.True(t, plan.ManagedRemainder.IsZero())
				wantValue, wantGas := int64(100), "3"
				if fallback {
					wantValue, wantGas = 90, "5"
				}
				outputValue, err := resultOutputsValue(plan.Outputs)
				require.NoError(t, err)
				require.Equal(t, wantValue, outputValue)
				require.Equal(t, "20", quantityOutput(t, plan.Outputs, "caller", quantityToken))
				require.Equal(t, wantGas, quantityOutput(t, plan.Outputs, "caller", quantityGas))
				outputAssets, err := resultOutputsAssets(plan.Outputs)
				require.NoError(t, err)
				bound, err := outputAssets.Find(wire.NewAssetNameFromString(quantityToken))
				require.NoError(t, err)
				require.Equal(t, uint32(1), bound.BindingSat)
				require.Equal(t, int64(100), outputValue+plan.SatoshiFee)
				require.NoError(t, VerifyResultInputCoverage(plan.InputUTXOs, plan.Outputs, plan.GasFee, quantityGas))
			})
		}
	}
}

func TestFailureRefundKeepsPartialBindingUnbound(t *testing.T) {
	for _, module := range []ModuleType{ModuleTemplate, ModuleEVM, ModuleAgent} {
		t.Run(fmt.Sprintf("module_%d", module), func(t *testing.T) {
			addr := testContractAddress(t, module, 51)
			assets := quantityAssets(t, 1, 0)
			assets[0].BindingSat = 2
			funding := []ContractOutput{
				newContractOutput("failed", 0, addr, 10, assets, nil),
				newContractOutput("failed", 1, addr, 10, assets, nil),
			}
			outcome, err := FundingFailureOutcome(FundingFailureRequest{
				TxID: "failed", Contract: addr, CallID: "failed-call", Recipient: "caller",
				Funding: funding, GasAsset: quantityGas, GasFee: scommon.NewDefaultDecimal(9),
			})
			require.NoError(t, err)
			plan, err := FailureResultPlan(outcome.ToRecord())
			require.NoError(t, err)
			if module == ModuleEVM {
				plan.InputScope = ResultInputScopeAllContractUTXOs
				plan.Outputs, err = canonicalIntentOutputs(addr, outcome.AssetIntents, AssetPrecisionPolicy{})
				require.NoError(t, err)
			}
			records := []ExecutionRecord{outcome.ToRecord()}
			plans := AttachCallFunding(AddGasFeesToResultPlans([]ResultPlan{plan}, records), records)
			var utxos []UTXO
			for _, input := range funding {
				utxos = append(utxos, UTXOFromTxOutput(input.OutPoint, addr, 1, &wire.TxOut{Value: 10, Assets: assets}))
			}
			plan, err = AugmentManagedResultPlan(ManagedResultRequest{
				Plan: plans[0], View: quantityView(t, addr, utxos...), GasAssetName: quantityGas,
			})
			require.NoError(t, err)
			require.True(t, plan.ManagedRemainder.IsZero())
			require.Equal(t, "10", quantityOutput(t, plan.Outputs, "caller", contract.SatoshiAssetName))
			require.Equal(t, "2", quantityOutput(t, plan.Outputs, "caller", quantityToken))
			require.Equal(t, int64(10), plan.SatoshiFee)
		})
	}
}

func TestResultFundingSkipsDustAndKeepsQuantityLedger(t *testing.T) {
	addr := testContractAddress(t, ModuleEVM, 49)
	var utxos []UTXO
	for i := 0; i < 2000; i++ {
		utxos = append(utxos, UTXOFromTxOutput(OutPoint{TxID: fmt.Sprintf("%064x", i)}, addr, 1, &wire.TxOut{Value: 1}))
	}
	large := UTXOFromTxOutput(OutPoint{TxID: fmt.Sprintf("%064x", 3000)}, addr, 2, &wire.TxOut{Value: 1000})
	utxos = append(utxos, large)
	request := ManagedResultRequest{
		Plan: ResultPlan{Contract: addr.MustEncode(), Outputs: []ResultOutput{{To: "user", Value: 77}}},
		View: quantityView(t, addr, utxos...), Managed: contract.ManagedBalance{Value: 1000}, GasAssetName: quantityGas,
	}
	plan, err := AugmentManagedResultPlan(request)
	require.NoError(t, err)
	require.Equal(t, []OutPoint{large.OutPoint}, plan.Inputs)
	require.Equal(t, int64(923), plan.ManagedRemainder.Value)
	require.Equal(t, "923", quantityOutput(t, plan.Outputs, addr.MustEncode(), contract.SatoshiAssetName))
	for i, j := 0, len(utxos)-1; i < j; i, j = i+1, j-1 {
		utxos[i], utxos[j] = utxos[j], utxos[i]
	}
	request.View.UTXOs = utxos
	replayed, err := AugmentManagedResultPlan(request)
	require.NoError(t, err)
	require.Equal(t, plan, replayed, "provider ordering must not change consensus selection")
}

func TestResultFundingReusesMultiAssetInputAndBacksChange(t *testing.T) {
	addr := testContractAddress(t, ModuleTemplate, 50)
	assets := quantityAssets(t, 40, 10)
	for i := range assets {
		if assets[i].Name.String() == quantityToken {
			assets[i].BindingSat = 2
		}
	}
	utxo := UTXOFromTxOutput(OutPoint{TxID: "multi"}, addr, 1, &wire.TxOut{Value: 100, Assets: assets})
	plan, err := AugmentManagedResultPlan(ManagedResultRequest{
		Plan: ResultPlan{Contract: addr.MustEncode(), GasFee: scommon.NewDefaultDecimal(2),
			Outputs: []ResultOutput{{To: "user", Assets: quantityAssets(t, 10, 0)}}},
		View: quantityView(t, addr, utxo), Managed: contract.ManagedBalance{Value: 100, Assets: assets}, GasAssetName: quantityGas,
	})
	require.NoError(t, err)
	require.Len(t, plan.Inputs, 1)
	require.Equal(t, "5", quantityOutput(t, plan.Outputs, "user", contract.SatoshiAssetName))
	require.Equal(t, "95", quantityOutput(t, plan.Outputs, addr.MustEncode(), contract.SatoshiAssetName))
	require.Equal(t, "30", quantityOutput(t, plan.Outputs, addr.MustEncode(), quantityToken))
	require.Equal(t, int64(95), plan.ManagedRemainder.Value)
	require.NoError(t, VerifyResultInputCoverage(plan.InputUTXOs, plan.Outputs, plan.GasFee, quantityGas))
}

func TestResultFundingUsesPartialBindingAsPlainSat(t *testing.T) {
	addr := testContractAddress(t, ModuleEVM, 52)
	assets := quantityAssets(t, 1, 0)
	assets[0].BindingSat = 2
	var utxos []UTXO
	managed := contract.ManagedBalance{}
	for i := uint32(0); i < 2; i++ {
		utxos = append(utxos, UTXOFromTxOutput(OutPoint{TxID: "bound", Vout: i}, addr, 1, &wire.TxOut{Value: 1, Assets: assets}))
		require.NoError(t, managed.Credit(1, assets))
	}
	plain, err := managed.PlainValue()
	require.NoError(t, err)
	require.Equal(t, int64(1), plain, "two units form one complete BindingSat group in the aggregate managed balance")
	plan, err := AugmentManagedResultPlan(ManagedResultRequest{
		Plan: ResultPlan{Contract: addr.MustEncode(), Outputs: []ResultOutput{{To: "user", Value: 1}}},
		View: quantityView(t, addr, utxos...), Managed: managed, GasAssetName: quantityGas,
	})
	require.NoError(t, err)
	require.Len(t, plan.Inputs, 1, "a partial-bound UTXO exposes its sat as plain funding on L2")
	require.Equal(t, "0", quantityOutput(t, plan.Outputs, addr.MustEncode(), contract.SatoshiAssetName))
	require.Equal(t, "1", quantityOutput(t, plan.Outputs, addr.MustEncode(), quantityToken))
	require.Equal(t, int64(1), plan.ManagedRemainder.Value)
	remaining, err := plan.ManagedRemainder.AssetAmount(quantityToken)
	require.NoError(t, err)
	require.Equal(t, "2", remaining.String(), "unselected contract UTXOs remain represented by the managed quantity ledger")
}


func TestResultFundingRechecksCarrierAfterPartialBindingMerge(t *testing.T) {
	addr := testContractAddress(t, ModuleTemplate, 54)
	partial := quantityAssets(t, 1, 0)
	partial[0].BindingSat = 2

	mandatory := UTXOFromTxOutput(OutPoint{TxID: "a-mandatory", Vout: 0}, addr, 1,
		&wire.TxOut{Value: 0, Assets: partial})
	assetSat := UTXOFromTxOutput(OutPoint{TxID: "b-partial-sat", Vout: 0}, addr, 1,
		&wire.TxOut{Value: 1, Assets: partial})
	plainSat := UTXOFromTxOutput(OutPoint{TxID: "c-plain-sat", Vout: 0}, addr, 1,
		&wire.TxOut{Value: 1})

	view := quantityView(t, addr, mandatory, assetSat, plainSat)
	selected, err := selectResultFunding(view,
		[]ResultOutput{{To: "user", Value: 1}}, quantityGas, ZeroDecimal(), 0,
		[]OutPoint{mandatory.OutPoint})
	require.NoError(t, err)
	require.Equal(t, []OutPoint{mandatory.OutPoint, assetSat.OutPoint, plainSat.OutPoint}, selected.Inputs,
		"combining partial assets creates one complete binding group, so change needs one more sat")
}
