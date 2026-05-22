package template

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAMMContractEncodeDecodeAndCheck(t *testing.T) {
	contract := NewAMMContract("ordx:f:test", "100.5", 20, "2010")
	require.NoError(t, contract.CheckContent())

	encoded, err := contract.Encode()
	require.NoError(t, err)

	var decoded AMMContract
	require.NoError(t, decoded.Decode(encoded))
	require.Equal(t, contract, &decoded)
	require.Equal(t, TemplateAMM, decoded.TemplateName())
	require.NoError(t, decoded.CheckContent())
}

func TestAMMContractRejectsBadK(t *testing.T) {
	contract := NewAMMContract("ordx:f:test", "100", 20, "1999")
	require.EqualError(t, contract.CheckContent(), "k is not the result of assetAmt*satValue")
}

func TestAMMInvokeParamsEncodeDecodeAndCheck(t *testing.T) {
	swapParam := &LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		UnitPrice: "10",
	}
	require.NoError(t, swapParam.CheckAMMSwap())

	addParam := &AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     20,
	}
	addEncoded, err := addParam.Encode()
	require.NoError(t, err)
	var decodedAdd AddLiquidityInvokeParam
	require.NoError(t, decodedAdd.Decode(addEncoded))
	require.Equal(t, addParam, &decodedAdd)
	require.NoError(t, decodedAdd.Check())

	removeParam := &RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    "5",
	}
	removeEncoded, err := removeParam.Encode()
	require.NoError(t, err)
	var decodedRemove RemoveLiquidityInvokeParam
	require.NoError(t, decodedRemove.Decode(removeEncoded))
	require.Equal(t, removeParam, &decodedRemove)
	require.NoError(t, decodedRemove.Check())
}

func TestAMMRejectsStakeUnstakeActions(t *testing.T) {
	contract := NewAMMContract("ordx:f:test", "100", 20, "2000")
	param, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeStake,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     20,
	}).Encode()
	require.NoError(t, err)

	require.Error(t, contract.CheckInvoke("stake", param))
	require.Error(t, contract.CheckInvoke("unstake", param))
	require.Error(t, contract.CheckInvoke(InvokeAPIAddLiquidity, param))
	require.NoError(t, contract.CheckInvoke(InvokeAPIRefund, nil))
	require.Error(t, contract.CheckInvoke(InvokeAPIProfit, nil))
}
