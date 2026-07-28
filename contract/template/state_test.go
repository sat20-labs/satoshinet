package template

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func testAsset(assetName string, amount int64) wire.TxAssets {
	return wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(assetName),
		Amount: *scommon.NewDefaultDecimal(amount),
	}}
}

func testContractOutput(txid string, vout uint32, contractAddr ContractAddress, value int64, assets wire.TxAssets) ContractOutput {
	return contractframework.ContractOutputFromFunding(contractcommon.FundingOutput{
		OutPoint: contractcommon.TxOutPoint{TxID: txid, Vout: vout},
		Vout:     vout,
		Contract: contractAddr,
		Value:    value,
		Assets:   assets,
	})
}

func testContractUTXO(txid string, vout uint32, contractAddr ContractAddress, value int64, assets wire.TxAssets) UTXO {
	output := testContractOutput(txid, vout, contractAddr, value, assets)
	return UTXO{
		OutPoint: output.OutPoint,
		Contract: output.Contract,
		TxOutput: output.IndexerTxOutput(),
	}
}

func requireDecimalString(t *testing.T, expected string, actual *scommon.Decimal) {
	t.Helper()
	if actual == nil {
		actual = scommon.NewDefaultDecimal(0)
	}
	require.Equal(t, expected, actual.String())
}

func TestApplyInvokeRecordsLimitOrderItem(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	contract := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)

	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("tx", 1, contract),
		FundingOutput: testContractOutput("tx", 1, contract, 30, nil),
		Height:        100,
		Timestamp:     200,
	})
	require.NoError(t, err)
	rawParam := append([]byte(nil), param...)
	require.Equal(t, int64(0), item.ID)
	require.Equal(t, OrderTypeBuy, item.OrderType)
	require.Equal(t, rawParam, item.Param)
	param[0] ^= 0xff
	require.NotEqual(t, param, item.Param)
	expected, err := limitOrderItemExpectedAmount(item)
	require.NoError(t, err)
	requireDecimalString(t, "10", expected)
	require.Equal(t, int64(20), item.RemainingValue)
	require.Equal(t, int64(10), item.OutValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, int64(1), state.NextItemID)
	require.Equal(t, uint64(1), state.InvokeCount)
	require.Len(t, state.Items, 1)
	require.Equal(t, rawParam, state.Items[0].Param)
	requireDecimalString(t, "30", state.LimitOrderData().TotalInputAssetB)
	requireDecimalString(t, "20", state.LimitOrderData().AssetBInPool)
}

func TestApplyInvokePreservesRawParamWhileResolvingGenericAssetName(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	contract := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)
	rawParam := append([]byte(nil), param...)

	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("raw-param", 1, contract),
		FundingOutput: testContractOutput("raw-param", 1, contract, 30, nil),
		Height:        100,
		Timestamp:     200,
	})
	require.NoError(t, err)
	require.Equal(t, "ordx:f:test", item.AssetName)
	require.Equal(t, rawParam, item.Param)

	var decoded LimitOrderInvokeParam
	require.NoError(t, decoded.Decode(item.Param))
	require.Empty(t, decoded.AssetName)
}

func TestApplyInvokeRecordsLimitOrderBuyExcessForRefund(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	contract := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)

	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("tx", 1, contract),
		FundingOutput: testContractOutput("tx", 1, contract, 200, nil),
		Height:        100,
		Timestamp:     200,
	})
	require.NoError(t, err)
	require.Equal(t, InvokeReasonNormal, item.Reason)
	require.Equal(t, int64(20), item.RemainingValue)
	require.Equal(t, int64(180), item.OutValue)
}

func TestApplyInvokeMarksLimitOrderSellInvalidWhenAssetAmountDiffers(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	contract := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)

	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("tx", 1, contract),
		FundingOutput: testContractOutput("tx", 1, contract, SwapInvokeFee, testAsset("ordx:f:test", 9)),
		Height:        100,
		Timestamp:     200,
	})
	require.NoError(t, err)
	require.Equal(t, InvokeReasonInvalid, item.Reason)
	require.Empty(t, item.RemainingAmt)
	require.Zero(t, item.RemainingValue)
}

func TestApplySellExtraSatsInvalid(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	contract := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeSell,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
	}).Encode()
	require.NoError(t, err)

	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("tx", 1, contract),
		FundingOutput: testContractOutput("tx", 1, contract, 10, testAsset("ordx:f:test", 10)),
		Height:        100,
		Timestamp:     200,
	})
	require.NoError(t, err)
	require.Equal(t, InvokeReasonInvalid, item.Reason)
	require.Empty(t, item.RemainingAmt)
	require.Zero(t, item.RemainingValue)
}

func TestApplyInvokeRecordsAMMAddLiquidityItem(t *testing.T) {
	runtime := testAMMRuntime(t)
	contract := runtime.Address()
	param, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     20,
	}).Encode()
	require.NoError(t, err)

	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIAddLiquidity,
		Param:         param,
		CallID:        DeriveInvokeCallID("tx", 1, contract),
		FundingOutput: testContractOutput("tx", 1, contract, 25, testAsset("ordx:f:test", 10)),
		Height:        100,
		Timestamp:     200,
	})
	require.NoError(t, err)
	require.Equal(t, OrderTypeAddLiquidity, item.OrderType)
	requireDecimalString(t, "10", item.InAmt)
	require.Equal(t, int64(20), item.RemainingValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.AMMData().AssetAInPool)
	require.Zero(t, state.AMMData().AssetBInPool)
	requireDecimalString(t, "10", state.AMMData().TotalInputAssetA)
	requireDecimalString(t, "25", state.AMMData().TotalInputAssetB)
}

func TestApplyInvokeMarksAMMAddLiquidityInvalidWhenDeclaredAssetMissing(t *testing.T) {
	runtime := testAMMRuntime(t)
	contract := runtime.Address()
	param, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: "ordx:f:test",
		Amt:       "10",
		Value:     20,
	}).Encode()
	require.NoError(t, err)

	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPIAddLiquidity,
		Param:         param,
		CallID:        DeriveInvokeCallID("tx", 1, contract),
		FundingOutput: testContractOutput("tx", 1, contract, 20, nil),
		Height:        100,
		Timestamp:     200,
	})
	require.NoError(t, err)
	require.Equal(t, InvokeReasonInvalid, item.Reason)
	require.Empty(t, item.RemainingAmt)
	require.Zero(t, item.RemainingValue)
}

func TestApplyInvokeRecordsAMMBuyWithExactFunding(t *testing.T) {
	runtime := testAMMRuntime(t)
	contract := runtime.Address()
	param, err := (&LimitOrderInvokeParam{
		OrderType: OrderTypeBuy,
		AssetName: "ordx:f:test",
		Amt:       "",
		UnitPrice: "10",
	}).Encode()
	require.NoError(t, err)

	item, err := runtime.ApplyInvoke(ApplyInvokeRequest{
		Action:        InvokeAPISwap,
		Param:         param,
		CallID:        DeriveInvokeCallID("tx", 1, contract),
		FundingOutput: testContractOutput("tx", 1, contract, 10, nil),
		Height:        100,
		Timestamp:     200,
	})
	require.NoError(t, err)
	require.Equal(t, OrderTypeBuy, item.OrderType)
	require.Equal(t, int64(SwapInvokeFee), item.ServiceFee)
	require.Equal(t, int64(10), item.RemainingValue)
	expected, err := limitOrderItemExpectedAmount(item)
	require.NoError(t, err)
	require.Zero(t, expected.Sign())
}

func TestApplyFundingTracksTemplateGasSeparately(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	err := runtime.ApplyFunding(testContractOutput("fund", 1, addr, 0, testAsset("ordx:f:gas", 50)), "ordx:f:gas")
	require.NoError(t, err)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "50", state.LimitOrderData().GasBalance)
	require.Empty(t, state.LimitOrderData().AssetAInPool)
	require.Zero(t, state.LimitOrderData().AssetBInPool)
}

func TestApplyGasFundingDoesNotChangeAMMPool(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()

	err := runtime.ApplyGasFunding(testContractOutput("invoke", 1, addr, 10, testAsset("ordx:f:test", 5)), "ordx:f:gas")
	require.NoError(t, err)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "100", state.AMMData().AssetAInPool)
	requireDecimalString(t, "20", state.AMMData().AssetBInPool)
	require.Empty(t, state.AMMData().GasBalance)
	require.True(t, state.AMMData().TradingReady)
}

func TestRuntimeStoreReconcileAssetCachesUsesContractUTXOs(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	running := state.AMMData()
	running.AssetAInPool = parseDecimalOrZero("1")
	running.AssetBInPool = scommon.NewDefaultDecimal(2)
	running.GasBalance = parseDecimalOrZero("3")
	require.NoError(t, runtime.saveRuntimeState(state))

	store := NewRuntimeStore()
	store.Add(runtime)
	gasAssetName := DefaultGasConfig().GasAssetName
	actualAssets := testAssets("ordx:f:test", 77, gasAssetName, 5)
	err = store.ReconcileAssetCaches(func(contract ContractAddress) ([]UTXO, error) {
		require.True(t, contract.Equal(runtime.Address()))
		return []UTXO{testContractUTXO("actual", 0, contract, 33, actualAssets)}, nil
	}, DefaultGasConfig())
	require.NoError(t, err)

	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "77", state.AMMData().AssetAInPool)
	requireDecimalString(t, "33", state.AMMData().AssetBInPool)
	requireDecimalString(t, "5", state.AMMData().GasBalance)
	requireDecimalString(t, "2541", state.AMMData().K)
	requireAMMPoolInvariant(t, state.AMMData())
}

func testLimitOrderRuntime(t *testing.T) *ContractRuntime {
	t.Helper()
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateLimitOrder,
		Version:         CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, content, "deployer-address", deploy.DeployNonce)
	require.NoError(t, err)
	runtime, err := NewRuntimeWithDeployer(addr, deploy, nil, "deployer-address")
	require.NoError(t, err)
	return runtime
}

func fundAMMRuntime(t *testing.T, runtime *ContractRuntime) {
	t.Helper()
	addr := runtime.Address()
	err := runtime.ApplyFunding(testContractOutput("deploy", 1, addr, 20, testAsset("ordx:f:test", 100)), "")
	require.NoError(t, err)
}

func fundAMMRuntimeWithAsset(t *testing.T, runtime *ContractRuntime, assetName string, amount int64, value int64) {
	t.Helper()
	addr := runtime.Address()
	err := runtime.ApplyFunding(testContractOutput("deploy", 1, addr, value, testAsset(assetName, amount)), "")
	require.NoError(t, err)
}

func testAMMRuntime(t *testing.T) *ContractRuntime {
	t.Helper()
	contract := NewAMMContract("ordx:f:test", "100", 20, "2000")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateAMM,
		Version:         CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, content, "deployer-address", deploy.DeployNonce)
	require.NoError(t, err)
	runtime, err := NewRuntimeWithDeployer(addr, deploy, nil, "deployer-address")
	require.NoError(t, err)
	return runtime
}

func testAMMRuntimeWithAsset(t *testing.T, assetName string, amount int64, value int64, k string) *ContractRuntime {
	t.Helper()
	contract := NewAMMContract(assetName, scommon.NewDefaultDecimal(amount).String(), value, k)
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateAMM,
		Version:         CurrentTemplateVersion,
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, content, "deployer-address", deploy.DeployNonce)
	require.NoError(t, err)
	runtime, err := NewRuntimeWithDeployer(addr, deploy, nil, "deployer-address")
	require.NoError(t, err)
	return runtime
}
