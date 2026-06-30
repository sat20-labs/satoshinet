package framework

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestSelectCanonicalInputsRequiredFirstThenSortedPrefix(t *testing.T) {
	contractAddr := testContractAddress(t, ModuleEVM, 1)
	gasAssetName := "ordx:ft:gas"
	utxos := []UTXO{
		mustCanonicalUTXO(t, OutPoint{TxID: "c", Vout: 0}, contractAddr, gasAssetName, 4, 3),
		mustCanonicalUTXO(t, OutPoint{TxID: "a", Vout: 0}, contractAddr, gasAssetName, 3, 1),
		mustCanonicalUTXO(t, OutPoint{TxID: "b", Vout: 0}, contractAddr, gasAssetName, 5, 2),
	}
	result, err := SelectCanonicalInputs(CanonicalSelectionRequest{
		Contract:      contractAddr,
		AssetName:     gasAssetName,
		Required:      mustCanonicalDecimal(t, 9),
		Available:     utxos,
		RequiredFirst: []OutPoint{{TxID: "c", Vout: 0}},
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.Total.Cmp(mustCanonicalDecimal(t, 12)))
	require.Equal(t, 0, result.Change.Cmp(mustCanonicalDecimal(t, 3)))
	require.Equal(t, []string{"c:0", "a:0", "b:0"}, []string{
		result.Inputs[0].OutPoint.String(),
		result.Inputs[1].OutPoint.String(),
		result.Inputs[2].OutPoint.String(),
	})
}

func TestSelectCanonicalInputsInsufficient(t *testing.T) {
	contractAddr := testContractAddress(t, ModuleEVM, 1)
	_, err := SelectCanonicalInputs(CanonicalSelectionRequest{
		Contract:  contractAddr,
		AssetName: "ordx:ft:gas",
		Required:  mustCanonicalDecimal(t, 10),
		Available: []UTXO{
			mustCanonicalUTXO(t, OutPoint{TxID: "a", Vout: 0}, contractAddr, "ordx:ft:gas", 1, 0),
		},
	})
	require.ErrorIs(t, err, ErrInsufficientFunds)
}

func TestBuildCanonicalResultPlan(t *testing.T) {
	contractAddr := testContractAddress(t, ModuleEVM, 1)
	gasAssetName := "ordx:ft:gas"
	available := []UTXO{
		mustCanonicalUTXO(t, OutPoint{TxID: "b", Vout: 0}, contractAddr, contract.SatoshiAssetName, 30, 12),
		mustCanonicalUTXO(t, OutPoint{TxID: "a", Vout: 0}, contractAddr, contract.SatoshiAssetName, 80, 11),
		mustCanonicalUTXO(t, OutPoint{TxID: "invoke", Vout: 1}, contractAddr, gasAssetName, 100, 10),
	}

	plan, err := BuildCanonicalResultPlan(ResultPlanRequest{
		Contract:               contractAddr,
		Available:              available,
		GasAssetName:           gasAssetName,
		GasFee:                 mustCanonicalDecimal(t, 60),
		RequiredGasFundingUTXO: []OutPoint{{TxID: "invoke", Vout: 1}},
		Intents: []AssetIntent{
			{
				From:      contractAddr,
				To:        "tb1qdest",
				AssetName: contract.SatoshiAssetName,
				Amount:    mustCanonicalDecimal(t, 70),
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, plan.InputUTXOs, 2)
	require.Equal(t, OutPoint{TxID: "invoke", Vout: 1}, plan.InputUTXOs[0].OutPoint)
	require.Equal(t, OutPoint{TxID: "a", Vout: 0}, plan.InputUTXOs[1].OutPoint)
	require.Equal(t, []ResultOutput{
		mustCanonicalResultOutput(t, "tb1qdest", contract.SatoshiAssetName, 70),
		{
			To:     contractAddr.MustEncode(),
			Value:  10,
			Assets: mustCanonicalResultOutput(t, contractAddr.MustEncode(), gasAssetName, 40).Assets,
		},
	}, plan.Outputs)
}

func TestResultGasRefundIntentRespectsRetainedGasFunding(t *testing.T) {
	contractAddr := testContractAddress(t, ModuleEVM, 1)
	gasAssetName := "ordx:ft:gas"
	input := OutPoint{TxID: "invoke", Vout: 1}
	intent, err := ResultGasRefundIntent(ResultGasRefund{
		CallID:             "call",
		To:                 "tb1qrefund",
		Inputs:             []OutPoint{input},
		GasFee:             mustCanonicalDecimal(t, 50),
		RetainedGasFunding: mustCanonicalDecimal(t, 300),
	}, contractAddr, []UTXO{
		mustCanonicalUTXO(t, input, contractAddr, gasAssetName, 1000, 10),
	}, gasAssetName)
	require.NoError(t, err)
	require.NotNil(t, intent)
	require.Equal(t, gasAssetName, intent.AssetName)
	require.Equal(t, "650", intent.Amount.String())
}

func TestResultGasRefundIntentRefundsAllUnretainedGas(t *testing.T) {
	contractAddr := testContractAddress(t, ModuleEVM, 1)
	gasAssetName := "ordx:ft:gas"
	input := OutPoint{TxID: "invoke", Vout: 1}
	intent, err := ResultGasRefundIntent(ResultGasRefund{
		CallID: "call",
		To:     "tb1qrefund",
		Inputs: []OutPoint{input},
		GasFee: mustCanonicalDecimal(t, 50),
	}, contractAddr, []UTXO{
		mustCanonicalUTXO(t, input, contractAddr, gasAssetName, 1000, 10),
	}, gasAssetName)
	require.NoError(t, err)
	require.NotNil(t, intent)
	require.Equal(t, gasAssetName, intent.AssetName)
	require.Equal(t, "950", intent.Amount.String())
}

func TestBuildCanonicalResultPlanTruncatesOutputsToAssetPrecision(t *testing.T) {
	contractAddr := testContractAddress(t, ModuleEVM, 1)
	assetName := "brc20:f:ooxx"
	available := []UTXO{
		mustCanonicalUTXODecimal(t, OutPoint{TxID: "asset", Vout: 0}, contractAddr, assetName, "10", 12),
	}

	plan, err := BuildCanonicalResultPlan(ResultPlanRequest{
		Contract:     contractAddr,
		Available:    available,
		GasAssetName: assetName,
		Precision: AssetPrecisionPolicy{
			Fallback: 8,
			Resolve: func(name string) (int, bool) {
				return 0, name == assetName
			},
		},
		Intents: []AssetIntent{
			{
				From:      contractAddr,
				To:        "tb1qdest",
				AssetName: assetName,
				Amount:    mustCanonicalDecimalString(t, "3.9"),
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, plan.InputUTXOs, 1)
	require.Len(t, plan.Outputs, 2)
	requireCanonicalResultOutputAssetString(t, plan.Outputs[0], assetName, "3")
	requireCanonicalResultOutputAssetString(t, plan.Outputs[1], assetName, "7")
}

func TestCanonicalCloseProfit(t *testing.T) {
	contractAddr := testContractAddress(t, ModuleEVM, 1)
	assetName := "ordx:ft:profit"
	funding := OutPoint{TxID: "close", Vout: 0}
	profit := OutPoint{TxID: "profit", Vout: 0}
	value := OutPoint{TxID: "value", Vout: 0}

	plans, err := (CanonicalResultPlanner{
		GasConfig: GasConfig{GasAssetName: "brc20:f:sgas"},
		UTXOs: func(got contract.ContractAddress) ([]UTXO, error) {
			require.True(t, got.Equal(contractAddr))
			return []UTXO{
				mustCanonicalUTXO(t, funding, contractAddr, contract.SatoshiAssetName, 0, 1),
				mustCanonicalUTXO(t, profit, contractAddr, assetName, 100, 2),
				mustCanonicalUTXO(t, value, contractAddr, contract.SatoshiAssetName, 10, 3),
			}, nil
		},
	}).BuildPlans([]ExecutionRecord{{
		Height:           100,
		Contract:         contractAddr,
		RequiresResult:   true,
		FundingInputs:    []OutPoint{funding},
		ResultFeeMode:    ResultFeeModePlainTxFee,
		CloseContract:    true,
		DeployerAddress:  "deployer",
		BootstrapAddress: "bootstrap",
	}})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.ElementsMatch(t, []OutPoint{funding, profit, value}, plans[0].Inputs)
	requireCanonicalResultValue(t, plans[0].Outputs, "deployer", 6)
	requireCanonicalResultValue(t, plans[0].Outputs, "bootstrap", 4)
	requireResultAssetString(t, plans[0].Outputs, "deployer", assetName, "60")
	requireResultAssetString(t, plans[0].Outputs, "bootstrap", assetName, "40")
	requireNoCanonicalResultOutputTo(t, plans[0].Outputs, contractAddr.MustEncode())
}

func TestBuildCanonicalResultPlanRejectsMixedContracts(t *testing.T) {
	contractAddr := testContractAddress(t, ModuleEVM, 1)
	other := testContractAddress(t, ModuleEVM, 9)

	_, err := BuildCanonicalResultPlan(ResultPlanRequest{
		Contract:     contractAddr,
		GasAssetName: "ordx:ft:gas",
		Intents: []AssetIntent{
			{
				From:      other,
				To:        "tb1qdest",
				AssetName: contract.SatoshiAssetName,
				Amount:    mustCanonicalDecimal(t, 1),
			},
		},
	})
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrInsufficientFunds))
}

func mustCanonicalUTXO(t *testing.T, outpoint OutPoint, contractAddr contract.ContractAddress,
	assetName string, amount uint64, height int64) UTXO {

	t.Helper()
	utxo := UTXO{
		OutPoint: outpoint,
		Contract: contractAddr,
		Height:   height,
	}
	if assetName == contract.SatoshiAssetName {
		utxo.TxOutput = indexerTxOutputFromWire(outpoint, &wire.TxOut{Value: int64(amount)})
		return utxo
	}
	assets, err := NewAssetSet(assetName, mustCanonicalDecimal(t, amount))
	require.NoError(t, err)
	utxo.TxOutput = indexerTxOutputFromWire(outpoint, &wire.TxOut{Assets: assets})
	return utxo
}

func mustCanonicalUTXODecimal(t *testing.T, outpoint OutPoint, contractAddr contract.ContractAddress,
	assetName, amount string, height int64) UTXO {

	t.Helper()
	utxo := UTXO{
		OutPoint: outpoint,
		Contract: contractAddr,
		Height:   height,
	}
	if assetName == contract.SatoshiAssetName {
		decimal := mustCanonicalDecimalString(t, amount)
		value, err := DecimalToInt64(*decimal)
		require.NoError(t, err)
		utxo.TxOutput = indexerTxOutputFromWire(outpoint, &wire.TxOut{Value: value})
		return utxo
	}
	assets, err := NewAssetSet(assetName, mustCanonicalDecimalString(t, amount))
	require.NoError(t, err)
	utxo.TxOutput = indexerTxOutputFromWire(outpoint, &wire.TxOut{Assets: assets})
	return utxo
}

func mustCanonicalResultOutput(t *testing.T, to, assetName string, amount uint64) ResultOutput {
	t.Helper()
	output, err := ResultOutputWithAsset(to, assetName, mustCanonicalDecimal(t, amount))
	require.NoError(t, err)
	return output
}

func mustCanonicalDecimal(t *testing.T, amount uint64) *scommon.Decimal {
	t.Helper()
	decimal, err := DecimalFromUint64(amount)
	require.NoError(t, err)
	return decimal
}

func mustCanonicalDecimalString(t *testing.T, amount string) *scommon.Decimal {
	t.Helper()
	decimal, err := scommon.NewDecimalFromString(amount, 8)
	require.NoError(t, err)
	return decimal
}

func requireCanonicalResultOutputAssetString(t *testing.T, output ResultOutput, assetName, amount string) {
	t.Helper()
	require.Len(t, output.Assets, 1)
	require.Equal(t, assetName, output.Assets[0].Name.String())
	require.Zero(t, output.Assets[0].Amount.Cmp(mustCanonicalDecimalString(t, amount)))
}

func requireCanonicalResultValue(t *testing.T, outputs []ResultOutput, to string, value int64) {
	t.Helper()
	for _, output := range outputs {
		if output.To == to && output.Value == value {
			return
		}
	}
	t.Fatalf("missing value %d to %s in %+v", value, to, outputs)
}

func requireResultAssetString(t *testing.T, outputs []ResultOutput, to, assetName, amount string) {
	t.Helper()
	for _, output := range outputs {
		if output.To != to {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.String() == assetName && asset.Amount.Cmp(mustCanonicalDecimalString(t, amount)) == 0 {
				return
			}
		}
	}
	t.Fatalf("missing asset %s amount %s to %s in %+v", assetName, amount, to, outputs)
}

func requireNoCanonicalResultOutputTo(t *testing.T, outputs []ResultOutput, to string) {
	t.Helper()
	for _, output := range outputs {
		require.NotEqual(t, to, output.To, "unexpected output to %s: %+v", to, output)
	}
}
