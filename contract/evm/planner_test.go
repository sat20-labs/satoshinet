package evm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildCanonicalResultPlan(t *testing.T) {
	contract := testContract(t)
	gasAssetName := "ordx:ft:gas"
	available := []UTXO{
		mustUTXO(t, OutPoint{TxID: "b", Vout: 0}, contract, SatoshiAssetName, 30, 12),
		mustUTXO(t, OutPoint{TxID: "a", Vout: 0}, contract, SatoshiAssetName, 80, 11),
		mustUTXO(t, OutPoint{TxID: "invoke", Vout: 1}, contract, gasAssetName, 100, 10),
	}

	plan, err := BuildCanonicalResultPlan(ResultPlanRequest{
		Contract:               contract,
		Available:              available,
		GasAssetName:           gasAssetName,
		GasFee:                 mustDefaultDecimal(t, 60),
		RequiredGasFundingUTXO: []OutPoint{{TxID: "invoke", Vout: 1}},
		Intents: []AssetIntent{
			{
				From:      contract,
				To:        "tb1qdest",
				AssetName: SatoshiAssetName,
				Amount:    mustDefaultDecimal(t, 70),
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, plan.Inputs, 2)
	require.Equal(t, OutPoint{TxID: "invoke", Vout: 1}, plan.Inputs[0].OutPoint)
	require.Equal(t, OutPoint{TxID: "a", Vout: 0}, plan.Inputs[1].OutPoint)
	require.Equal(t, []ResultOutput{
		mustResultOutput(t, "tb1qdest", SatoshiAssetName, 70),
		{
			To:     contract.MustEncode(),
			Value:  10,
			Assets: mustResultOutput(t, contract.MustEncode(), gasAssetName, 40).Assets,
		},
	}, plan.Outputs)
}

func TestBuildCanonicalResultPlanRejectsMixedContracts(t *testing.T) {
	contract := testContract(t)
	otherHash := ContractAddressHash(contract)
	otherHash[0] ^= 1
	other := testContractWithHash(t, otherHash)

	_, err := BuildCanonicalResultPlan(ResultPlanRequest{
		Contract:     contract,
		GasAssetName: "ordx:ft:gas",
		Intents: []AssetIntent{
			{From: other, To: "tb1qdest", AssetName: SatoshiAssetName, Amount: mustDefaultDecimal(t, 1)},
		},
	})
	require.Error(t, err)
}
