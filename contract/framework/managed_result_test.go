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

func mustManagedResultAssets(t *testing.T, assetName string, amount uint64) wire.TxAssets {
	t.Helper()
	assets, err := NewAssetSet(assetName, mustManagedResultDecimal(t, amount))
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
