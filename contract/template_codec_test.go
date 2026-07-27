package contract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSharedTemplateInvokeDecodersRejectTrailingFields(t *testing.T) {
	limitInvoke, err := (&TemplateLimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)
	addLiquidity, err := (&TemplateAddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     20,
	}).Encode()
	require.NoError(t, err)
	removeLiquidity, err := (&TemplateRemoveLiquidityInvokeParam{
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
		{name: "limit invoke", data: limitInvoke, decode: (&TemplateLimitOrderInvokeParam{}).Decode},
		{name: "add liquidity", data: addLiquidity, decode: (&TemplateAddLiquidityInvokeParam{}).Decode},
		{name: "remove liquidity", data: removeLiquidity, decode: (&TemplateRemoveLiquidityInvokeParam{}).Decode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			withTrailingField := append(append([]byte(nil), test.data...), 0x51)
			require.ErrorContains(t, test.decode(withTrailingField), "unexpected")
		})
	}
}
