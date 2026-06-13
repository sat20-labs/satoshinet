package template

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBuildSettlementResultPlansUsesSettledItemFunding(t *testing.T) {
	plan := &SettlementPlan{
		Contract: "tc-address",
		Height:   100,
		ItemIDs:  []int64{1, 2},
		Transfers: []SettlementTransfer{{
			ItemID:    1,
			To:        "buyer",
			AssetName: "ordx:f:test",
			AssetAmt:  "10",
			SatValue:  5,
			Reason:    SettlementReasonDeal,
		}},
	}
	records := []ExecutionRecord{{
		ItemIDs: []int64{1},
		FundingInputs: []OutPoint{{
			TxID: testHash(1),
			Vout: 0,
		}},
	}, {
		ItemIDs: []int64{2},
		FundingInputs: []OutPoint{{
			TxID: testHash(2),
			Vout: 1,
		}},
	}}

	resultPlans, err := BuildSettlementResultPlans([]*SettlementPlan{plan}, records)
	require.NoError(t, err)
	require.Len(t, resultPlans, 1)
	require.Equal(t, []int64{1, 2}, resultPlans[0].ItemIDs)
	require.Equal(t, []OutPoint{{TxID: testHash(1), Vout: 0}, {TxID: testHash(2), Vout: 1}}, resultPlans[0].Inputs)
	require.Len(t, resultPlans[0].Outputs, 1)
	require.Equal(t, "buyer", resultPlans[0].Outputs[0].To)
	require.Equal(t, int64(5), resultPlans[0].Outputs[0].Value)
	require.Equal(t, "ordx:f:test", resultPlans[0].Outputs[0].AssetName)
	require.Equal(t, "10", resultPlans[0].Outputs[0].AssetAmt)
	require.Len(t, resultPlans[0].Outputs[0].Assets, 1)
}

func TestBuildAndVerifySettlementResultTx(t *testing.T) {
	plans := []ResultPlan{{
		Contract: "tc-address",
		Height:   100,
		ItemIDs:  []int64{1},
		Inputs: []OutPoint{{
			TxID: testHash(1),
			Vout: 0,
		}},
		Outputs: []ResultOutput{{
			To:    "seller",
			Value: 20,
		}},
	}}
	resolveScript := func(output ResultOutput) ([]byte, error) {
		return []byte{0x51}, nil
	}

	tx, err := BuildResultTx(ResultTxBuildRequest{
		Status:        ResultStatusSuccess,
		Plans:         plans,
		ResolveScript: resolveScript,
	})
	require.NoError(t, err)
	require.Len(t, tx.TxIn, 1)
	require.Len(t, tx.TxOut, 2)

	payload, err := ResultPayloadFromTx(tx)
	require.NoError(t, err)
	require.Equal(t, ResultStatusSuccess, payload.Status)
	require.Equal(t, uint16(1), payload.ResultCount)

	verifier := CanonicalResultVerifier{
		ResolveOutput: func(resultTx *wire.MsgTx) ([]ResultOutput, error) {
			return []ResultOutput{{To: "seller", Value: 20}}, nil
		},
	}
	require.NoError(t, verifier.Verify(tx, plans, ResultStatusSuccess))
}

func TestCanonicalResultVerifierRejectsInputMismatch(t *testing.T) {
	plans := []ResultPlan{{
		ItemIDs: []int64{1},
		Inputs:  []OutPoint{{TxID: testHash(1), Vout: 0}},
	}}
	tx, err := BuildResultTx(ResultTxBuildRequest{
		Status: ResultStatusSuccess,
		Plans: []ResultPlan{{
			ItemIDs: []int64{1},
			Inputs:  []OutPoint{{TxID: testHash(2), Vout: 0}},
		}},
		ResolveScript: func(output ResultOutput) ([]byte, error) {
			return []byte{0x51}, nil
		},
	})
	require.NoError(t, err)

	err = (CanonicalResultVerifier{}).Verify(tx, plans, ResultStatusSuccess)
	require.Error(t, err)
}

func TestAugmentClosedAMMCloseOutputsUseActualContractBalance(t *testing.T) {
	runtime := testAMMRuntime(t)
	addr := runtime.Address()
	state := TemplateRuntimeState{
		Running: RunningData{
			Closed:       true,
			AssetAInPool: parseDecimalOrZero("100"),
			AssetBInPool: parseDecimalOrZero("20"),
		},
	}
	require.NoError(t, runtime.saveRuntimeState(state))
	store := NewRuntimeStore()
	store.Add(runtime)
	provider := func(contract ContractAddress) ([]UTXO, error) {
		return []UTXO{{
			OutPoint: OutPoint{TxID: testHash(9), Vout: 0},
			Contract: contract,
			Value:    5,
			Assets:   testAsset("ordx:f:test", 40),
		}}, nil
	}
	plans := []ResultPlan{{
		Contract: addr.EncodeAddress(),
		ItemIDs:  []int64{1},
		Outputs: []ResultOutput{{
			To:        "lp-address",
			Value:     20,
			AssetName: "ordx:f:test",
			AssetAmt:  "100",
			Assets:    testAsset("ordx:f:test", 100),
		}},
	}}

	augmented, err := AugmentResultPlans(plans, store, DefaultGasConfig(), provider)
	require.NoError(t, err)
	require.Len(t, augmented, 1)
	require.Len(t, augmented[0].Outputs, 1)
	require.Equal(t, int64(5), augmented[0].Outputs[0].Value)
	requireResultPlanAssetTo(t, augmented[0], "lp-address", "ordx:f:test", "40")

	_, err = resultAssetsChange(testAsset("ordx:f:test", 40), augmented[0].Outputs, DefaultGasConfig().GasAssetName, nil)
	require.NoError(t, err)
}

func testHash(n byte) string {
	return fmt.Sprintf("%064x", n)
}
