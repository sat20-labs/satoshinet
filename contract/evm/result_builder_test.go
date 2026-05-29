package evm

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBuildResultTx(t *testing.T) {
	contract := testContract(t)
	inputHash := chainhash.Hash{1, 2, 3}
	plan := ResultPlan{
		Inputs: []UTXO{mustUTXO(t, OutPoint{TxID: inputHash.String(), Vout: 2}, contract, "ordx:ft:gas", 100, 0)},
		Outputs: []ResultOutput{
			mustResultOutput(t, "tb1qdest", SatoshiAssetName, 70),
			mustResultOutput(t, contract.MustEncode(), "ordx:ft:gas", 30),
		},
	}
	tx, err := BuildResultTx(ResultTxBuildRequest{
		Status:      ResultStatusSuccess,
		ResultCount: 1,
		Plans:       []ResultPlan{plan},
		ResolveScript: func(output ResultOutput) ([]byte, error) {
			if output.To == contract.MustEncode() {
				return ContractPkScript(contract)
			}
			return []byte{txscript.OP_TRUE}, nil
		},
	})
	require.NoError(t, err)
	require.Len(t, tx.TxIn, 1)
	require.Equal(t, inputHash, tx.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, uint32(2), tx.TxIn[0].PreviousOutPoint.Index)
	require.Len(t, tx.TxOut, 3)
	require.Equal(t, int64(70), tx.TxOut[0].Value)
	require.Len(t, tx.TxOut[1].Assets, 1)
	require.Equal(t, "ordx:ft:gas", tx.TxOut[1].Assets[0].Name.String())

	parsed, err := ParseTx(tx, testContractResolver)
	require.NoError(t, err)
	require.Equal(t, TxTypeResult, parsed.Type)
	require.Equal(t, uint16(1), parsed.Result.ResultCount)
}

func TestBuildResultTxRejectsUnsupportedExtraData(t *testing.T) {
	_, err := BuildResultTx(ResultTxBuildRequest{
		Status:      ResultStatusSuccess,
		ResultCount: 1,
		Plans: []ResultPlan{{
			Outputs: []ResultOutput{{To: "tb1qdest", Value: 1, ExtraData: []byte{1}}},
		}},
		ResolveScript: func(ResultOutput) ([]byte, error) { return []byte{txscript.OP_TRUE}, nil },
	})
	require.Error(t, err)
}

func TestBuildCanonicalResultTxDeployOnly(t *testing.T) {
	tx, err := BuildCanonicalResultTx(CanonicalResultTxRequest{
		Status: ResultStatusSuccess,
		Records: []ExecutionRecord{{
			Kind:           ExecutionKindDeploy,
			Type:           TxTypeDeploy,
			Contract:       testContract(t),
			Status:         ResultStatusSuccess,
			RequiresResult: true,
		}},
	})
	require.NoError(t, err)
	require.Empty(t, tx.TxIn)
	require.Len(t, tx.TxOut, 1)

	parsed, err := ParseTx(tx, nil)
	require.NoError(t, err)
	require.Equal(t, TxTypeResult, parsed.Type)
	require.Equal(t, uint16(1), parsed.Result.ResultCount)
}

func TestBuildCanonicalResultTxInvoke(t *testing.T) {
	contract := testContract(t)
	gasHash := chainhash.Hash{1}
	assetHash := chainhash.Hash{2}
	gasAssetName := "ordx:ft:gas"
	gasInput := OutPoint{TxID: gasHash.String(), Vout: 1}
	assetInput := OutPoint{TxID: assetHash.String(), Vout: 0}
	record := ExecutionRecord{
		Kind:           ExecutionKindInvoke,
		Type:           TxTypeInvoke,
		Contract:       contract,
		Status:         ResultStatusSuccess,
		GasUsed:        10,
		FundingInputs:  []OutPoint{gasInput},
		RequiresResult: true,
		AssetIntents: []AssetIntent{{
			From:      contract,
			To:        "tb1qdest",
			AssetName: SatoshiAssetName,
			Amount:    mustDefaultDecimal(t, 70),
		}},
	}
	available := []UTXO{
		mustUTXO(t, gasInput, contract, gasAssetName, 100000, 10),
		mustUTXO(t, assetInput, contract, SatoshiAssetName, 80, 11),
	}
	gasConfig := GasConfig{GasAssetName: gasAssetName, FixedGasPrice: 2, InvokeBaseGas: 10, ResultBaseGas: 5}

	tx, err := BuildCanonicalResultTx(CanonicalResultTxRequest{
		Status:    ResultStatusSuccess,
		Records:   []ExecutionRecord{record},
		GasConfig: gasConfig,
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveScript: func(output ResultOutput) ([]byte, error) {
			if output.To == contract.MustEncode() {
				return ContractPkScript(contract)
			}
			return []byte{txscript.OP_TRUE}, nil
		},
	})
	require.NoError(t, err)
	require.Len(t, tx.TxIn, 2)
	require.Len(t, tx.TxOut, 3)
	require.Equal(t, gasHash, tx.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, assetHash, tx.TxIn[1].PreviousOutPoint.Hash)
	require.Equal(t, int64(10), tx.TxOut[1].Value)
	require.Len(t, tx.TxOut[1].Assets, 1)

	verifier := CanonicalResultVerifier{
		GasConfig: gasConfig,
		UTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveOutput: func(tx *wire.MsgTx) ([]ResultOutput, error) {
			return ResultOutputsFromTx(tx, TestnetContractPrefix, func(pkScript []byte) (string, bool, error) {
				if len(pkScript) == 1 && pkScript[0] == txscript.OP_TRUE {
					return "tb1qdest", true, nil
				}
				return "", false, nil
			})
		},
	}
	require.NoError(t, verifier.Verify(tx, []ExecutionRecord{record}))
}
