package template

import (
	"strconv"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
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
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		FundingOutput: testContractOutput("buy", 1, addr, 10, nil),
		Height:        1,
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
	requireDecimalString(t, "66.844919786", state.AMMData().AssetAInPool)
	requireDecimalString(t, "30", state.AMMData().AssetBInPool)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
}

func TestAMMOverfundUsesActualPoolK(t *testing.T) {
	runtime := testAMMRuntime(t)
	addr := runtime.Address()
	require.NoError(t, runtime.ApplyFunding(
		testContractOutput("deploy", 1, addr, 20, testAsset("ordx:f:test", 110)), ""))

	before, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "2000", before.AMMData().RequiredK)
	requireDecimalString(t, "2200", before.AMMData().K)
	requireDecimalString(t, "46.9041575982", before.AMMData().TotalLPTAmt)

	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "30",
		UnitPrice: "10",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		Invoker:       "buyer",
		FundingOutput: testContractOutput("buy", 1, addr, 10, nil),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
	require.True(t, parseDecimalOrZero(plan.Deals[0].AssetAmt).Cmp(parseDecimalOrZero("43.155080214")) < 0)
	after, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireAMMPoolInvariant(t, after.AMMData())
	require.True(t, after.AMMData().K.Cmp(before.AMMData().K) >= 0)
}

func TestAMMDirectFundingCannotBeCaptured(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	require.NoError(t, runtime.ApplyFunding(
		testContractOutput("donation", 1, addr, 0, testAsset("ordx:f:test", 10)), ""))

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "2200", state.AMMData().K)
	requireAMMPoolInvariant(t, state.AMMData())
}

func TestAMMRetentionUpdatesPoolK(t *testing.T) {
	state := TemplateRuntimeState{}
	running := state.AMMData()
	running.AssetAInPool = parseDecimalOrZero("100")
	running.AssetBInPool = parseDecimalOrZero("20")
	running.K = parseDecimalOrZero("2000")
	contract := NewAMMContract("ordx:f:test", "100", 20, "2000")

	contract.ApplyRunningData(&state, &InvokeItem{RetainedAssetA: parseDecimalOrZero("10")})
	requireDecimalString(t, "2200", state.AMMData().K)
	requireAMMPoolInvariant(t, state.AMMData())
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
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		Invoker:       "buyer",
		FundingOutput: testContractOutput("buy", 1, addr, 10, nil),
		Height:        1,
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
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("sell", 1, addr),
		FundingOutput: testContractOutput("sell", 1, addr, SwapInvokeFee, testAsset("ordx:f:test", 100)),
		Height:        1,
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
	requireDecimalString(t, "200", state.AMMData().AssetAInPool)
	requireDecimalString(t, "11", state.AMMData().AssetBInPool)
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
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		FundingOutput: testContractOutput("buy", 1, addr, 10, nil),
		Height:        1,
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

func TestSettleAMMBuyNeedsFeeAdjustedFunding(t *testing.T) {
	const assetName = "brc20:f:ooxx"
	newRuntime := func(t *testing.T) *ContractRuntime {
		t.Helper()
		runtime := testAMMRuntimeWithAsset(t, assetName, 100000, 100000, "10000000000")
		fundAMMRuntimeWithAsset(t, runtime, assetName, 100000, 100000)
		return runtime
	}
	applyBuy := func(t *testing.T, runtime *ContractRuntime, value int64) *SettlementPlan {
		t.Helper()
		addr := runtime.Address()
		param, err := (&LimitOrderInvokeParam{
			OrderType: OrderTypeBuy,
			AssetName: assetName,
			Amt:       "100",
			UnitPrice: strconv.FormatInt(value, 10),
		}).Encode()
		require.NoError(t, err)
		_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
			Action:        InvokeAPISwap,
			Param:         param,
			CallID:        DeriveInvokeCallID(testHash(byte(value)), 1, addr),
			Invoker:       "buyer",
			FundingOutput: testContractOutput(testHash(byte(value)), 1, addr, value, nil),
			Height:        1,
		})
		require.NoError(t, err)
		plan, err := runtime.SettleBlock(1)
		require.NoError(t, err)
		return plan
	}

	t.Run("100 sats refunds because output is below minimum", func(t *testing.T) {
		runtime := newRuntime(t)
		plan := applyBuy(t, runtime, 100)
		require.Empty(t, plan.Deals)
		require.Len(t, plan.Transfers, 1)
		require.Equal(t, int64(100), plan.Transfers[0].SatValue)
		require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)
		state, err := runtime.RuntimeState()
		require.NoError(t, err)
		require.Equal(t, InvokeReasonSlippageProtect, state.Items[0].Reason)
		require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	})

	t.Run("101 sats uses the full input once minimum output is met", func(t *testing.T) {
		runtime := newRuntime(t)
		plan := applyBuy(t, runtime, 101)
		require.Len(t, plan.Deals, 1)
		require.Equal(t, int64(101), plan.Deals[0].SatValue)
		require.Equal(t, "100.0917161078", plan.Deals[0].AssetAmt)
		require.Len(t, plan.Transfers, 1)
		require.Equal(t, "100.0917161078", plan.Transfers[0].AssetAmt)
		state, err := runtime.RuntimeState()
		require.NoError(t, err)
		require.Equal(t, ItemStatusDealt, state.Items[0].Done)

		store := NewRuntimeStore()
		store.Add(runtime)
		resultPlans, err := BuildSettlementResultPlans([]*SettlementPlan{plan}, nil, func(name string) (int, bool) {
			return 0, name == assetName
		})
		require.NoError(t, err)
		resultPlans, err = AugmentResultPlans(resultPlans, store, DefaultGasConfig(), nil, func(name string) (int, bool) {
			return 0, name == assetName
		})
		require.NoError(t, err)
		require.Len(t, resultPlans, 1)
		require.Equal(t, "buyer", resultPlans[0].Outputs[0].To)
		require.Equal(t, "100", resultPlans[0].Outputs[0].AssetAmt)
	})

	t.Run("input above the minimum is fully swapped", func(t *testing.T) {
		runtime := newRuntime(t)
		plan := applyBuy(t, runtime, 110)
		require.Len(t, plan.Deals, 1)
		require.Equal(t, int64(110), plan.Deals[0].SatValue)
		require.True(t, parseDecimalOrZero(plan.Deals[0].AssetAmt).Cmp(parseDecimalOrZero("100")) > 0)
		require.Len(t, plan.Transfers, 1)
		require.Equal(t, "buyer", plan.Transfers[0].To)
		require.Equal(t, plan.Deals[0].AssetAmt, plan.Transfers[0].AssetAmt)
		require.Equal(t, SettlementReasonDeal, plan.Transfers[0].Reason)

		state, err := runtime.RuntimeState()
		require.NoError(t, err)
		require.Equal(t, ItemStatusDealt, state.Items[0].Done)
		require.True(t, state.AMMData().AssetAInPool.Cmp(parseDecimalOrZero("99900")) < 0)
		requireDecimalString(t, "100110", state.AMMData().AssetBInPool)
	})
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
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		FundingOutput: testContractOutput("buy", 1, addr, 10, nil),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	err = runtime.ApplyFunding(testContractOutput("partial", 1, addr, 20, testAsset("ordx:f:test", 90)), "")
	require.NoError(t, err)
	plan, err = runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	err = runtime.ApplyFunding(testContractOutput("rest", 1, addr, 0, testAsset("ordx:f:test", 10)), "")
	require.NoError(t, err)
	plan, err = runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)
}

func TestSettleAMMAddLiquidityCanMakePoolReady(t *testing.T) {
	runtime := testAMMRuntime(t)
	addr := runtime.Address()
	err := runtime.ApplyFunding(testContractOutput("deploy", 1, addr, 20, testAsset("ordx:f:test", 90)), "")
	require.NoError(t, err)

	buyParam, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "1",
		UnitPrice: "10",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         buyParam,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		FundingOutput: testContractOutput("buy", 1, addr, 10, nil),
		Height:        1,
	})
	require.NoError(t, err)
	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     3,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIAddLiquidity,
		Param:         addParam,
		CallID:        DeriveInvokeCallID("add", 1, addr),
		FundingOutput: testContractOutput("add", 1, addr, 3, testAsset("ordx:f:test", 10)),
		Height:        2,
	})
	require.NoError(t, err)

	plan, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AMMData().TradingReady)
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
	err := runtime.ApplyFunding(testContractOutput("deploy", 1, addr, 20, testAsset("ordx:f:test", 100)), "")
	require.NoError(t, err)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AMMData().TradingReady)

	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "1",
		UnitPrice: "170",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("buy1", 1, addr),
		FundingOutput: testContractOutput("buy1", 1, addr, 170, nil),
		Height:        1,
	})
	require.NoError(t, err)
	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)

	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AMMData().TradingReady)
	require.NotNil(t, state.AMMData().AssetAInPool)
	require.NotNil(t, state.AMMData().RequiredAssetA)
	require.True(t, state.AMMData().AssetAInPool.Cmp(state.AMMData().RequiredAssetA) < 0)

	param, err = (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "1",
		UnitPrice: "1",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("sell1", 1, addr),
		FundingOutput: testContractOutput("sell1", 1, addr, SwapInvokeFee, testAsset("ordx:f:test", 10)),
		Height:        2,
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
		Action:        InvokeAPIAddLiquidity,
		Param:         addParam,
		CallID:        DeriveInvokeCallID("add", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("add", 1, addr, 20, testAsset("ordx:f:test", 100)),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Empty(t, plan.Transfers)
	require.ElementsMatch(t, []int64{0}, plan.ItemIDs)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "200", state.AMMData().AssetAInPool)
	requireDecimalString(t, "40", state.AMMData().AssetBInPool)
	require.NotEmpty(t, state.AMMData().TotalLPTAmt)
	aliceLPT := state.AMMData().LPBalances["alice"]
	require.NotEmpty(t, aliceLPT)

	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    aliceLPT.String(),
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIRemoveLiquidity,
		Param:         removeParam,
		CallID:        DeriveInvokeCallID("remove", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("remove", 1, addr, SwapInvokeFee, nil),
		Height:        2,
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
	require.NotNil(t, state.AMMData().AssetAInPool)
	require.NotNil(t, state.AMMData().AssetBInPool)
	require.True(t, state.AMMData().AssetAInPool.Cmp(parseDecimalOrZero("200")) < 0)
	require.True(t, state.AMMData().AssetBInPool.Cmp(scommon.NewDefaultDecimal(40)) < 0)
	require.Empty(t, state.AMMData().LPBalances["alice"])
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
		Action:        InvokeAPIAddLiquidity,
		Param:         addParam,
		CallID:        DeriveInvokeCallID("add", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("add", 1, addr, 50, testAsset("ordx:f:test", 100)),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Transfers)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "200", state.AMMData().AssetAInPool)
	requireDecimalString(t, "40", state.AMMData().AssetBInPool)
	require.Equal(t, int64(40), state.AMMData().LPCosts["alice"])
}

func TestSettleAMMAddLiqRefundsExcess(t *testing.T) {
	runtime := testAMMRuntimeWithAsset(t, "brc20:f:ooxx", 10, 10, "100")
	fundAMMRuntimeWithAsset(t, runtime, "brc20:f:ooxx", 10, 10)
	addr := runtime.Address()
	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "brc20:f:ooxx",
		Amt:       "8",
		Value:     6,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIAddLiquidity,
		Param:         addParam,
		CallID:        DeriveInvokeCallID("add", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("add", 1, addr, 6, testAsset("brc20:f:ooxx", 8)),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)
	require.Equal(t, "2", plan.Transfers[0].AssetAmt)
	require.Equal(t, int64(0), plan.Transfers[0].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "16", state.AMMData().AssetAInPool)
	requireDecimalString(t, "16", state.AMMData().AssetBInPool)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
	requireDecimalString(t, "6", state.Items[0].OutAmt)
}

func TestAMMAddLiqFractionRefund(t *testing.T) {
	runtime := testAMMRuntimeWithAsset(t, "brc20:f:ooxx", 1000000, 100000, "100000000000")
	fundAMMRuntimeWithAsset(t, runtime, "brc20:f:ooxx", 1000000, 100000)
	addr := runtime.Address()
	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "brc20:f:ooxx",
		Amt:       "220000",
		Value:     20000,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIAddLiquidity,
		Param:         addParam,
		CallID:        DeriveInvokeCallID("add", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("add", 1, addr, 20000, testAsset("brc20:f:ooxx", 220000)),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "20000", plan.Transfers[0].AssetAmt)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "1200000", state.AMMData().AssetAInPool)
	requireDecimalString(t, "120000", state.AMMData().AssetBInPool)
}

func TestSettleAMMAddLiqResultRefundsExcess(t *testing.T) {
	assetName := "brc20:f:ooxx"
	runtime := testAMMRuntimeWithAsset(t, assetName, 10, 10, "100")
	fundAMMRuntimeWithAsset(t, runtime, assetName, 10, 10)
	addr := runtime.Address()
	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: assetName,
		Amt:       "8",
		Value:     6,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIAddLiquidity,
		Param:         addParam,
		CallID:        DeriveInvokeCallID("add", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("add", 1, addr, 6, testAsset(assetName, 8)),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	store := NewRuntimeStore()
	store.Add(runtime)
	provider := func(contractAddr ContractAddress) ([]contractframework.UTXO, error) {
		require.True(t, addr.Equal(contractAddr))
		return []contractframework.UTXO{testContractUTXO("pool", 0, addr, 16, testAsset(assetName, 18))}, nil
	}
	assetPrecision := func(name string) (int, bool) {
		return 0, name == assetName
	}
	resultPlans, err := BuildSettlementResultPlans([]*SettlementPlan{plan}, nil, assetPrecision)
	require.NoError(t, err)
	resultPlans, err = AugmentResultPlans(resultPlans, store, DefaultGasConfig(), provider, assetPrecision)
	require.NoError(t, err)
	require.Len(t, resultPlans, 1)
	requireResultPlanAssetTo(t, resultPlans[0], "alice", assetName, "2")
	requireResultPlanAssetTo(t, resultPlans[0], addr.MustEncode(), assetName, "16")
	requireResultPlanValueTo(t, resultPlans[0], addr.MustEncode(), 16)
	requireNoResultPlanOutputTo(t, resultPlans[0], "bootstrap-address")
}

func TestSettleAMMBuySmallIntegerRefund(t *testing.T) {
	runtime := testAMMRuntimeWithAsset(t, "brc20:f:ooxx", 18, 19, "342")
	fundAMMRuntimeWithAsset(t, runtime, "brc20:f:ooxx", 18, 19)
	addr := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "brc20:f:ooxx",
		UnitPrice: "1",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("buy", 1, addr),
		Invoker:       "buyer",
		FundingOutput: testContractOutput("buy", 1, addr, 1, nil),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlockWithGasConfigAndPrecision(1, DefaultGasConfig(), func(name string) (int, bool) {
		return 0, name == "brc20:f:ooxx"
	})
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "buyer", plan.Transfers[0].To)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)
	require.Equal(t, int64(1), plan.Transfers[0].SatValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "18", state.AMMData().AssetAInPool)
	requireDecimalString(t, "19", state.AMMData().AssetBInPool)
	require.Equal(t, InvokeReasonNoEnoughAsset, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
}

func TestSettleAMMSellSmallIntegerRefund(t *testing.T) {
	runtime := testAMMRuntimeWithAsset(t, "brc20:f:ooxx", 18, 19, "342")
	fundAMMRuntimeWithAsset(t, runtime, "brc20:f:ooxx", 18, 19)
	addr := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "brc20:f:ooxx",
		UnitPrice: "1",
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("sell", 1, addr),
		Invoker:       "seller",
		FundingOutput: testContractOutput("sell", 1, addr, 0, testAsset("brc20:f:ooxx", 1)),
		Height:        1,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlockWithGasConfigAndPrecision(1, DefaultGasConfig(), func(name string) (int, bool) {
		return 0, name == "brc20:f:ooxx"
	})
	require.NoError(t, err)
	require.Empty(t, plan.Deals)
	require.Len(t, plan.Transfers, 1)
	require.Equal(t, "seller", plan.Transfers[0].To)
	require.Equal(t, SettlementReasonRefund, plan.Transfers[0].Reason)
	require.Equal(t, "1", plan.Transfers[0].AssetAmt)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "18", state.AMMData().AssetAInPool)
	requireDecimalString(t, "19", state.AMMData().AssetBInPool)
	require.Equal(t, InvokeReasonNoEnoughAsset, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
}

func TestSettleAMMRemoveLiquiditySendsProfitShareToFoundation(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	applyAMMAddLiquidityForTest(t, runtime, addr, "add", "alice", "100", 100, 20, 1)
	plan, err := runtime.SettleBlock(1)
	require.NoError(t, err)
	require.Empty(t, plan.Transfers)

	applyAMMSwapInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "50", "20", 20, nil, 2)
	plan, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	require.Len(t, plan.Deals, 1)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	aliceLPT := state.AMMData().LPBalances["alice"]
	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    aliceLPT.String(),
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIRemoveLiquidity,
		Param:         removeParam,
		CallID:        DeriveInvokeCallID("remove", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("remove", 1, addr, SwapInvokeFee, nil),
		Height:        3,
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
	require.True(t, state.AMMData().TradingReady)
	aliceLPT := state.AMMData().LPBalances["alice"]
	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    aliceLPT.String(),
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIRemoveLiquidity,
		Param:         removeParam,
		CallID:        DeriveInvokeCallID("remove", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("remove", 1, addr, SwapInvokeFee, nil),
		Height:        2,
	})
	require.NoError(t, err)
	_, err = runtime.SettleBlock(2)
	require.NoError(t, err)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.False(t, state.AMMData().TradingReady)

	applyAMMAddLiquidityForTest(t, runtime, addr, "add1", "bob", "99", 99, 20, 3)
	_, err = runtime.SettleBlock(3)
	require.NoError(t, err)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.False(t, state.AMMData().TradingReady)

	applyAMMAddLiquidityForTest(t, runtime, addr, "add2", "bob", "1", 1, 1, 4)
	_, err = runtime.SettleBlock(4)
	require.NoError(t, err)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AMMData().TradingReady)
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
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("sell", 1, addr),
		FundingOutput: testContractOutput("sell", 1, addr, SwapInvokeFee, testAsset("ordx:f:test", 100)),
		Height:        1,
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
	requireDecimalString(t, "100", state.AMMData().AssetAInPool)
	requireDecimalString(t, "20", state.AMMData().AssetBInPool)
}

func TestSettleAMMProcessesBatchSwapsSequentially(t *testing.T) {
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
	require.Equal(t, "33.155080214", plan.Deals[0].AssetAmt)
	require.Equal(t, "16.6107616302", plan.Deals[1].AssetAmt)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "bob", plan.Transfers[1].To)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, ItemStatusDealt, state.Items[0].Done)
	require.Equal(t, ItemStatusDealt, state.Items[1].Done)
	requireDecimalString(t, "40", state.AMMData().AssetBInPool)
	require.NotNil(t, state.AMMData().AssetAInPool)
	requireDecimalString(t, "50.2341581558", state.AMMData().AssetAInPool)
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
	aliceLPT := state.AMMData().LPBalances["alice"]
	require.NotEmpty(t, aliceLPT)
	removeTooMuch := scommon.DecimalAdd(aliceLPT, aliceLPT).String()
	removeParam, err := (&RemoveLiquidityInvokeParam{
		OrderType: OrderTypeRemoveLiquidity,
		AssetName: "ordx:f:test",
		LptAmt:    removeTooMuch,
	}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIRemoveLiquidity,
		Param:         removeParam,
		CallID:        DeriveInvokeCallID("remove", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("remove", 1, addr, SwapInvokeFee, nil),
		Height:        2,
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
	require.Empty(t, state.AMMData().LPBalances["alice"])
	require.NotNil(t, state.AMMData().AssetAInPool)
	require.NotNil(t, state.AMMData().AssetBInPool)
	require.True(t, state.AMMData().AssetAInPool.Cmp(parseDecimalOrZero("100")) >= 0)
	require.True(t, state.AMMData().AssetBInPool.Cmp(scommon.NewDefaultDecimal(20)) >= 0)
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
	}
	running := state.AMMData()
	running.AssetAInPool = parseDecimalOrZero("10")
	running.AssetBInPool = scommon.NewDefaultDecimal(20)
	running.TradingReady = true
	running.TotalLPTAmt = parseDecimalOrZero("12")
	running.LPBalances = map[string]*scommon.Decimal{
		"alice": parseDecimalOrZero("10"),
		"bob":   parseDecimalOrZero("2"),
	}
	running.LPCosts = map[string]int64{
		"alice": 20,
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
		Action:        InvokeAPIRemoveLiquidity,
		Param:         removeParam,
		CallID:        DeriveInvokeCallID("remove", 1, addr),
		Invoker:       "alice",
		FundingOutput: testContractOutput("remove", 1, addr, SwapInvokeFee, nil),
		Height:        1,
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
	requireDecimalString(t, "100", state.AMMData().AssetAInPool)
	requireDecimalString(t, "20", state.AMMData().AssetBInPool)
}

func TestSettleAMMCloseClearsPoolState(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()
	closeParam, err := (&CloseInvokeParam{}).Encode()
	require.NoError(t, err)
	_, err = runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIClose,
		Param:         closeParam,
		CallID:        DeriveInvokeCallID("close", 1, addr),
		Invoker:       "deployer-address",
		FundingOutput: testContractOutput("close", 1, addr, SwapInvokeFee, nil),
		Height:        2,
	})
	require.NoError(t, err)

	plan, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	require.NotEmpty(t, plan.ItemIDs)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AMMData().Closed)
	require.Empty(t, state.AMMData().AssetAInPool)
	require.Empty(t, state.AMMData().AssetBInPool)
	require.False(t, state.AMMData().TradingReady)
	require.Empty(t, state.AMMData().TotalLPTAmt)
	require.Empty(t, state.AMMData().LPBalances)
	require.Empty(t, state.AMMData().LPCosts)
}

func TestAMMCloseSortsLPsAndAssignsExactRemainder(t *testing.T) {
	state := TemplateRuntimeState{}
	running := state.AMMData()
	running.AssetAInPool = parseDecimalOrZero("10")
	running.AssetBInPool = scommon.NewDefaultDecimal(11)
	running.TotalLPTAmt = parseDecimalOrZero("3")
	running.LPBalances = map[string]*scommon.Decimal{
		"bob":   parseDecimalOrZero("2"),
		"alice": parseDecimalOrZero("1"),
	}
	plan := &SettlementPlan{}
	appendAMMLPCloseTransfers(&state, plan, &InvokeItem{ID: 9}, "ordx:f:test")
	require.Len(t, plan.Transfers, 2)
	require.Equal(t, "alice", plan.Transfers[0].To)
	require.Equal(t, "bob", plan.Transfers[1].To)
	require.Equal(t, int64(11), plan.Transfers[0].SatValue+plan.Transfers[1].SatValue)
	totalAsset := parseDecimalOrZero(plan.Transfers[0].AssetAmt).AddAlignPrecision(parseDecimalOrZero(plan.Transfers[1].AssetAmt))
	require.Zero(t, totalAsset.Cmp(parseDecimalOrZero("10")))
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
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID(callID, 1, addr),
		Invoker:       invoker,
		FundingOutput: testContractOutput(callID, 1, addr, value, assets),
		Height:        height,
	})
	require.NoError(t, err)
}

func requireAMMPoolInvariant(t *testing.T, running *AMMRunningData) {
	t.Helper()
	require.NotNil(t, running)
	require.NotNil(t, running.AssetAInPool)
	require.NotNil(t, running.AssetBInPool)
	require.NotNil(t, running.K)
	want := scommon.DecimalMul(running.AssetAInPool, running.AssetBInPool)
	require.Zero(t, running.K.Cmp(want))
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
		Action:        InvokeAPIAddLiquidity,
		Param:         addParam,
		CallID:        DeriveInvokeCallID(callID, 1, addr),
		Invoker:       invoker,
		FundingOutput: testContractOutput(callID, 1, addr, value, testAsset("ordx:f:test", assetAmount)),
		Height:        height,
	})
	require.NoError(t, err)
}
