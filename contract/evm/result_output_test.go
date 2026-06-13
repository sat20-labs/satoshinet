package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestResultOutputsFromTx(t *testing.T) {
	contract := testContract(t)
	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)
	resultScript, err := evmcommon.ResultNullDataScript(ResultPayload{
		Status:      ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)

	tx := wire.NewMsgTx(2)
	tx.AddTxOut(wire.NewTxOut(77, nil, []byte{0x51}))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   wire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "gas"},
		Amount: *scommon.NewDefaultDecimal(10),
	}}, contractScript))
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	outputs, err := ResultOutputsFromTx(tx, TestnetContractPrefix, func(pkScript []byte) (string, bool, error) {
		if len(pkScript) == 1 && pkScript[0] == 0x51 {
			return "tb1qdest", true, nil
		}
		return "", false, nil
	})
	require.NoError(t, err)
	require.Equal(t, []ResultOutput{
		mustResultOutput(t, "tb1qdest", SatoshiAssetName, 77),
		mustResultOutput(t, contract.MustEncode(), "ordx:ft:gas", 10),
	}, outputs)
}

func TestResultOutputsFromTxAllowsFractionalAsset(t *testing.T) {
	tx := wire.NewMsgTx(2)
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   wire.AssetName{Protocol: "ordx", Type: "ft", Ticker: "gas"},
		Amount: *scommon.NewDecimal(1, 1),
	}}, []byte{0x51}))

	outputs, err := ResultOutputsFromTx(tx, TestnetContractPrefix, func(pkScript []byte) (string, bool, error) {
		return "tb1qdest", true, nil
	})
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, 0, outputs[0].Assets[0].Amount.Cmp(scommon.NewDecimal(1, 1)))
}
