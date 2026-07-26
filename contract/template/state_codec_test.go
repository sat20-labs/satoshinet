package template

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeStoreMarshalRoundTrip(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	store := NewRuntimeStore()
	store.Add(runtime)

	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	decoded, err := DecodeRuntimeStore(encoded, nil)
	require.NoError(t, err)
	require.Equal(t, store.StateRoot(), decoded.StateRoot())

	got, ok := decoded.Get(runtime.Address())
	require.True(t, ok)
	state, err := got.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AMMData().TradingReady)
	requireDecimalString(t, "100", state.AMMData().AssetAInPool)
}

func TestInvokeItemUnmarshalRejectsInvalidDecimal(t *testing.T) {
	var item InvokeItem
	err := json.Unmarshal([]byte(`{"id":1,"inAmt":"not-a-decimal"}`), &item)
	require.Error(t, err)
}

func TestInvokeItemStoresOnlyCompactInvokeParam(t *testing.T) {
	param := mustLimitOrderItemParam(t, OrderTypeBuy, "ordx:f:test", "10", "2")
	item := InvokeItem{
		ID:        7,
		Action:    InvokeAPISwap,
		OrderType: OrderTypeBuy,
		Param:     append([]byte(nil), param...),
	}

	encoded, err := json.Marshal(item)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	require.Contains(t, fields, "param")
	for _, field := range []string{"unitPrice", "expectedAmt", "blobKeyLimit", "refundItemIds"} {
		require.NotContains(t, fields, field)
	}

	var decoded InvokeItem
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, param, decoded.Param)

	typ := reflect.TypeOf(InvokeItem{})
	paramField, ok := typ.FieldByName("Param")
	require.True(t, ok)
	require.Equal(t, reflect.TypeOf([]byte(nil)), paramField.Type)
	for _, field := range []string{"UnitPrice", "ExpectedAmt", "BlobKeyLimit", "RefundItemIDs"} {
		_, ok := typ.FieldByName(field)
		require.False(t, ok, "contract-specific invoke parameter field %s must not be expanded", field)
	}
}

func TestInvokeItemRejectsLegacyExpandedInvokeParameters(t *testing.T) {
	for _, field := range []string{"unitPrice", "expectedAmt", "blobKeyLimit", "refundItemIds"} {
		t.Run(field, func(t *testing.T) {
			encoded := []byte(`{"id":1,"callId":"call","action":"swap","` + field + `":"legacy","reason":"","done":0}`)
			if field == "blobKeyLimit" {
				encoded = []byte(`{"id":1,"callId":"call","action":"config","blobKeyLimit":1,"reason":"","done":0}`)
			}
			if field == "refundItemIds" {
				encoded = []byte(`{"id":1,"callId":"call","action":"refund","refundItemIds":[1],"reason":"","done":0}`)
			}
			var item InvokeItem
			if err := json.Unmarshal(encoded, &item); err == nil {
				t.Fatalf("expected legacy field %s to be rejected", field)
			}
		})
	}
}
