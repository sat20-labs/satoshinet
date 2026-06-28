package framework

import (
	"testing"

	"github.com/stretchr/testify/require"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const managedResultGasAsset = "brc20:f:sgas"

func TestAugmentResultPlanWithManagedStateUsesPhysicalGasWhenManagedGasMissing(t *testing.T) {
	plan, err := AugmentResultPlanWithManagedState(ManagedResultAugmentRequest{
		Plan: ResultPlan{
			Contract: "tc1qcontract",
		},
		View: ResultPlanUTXOView{
			Inputs: []OutPoint{{TxID: "gas-utxo", Vout: 0}},
			Assets: mustManagedResultAssets(t, managedResultGasAsset, 200),
		},
		GasAssetName:     managedResultGasAsset,
		GasFee:           mustManagedResultDecimal(t, 50),
		BootstrapAddress: "bootstrap",
		SurplusMode:      ResultSurplusToBootstrap,
	})
	require.NoError(t, err)
	require.Len(t, plan.Outputs, 1)
	require.Equal(t, "bootstrap", plan.Outputs[0].To)
	requireManagedResultAssetAmount(t, plan.Outputs[0], managedResultGasAsset, 150)
}

func TestAugmentResultPlanWithManagedStateUsesPhysicalGasForManagedGasShortfall(t *testing.T) {
	plan, err := AugmentResultPlanWithManagedState(ManagedResultAugmentRequest{
		Plan: ResultPlan{
			Contract: "tc1qcontract",
		},
		View: ResultPlanUTXOView{
			Inputs: []OutPoint{{TxID: "gas-utxo", Vout: 0}},
			Assets: mustManagedResultAssets(t, managedResultGasAsset, 200),
		},
		ManagedAssets: ResultOutput{
			Assets: mustManagedResultAssets(t, managedResultGasAsset, 20),
		},
		GasAssetName:     managedResultGasAsset,
		GasFee:           mustManagedResultDecimal(t, 50),
		BootstrapAddress: "bootstrap",
		SurplusMode:      ResultSurplusToBootstrap,
	})
	require.NoError(t, err)
	require.Len(t, plan.Outputs, 1)
	require.Equal(t, "bootstrap", plan.Outputs[0].To)
	requireManagedResultAssetAmount(t, plan.Outputs[0], managedResultGasAsset, 150)
}

func TestAugmentResultPlanWithManagedStateRejectsWhenPhysicalGasIsInsufficient(t *testing.T) {
	_, err := AugmentResultPlanWithManagedState(ManagedResultAugmentRequest{
		Plan: ResultPlan{
			Contract: "tc1qcontract",
		},
		View: ResultPlanUTXOView{
			Inputs: []OutPoint{{TxID: "gas-utxo", Vout: 0}},
			Assets: mustManagedResultAssets(t, managedResultGasAsset, 30),
		},
		GasAssetName:     managedResultGasAsset,
		GasFee:           mustManagedResultDecimal(t, 50),
		BootstrapAddress: "bootstrap",
		SurplusMode:      ResultSurplusToBootstrap,
	})
	require.ErrorContains(t, err, "insufficient gas asset for result fee")
}

func TestAugmentResultPlanWithManagedStateTruncatesManagedAssetsToAssetPrecision(t *testing.T) {
	assetName := "brc20:f:ooxx"
	plan, err := AugmentResultPlanWithManagedState(ManagedResultAugmentRequest{
		Plan: ResultPlan{
			Contract: "tc1qcontract",
		},
		View: ResultPlanUTXOView{
			Inputs: []OutPoint{{TxID: "asset-utxo", Vout: 0}},
			Assets: mustManagedResultAssetsDecimal(t, assetName, "10"),
		},
		ManagedAssets: ResultOutput{
			To:     "tc1qcontract",
			Assets: mustManagedResultAssetsDecimal(t, assetName, "3.9"),
		},
		Precision: AssetPrecisionPolicy{
			Fallback: 8,
			Resolve: func(name string) (int, bool) {
				return 0, name == assetName
			},
		},
		BootstrapAddress: "bootstrap",
		SurplusMode:      ResultSurplusToBootstrap,
	})
	require.NoError(t, err)
	require.Len(t, plan.Outputs, 2)
	require.Equal(t, "tc1qcontract", plan.Outputs[0].To)
	requireManagedResultAssetString(t, plan.Outputs[0], assetName, "3")
	require.Equal(t, "bootstrap", plan.Outputs[1].To)
	requireManagedResultAssetString(t, plan.Outputs[1], assetName, "7")
}

func mustManagedResultAssets(t *testing.T, assetName string, amount uint64) wire.TxAssets {
	t.Helper()
	assets, err := NewAssetSet(assetName, mustManagedResultDecimal(t, amount))
	require.NoError(t, err)
	return assets
}

func mustManagedResultAssetsDecimal(t *testing.T, assetName, amount string) wire.TxAssets {
	t.Helper()
	decimal, err := scommon.NewDecimalFromString(amount, 8)
	require.NoError(t, err)
	assets, err := NewAssetSet(assetName, decimal)
	require.NoError(t, err)
	return assets
}

func mustManagedResultDecimal(t *testing.T, amount uint64) *scommon.Decimal {
	t.Helper()
	decimal, err := DecimalFromUint64(amount)
	require.NoError(t, err)
	return decimal
}

func requireManagedResultAssetAmount(t *testing.T, output ResultOutput, assetName string, amount uint64) {
	t.Helper()
	name := wire.NewAssetNameFromString(assetName)
	require.NotNil(t, name)
	asset, err := output.Assets.Find(name)
	require.NoError(t, err)
	require.Zero(t, asset.Amount.Cmp(mustManagedResultDecimal(t, amount)))
}

func requireManagedResultAssetString(t *testing.T, output ResultOutput, assetName, amount string) {
	t.Helper()
	name := wire.NewAssetNameFromString(assetName)
	require.NotNil(t, name)
	asset, err := output.Assets.Find(name)
	require.NoError(t, err)
	want, err := scommon.NewDecimalFromString(amount, 8)
	require.NoError(t, err)
	require.Zero(t, asset.Amount.Cmp(want), "got %s want %s", asset.Amount.String(), want.String())
}
