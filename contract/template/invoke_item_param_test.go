package template

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

func testDefaultInvokeParam(t testing.TB, orderType int, unitPrice, expectedAmt string) []byte {
	t.Helper()
	param, err := (defaultInvokeParam{
		OrderType:   orderType,
		UnitPrice:   unitPrice,
		ExpectedAmt: expectedAmt,
	}).Encode()
	require.NoError(t, err)
	return param
}

func testInvokeItem(t testing.TB, orderType int, unitPrice, expectedAmt string) InvokeItem {
	t.Helper()
	return InvokeItem{
		Action: contractcommon.ContractInvokeAPIDefault,
		Param:  testDefaultInvokeParam(t, orderType, unitPrice, expectedAmt),
	}
}

func requireInvokeItemOrderType(t testing.TB, expected int, item *InvokeItem) {
	t.Helper()
	actual, err := invokeItemOrderType(item)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func requireInvokeItemExpectedAmt(t testing.TB, expected string, item *InvokeItem) {
	t.Helper()
	actual, err := invokeItemExpectedAmt(item)
	require.NoError(t, err)
	if expected == "" {
		require.Nil(t, actual)
		return
	}
	require.Equal(t, expected, decimalString(actual))
}

func testExpectedDecimal(value string) *scommon.Decimal {
	if value == "" {
		return nil
	}
	return parseDecimalOrZero(value)
}

func testLimitOrderInvokeParamBytes(t testing.TB, orderType int, assetName, amt, unitPrice string) []byte {
	t.Helper()
	param, err := (&LimitOrderInvokeParam{
		OrderType: orderType,
		AssetName: assetName,
		Amt:       amt,
		UnitPrice: unitPrice,
	}).Encode()
	require.NoError(t, err)
	return param
}

func testAddLiquidityInvokeParamBytes(t testing.TB, assetName, amt string, value int64) []byte {
	t.Helper()
	param, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: assetName,
		Amt:       amt,
		Value:     value,
	}).Encode()
	require.NoError(t, err)
	return param
}

func testRemoveLiquidityInvokeParamBytes(t testing.TB, assetName, lptAmt string) []byte {
	t.Helper()
	param, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: assetName,
		LptAmt:    lptAmt,
	}).Encode()
	require.NoError(t, err)
	return param
}

func TestInvokeItemSchemaKeepsInvokeParametersOpaque(t *testing.T) {
	typ := reflect.TypeOf(InvokeItem{})
	param, ok := typ.FieldByName("Param")
	require.True(t, ok)
	require.Equal(t, reflect.TypeOf([]byte(nil)), param.Type)
	require.Equal(t, "param,omitempty", param.Tag.Get("json"))
	for _, obsolete := range []string{
		"OrderType", "UnitPrice", "ExpectedAmt", "BlobKeyLimit", "RefundItemIDs",
	} {
		_, ok := typ.FieldByName(obsolete)
		require.False(t, ok, "contract-specific parameter field %s must not be expanded into InvokeItem", obsolete)
	}
}

func TestInvokeItemJSONStoresOnlyCompactParam(t *testing.T) {
	param, err := (&AutopayConfigInvokeParam{
		AmountPerBlock: "7",
		BlobKeyLimit:   9,
	}).Encode()
	require.NoError(t, err)

	item := InvokeItem{
		ID:     3,
		Action: InvokeAPIConfig,
		Param:  append([]byte(nil), param...),
		Reason: InvokeReasonNormal,
		Done:   ItemStatusDealt,
	}
	encoded, err := json.Marshal(item)
	require.NoError(t, err)
	for _, obsolete := range []string{
		"orderType", "unitPrice", "expectedAmt", "blobKeyLimit", "refundItemIds",
	} {
		require.NotContains(t, string(encoded), obsolete)
	}
	require.Contains(t, string(encoded), `"param"`)

	var decoded InvokeItem
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, param, decoded.Param)
	amount, limit, err := invokeItemAutopayConfig(&decoded)
	require.NoError(t, err)
	require.Equal(t, "7", decimalString(amount))
	require.Equal(t, uint32(9), limit)
}

func TestNewInvokeItemPreservesOriginalOPReturnParam(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	contract := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)
	original := append([]byte(nil), param...)

	item, err := NewInvokeItemFromRequest(runtime.contract, 1, ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        "call",
		Invoker:       "invoker",
		FundingOutput: testContractOutput("tx", 1, contract, 30, nil),
		Height:        10,
		Timestamp:     20,
	})
	require.NoError(t, err)
	require.Equal(t, original, item.Param)
	require.False(t, &param[0] == &item.Param[0], "InvokeItem must own its parameter bytes")

	param[0] ^= 0xff
	require.True(t, bytes.Equal(original, item.Param))
}

func TestInvokeItemJSONRejectsMalformedParam(t *testing.T) {
	encoded, err := json.Marshal(invokeItemJSON{
		Action: InvokeAPISwap,
		Param:  []byte{0xff},
		Reason: InvokeReasonNormal,
	})
	require.NoError(t, err)
	var item InvokeItem
	require.ErrorContains(t, json.Unmarshal(encoded, &item), "invalid invoke item param")
}
