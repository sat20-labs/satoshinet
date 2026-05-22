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
		Action: InvokeAPISwap,
		Param:  sellParam,
		CallID: DeriveInvokeCallID("sell", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "sell", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
			Assets:   testAsset("ordx:f:test", 10),
		}},
		Height: 1,
	})
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  buyParam,
		CallID: DeriveInvokeCallID("buy", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy", Vout: 1},
			Contract: addr,
			Value:    40,
		}},
		Height: 1,
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
	require.Equal(t, "10", state.Items[1].OutAmt)
	require.Equal(t, int64(10), state.Items[1].OutValue)
	require.Equal(t, "10", state.Running.TotalDealAsset)
	require.Equal(t, int64(30), state.Running.TotalDealGas)
	require.Equal(t, 2, state.Running.TotalDealCount)
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
		Action: InvokeAPISwap,
		Param:  sellParam,
		CallID: DeriveInvokeCallID("sell", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "sell", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
			Assets:   testAsset("ordx:f:test", 10),
		}},
		Height: 1,
	})
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  buyParam,
		CallID: DeriveInvokeCallID("buy", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy", Vout: 1},
			Contract: addr,
			Value:    30,
		}},
		Height: 1,
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
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "alice", OrderTypeBuy, "30", "10", 312, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell1", "bob", OrderTypeSell, "10", "10", SwapInvokeFee, testAsset("ordx:f:test", 10), 2)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(100), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "bob", plan.Transfers[1].To)
	require.Equal(t, int64(100), plan.Transfers[1].SatValue)

	applyLimitOrderInvokeForTest(t, runtime, addr, "sell2", "carol", OrderTypeSell, "10", "10", SwapInvokeFee, testAsset("ordx:f:test", 10), 3)
	plan, err = runtime.SettleBlock(3)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(100), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "10", plan.Transfers[0].AssetAmt)
	require.Equal(t, "carol", plan.Transfers[1].To)
	require.Equal(t, int64(100), plan.Transfers[1].SatValue)

	applyLimitOrderInvokeForTest(t, runtime, addr, "sell3", "dave", OrderTypeSell, "10", "10", SwapInvokeFee, testAsset("ordx:f:test", 10), 4)
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
	require.Equal(t, "30", state.Items[0].OutAmt)
	require.Equal(t, int64(0), state.Items[0].RemainingValue)
}

func TestSettleLimitOrdersLargeSellFilledByMultipleSmallBuys(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell", "alice", OrderTypeSell, "30", "10", SwapInvokeFee, testAsset("ordx:f:test", 30), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy1", "bob", OrderTypeBuy, "10", "12", 130, nil, 2)

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

	applyLimitOrderInvokeForTest(t, runtime, addr, "buy2", "carol", OrderTypeBuy, "10", "11", 120, nil, 3)
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

	applyLimitOrderInvokeForTest(t, runtime, addr, "buy3", "dave", OrderTypeBuy, "10", "10", 110, nil, 4)
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

func TestSettleLimitOrdersBuyTakesLowerSellPricesAndLeavesRemainder(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell10", "seller10", OrderTypeSell, "10", "10", SwapInvokeFee, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell9", "seller9", OrderTypeSell, "10", "9", SwapInvokeFee, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell8", "seller8", OrderTypeSell, "10", "8", SwapInvokeFee, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "40", "10", 413, nil, 2)

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
	require.Equal(t, "30", buy.OutAmt)
	require.Equal(t, int64(130), buy.RemainingValue)
	require.Equal(t, "10", buy.UnitPrice)
}

func TestSettleLimitOrdersRefundsPartiallyDealtOrder(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell8", "seller8", OrderTypeSell, "10", "8", SwapInvokeFee, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell9", "seller9", OrderTypeSell, "10", "9", SwapInvokeFee, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell10", "seller10", OrderTypeSell, "10", "10", SwapInvokeFee, testAsset("ordx:f:test", 10), 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "40", "10", 413, nil, 2)

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
		Action:  InvokeAPISwap,
		Param:   buyParam,
		CallID:  DeriveInvokeCallID("buy", 1, addr),
		Invoker: "alice",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy", Vout: 1},
			Contract: addr,
			Value:    30,
		}},
		Height: 1,
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
	require.Equal(t, int64(20), plan.Transfers[0].SatValue)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)
	require.ElementsMatch(t, []int64{0, 1}, plan.ItemIDs)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	require.Equal(t, InvokeReasonRefund, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[1].Done)
	require.Zero(t, state.Running.TotalDealCount)
	require.Equal(t, int64(20), state.Running.TotalRefundGas)
}

func TestSettleLimitOrdersRefundCanTargetOneOrder(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy0", "alice", OrderTypeBuy, "10", "2", 30, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy1", "alice", OrderTypeBuy, "10", "3", 40, nil, 1)
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
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy0", "alice", OrderTypeBuy, "10", "2", 30, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy1", "alice", OrderTypeBuy, "10", "3", 40, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy2", "alice", OrderTypeBuy, "10", "4", 50, nil, 1)
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
		Action:  InvokeAPISwap,
		Param:   param,
		CallID:  DeriveInvokeCallID(callID, 1, addr),
		Invoker: invoker,
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: callID, Vout: 1},
			Contract: addr,
			Value:    value,
			Assets:   assets,
		}},
		Height: height,
	})
	require.NoError(t, err)
}
