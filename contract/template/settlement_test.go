package template

import (
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSettleLimitOrdersMatchesByPriceAndTime(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	sellParam, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)
	buyParam, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "3",
	}).Encode()
	require.NoError(t, err)

	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         sellParam,
		CallID:        DeriveInvokeCallID("sell", 1, addr),
		FundingOutput: testContractOutput("sell", 1, addr, 0, testAsset("ordx:f:test", 10)),
		Height:        1,
	})
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         buyParam,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		FundingOutput: testContractOutput("buy", 1, addr, 30, nil),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(1), plan.Deals[0].BuyItemID)
	require.Equal(t, int64(0), plan.Deals[0].SellItemID)
	require.Equal(t, "10", plan.Deals[0].AssetAmt)
	require.Equal(t, int64(20), plan.Deals[0].SatValue)
	require.ElementsMatch(t, []int64{0, 1}, plan.ItemIDs)
	require.Len(t, plan.Transfers, 3)
	require.Equal(t, int64(1), plan.Transfers[0].ItemID)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, int64(0), plan.Transfers[1].ItemID)
	require.Equal(t, int64(20), plan.Transfers[1].SatValue)
	require.Equal(t, int64(1), plan.Transfers[2].ItemID)
	require.Equal(t, int64(10), plan.Transfers[2].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
	require.Equal(t, int64(20), state.Items[0].OutValue)
	require.Equal(t, ItemStatusDealt, state.Items[1].Done)
	requireDecimalString(t, "10", state.Items[1].OutAmt)
	require.Equal(t, int64(10), state.Items[1].OutValue)
	requireDecimalString(t, "10", state.LimitOrderData().TotalDealAssetA)
	requireDecimalString(t, "30", state.LimitOrderData().TotalDealAssetB)
	require.Equal(t, 2, state.LimitOrderData().TotalDealCount)
}

func TestSettleLimitOrdersStopsWhenPriceDoesNotCross(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	sellParam, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "4",
	}).Encode()
	require.NoError(t, err)
	buyParam, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "3",
	}).Encode()
	require.NoError(t, err)

	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         sellParam,
		CallID:        DeriveInvokeCallID("sell", 1, addr),
		FundingOutput: testContractOutput("sell", 1, addr, 0, testAsset("ordx:f:test", 10)),
		Height:        1,
	})
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         buyParam,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		FundingOutput: testContractOutput("buy", 1, addr, 30, nil),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Empty(t, plan.Transfers)
}

func TestSettleLimitOrdersLargeBuyFilledByMultipleSmallSells(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "alice", OrderTypeBuy, "30", "10", 302, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell1", "bob", OrderTypeSell, "10", "10", 0, testAsset("ordx:f:test", 10), 2)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(100), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "bob", plan.Transfers[1].To)
	require.Equal(t, int64(100), plan.Transfers[1].SatValue)

	applyLimitOrderInvokeForTest(t, runtime, addr, "sell2", "carol", OrderTypeSell, "10", "10", 0, testAsset("ordx:f:test", 10), 3)
	plan, err = runtime.SettleBlock(3)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(100), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "carol", plan.Transfers[1].To)
	require.Equal(t, int64(100), plan.Transfers[1].SatValue)

	applyLimitOrderInvokeForTest(t, runtime, addr, "sell3", "dave", OrderTypeSell, "10", "10", 0, testAsset("ordx:f:test", 10), 4)
	plan, err = runtime.SettleBlock(4)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(100), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "dave", plan.Transfers[1].To)
	require.Equal(t, int64(100), plan.Transfers[1].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
	requireDecimalString(t, "30", state.Items[0].OutAmt)
	require.Equal(t, int64(0), state.Items[0].RemainingValue)
}

func TestSettleLimitOrdersLargeSellFilledByMultipleSmallBuys(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell", "alice", OrderTypeSell, "30", "10", 0, testAsset("ordx:f:test", 30), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy1", "bob", OrderTypeBuy, "10", "12", 120, nil, 2)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, "10", plan.Deals[0].UnitPrice)
	require.Equal(t, int64(100), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 3)
	require.Equal(t, "bob", plan.Transfers[0].To)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "alice", plan.Transfers[1].To)
	require.Equal(t, int64(100), plan.Transfers[1].SatValue)
	require.Equal(t, "bob", plan.Transfers[2].To)
	require.Equal(t, int64(20), plan.Transfers[2].SatValue)

	applyLimitOrderInvokeForTest(t, runtime, addr, "buy2", "carol", OrderTypeBuy, "10", "11", 110, nil, 3)
	plan, err = runtime.SettleBlock(3)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, "10", plan.Deals[0].UnitPrice)
	require.Equal(t, int64(100), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 3)
	require.Equal(t, "carol", plan.Transfers[0].To)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "alice", plan.Transfers[1].To)
	require.Equal(t, int64(100), plan.Transfers[1].SatValue)
	require.Equal(t, "carol", plan.Transfers[2].To)
	require.Equal(t, int64(10), plan.Transfers[2].SatValue)

	applyLimitOrderInvokeForTest(t, runtime, addr, "buy3", "dave", OrderTypeBuy, "10", "10", 100, nil, 4)
	plan, err = runtime.SettleBlock(4)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, "10", plan.Deals[0].UnitPrice)
	require.Equal(t, int64(100), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "dave", plan.Transfers[0].To)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "alice", plan.Transfers[1].To)
	require.Equal(t, int64(100), plan.Transfers[1].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
	require.Equal(t, int64(300), state.Items[0].OutValue)
	require.Empty(t, state.Items[0].RemainingAmt)
}

func TestSettleLimitOrdersRefundsSellerDustRemainder(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	dustAsset := wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString("ordx:f:test"),
		Amount: *parseDecimalOrZero("1.1"),
	}}
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell", "seller", OrderTypeSell, "1.1", "1", 0, dustAsset, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "1", "1", 1, nil, 1)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, "1", plan.Deals[0].AssetAmt)
	require.Len(t, plan.Transfers, 3)
	require.Equal(t, "seller", plan.Transfers[2].To)
	require.Equal(t, "0.1", plan.Transfers[2].AssetAmt)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[2].Reason)
}

func TestSettleLimitOrdersBuyTakesLowerSellPricesAndLeavesRemainder(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell10", "seller10", OrderTypeSell, "10", "10", 0, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell9", "seller9", OrderTypeSell, "10", "9", 0, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell8", "seller8", OrderTypeSell, "10", "8", 0, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "40", "10", 403, nil, 2)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 3)
	require.Equal(t, "8", plan.Deals[0].UnitPrice)
	require.Equal(t, int64(80), plan.Deals[0].SatValue)
	require.Equal(t, "9", plan.Deals[1].UnitPrice)
	require.Equal(t, int64(90), plan.Deals[1].SatValue)
	require.Equal(t, "10", plan.Deals[2].UnitPrice)
	require.Equal(t, int64(100), plan.Deals[2].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	buy := state.Items[3]
	require.Equal(t, ItemStatusInit, buy.Done)
	requireDecimalString(t, "30", buy.OutAmt)
	require.Equal(t, int64(130), buy.RemainingValue)
	unitPrice, err := invokeItemUnitPrice(&buy)
	require.NoError(t, err)
	require.Equal(t, "10", unitPrice)
}

func TestSettleLimitOrdersRefundsPartiallyDealtOrder(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell8", "seller8", OrderTypeSell, "10", "8", 0, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell9", "seller9", OrderTypeSell, "10", "9", 0, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell10", "seller10", OrderTypeSell, "10", "10", 0, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "40", "10", 403, nil, 2)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 3)

	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRefund,
		CallID:  DeriveInvokeCallID("refund", 1, addr),
		Invoker: "buyer",
		Height:  3,
	})
	require.NoError(t, err)

	plan, err = runtime.SettleBlock(3)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, int64(3), plan.Transfers[0].ItemID)
	require.Equal(t, "buyer", plan.Transfers[0].To)
	require.Equal(t, int64(130), plan.Transfers[0].SatValue)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusRefunded, state.Items[3].Done)
	require.Equal(t, InvokeReasonRefund, state.Items[3].Reason)
	require.Empty(t, state.Items[3].RemainingAmt)
	require.Zero(t, state.Items[3].RemainingValue)
}

func TestSettleLimitOrdersRefundsInvokerOpenOrders(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	buyParam, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)

	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         buyParam,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("buy", 1, addr, 30, nil),
		Height:        1,
	})
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRefund,
		CallID:  DeriveInvokeCallID("refund", 1, addr),
		Invoker: "alice",
		Height:  2,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, int64(0), plan.Transfers[0].ItemID)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, int64(30), plan.Transfers[0].SatValue)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)
	require.ElementsMatch(t, []int64{0, 1}, plan.ItemIDs)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	require.Equal(t, InvokeReasonRefund, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[1].Done)
	require.Zero(t, state.LimitOrderData().TotalDealCount)
	requireDecimalString(t, "30", state.LimitOrderData().TotalRefundAssetB)
}

func TestSettleLimitOrdersRefundCanTargetOneOrder(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy0", "alice", OrderTypeBuy, "10", "2", 20, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy1", "alice", OrderTypeBuy, "10", "3", 30, nil, 1)
	refundParam, err := (&RefundInvokeParam{ItemIDs: []int64{0}}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRefund,
		Param:   refundParam,
		CallID:  DeriveInvokeCallID("refund", 1, addr),
		Invoker: "alice",
		Height:  2,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, int64(0), plan.Transfers[0].ItemID)
	require.ElementsMatch(t, []int64{0, 2}, plan.ItemIDs)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	require.Equal(t, ItemStatusInit, state.Items[1].Done)
	require.Equal(t, ItemStatusRefunded, state.Items[2].Done)
}

func TestSettleLimitOrdersRefundCanTargetMultipleOrders(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy0", "alice", OrderTypeBuy, "10", "2", 20, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy1", "alice", OrderTypeBuy, "10", "3", 30, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy2", "alice", OrderTypeBuy, "10", "4", 40, nil, 1)
	refundParam, err := (&RefundInvokeParam{ItemIDs: []int64{0, 2}}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRefund,
		Param:   refundParam,
		CallID:  DeriveInvokeCallID("refund", 1, addr),
		Invoker: "alice",
		Height:  2,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 2)
	require.ElementsMatch(t, []int64{0, 2, 3}, plan.ItemIDs)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	require.Equal(t, ItemStatusInit, state.Items[1].Done)
	require.Equal(t, ItemStatusRefunded, state.Items[2].Done)
	require.Equal(t, ItemStatusRefunded, state.Items[3].Done)
}

func TestSettleLimitOrdersSamePriceUsesFIFO(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell0", "seller0", OrderTypeSell, "10", "10", 0, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell1", "seller1", OrderTypeSell, "10", "10", 0, testAsset("ordx:f:test", 10), 2)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "10", "10", 100, nil, 3)

	plan, err := runtime.SettleBlock(3)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(0), plan.Deals[0].SellItemID)
	require.Equal(t, int64(2), plan.Deals[0].BuyItemID)
	require.Equal(t, "seller0", plan.Transfers[1].To)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
	require.Equal(t, ItemStatusInit, state.Items[1].Done)
	requireDecimalString(t, "10", state.Items[1].RemainingAmt)
}

func TestSettleLimitOrdersIgnoreFutureHeightOrders(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell", "seller", OrderTypeSell, "10", "10", 0, testAsset("ordx:f:test", 10), 5)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "10", "10", 100, nil, 5)

	plan, err := runtime.SettleBlock(4)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Empty(t, plan.Transfers)

	plan, err = runtime.SettleBlock(5)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
}

func TestSettleLimitOrdersBuyRefundsSurplusWhenFilledAtBetterPrice(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell", "seller", OrderTypeSell, "20", "8", 0, testAsset("ordx:f:test", 20), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "20", "10", 201, nil, 2)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(160), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 3)
	require.Equal(t, "buyer", plan.Transfers[0].To)
	require.Equal(t, "20", plan.Transfers[0].AssetAmt)
	require.Equal(t, "seller", plan.Transfers[1].To)
	require.Equal(t, int64(160), plan.Transfers[1].SatValue)
	require.Equal(t, "buyer", plan.Transfers[2].To)
	require.Equal(t, int64(40), plan.Transfers[2].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[1].Done)
	require.Zero(t, state.Items[1].RemainingValue)
	require.Equal(t, int64(40), state.Items[1].OutValue)
}

func TestSettleLimitOrdersCapsBuyExpectedAcrossPrices(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell1", "seller1", OrderTypeSell, "500", "1", 0, testAsset("ordx:f:test", 500), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell2", "seller2", OrderTypeSell, "500", "2", 0, testAsset("ordx:f:test", 500), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "800", "2", 1612, nil, 2)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 2)
	require.Equal(t, int64(500), plan.Deals[0].SatValue)
	require.Equal(t, "500", plan.Deals[0].AssetAmt)
	require.Equal(t, int64(600), plan.Deals[1].SatValue)
	require.Equal(t, "300", plan.Deals[1].AssetAmt)
	require.Len(t, plan.Transfers, 5)
	require.Equal(t, "buyer", plan.Transfers[0].To)
	require.Equal(t, "500", plan.Transfers[0].AssetAmt)
	require.Equal(t, "seller1", plan.Transfers[1].To)
	require.Equal(t, int64(500), plan.Transfers[1].SatValue)
	require.Equal(t, "buyer", plan.Transfers[2].To)
	require.Equal(t, "300", plan.Transfers[2].AssetAmt)
	require.Equal(t, "seller2", plan.Transfers[3].To)
	require.Equal(t, int64(600), plan.Transfers[3].SatValue)
	require.Equal(t, "buyer", plan.Transfers[4].To)
	require.Equal(t, int64(500), plan.Transfers[4].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[2].Done)
	requireDecimalString(t, "800", state.Items[2].OutAmt)
	require.Equal(t, int64(500), state.Items[2].OutValue)
	require.Equal(t, int64(0), state.Items[2].RemainingValue)
	require.Equal(t, ItemStatusInit, state.Items[1].Done)
	requireDecimalString(t, "200", state.Items[1].RemainingAmt)
}

func TestSettleLimitOrdersRefundCannotCancelOtherUsersOrder(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "alice", OrderTypeBuy, "10", "2", 20, nil, 1)
	refundParam, err := (&RefundInvokeParam{ItemIDs: []int64{0}}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRefund,
		Param:   refundParam,
		CallID:  DeriveInvokeCallID("refund", 1, addr),
		Invoker: "bob",
		Height:  2,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Empty(t, plan.Transfers)
	require.ElementsMatch(t, []int64{1}, plan.ItemIDs)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusInit, state.Items[0].Done)
	require.Equal(t, ItemStatusRefunded, state.Items[1].Done)
}

func applyLimitOrderInvokeForTest(t *testing.T, runtime *ContractRuntime, addr ContractAddress,
	callID, invoker string, orderType int, amt, unitPrice string, value int64, assets wire.TxAssets, height int64) {

	t.Helper()
	param, err := (&LimitOrderInvokeParam{
		OrderType: orderType,
		AssetName: "ordx:f:test",
		Amt:       amt,
		UnitPrice: unitPrice,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID(callID, 1, addr),
		Invoker:       invoker,
		FundingOutput: testContractOutput(callID, 1, addr, value, assets),
		Height:        height,
	})
	require.NoError(t, err)
}
