package template

import (
	"encoding/json"
	"testing"

	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

func TestLimitOrderStateViewReturnsDepthWithoutItems(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	state := TemplateRuntimeState{
		InvokeCount: 3,
		Items: []InvokeItem{
			{
				OrderType:      OrderTypeSell,
				Height:         10,
				AssetName:      "ordx:f:test",
				UnitPrice:      "2",
				RemainingAmt:   parseDecimalOrZero("10"),
				RemainingValue: 0,
				Reason:         InvokeReasonNormal,
				Done:           ItemStatusInit,
			},
			{
				OrderType:      OrderTypeSell,
				Height:         11,
				AssetName:      "ordx:f:test",
				UnitPrice:      "2",
				RemainingAmt:   parseDecimalOrZero("7"),
				RemainingValue: 0,
				Reason:         InvokeReasonNormal,
				Done:           ItemStatusInit,
			},
			{
				OrderType:      OrderTypeBuy,
				Height:         12,
				AssetName:      "ordx:f:test",
				UnitPrice:      "3",
				ExpectedAmt:    parseDecimalOrZero("9"),
				OutAmt:         parseDecimalOrZero("2"),
				RemainingValue: 21,
				Reason:         InvokeReasonNormal,
				Done:           ItemStatusInit,
			},
			{
				OrderType:      OrderTypeSell,
				Height:         13,
				AssetName:      "ordx:f:test",
				UnitPrice:      "1",
				RemainingAmt:   parseDecimalOrZero("100"),
				RemainingValue: 0,
				Reason:         InvokeReasonInvalid,
				Done:           ItemStatusInit,
			},
		},
	}
	require.NoError(t, runtime.saveRuntimeState(state))

	got, err := runtime.StateView(contractframework.StateViewContext{Height: 12})
	require.NoError(t, err)
	view, ok := got.(LimitOrderStateView)
	require.True(t, ok)
	require.Equal(t, "ordx:f:test", view.AssetName)
	require.Equal(t, 1, view.ActiveBuyCount)
	require.Equal(t, 2, view.ActiveSellCount)
	require.Len(t, view.BuyDepth, 1)
	require.Equal(t, &DepthInfo{Price: "3", Amt: "7", Value: 21}, view.BuyDepth[0])
	require.Len(t, view.SellDepth, 1)
	require.Equal(t, &DepthInfo{Price: "2", Amt: "17", Value: 34}, view.SellDepth[0])

	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "items")
	require.NotContains(t, string(encoded), "pool")
}

func TestAMMStateViewReturnsPoolSummary(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)

	got, err := runtime.StateView(contractframework.StateViewContext{Height: 1})
	require.NoError(t, err)
	view, ok := got.(AMMStateView)
	require.True(t, ok)
	require.Equal(t, TemplateAMM, view.TemplateName)
	require.Equal(t, "ordx:f:test", view.AssetName)
	require.Equal(t, []string{"ordx:f:test", SatoshiAssetName}, view.Assets)
	require.Equal(t, "100", view.AssetAInPool)
	require.Equal(t, "20", view.AssetBInPool)
	require.Equal(t, "2000", view.K)
}
