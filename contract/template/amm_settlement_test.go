package template

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSettleAMMBuyUsesConstantProductPool(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "30",
		UnitPrice: "10",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("buy", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy", Vout: 1},
			Contract: addr,
			Value:    10,
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(0), plan.Deals[0].BuyItemID)
	require.Equal(t, "33.155080214", plan.Deals[0].AssetAmt)
	require.Equal(t, int64(10), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "33.155080214", plan.Transfers[0].AssetAmt)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "66.844919786", state.Running.AssetAInPool)
	requireDecimalString(t, "30", state.Running.AssetBInPool)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
}

func TestSettleAMMBuyResultOutputsUseAssetPrecision(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "30",
		UnitPrice: "10",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPISwap,
		Param:   param,
		CallID:  DeriveInvokeCallID("buy", 1, addr),
		Invoker: "buyer",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy", Vout: 1},
			Contract: addr,
			Value:    10,
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	store := NewRuntimeStore()
	store.Add(runtime)
	assetPrecision := func(name string) (int, bool) {
		return 0, name == "ordx:f:test"
	}
	resultPlans, err := BuildSettlementResultPlans([]*SettlementPlan{plan}, nil, assetPrecision)
	require.NoError(t, err)
	resultPlans, err = AugmentResultPlans(resultPlans, store, DefaultGasConfig(), nil, assetPrecision)
	require.NoError(t, err)
	require.Len(t, resultPlans, 1)
	require.Len(t, resultPlans[0].Outputs, 2)
	require.Equal(t, "buyer", resultPlans[0].Outputs[0].To)
	require.Equal(t, "33", resultPlans[0].Outputs[0].AssetAmt)
	require.Len(t, resultPlans[0].Outputs[0].Assets, 1)
	require.Equal(t, "33", resultPlans[0].Outputs[0].Assets[0].Amount.String())
	require.Equal(t, addr.MustEncode(), resultPlans[0].Outputs[1].To)
	require.Len(t, resultPlans[0].Outputs[1].Assets, 1)
	require.Equal(t, "66", resultPlans[0].Outputs[1].Assets[0].Amount.String())
}

func TestSettleAMMSellUsesConstantProductPool(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "3",
		UnitPrice: "100",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("sell", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "sell", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
			Assets:   testAsset("ordx:f:test", 100),
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.Equal(t, int64(0), plan.Deals[0].SellItemID)
	require.Equal(t, "100", plan.Deals[0].AssetAmt)
	require.Equal(t, int64(9), plan.Deals[0].SatValue)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, int64(9), plan.Transfers[0].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "200", state.Running.AssetAInPool)
	requireDecimalString(t, "11", state.Running.AssetBInPool)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
}

func TestSettleAMMRejectsSlippage(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "60",
		UnitPrice: "10",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("buy", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy", Vout: 1},
			Contract: addr,
			Value:    10,
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, int64(10), plan.Transfers[0].SatValue)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, InvokeReasonSlippageProtect, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
}

func TestSettleAMMWaitsUntilPoolMeetsK(t *testing.T) {
	runtime := testAMMRuntime(t)
	addr := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "1",
		UnitPrice: "10",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("buy", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy", Vout: 1},
			Contract: addr,
			Value:    10,
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	err = runtime.ApplyFunding([]ContractOutput{{
		OutPoint: OutPoint{TxID: "partial", Vout: 1},
		Contract: addr,
		Value:    20,
		Assets:   testAsset("ordx:f:test", 90),
	}}, "")
	require.NoError(t, err)
	plan, err = runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	err = runtime.ApplyFunding([]ContractOutput{{
		OutPoint: OutPoint{TxID: "rest", Vout: 1},
		Contract: addr,
		Assets:   testAsset("ordx:f:test", 10),
	}}, "")
	require.NoError(t, err)
	plan, err = runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
}

func TestSettleAMMAddLiquidityCanMakePoolReady(t *testing.T) {
	runtime := testAMMRuntime(t)
	addr := runtime.Address()
	err := runtime.ApplyFunding([]ContractOutput{{
		OutPoint: OutPoint{TxID: "deploy", Vout: 1},
		Contract: addr,
		Value:    20,
		Assets:   testAsset("ordx:f:test", 90),
	}}, "")
	require.NoError(t, err)

	buyParam, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "1",
		UnitPrice: "10",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  buyParam,
		CallID: DeriveInvokeCallID("buy", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy", Vout: 1},
			Contract: addr,
			Value:    10,
		}},
		Height: 1,
	})
	require.NoError(t, err)
	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     1,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPIAddLiquidity,
		Param:  addParam,
		CallID: DeriveInvokeCallID("add", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "add", Vout: 1},
			Contract: addr,
			Value:    1,
			Assets:   testAsset("ordx:f:test", 10),
		}},
		Height: 2,
	})
	require.NoError(t, err)

	plan, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.TradingReady)
	require.Equal(t, ItemStatusInit, state.Items[0].Done)
	require.Equal(t, ItemStatusDealt, state.Items[1].Done)

	plan, err = runtime.SettleBlock(3)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
}

func TestSettleAMMDoesNotRecheckInitialKAfterReady(t *testing.T) {
	runtime := testAMMRuntime(t)
	addr := runtime.Address()
	err := runtime.ApplyFunding([]ContractOutput{{
		OutPoint: OutPoint{TxID: "deploy", Vout: 1},
		Contract: addr,
		Value:    20,
		Assets:   testAsset("ordx:f:test", 100),
	}}, "")
	require.NoError(t, err)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.TradingReady)

	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "1",
		UnitPrice: "170",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("buy1", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "buy1", Vout: 1},
			Contract: addr,
			Value:    170,
		}},
		Height: 1,
	})
	require.NoError(t, err)
	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)

	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.TradingReady)
	require.NotNil(t, state.Running.AssetAInPool)
	require.NotNil(t, state.Running.RequiredAssetA)
	require.True(t, state.Running.AssetAInPool.Cmp(state.Running.RequiredAssetA) < 0)

	param, err = (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "1",
		UnitPrice: "1",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("sell1", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "sell1", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
			Assets:   testAsset("ordx:f:test", 1),
		}},
		Height: 2,
	})
	require.NoError(t, err)
	plan, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
}

func TestSettleAMMAddAndRemoveLiquidity(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "100",
		Value:     20,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIAddLiquidity,
		Param:   addParam,
		CallID:  DeriveInvokeCallID("add", 1, addr),
		Invoker: "alice",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "add", Vout: 1},
			Contract: addr,
			Value:    20,
			Assets:   testAsset("ordx:f:test", 100),
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Empty(t, plan.Transfers)
	require.ElementsMatch(t, []int64{0}, plan.ItemIDs)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "200", state.Running.AssetAInPool)
	requireDecimalString(t, "40", state.Running.AssetBInPool)
	require.NotEmpty(t, state.Running.TotalLPTAmt)
	aliceLPT := state.Running.LPBalances["alice"]
	require.NotEmpty(t, aliceLPT)

	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    aliceLPT.String(),
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRemoveLiquidity,
		Param:   removeParam,
		CallID:  DeriveInvokeCallID("remove", 1, addr),
		Invoker: "alice",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "remove", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
		}},
		Height: 2,
	})
	require.NoError(t, err)

	plan, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.True(t, parseDecimalOrZero(plan.Transfers[0].AssetAmt).Sign() > 0)
	require.Greater(t, plan.Transfers[0].SatValue, int64(0))

	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.NotNil(t, state.Running.AssetAInPool)
	require.NotNil(t, state.Running.AssetBInPool)
	require.True(t, state.Running.AssetAInPool.Cmp(parseDecimalOrZero("200")) < 0)
	require.True(t, state.Running.AssetBInPool.Cmp(scommon.NewDefaultDecimal(40)) < 0)
	require.Empty(t, state.Running.LPBalances["alice"])
}

func TestSettleAMMAddLiquidityUsesDeclaredValueOnly(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "100",
		Value:     20,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIAddLiquidity,
		Param:   addParam,
		CallID:  DeriveInvokeCallID("add", 1, addr),
		Invoker: "alice",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "add", Vout: 1},
			Contract: addr,
			Value:    50,
			Assets:   testAsset("ordx:f:test", 100),
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Transfers)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "200", state.Running.AssetAInPool)
	requireDecimalString(t, "40", state.Running.AssetBInPool)
	require.Equal(t, int64(40), state.Running.LPCosts["alice"])
}

func TestSettleAMMRemoveLiquiditySendsProfitShareToFoundation(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	applyAMMAddLiquidityForTest(t, runtime, addr, "add", "alice", "100", 100, 20, 1)
	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Transfers)

	applyAMMSwapInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "1", "10", 10, nil, 2)
	plan, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	aliceLPT := state.Running.LPBalances["alice"]
	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    aliceLPT.String(),
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRemoveLiquidity,
		Param:   removeParam,
		CallID:  DeriveInvokeCallID("remove", 1, addr),
		Invoker: "alice",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "remove", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
		}},
		Height: 3,
	})
	require.NoError(t, err)

	plan, err = runtime.SettleBlock(3)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "deployer-address", plan.Transfers[1].To)
	require.True(t, parseDecimalOrZero(plan.Transfers[1].AssetAmt).Sign() > 0 || plan.Transfers[1].SatValue > 0)
}

func TestSettleAMMEmptyPoolRequiresInitialKBeforeReadyAgain(t *testing.T) {
	runtime := testAMMRuntime(t)
	addr := runtime.Address()
	applyAMMAddLiquidityForTest(t, runtime, addr, "add0", "alice", "100", 100, 20, 1)
	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.TradingReady)
	aliceLPT := state.Running.LPBalances["alice"]
	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    aliceLPT.String(),
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRemoveLiquidity,
		Param:   removeParam,
		CallID:  DeriveInvokeCallID("remove", 1, addr),
		Invoker: "alice",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "remove", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
		}},
		Height: 2,
	})
	require.NoError(t, err)
	_, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.False(t, state.Running.TradingReady)

	applyAMMAddLiquidityForTest(t, runtime, addr, "add1", "bob", "99", 99, 20, 3)
	_, err = runtime.SettleBlock(3)
	require.NoError(t, err)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.False(t, state.Running.TradingReady)

	applyAMMAddLiquidityForTest(t, runtime, addr, "add2", "bob", "1", 1, 1, 4)
	_, err = runtime.SettleBlock(4)
	require.NoError(t, err)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.TradingReady)
}

func TestSettleAMMRejectsSellSlippage(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "100",
		UnitPrice: "100",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("sell", 1, addr),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "sell", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
			Assets:   testAsset("ordx:f:test", 100),
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "100", plan.Transfers[0].AssetAmt)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, InvokeReasonSlippageProtect, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	requireDecimalString(t, "100", state.Running.AssetAInPool)
	requireDecimalString(t, "20", state.Running.AssetBInPool)
}

func TestSettleAMMProcessesSwapsFIFOAgainstMutatingPool(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	applyAMMSwapInvokeForTest(t, runtime, addr, "buy0", "alice", OrderTypeBuy, "", "10", 10, nil, 1)
	applyAMMSwapInvokeForTest(t, runtime, addr, "buy1", "bob", OrderTypeBuy, "", "10", 10, nil, 1)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 2)
	require.Equal(t, int64(0), plan.Deals[0].BuyItemID)
	require.Equal(t, int64(1), plan.Deals[1].BuyItemID)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "bob", plan.Transfers[1].To)
	require.True(t, parseDecimalOrZero(plan.Deals[0].AssetAmt).Cmp(parseDecimalOrZero(plan.Deals[1].AssetAmt)) > 0)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
	require.Equal(t, ItemStatusDealt, state.Items[1].Done)
	requireDecimalString(t, "40", state.Running.AssetBInPool)
	require.NotNil(t, state.Running.AssetAInPool)
	require.True(t, state.Running.AssetAInPool.Cmp(parseDecimalOrZero("100")) < 0)
}

func TestSettleAMMRemoveLiquidityCapsAtOwnedAmount(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	applyAMMAddLiquidityForTest(t, runtime, addr, "add", "alice", "100", 100, 20, 1)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{0}, plan.ItemIDs)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	aliceLPT := state.Running.LPBalances["alice"]
	require.NotEmpty(t, aliceLPT)
	removeTooMuch := scommon.DecimalAdd(aliceLPT, aliceLPT).String()
	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    removeTooMuch,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRemoveLiquidity,
		Param:   removeParam,
		CallID:  DeriveInvokeCallID("remove", 1, addr),
		Invoker: "alice",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "remove", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
		}},
		Height: 2,
	})
	require.NoError(t, err)

	plan, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.True(t, parseDecimalOrZero(plan.Transfers[0].AssetAmt).Cmp(parseDecimalOrZero("99")) > 0)
	require.True(t, parseDecimalOrZero(plan.Transfers[0].AssetAmt).Cmp(parseDecimalOrZero("100")) <= 0)
	require.Greater(t, plan.Transfers[0].SatValue, int64(0))
	require.LessOrEqual(t, plan.Transfers[0].SatValue, int64(20))

	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Running.LPBalances["alice"])
	require.NotNil(t, state.Running.AssetAInPool)
	require.NotNil(t, state.Running.AssetBInPool)
	require.True(t, state.Running.AssetAInPool.Cmp(parseDecimalOrZero("100")) >= 0)
	require.True(t, state.Running.AssetBInPool.Cmp(scommon.NewDefaultDecimal(20)) >= 0)
}

func TestSettleAMMPartialRemoveLiquidityKeepsAssetPrecision(t *testing.T) {
	state := TemplateRuntimeState{
		Items: []InvokeItem{{
			ID:           0,
			Action:       InvokeAPIRemoveLiquidity,
			OrderType:    OrderTypeRemoveLiquidity,
			Reason:       InvokeReasonNormal,
			Address:      "alice",
			AssetName:    "ordx:f:test",
			ExpectedAmt:  parseDecimalOrZero("5"),
			RemainingAmt: parseDecimalOrZero("5"),
		}},
		Running: RunningData{
			AssetAInPool: parseDecimalOrZero("10"),
			AssetBInPool: scommon.NewDefaultDecimal(20),
			TradingReady: true,
			TotalLPTAmt:  parseDecimalOrZero("12"),
			LPBalances: map[string]*scommon.Decimal{
				"alice": parseDecimalOrZero("10"),
				"bob":   parseDecimalOrZero("2"),
			},
			LPCosts: map[string]int64{
				"alice": 20,
			},
		},
	}
	plan := &SettlementPlan{}

	changed, err := applyAMMLiquidity(&state, plan, "foundation")
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, plan.Transfers, 2)
	for _, transfer := range plan.Transfers {
		_, err := newAssetSet(transfer.AssetName, transfer.AssetAmt)
		require.NoError(t, err)
	}
}

func TestSettleAMMRemoveLiquidityWithoutBalanceClosesDirectly(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    "1",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIRemoveLiquidity,
		Param:   removeParam,
		CallID:  DeriveInvokeCallID("remove", 1, addr),
		Invoker: "alice",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "remove", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
		}},
		Height: 1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Empty(t, plan.Transfers)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusClosedDirectly, state.Items[0].Done)
	require.Equal(t, InvokeReasonNoEnoughAsset, state.Items[0].Reason)
	requireDecimalString(t, "100", state.Running.AssetAInPool)
	requireDecimalString(t, "20", state.Running.AssetBInPool)
}

func TestSettleAMMCloseClearsPoolState(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	closeParam, err := (&CloseInvokeParam{}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIClose,
		Param:   closeParam,
		CallID:  DeriveInvokeCallID("close", 1, addr),
		Invoker: "deployer-address",
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "close", Vout: 1},
			Contract: addr,
			Value:    SwapInvokeFee,
		}},
		Height: 2,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.NotEmpty(t, plan.ItemIDs)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.Closed)
	require.Empty(t, state.Running.AssetAInPool)
	require.Empty(t, state.Running.AssetBInPool)
	require.False(t, state.Running.TradingReady)
	require.Empty(t, state.Running.TotalLPTAmt)
	require.Empty(t, state.Running.LPBalances)
	require.Empty(t, state.Running.LPCosts)
}

func applyAMMSwapInvokeForTest(t *testing.T, runtime *ContractRuntime, addr ContractAddress,
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

func applyAMMAddLiquidityForTest(t *testing.T, runtime *ContractRuntime, addr ContractAddress,
	callID, invoker, amt string, assetAmount, value int64, height int64) {

	t.Helper()
	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       amt,
		Value:     value,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:  InvokeAPIAddLiquidity,
		Param:   addParam,
		CallID:  DeriveInvokeCallID(callID, 1, addr),
		Invoker: invoker,
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: callID, Vout: 1},
			Contract: addr,
			Value:    value,
			Assets:   testAsset("ordx:f:test", assetAmount),
		}},
		Height: height,
	})
	require.NoError(t, err)
}
