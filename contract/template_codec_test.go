package contract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutopayConfigOptionalGasFundingCodec(t *testing.T) {
	legacy := &TemplateAutopayConfigInvokeParam{AmountPerBlock: "10", BlobKeyLimit: 1}
	raw, err := legacy.Encode()
	require.NoError(t, err)
	var decoded TemplateAutopayConfigInvokeParam
	require.NoError(t, decoded.Decode(raw))
	require.Equal(t, *legacy, decoded)
	roundtrip, err := decoded.Encode()
	require.NoError(t, err)
	require.Equal(t, raw, roundtrip)
	for _, param := range []TemplateAutopayConfigInvokeParam{
		{GasFundingAmount: "400"},
		{AmountPerBlock: "10", BlobKeyLimit: 2, GasFundingAmount: "400"},
	} {
		raw, err := param.Encode()
		require.NoError(t, err)
		require.NoError(t, decoded.Decode(raw))
		require.Equal(t, param, decoded)
		require.Error(t, decoded.Decode(append(raw, 0x51)))
	}
	// Reusing a decoder must not retain the optional field from another call.
	require.NoError(t, decoded.Decode(raw))
	require.Empty(t, decoded.GasFundingAmount)
}

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
