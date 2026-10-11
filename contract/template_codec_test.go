package contract

import (
	"testing"

	"github.com/sat20-labs/satoshinet/txscript"
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

func TestTemplateRefundRejectsNonNumericOpcodes(t *testing.T) {
	for _, test := range []struct {
		name   string
		opcode byte
	}{
		{name: "invalid", opcode: 0xff},
		{name: "reserved", opcode: txscript.OP_RESERVED},
		{name: "drop", opcode: txscript.OP_DROP},
		{name: "dup", opcode: txscript.OP_DUP},
		{name: "checksig", opcode: txscript.OP_CHECKSIG},
		{name: "return", opcode: txscript.OP_RETURN},
		{name: "nop", opcode: txscript.OP_NOP},
	} {
		t.Run(test.name, func(t *testing.T) {
			var decoded TemplateRefundInvokeParam
			require.Error(t, decoded.Decode([]byte{test.opcode}))
			require.Error(t, decoded.Decode([]byte{txscript.OP_1, test.opcode}))
		})
	}
}

func TestTemplateRefundPreservesIntegerEncodings(t *testing.T) {
	ids := []int64{0, 1, 16, 17, 127, 128, 1<<63 - 1}
	raw, err := (&TemplateRefundInvokeParam{ItemIDs: ids}).Encode()
	require.NoError(t, err)
	var decoded TemplateRefundInvokeParam
	require.NoError(t, decoded.Decode(raw))
	require.Equal(t, ids, decoded.ItemIDs)

	// Existing data pushes remain valid, including nonminimal zero encodings.
	for _, test := range []struct {
		name string
		data []byte
		want []int64
	}{
		{name: "empty means all own orders"},
		{name: "zero opcode", data: []byte{txscript.OP_0}, want: []int64{0}},
		{name: "zero data", data: []byte{txscript.OP_DATA_1, 0}, want: []int64{0}},
		{name: "empty pushdata1", data: []byte{txscript.OP_PUSHDATA1, 0}, want: []int64{0}},
		{name: "pushdata1", data: []byte{txscript.OP_PUSHDATA1, 1, 17}, want: []int64{17}},
		{name: "pushdata2", data: []byte{txscript.OP_PUSHDATA2, 1, 0, 17}, want: []int64{17}},
		{name: "pushdata4", data: []byte{txscript.OP_PUSHDATA4, 1, 0, 0, 0, 17}, want: []int64{17}},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, decoded.Decode(test.data))
			require.Equal(t, test.want, decoded.ItemIDs)
		})
	}
}

func TestTemplateRefundRetainsInvalidIntegerBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "negative opcode", data: []byte{txscript.OP_1NEGATE}},
		{name: "negative data", data: []byte{txscript.OP_DATA_1, 0x81}},
		{name: "truncated push", data: []byte{txscript.OP_PUSHDATA1}},
		{name: "truncated payload", data: []byte{txscript.OP_DATA_2, 1}},
		{name: "over eight bytes", data: []byte{txscript.OP_DATA_9, 1, 0, 0, 0, 0, 0, 0, 0, 0}},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, (&TemplateRefundInvokeParam{}).Decode(test.data))
		})
	}
	_, err := (&TemplateRefundInvokeParam{ItemIDs: []int64{-1}}).Encode()
	require.Error(t, err)
}
