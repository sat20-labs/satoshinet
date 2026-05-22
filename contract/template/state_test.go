package template

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func testAsset(assetName string, amount int64) wire.TxAssets {
	return wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(assetName),
		Amount: *scommon.NewDefaultDecimal(amount),
	}}
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
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("tx", 1, contract),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "tx", Vout: 1},
			Vout:     1,
			Contract: contract,
			Value:    30,
		}},
		Height:    100,
		Timestamp: 200,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), item.ID)
	require.Equal(t, OrderTypeBuy, item.OrderType)
	require.Equal(t, "10", item.ExpectedAmt)
	require.Equal(t, int64(20), item.RemainingValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, int64(1), state.NextItemID)
	require.Equal(t, uint64(1), state.InvokeCount)
	require.Len(t, state.Items, 1)
	require.Equal(t, int64(30), state.Running.TotalInputGas)
}

func TestApplyInvokeMarksLimitOrderBuyInvalidWhenFundingIsOutsideTolerance(t *testing.T) {
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
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("tx", 1, contract),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "tx", Vout: 1},
			Vout:     1,
			Contract: contract,
			Value:    200,
		}},
		Height:    100,
		Timestamp: 200,
	})
	require.NoError(t, err)
	require.Equal(t, InvokeReasonInvalid, item.Reason)
	require.Zero(t, item.RemainingValue)
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
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("tx", 1, contract),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "tx", Vout: 1},
			Vout:     1,
			Contract: contract,
			Value:    SwapInvokeFee,
			Assets:   testAsset("ordx:f:test", 9),
		}},
		Height:    100,
		Timestamp: 200,
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
		Action: InvokeAPIAddLiquidity,
		Param:  param,
		CallID: DeriveInvokeCallID("tx", 1, contract),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "tx", Vout: 1},
			Vout:     1,
			Contract: contract,
			Value:    25,
			Assets:   testAsset("ordx:f:test", 10),
		}},
		Height:    100,
		Timestamp: 200,
	})
	require.NoError(t, err)
	require.Equal(t, OrderTypeAddLiquidity, item.OrderType)
	require.Equal(t, "10", item.InAmt)
	require.Equal(t, int64(20), item.RemainingValue)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Running.AssetAmtInPool)
	require.Zero(t, state.Running.SatValueInPool)
	require.Equal(t, "10", state.Running.TotalInputAsset)
	require.Equal(t, int64(25), state.Running.TotalInputGas)
}

func TestApplyInvokeRecordsAMMBuyWithInvokeFeeOnly(t *testing.T) {
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
		Action: InvokeAPISwap,
		Param:  param,
		CallID: DeriveInvokeCallID("tx", 1, contract),
		FundingOutputs: []ContractOutput{{
			OutPoint: OutPoint{TxID: "tx", Vout: 1},
			Vout:     1,
			Contract: contract,
			Value:    20,
		}},
		Height:    100,
		Timestamp: 200,
	})
	require.NoError(t, err)
	require.Equal(t, OrderTypeBuy, item.OrderType)
	require.Equal(t, int64(SwapInvokeFee), item.ServiceFee)
	require.Equal(t, int64(10), item.RemainingValue)
	require.Empty(t, item.ExpectedAmt)
}

func TestApplyFundingTracksTemplateGasSeparately(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	err := runtime.ApplyFunding([]ContractOutput{{
		OutPoint: OutPoint{TxID: "fund", Vout: 1},
		Contract: addr,
		Assets:   testAsset("ordx:f:gas", 50),
	}}, "ordx:f:gas")
	require.NoError(t, err)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, "50", state.Running.GasBalance)
	require.Empty(t, state.Running.AssetAmtInPool)
	require.Zero(t, state.Running.SatValueInPool)
}

func TestApplyGasFundingDoesNotChangeAMMPool(t *testing.T) {
	runtime := testAMMRuntime(t)
	fundAMMRuntime(t, runtime)
	addr := runtime.Address()

	err := runtime.ApplyGasFunding([]ContractOutput{{
		OutPoint: OutPoint{TxID: "invoke", Vout: 1},
		Contract: addr,
		Value:    10,
		Assets:   testAsset("ordx:f:test", 5),
	}}, "ordx:f:gas")
	require.NoError(t, err)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, "100", state.Running.AssetAmtInPool)
	require.Equal(t, int64(20), state.Running.SatValueInPool)
	require.Empty(t, state.Running.GasBalance)
	require.True(t, state.Running.TradingReady)
}

func testLimitOrderRuntime(t *testing.T) *ContractRuntime {
	t.Helper()
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		TemplateName:    TemplateLimitOrder,
		TemplateVersion: CurrentTemplateVersion,
		Deployer:        "deployer-address",
		Random:          []byte("random"),
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, content, deploy.Deployer, deploy.Random)
	require.NoError(t, err)
	runtime, err := NewRuntime(addr, deploy, nil)
	require.NoError(t, err)
	return runtime
}

func fundAMMRuntime(t *testing.T, runtime *ContractRuntime) {
	t.Helper()
	addr := runtime.Address()
	err := runtime.ApplyFunding([]ContractOutput{{
		OutPoint: OutPoint{TxID: "deploy", Vout: 1},
		Contract: addr,
		Value:    20,
		Assets:   testAsset("ordx:f:test", 100),
	}}, "")
	require.NoError(t, err)
}

func testAMMRuntime(t *testing.T) *ContractRuntime {
	t.Helper()
	contract := NewAMMContract("ordx:f:test", "100", 20, "2000")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        1000,
		TemplateName:    TemplateAMM,
		TemplateVersion: CurrentTemplateVersion,
		Deployer:        "deployer-address",
		Random:          []byte("random"),
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, content, deploy.Deployer, deploy.Random)
	require.NoError(t, err)
	runtime, err := NewRuntime(addr, deploy, nil)
	require.NoError(t, err)
	return runtime
}
