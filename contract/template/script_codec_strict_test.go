package template

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTemplateScriptDecodersRejectTrailingFields(t *testing.T) {
	limitContract, err := NewLimitOrderContract("ordx:f:test").Encode()
	require.NoError(t, err)
	ammContract, err := NewAMMContract("ordx:f:test", "100", 20, "2000").Encode()
	require.NoError(t, err)
	autopayContract, err := NewAutopayContract("svc", "recipient", "ordx:f:test", "1").Encode()
	require.NoError(t, err)
	limitInvoke, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)
	addLiquidity, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     20,
	}).Encode()
	require.NoError(t, err)
	removeLiquidity, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    "5",
	}).Encode()
	require.NoError(t, err)

	tests := []struct {
		name   string
		data   []byte
		decode func([]byte) error
	}{
		{name: "limit contract", data: limitContract, decode: (&LimitOrderContract{}).Decode},
		{name: "amm contract", data: ammContract, decode: (&AMMContract{}).Decode},
		{name: "autopay contract", data: autopayContract, decode: (&AutopayContract{}).Decode},
		{name: "limit invoke", data: limitInvoke, decode: (&LimitOrderInvokeParam{}).Decode},
		{name: "add liquidity", data: addLiquidity, decode: (&AddLiquidityInvokeParam{}).Decode},
		{name: "remove liquidity", data: removeLiquidity, decode: (&RemoveLiquidityInvokeParam{}).Decode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withTrailingField := append(append([]byte(nil), test.data...), 0x51)
			require.ErrorContains(t, test.decode(withTrailingField), "unexpected")
		})
	}
}
