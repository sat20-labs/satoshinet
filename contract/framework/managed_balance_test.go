package framework

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const quantityToken = "ordx:f:quantity"
const quantityGas = "brc20:f:gas"

func quantityAssets(t *testing.T, token, gas int64) wire.TxAssets {
	t.Helper()
	var assets wire.TxAssets
	for name, amount := range map[string]int64{quantityToken: token, quantityGas: gas} {
		if amount == 0 {
			continue
		}
		a, err := NewAssetSet(name, scommon.NewDefaultDecimal(amount))
		require.NoError(t, err)
		require.NoError(t, assets.Merge(a))
	}
	return assets
}

func quantityOutput(t *testing.T, outputs []ResultOutput, recipient, name string) string {
	t.Helper()
	total := ZeroDecimal()
	for _, output := range outputs {
		if output.To != recipient {
			continue
		}
		if name == contract.SatoshiAssetName {
			total = total.AddAlignPrecision(scommon.NewDefaultDecimal(output.Value))
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.String() == name {
				total = total.AddAlignPrecision(asset.Amount.Clone())
			}
		}
	}
	return total.String()
}

func quantityView(t *testing.T, addr contract.ContractAddress, utxos ...UTXO) ResultPlanUTXOView {
	t.Helper()
	view, err := CollectResultPlanUTXOs(ResultPlan{Contract: addr.MustEncode()},
		func(contract.ContractAddress) ([]UTXO, error) { return utxos, nil })
	require.NoError(t, err)
	return view
}

// The same settlement contract applies to every runtime type. Physical UTXO
// fragmentation does not enter the managed quantity ledger or profit policy.
func TestManagedSettlementConformanceMatrix(t *testing.T) {
	for _, module := range []ModuleType{ModuleTemplate, ModuleEVM, ModuleAgent} {
		for _, closed := range []bool{false, true} {
			t.Run(fmt.Sprintf("module_%d/closed_%v", module, closed), func(t *testing.T) {
				addr := testContractAddress(t, module, 42)
				managed := contract.ManagedBalance{Value: 100, Assets: quantityAssets(t, 100, 10)}
				utxo := UTXOFromTxOutput(OutPoint{TxID: "balance", Vout: 0}, addr, 1,
					&wire.TxOut{Value: 105, Assets: quantityAssets(t, 107, 10)})
				plan, err := AugmentManagedResultPlan(ManagedResultRequest{
					Plan: ResultPlan{
						Contract: addr.MustEncode(), GasFee: scommon.NewDefaultDecimal(2),
						Outputs: []ResultOutput{{To: "user", Value: 40, Assets: quantityAssets(t, 40, 0)}},
					},
					View: quantityView(t, addr, utxo), Managed: managed,
					Closed: closed, DeployerAddress: "deployer", BootstrapAddress: "bootstrap",
					GasAssetName: quantityGas, Precision: AssetPrecisionPolicy{Fallback: 0},
				})
				require.NoError(t, err)
				require.Equal(t, "40", quantityOutput(t, plan.Outputs, "user", quantityToken))
				require.Equal(t, "40", quantityOutput(t, plan.Outputs, "user", contract.SatoshiAssetName))
				if closed {
					require.Equal(t, "42", quantityOutput(t, plan.Outputs, "deployer", quantityToken))
					require.Equal(t, "25", quantityOutput(t, plan.Outputs, "bootstrap", quantityToken))
					require.Equal(t, "42", quantityOutput(t, plan.Outputs, "deployer", contract.SatoshiAssetName))
					require.Equal(t, "23", quantityOutput(t, plan.Outputs, "bootstrap", contract.SatoshiAssetName))
					require.Equal(t, "5", quantityOutput(t, plan.Outputs, "deployer", quantityGas))
					require.Equal(t, "3", quantityOutput(t, plan.Outputs, "bootstrap", quantityGas))
					require.Equal(t, "0", quantityOutput(t, plan.Outputs, addr.MustEncode(), quantityToken))
				} else {
					require.Equal(t, "67", quantityOutput(t, plan.Outputs, addr.MustEncode(), quantityToken))
					require.Equal(t, "65", quantityOutput(t, plan.Outputs, addr.MustEncode(), contract.SatoshiAssetName))
					require.Equal(t, "8", quantityOutput(t, plan.Outputs, addr.MustEncode(), quantityGas))
					require.Equal(t, "0", quantityOutput(t, plan.Outputs, "bootstrap", quantityToken))
					require.Equal(t, "0", quantityOutput(t, plan.Outputs, "bootstrap", contract.SatoshiAssetName))
					require.Equal(t, "0", quantityOutput(t, plan.Outputs, "deployer", quantityToken))
				}
				require.NoError(t, VerifyResultInputCoverage(plan.InputUTXOs, plan.Outputs, plan.GasFee, quantityGas))
				require.NoError(t, ApplyManagedResultBalances([]ResultPlan{plan},
					func(contract.ContractAddress) (*contract.ManagedBalance, bool) { return &managed, true },
					func(contract.ContractAddress) (bool, error) { return closed, nil }))
				if closed {
					require.True(t, managed.IsZero())
				} else {
					require.Equal(t, int64(60), managed.Value)
					amount, err := managed.AssetAmount(quantityToken)
					require.NoError(t, err)
					require.Equal(t, "60", amount.String())
				}
			})
		}
	}
}

func TestManagedSettlementCannotSpendAnomalyOrHideDeficit(t *testing.T) {
	addr := testContractAddress(t, ModuleEVM, 43)
	for _, tc := range []struct {
		name                     string
		managed, physical, spend int64
	}{
		{"spend_unmanaged", 10, 100, 11},
		{"physical_deficit", 100, 99, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			managed := contract.ManagedBalance{Value: tc.managed}
			utxo := UTXOFromTxOutput(OutPoint{TxID: "balance"}, addr, 1, &wire.TxOut{Value: tc.physical})
			_, err := AugmentManagedResultPlan(ManagedResultRequest{
				Plan: ResultPlan{Contract: addr.MustEncode(), Outputs: []ResultOutput{{To: "user", Value: tc.spend}}},
				View: quantityView(t, addr, utxo), Managed: managed,
				BootstrapAddress: "bootstrap", GasAssetName: quantityGas,
			})
			require.ErrorIs(t, err, ErrAccountingInvariant)
			require.Equal(t, tc.managed, managed.Value)
		})
	}
}

func TestManagedFailureRefundDoesNotDebitOtherUsers(t *testing.T) {
	addr := testContractAddress(t, ModuleAgent, 44)
	managed := contract.ManagedBalance{Value: 100, Assets: quantityAssets(t, 100, 10)}
	old := UTXOFromTxOutput(OutPoint{TxID: "old"}, addr, 1, &wire.TxOut{
		Value: managed.Value, Assets: managed.Assets.Clone(),
	})
	funding := newContractOutput("failed", 0, addr, 20, quantityAssets(t, 30, 5), nil)
	failed := UTXOFromTxOutput(funding.OutPoint, addr, 2, &wire.TxOut{Value: 20, Assets: funding.TxAssets()})
	request := FundingFailureRequest{
		Height: 2, TxID: "failed", Kind: ExecutionKindInvoke, Contract: addr, CallID: "failed-call",
		Recipient: "caller", Funding: []ContractOutput{funding}, GasAsset: quantityGas,
		GasFee: scommon.NewDefaultDecimal(2), GasLimit: 100000,
	}
	outcome, err := FundingFailureOutcome(request)
	require.NoError(t, err)
	require.Equal(t, ResultFeeModeGasAsset, outcome.ResultFeeMode)
	require.True(t, outcome.RequiresResult)
	plan, err := FailureResultPlan(outcome.ToRecord())
	require.NoError(t, err)
	plan.InputScope = ResultInputScopeAllContractUTXOs
	plans := AddGasFeesToResultPlans([]ResultPlan{plan}, []ExecutionRecord{outcome.ToRecord()})
	plans = AttachCallFunding(plans, []ExecutionRecord{outcome.ToRecord()})
	plan, err = AugmentManagedResultPlan(ManagedResultRequest{
		Plan: plans[0], View: quantityView(t, addr, old, failed), Managed: managed,
		GasAssetName: quantityGas, BootstrapAddress: "bootstrap",
	})
	require.NoError(t, err)
	require.Equal(t, "20", quantityOutput(t, plan.Outputs, "caller", contract.SatoshiAssetName))
	require.Equal(t, "30", quantityOutput(t, plan.Outputs, "caller", quantityToken))
	require.Equal(t, "3", quantityOutput(t, plan.Outputs, "caller", quantityGas))
	require.Equal(t, []OutPoint{failed.OutPoint}, plan.Inputs)
	require.Equal(t, managed, *plan.ManagedRemainder)
	require.Equal(t, "0", quantityOutput(t, plan.Outputs, addr.MustEncode(), contract.SatoshiAssetName))
	require.NoError(t, VerifyResultInputCoverage(plan.InputUTXOs, plan.Outputs, plan.GasFee, quantityGas))
}

func TestManagedFailureRefundFallsBackToTenPlainSats(t *testing.T) {
	addr := testContractAddress(t, ModuleTemplate, 45)
	funding := newContractOutput("failed", 0, addr, 20, quantityAssets(t, 3, 5), nil)
	outcome, err := FundingFailureOutcome(FundingFailureRequest{
		TxID: "failed", Contract: addr, CallID: "failed-call", Recipient: "caller",
		Funding: []ContractOutput{funding}, GasAsset: quantityGas, GasFee: scommon.NewDefaultDecimal(9),
	})
	require.NoError(t, err)
	require.Equal(t, ResultFeeModeSatoshiFee, outcome.ResultFeeMode)
	require.True(t, outcome.RequiresResult)
	require.Empty(t, outcome.GasRefundRecipient)
	plan, err := FailureResultPlan(outcome.ToRecord())
	require.NoError(t, err)
	plans := AddSatoshiFeesToResultPlans([]ResultPlan{plan}, []ExecutionRecord{outcome.ToRecord()})
	require.Len(t, plans, 1)
	require.Equal(t, int64(InvalidRefundSatoshiFee), plans[0].SatoshiFee)
	view := quantityView(t, addr, UTXOFromTxOutput(funding.OutPoint, addr, 1,
		&wire.TxOut{Value: 20, Assets: funding.TxAssets()}))
	plan, err = AugmentManagedResultPlan(ManagedResultRequest{
		Plan: plans[0], View: view, Managed: contract.ManagedBalance{}, GasAssetName: quantityGas,
	})
	require.NoError(t, err)
	require.Equal(t, "10", quantityOutput(t, plan.Outputs, "caller", contract.SatoshiAssetName))
	require.Equal(t, "3", quantityOutput(t, plan.Outputs, "caller", quantityToken))
	require.Equal(t, "5", quantityOutput(t, plan.Outputs, "caller", quantityGas))
}

func TestManagedFailureWithoutRefundFeeBecomesUnmanaged(t *testing.T) {
	addr := testContractAddress(t, ModuleAgent, 46)
	funding := newContractOutput("failed", 0, addr, 8, quantityAssets(t, 3, 5), nil)
	outcome, err := FundingFailureOutcome(FundingFailureRequest{
		TxID: "failed", Contract: addr, CallID: "failed-call", Recipient: "caller",
		Funding: []ContractOutput{funding}, GasAsset: quantityGas, GasFee: scommon.NewDefaultDecimal(9),
	})
	require.NoError(t, err)
	require.False(t, outcome.RequiresResult)
	require.Empty(t, outcome.AssetIntents)
	require.Empty(t, outcome.GasRefundRecipient)
	require.Zero(t, outcome.GasFee.Sign())

	// A later active Result leaves failed funding unmanaged at the address.
	// Only close recovers those quantities for bootstrap.
	managed := contract.ManagedBalance{Value: 100, Assets: quantityAssets(t, 20, 10)}
	old := UTXOFromTxOutput(OutPoint{TxID: "old"}, addr, 1,
		&wire.TxOut{Value: managed.Value, Assets: managed.Assets.Clone()})
	failed := UTXOFromTxOutput(funding.OutPoint, addr, 2,
		&wire.TxOut{Value: 8, Assets: funding.TxAssets()})
	request := ManagedResultRequest{
		Plan: ResultPlan{Contract: addr.MustEncode()}, View: quantityView(t, addr, old, failed),
		Managed: managed, GasAssetName: quantityGas, BootstrapAddress: "bootstrap", DeployerAddress: "deployer",
	}
	plan, err := AugmentManagedResultPlan(request)
	require.NoError(t, err)
	require.Empty(t, plan.Inputs)
	require.Empty(t, plan.Outputs)
	require.Equal(t, managed, *plan.ManagedRemainder)
	request.Closed = true
	plan, err = AugmentManagedResultPlan(request)
	require.NoError(t, err)
	require.Len(t, plan.Inputs, 2)
	require.Equal(t, "38", quantityOutput(t, plan.Outputs, "bootstrap", contract.SatoshiAssetName))
	require.Equal(t, "9", quantityOutput(t, plan.Outputs, "bootstrap", quantityToken))
	require.Equal(t, "8", quantityOutput(t, plan.Outputs, "bootstrap", quantityGas))
	require.Equal(t, "0", quantityOutput(t, plan.Outputs, addr.MustEncode(), contract.SatoshiAssetName))
}


func TestManagedCloseRequiresCarrierAfterPartialAssetsAggregate(t *testing.T) {
	addr := testContractAddress(t, ModuleTemplate, 53)
	partial := quantityAssets(t, 1, 0)
	require.Len(t, partial, 1)
	partial[0].BindingSat = 2
	managedAssets := quantityAssets(t, 3, 0)
	managedAssets[0].BindingSat = 2

	managed := contract.ManagedBalance{Value: 0, Assets: managedAssets.Clone()}
	var utxos []UTXO
	for i := uint32(0); i < 3; i++ {
		utxos = append(utxos, UTXOFromTxOutput(
			OutPoint{TxID: "close-partial", Vout: i}, addr, 1,
			&wire.TxOut{Value: 0, Assets: partial.Clone()}))
	}

	request := ManagedResultRequest{
		Plan: ResultPlan{Contract: addr.MustEncode()},
		View: quantityView(t, addr, utxos...),
		Managed: managed,
		Closed: true,
		DeployerAddress: "deployer",
		BootstrapAddress: "bootstrap",
		GasAssetName: quantityGas,
	}
	_, err := AugmentManagedResultPlan(request)
	require.Error(t, err)
	require.ErrorContains(t, err, "insufficient carrier sats")

	// After one plain sat is added to the managed balance, the same close can
	// allocate the required carrier first and complete successfully.
	satUTXO := UTXOFromTxOutput(OutPoint{TxID: "close-carrier", Vout: 0}, addr, 1,
		&wire.TxOut{Value: 1})
	request.View = quantityView(t, addr, append(utxos, satUTXO)...)
	request.Managed.Value = 1
	plan, err := AugmentManagedResultPlan(request)
	require.NoError(t, err)
	require.Equal(t, "2", quantityOutput(t, plan.Outputs, "deployer", quantityToken))
	require.Equal(t, "1", quantityOutput(t, plan.Outputs, "bootstrap", quantityToken))
	require.Equal(t, "1", quantityOutput(t, plan.Outputs, "deployer", contract.SatoshiAssetName))
	require.Equal(t, "0", quantityOutput(t, plan.Outputs, "bootstrap", contract.SatoshiAssetName))
}
