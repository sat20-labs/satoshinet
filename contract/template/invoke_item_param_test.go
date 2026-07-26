package template

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func mustLimitOrderItemParam(t *testing.T, orderType int, assetName, amount, unitPrice string) []byte {
	t.Helper()
	param, err := (&LimitOrderInvokeParam{
		OrderType: orderType,
		AssetName: assetName,
		Amt:       amount,
		UnitPrice: unitPrice,
	}).Encode()
	require.NoError(t, err)
	return param
}

func mustRemoveLiquidityItemParam(t *testing.T, assetName, amount string) []byte {
	t.Helper()
	param, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: assetName,
		LptAmt:    amount,
	}).Encode()
	require.NoError(t, err)
	return param
}
