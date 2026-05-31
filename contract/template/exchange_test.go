package template

import (
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestExchangeDefaultFundAndBuy(t *testing.T) {
	contract := testExchangeContract()
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasConfig := exchangeTestGasConfig(DefaultGasConfig().GasAssetName)
	fundTx := testTemplateDefaultInvokeTx(t, addr, 0, testAsset(contract.AssetAName, 100))
	buyTx := testTemplateDefaultInvokeTx(t, addr, 0, testAsset(contract.AssetBName, 24))

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs:       []*wire.MsgTx{deployTx, fundTx, buyTx},
		Store:     store,
		GasConfig: gasConfig,
		ResolveInvoker: func(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
			if tx == buyTx {
				return "buyer-address", nil
			}
			return "funder-address", nil
		},
		BlockHeight: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 3)
	require.Len(t, result.SettlementPlans, 1)
	require.ElementsMatch(t, []int64{0, 1}, result.SettlementPlans[0].ItemIDs)
	require.Len(t, result.SettlementPlans[0].Transfers, 2)
	require.Equal(t, "buyer-address", result.SettlementPlans[0].Transfers[0].To)
	require.Equal(t, contract.AssetAName, result.SettlementPlans[0].Transfers[0].AssetName)
	require.Equal(t, "11.88", result.SettlementPlans[0].Transfers[0].AssetAmt)
	require.Equal(t, "deployer-address", result.SettlementPlans[0].Transfers[1].To)
	require.Equal(t, contract.AssetBName, result.SettlementPlans[0].Transfers[1].AssetName)
	require.Equal(t, "23.76", result.SettlementPlans[0].Transfers[1].AssetAmt)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "88.12", state.Running.AssetAInPool)
	requireDecimalString(t, "0.24", state.Running.AssetBInPool)
	requireDecimalString(t, "11.88", state.Running.TotalDealAssetA)
	require.Empty(t, state.Running.GasBalance)

	provider := ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, fundTx, buyTx}, TestnetContractPrefix)
	plans, err := AugmentResultPlans(result.ResultPlans, store, gasConfig, provider)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanAsset(t, plans[0], contract.AssetAName, "88.12")
	requireResultPlanAsset(t, plans[0], contract.AssetBName, "0.24")
}

func TestExchangeDefaultInvokeRetainsAssetB(t *testing.T) {
	gas := DefaultGasConfig().GasAssetName
	contract := NewExchangeContract("ordx:f:asset_a", gas, ExchangePriceModeHeight, []ExchangePriceStep{{
		Threshold: "0",
		BPerA:     "2",
	}})
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasConfig := exchangeTestGasConfig(gas)
	fundTx := testTemplateDefaultInvokeTx(t, addr, 0, testAsset(contract.AssetAName, 100))
	buyTx := testTemplateDefaultInvokeTx(t, addr, 0, testAsset(gas, 21))

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs:       []*wire.MsgTx{deployTx, fundTx, buyTx},
		Store:     store,
		GasConfig: gasConfig,
		ResolveInvoker: func(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
			if tx == buyTx {
				return "buyer-address", nil
			}
			return "funder-address", nil
		},
		BlockHeight: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Transfers, 2)
	require.Equal(t, "10", result.SettlementPlans[0].Transfers[0].AssetAmt)
	require.Equal(t, "20", result.SettlementPlans[0].Transfers[1].AssetAmt)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "90", state.Running.AssetAInPool)
	require.Empty(t, state.Running.AssetBInPool)
	requireDecimalString(t, "10", state.Running.TotalDealAssetA)
}

func TestExchangeDefaultInvokeRetainsAssetA(t *testing.T) {
	gas := DefaultGasConfig().GasAssetName
	contract := NewExchangeContract(gas, "ordx:f:asset_b", ExchangePriceModeHeight, []ExchangePriceStep{{
		Threshold: "0",
		BPerA:     "2",
	}})
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasConfig := exchangeTestGasConfig(gas)
	fundTx := testTemplateDefaultInvokeTx(t, addr, 0, testAsset(gas, 101))
	buyTx := testTemplateDefaultInvokeTx(t, addr, 0, testAsset(contract.AssetBName, 20))

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs:       []*wire.MsgTx{deployTx, fundTx, buyTx},
		Store:     store,
		GasConfig: gasConfig,
		ResolveInvoker: func(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
			if tx == buyTx {
				return "buyer-address", nil
			}
			return "funder-address", nil
		},
		BlockHeight: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Transfers, 2)
	require.Equal(t, "9.9", result.SettlementPlans[0].Transfers[0].AssetAmt)
	require.Equal(t, "19.8", result.SettlementPlans[0].Transfers[1].AssetAmt)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "90.1", state.Running.AssetAInPool)
	requireDecimalString(t, "0.2", state.Running.AssetBInPool)
	requireDecimalString(t, "9.9", state.Running.TotalDealAssetA)
}

func TestExchangeSoldAmountPriceTiersWithinSingleInvoke(t *testing.T) {
	contract := NewExchangeContract("ordx:f:asset_a", "ordx:f:asset_b", ExchangePriceModeSoldA, []ExchangePriceStep{{
		Threshold: "0",
		BPerA:     "2",
	}, {
		Threshold: "10",
		BPerA:     "3",
	}})
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	gasConfig := exchangeTestGasConfig(gas)
	fundTx := testTemplateDefaultInvokeTx(t, addr, 0, testAssets(gas, 2, contract.AssetAName, 100))
	buyTx := testTemplateDefaultInvokeTx(t, addr, 0, testAssets(gas, 2, contract.AssetBName, 50))

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs:       []*wire.MsgTx{deployTx, fundTx, buyTx},
		Store:     store,
		GasConfig: gasConfig,
		ResolveInvoker: func(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
			if tx == buyTx {
				return "buyer-address", nil
			}
			return "funder-address", nil
		},
		BlockHeight: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Transfers, 2)
	require.Equal(t, contract.AssetAName, result.SettlementPlans[0].Transfers[0].AssetName)
	require.Equal(t, "20", result.SettlementPlans[0].Transfers[0].AssetAmt)
	require.Equal(t, contract.AssetBName, result.SettlementPlans[0].Transfers[1].AssetName)
	require.Equal(t, "50", result.SettlementPlans[0].Transfers[1].AssetAmt)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "80", state.Running.AssetAInPool)
	requireDecimalString(t, "20", state.Running.TotalDealAssetA)
	requireDecimalString(t, "50", state.Running.TotalDealAssetB)
}

func TestExchangeCloseReturnsRemainingAssetAToDeployer(t *testing.T) {
	contract := testExchangeContract()
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	gasConfig := exchangeTestGasConfig(gas)
	fundTx := testTemplateDefaultInvokeTx(t, addr, 0, testAssets(gas, 5, contract.AssetAName, 100))
	closeTx := testExchangeCloseTx(t, addr, testAsset(gas, 1))

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs:       []*wire.MsgTx{deployTx, fundTx, closeTx},
		Store:     store,
		GasConfig: gasConfig,
		ResolveInvoker: func(tx *wire.MsgTx, parsed ParsedTx) (string, error) {
			return "deployer-address", nil
		},
		BlockHeight: 10,
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Transfers, 1)
	require.Equal(t, "deployer-address", result.SettlementPlans[0].Transfers[0].To)
	require.Equal(t, "100", result.SettlementPlans[0].Transfers[0].AssetAmt)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.Closed)
	require.Empty(t, state.Running.AssetAInPool)

	provider := ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, fundTx, closeTx}, TestnetContractPrefix)
	plans, err := AugmentResultPlans(result.ResultPlans, store, gasConfig, provider)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanAssetTo(t, plans[0], "deployer-address", contract.AssetAName, "100")
	requireResultPlanAssetTo(t, plans[0], "deployer-address", gas, "4")
	requireNoResultPlanOutputTo(t, plans[0], addr.MustEncode())
}

func testExchangeContract() *ExchangeContract {
	return NewExchangeContract("ordx:f:asset_a", "ordx:f:asset_b", ExchangePriceModeHeight, []ExchangePriceStep{{
		Threshold: "0",
		BPerA:     "2",
	}})
}

func exchangeTestGasConfig(gasAssetName string) GasConfig {
	return GasConfig{
		GasAssetName:    gasAssetName,
		DeployBaseGas:   1,
		InvokeBaseGas:   1,
		ResultBaseGas:   1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}
}

func testExchangeCloseTx(t *testing.T, contract ContractAddress, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	param, err := (&CloseInvokeParam{}).Encode()
	require.NoError(t, err)
	invokeScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPIClose,
		Param:     param,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(0, assets, testTemplateContractScript(contract)))
	return tx
}

func requireResultPlanAssetTo(t *testing.T, plan ResultPlan, to, assetName, amount string) {
	t.Helper()
	name := wire.NewAssetNameFromString(assetName)
	require.NotNil(t, name)
	for _, output := range plan.Outputs {
		if output.To != to {
			continue
		}
		asset, err := output.Assets.Find(name)
		if err == nil && asset.Amount.String() == amount {
			return
		}
	}
	t.Fatalf("result plan missing asset %s amount %s to %s: %+v", assetName, amount, to, plan.Outputs)
}

func requireNoResultPlanOutputTo(t *testing.T, plan ResultPlan, to string) {
	t.Helper()
	for _, output := range plan.Outputs {
		require.NotEqual(t, to, output.To, "unexpected result output to %s: %+v", to, output)
	}
}
