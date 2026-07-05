package template

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func testTemplateExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	if req.ResolveInvoker == nil {
		req.ResolveInvoker = testTemplateInvokerResolver
	}
	return ExecuteBlock(req)
}

func testTemplateInvokerResolver(tx *wire.MsgTx, contractTx Tx) (string, error) {
	if contractTx.Kind == TxTypeDeploy {
		return "deployer-address", nil
	}
	return "invoker-address", nil
}

type testBaseGasLimitOrderContract struct {
	LimitOrderContract
	baseGas contractframework.BaseGasConfig
}

func newTestBaseGasLimitOrderContract(assetName string, baseGas contractframework.BaseGasConfig) *testBaseGasLimitOrderContract {
	return &testBaseGasLimitOrderContract{
		LimitOrderContract: *NewLimitOrderContract(assetName),
		baseGas:            baseGas,
	}
}

func (c *testBaseGasLimitOrderContract) BaseGasConfig() contractframework.BaseGasConfig {
	return c.baseGas
}

func testTemplateRegistryWithBaseGas(baseGas contractframework.BaseGasConfig) *Registry {
	registry := NewRegistry()
	mustRegister(registry, TemplateLimitOrder, func() Contract {
		return newTestBaseGasLimitOrderContract("", baseGas)
	})
	return registry
}

func TestBackendDeployThenInvoke(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	invokeTx := testTemplateLimitOrderInvokeTx(t, addr, OrderTypeBuy)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, invokeTx},
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.Equal(t, TxTypeInvoke, result.Records[1].Type)
	require.True(t, addr.Equal(result.Records[0].Contract))
	require.True(t, addr.Equal(result.Records[1].Contract))
	require.NotEqual(t, [32]byte{}, result.StateRoot)
}

func TestBackendDeployGasResult(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	deployTx.TxOut[1].Assets = testAsset(gas, 5)
	gasConfig := GasConfig{
		GasAssetName:    gas,
		DeployBaseGas:   1,
		InvokeBaseGas:   1,
		ResultBaseGas:   1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx},
		GasConfig:   gasConfig,
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.True(t, addr.Equal(result.Records[0].Contract))
	require.True(t, result.Records[0].RequiresResult)
	require.Len(t, result.ResultPlans, 1)
	require.Equal(t, addr.MustEncode(), result.ResultPlans[0].Contract)
	require.Empty(t, result.ResultPlans[0].ItemIDs)
	require.Len(t, result.ResultPlans[0].Inputs, 1)
	require.Equal(t, "0.001", result.ResultPlans[0].GasFee.String())
}

func TestBackendUsesTemplateDeployBaseGas(t *testing.T) {
	baseGas := contractframework.BaseGasConfig{DeployBaseGas: 1}
	contract := newTestBaseGasLimitOrderContract("ordx:f:test", baseGas)
	deployTx, addr := testTemplateDeployTxWithGasLimit(t, contract, 7, 1)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:      []*wire.MsgTx{deployTx},
		Registry: testTemplateRegistryWithBaseGas(baseGas),
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.True(t, addr.Equal(result.Records[0].Contract))
	require.EqualValues(t, 1, result.Records[0].GasLimit)
}

func TestBackendUsesTemplateInvokeBaseGas(t *testing.T) {
	baseGas := contractframework.BaseGasConfig{InvokeBaseGas: DefaultGasConfig().InvokeBaseGas + 1}
	contract := newTestBaseGasLimitOrderContract("ordx:f:test", baseGas)
	deployTx, addr := testTemplateDeployTx(t, contract)
	invokeTx := testTemplateLimitOrderInvokeTx(t, addr, OrderTypeBuy)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:      []*wire.MsgTx{deployTx, invokeTx},
		Registry: testTemplateRegistryWithBaseGas(baseGas),
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
}

func TestDeployRequiresResult(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx},
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.True(t, addr.Equal(result.Records[0].Contract))
	require.True(t, result.Records[0].RequiresResult)
	require.Zero(t, result.Records[0].GasFee.Sign())
	require.Len(t, result.ResultPlans, 1)
	require.Equal(t, addr.MustEncode(), result.ResultPlans[0].Contract)
	require.Empty(t, result.ResultPlans[0].ItemIDs)
	require.Len(t, result.ResultPlans[0].Inputs, 1)
	require.Zero(t, result.ResultPlans[0].GasFee.Sign())
}

func TestBackendSettlesLimitOrdersOnFinalize(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, 0, testAssets(gasAssetName, 50, "ordx:f:test", 10))
	buyTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeBuy, 20, testAsset(gasAssetName, 50))

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, sellTx, buyTx},
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.ResultPlans, 1)
	require.Len(t, result.SettlementPlans[0].Deals, 1)
	require.Equal(t, "10", result.SettlementPlans[0].Deals[0].AssetAmt)
	require.Equal(t, int64(20), result.SettlementPlans[0].Deals[0].SatValue)
	require.True(t, templateRecordsHaveAssetIntents(result.Records))
	require.ElementsMatch(t, []int64{0, 1}, result.SettlementPlans[0].ItemIDs)
	require.ElementsMatch(t, result.SettlementPlans[0].ItemIDs, result.ResultPlans[0].ItemIDs)
	require.Len(t, result.ResultPlans[0].Inputs, 3)
	require.NotEqual(t, [32]byte{}, result.StateRoot)
}

func TestBackendBuyExcessRefund(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:     gas,
		BootstrapAddress: "bootstrap-address",
		DeployBaseGas:    1,
		InvokeBaseGas:    1,
		ResultBaseGas:    1,
		MaxGasPerInvoke:  DefaultGasConfig().MaxGasPerInvoke,
	}
	buyTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeBuy, 30, testAsset(gas, 50))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, buyTx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.ResultPlans, 1)
	require.Len(t, result.SettlementPlans[0].Transfers, 1)
	require.Equal(t, "invoker-address", result.SettlementPlans[0].Transfers[0].To)
	require.Equal(t, int64(10), result.SettlementPlans[0].Transfers[0].SatValue)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.Items, 1)
	require.Equal(t, int64(20), state.Items[0].RemainingValue)
	require.Zero(t, state.Items[0].OutValue)
	requireDecimalString(t, "20", state.Running.AssetBInPool)

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, buyTx}, TestnetContractPrefix, ContractTypeTemplate)
	plans, err := AugmentResultPlans(result.ResultPlans, store, gasConfig, provider, nil)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Contains(t, plans[0].Inputs, OutPoint{TxID: buyTx.TxID(), Vout: 1})
	requireResultPlanValueTo(t, plans[0], "invoker-address", 10)
	requireResultPlanValueTo(t, plans[0], addr.MustEncode(), 20)
	requireResultPlanAssetTo(t, plans[0], "invoker-address", gas, "49.999")
	requireNoResultPlanAssetTo(t, plans[0], addr.MustEncode(), gas)
	requireNoResultPlanOutputTo(t, plans[0], "bootstrap-address")
}

func TestBackendSellExtraSatsRefund(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:     gas,
		BootstrapAddress: "bootstrap-address",
		DeployBaseGas:    1,
		InvokeBaseGas:    1,
		ResultBaseGas:    1,
		MaxGasPerInvoke:  DefaultGasConfig().MaxGasPerInvoke,
	}
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, 10,
		testAssets(gas, 5, contract.AssetName, 10))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, sellTx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.ResultPlans, 1)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.Items, 1)
	require.Equal(t, InvokeReasonInvalid, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	require.Empty(t, state.Running.AssetAInPool)
	require.Zero(t, state.Running.AssetBInPool)
	requireDecimalString(t, "0", state.Running.GasBalance)

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, sellTx}, TestnetContractPrefix, ContractTypeTemplate)
	plans, err := AugmentResultPlans(result.ResultPlans, store, gasConfig, provider, nil)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanValueTo(t, plans[0], "invoker-address", 10)
	requireResultPlanAssetTo(t, plans[0], "invoker-address", contract.AssetName, "10")
	requireResultPlanAssetTo(t, plans[0], "invoker-address", gas, "4.999")
	requireNoResultPlanOutputTo(t, plans[0], "bootstrap-address")
}

func TestBackendMarksInvokeInvalidWhenDeclaredSellAssetMissing(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, 0, testAsset(gasAssetName, 50))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, sellTx},
		Store:       store,
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.Items, 1)
	require.Equal(t, InvokeReasonInvalid, state.Items[0].Reason)
}

func TestBackendSettlesLimitOrdersAcrossStoreReload(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, 0, testAssets(gasAssetName, 50, "ordx:f:test", 10))
	parsedSell, err := ParseTx(sellTx, testTemplateContractResolver)
	require.NoError(t, err)
	parsedSellGas, err := parsedSell.ContractOutputs[0].AssetAmount(gasAssetName)
	require.NoError(t, err)
	require.Equal(t, "50", parsedSellGas.String())

	store := NewRuntimeStore()
	gasConfig := GasConfig{
		GasAssetName:    DefaultGasConfig().GasAssetName,
		DeployBaseGas:   1,
		InvokeBaseGas:   1,
		ResultBaseGas:   1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}
	first, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, sellTx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, first.ResultPlans, 1)
	firstProvider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, sellTx}, TestnetContractPrefix, ContractTypeTemplate)
	firstPlans, err := AugmentResultPlans(first.ResultPlans, store, gasConfig, firstProvider, nil)
	require.NoError(t, err)
	requireResultPlanAssetTo(t, firstPlans[0], "invoker-address", gasAssetName, "49.999")
	requireResultPlanAsset(t, firstPlans[0], contract.AssetName, "10")
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "0", state.Running.GasBalance)

	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	store, err = DecodeRuntimeStore(encoded, nil)
	require.NoError(t, err)
	runtime, ok = store.Get(addr)
	require.True(t, ok)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "0", state.Running.GasBalance)

	buyTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeBuy, 20, testAsset(gasAssetName, 50))
	parsedBuy, err := ParseTx(buyTx, testTemplateContractResolver)
	require.NoError(t, err)
	require.Len(t, parsedBuy.ContractOutputs, 1)
	parsedBuyGas, err := parsedBuy.ContractOutputs[0].AssetAmount(gasAssetName)
	require.NoError(t, err)
	require.Equal(t, "50", parsedBuyGas.String())
	executor := NewBackend(BlockExecutionRequest{
		Store:          store,
		GasConfig:      gasConfig,
		BlockHeight:    101,
		ResolveInvoker: testTemplateInvokerResolver,
	})
	require.NoError(t, executor.ExecuteTx(buyTx))
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "0", state.Running.GasBalance)

	second, err := executor.Finalize()
	require.NoError(t, err)
	require.Len(t, second.SettlementPlans, 1)
	require.Len(t, second.ResultPlans, 1)
	require.Len(t, second.SettlementPlans[0].Deals, 1)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "0", state.Running.GasBalance)
	resultPlans, err := AugmentResultPlans(second.ResultPlans, store, gasConfig, nil, nil)
	require.NoError(t, err)
	requireNoResultPlanAssetTo(t, resultPlans[0], addr.MustEncode(), gasAssetName)
	currentOnly := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{sellTx, buyTx}, TestnetContractPrefix, ContractTypeTemplate)
	resultPlans, err = AugmentResultPlans(second.ResultPlans, store, gasConfig, currentOnly, nil)
	require.NoError(t, err)
	requireResultPlanAssetTo(t, resultPlans[0], "invoker-address", gasAssetName, "49.999")
}

func TestBackendInvalidInvokeAbsorbsKnownFunding(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:    gas,
		DeployBaseGas:   1,
		InvokeBaseGas:   1,
		ResultBaseGas:   1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}
	invokeTx := testTemplateInvalidInvokeTx(t, addr, 7, testAssets(gas, 5, contract.AssetName, 10))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, invokeTx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
	require.Len(t, result.SettlementPlans, 1)
	require.ElementsMatch(t, []int64{0}, result.SettlementPlans[0].ItemIDs)
	require.Len(t, result.ResultPlans, 1)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Running.AssetAInPool)
	require.Zero(t, state.Running.AssetBInPool)
	requireDecimalString(t, "0", state.Running.GasBalance)

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, invokeTx}, TestnetContractPrefix, ContractTypeTemplate)
	plans, err := AugmentResultPlans(result.ResultPlans, store, gasConfig, provider, nil)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanValueTo(t, plans[0], "invoker-address", 7)
	requireResultPlanAssetTo(t, plans[0], "invoker-address", contract.AssetName, "10")
	requireResultPlanAssetTo(t, plans[0], "invoker-address", gas, "4.999")
}

func TestBackendLimitOrderCloseRefundsOwnersAndSplitsProfit(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:     gas,
		BootstrapAddress: "bootstrap-address",
		DeployBaseGas:    1,
		InvokeBaseGas:    1,
		ResultBaseGas:    1,
		MaxGasPerInvoke:  DefaultGasConfig().MaxGasPerInvoke,
	}
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, 0,
		testAssets(gas, 5, contract.AssetName, 10))
	closeTx := testExchangeCloseTx(t, addr, testAsset(gas, 2))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:       []*wire.MsgTx{deployTx, sellTx, closeTx},
		Store:     store,
		GasConfig: gasConfig,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			if contractTx.Kind == TxTypeDeploy {
				return "deployer-address", nil
			}
			if tx == closeTx {
				return "deployer-address", nil
			}
			return "seller-address", nil
		},
		BlockHeight: 100,
	})
	require.NoError(t, err)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.Closed)

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, sellTx, closeTx}, TestnetContractPrefix, ContractTypeTemplate)
	plans, err := AugmentResultPlans(result.ResultPlans, store, gasConfig, provider, nil)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanAssetTo(t, plans[0], "seller-address", contract.AssetName, "10")
	requireResultPlanAssetTo(t, plans[0], "seller-address", gas, "4.999")
	requireResultPlanAssetTo(t, plans[0], "deployer-address", gas, "1.999")
	requireNoResultPlanOutputTo(t, plans[0], addr.MustEncode())
}

func TestAMMCloseProfit(t *testing.T) {
	contract := NewAMMContract("ordx:f:test", "100", 20, "2000")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:     gas,
		BootstrapAddress: "bootstrap-address",
		DeployBaseGas:    1,
		InvokeBaseGas:    1,
		ResultBaseGas:    1,
		MaxGasPerInvoke:  DefaultGasConfig().MaxGasPerInvoke,
	}
	deployTx.TxOut[1].Value = 20
	deployTx.TxOut[1].Assets = testAssets(gas, 5, contract.AssetName, 100)
	addTx := testTemplateAMMAddLiquidityTx(t, addr, contract.AssetName, "100", 20,
		testAssets(gas, 5, contract.AssetName, 100))
	closeTx := testExchangeCloseTx(t, addr, testAsset(gas, 2))
	profitTx := testTemplateContractDepositTx(t, addr, 10, testAsset("ordx:f:profit", 100))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:       []*wire.MsgTx{deployTx, addTx, closeTx},
		Store:     store,
		GasConfig: gasConfig,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			if contractTx.Kind == TxTypeDeploy {
				return "deployer-address", nil
			}
			if tx == closeTx {
				return "deployer-address", nil
			}
			return "lp-address", nil
		},
		BlockHeight: 100,
	})
	require.NoError(t, err)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.Closed)
	require.Empty(t, state.Running.AssetAInPool)
	require.Empty(t, state.Running.AssetBInPool)
	require.Empty(t, state.Running.LPBalances)

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, addTx, closeTx, profitTx}, TestnetContractPrefix, ContractTypeTemplate)
	plans, err := AugmentResultPlans(result.ResultPlans, store, gasConfig, provider, nil)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanAssetTo(t, plans[0], "deployer-address", contract.AssetName, "100")
	requireResultPlanValueTo(t, plans[0], "deployer-address", 20)
	requireResultPlanAssetTo(t, plans[0], "lp-address", contract.AssetName, "100")
	requireResultPlanValueTo(t, plans[0], "lp-address", 20)
	requireResultPlanAssetTo(t, plans[0], "lp-address", gas, "4.999")
	requireResultPlanAssetTo(t, plans[0], "deployer-address", gas, "6.998")
	requireResultPlanValueTo(t, plans[0], "deployer-address", 6)
	requireResultPlanValueTo(t, plans[0], "bootstrap-address", 4)
	requireResultPlanAssetTo(t, plans[0], "deployer-address", "ordx:f:profit", "60")
	requireResultPlanAssetTo(t, plans[0], "bootstrap-address", "ordx:f:profit", "40")
	requireNoResultPlanOutputTo(t, plans[0], addr.MustEncode())
}

func TestBackendDefaultInvokeLimitOrderNoPriceNoOp(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	defaultTx := testTemplateDefaultInvokeTx(t, addr, 20, testAsset(DefaultGasConfig().GasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))

	store := NewRuntimeStore()
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, defaultTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Items)
	require.Zero(t, state.InvokeCount)
}

func TestBackendDefaultInvokeLimitOrderBuyAtMarketPrice(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, 0, testAssets(gasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas), "ordx:f:test", 10))
	defaultBuyTx := testTemplateDefaultInvokeTx(t, addr, 20, testAsset(gasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))

	store := NewRuntimeStore()
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, sellTx, defaultBuyTx}, Store: store, BlockHeight: 100})
	require.NoError(t, err)
	require.Len(t, result.Records, 3)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Deals, 1)
	require.Equal(t, "10", result.SettlementPlans[0].Deals[0].AssetAmt)
	require.Equal(t, int64(20), result.SettlementPlans[0].Deals[0].SatValue)
	require.Equal(t, "2", result.SettlementPlans[0].Deals[0].UnitPrice)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.Items, 2)
	require.Equal(t, contractcommon.ContractInvokeAPIDefault, state.Items[1].Action)
	require.Equal(t, OrderTypeBuy, state.Items[1].OrderType)
}

func TestBackendLimitOrderDefaultGas(t *testing.T) {
	assetName := "brc20:f:ooxx"
	contract := NewLimitOrderContract(assetName)
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	buyTx := testTemplateLimitOrderInvokeTxFor(t, addr, OrderTypeBuy, assetName, "1", "1", 1,
		testAsset(gasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))
	sellTx := testTemplateDefaultInvokeTx(t, addr, 0,
		testAssets(gasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas), assetName, 1))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, buyTx, sellTx},
		Store:       store,
		BlockHeight: 100,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			switch tx {
			case deployTx:
				return "deployer-address", nil
			case buyTx:
				return "buyer-address", nil
			case sellTx:
				return "seller-address", nil
			default:
				return "unknown-address", nil
			}
		},
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Deals, 1)
	require.Equal(t, "1", result.SettlementPlans[0].Deals[0].AssetAmt)
	require.Equal(t, int64(1), result.SettlementPlans[0].Deals[0].SatValue)

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{buyTx, sellTx}, TestnetContractPrefix, ContractTypeTemplate)
	plans, err := AugmentResultPlans(result.ResultPlans, store, DefaultGasConfig(), provider, func(name string) (int, bool) {
		return 0, name == assetName
	})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanValueTo(t, plans[0], "seller-address", 1)
	requireResultPlanAssetTo(t, plans[0], "buyer-address", assetName, "1")
	requireNoResultPlanOutputTo(t, plans[0], "bootstrap-address")
	requireNoResultPlanAssetTo(t, plans[0], addr.MustEncode(), assetName)
}

func TestBackendAMMPrecisionKeepsRemainder(t *testing.T) {
	assetName := "brc20:f:ooxx"
	contract := NewAMMContract(assetName, "100", 20, "2000")
	deployTx, addr := testTemplateDeployTx(t, contract)
	deployTx.TxOut[1].Value = 20
	deployTx.TxOut[1].Assets = testAsset(assetName, 100)
	gasAssetName := DefaultGasConfig().GasAssetName
	buyTx := testTemplateDefaultInvokeTx(t, addr, 1,
		testAsset(gasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, buyTx},
		Store:       store,
		BlockHeight: 100,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			switch tx {
			case deployTx:
				return "deployer-address", nil
			case buyTx:
				return "buyer-address", nil
			default:
				return "unknown-address", nil
			}
		},
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Deals, 1)

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, buyTx}, TestnetContractPrefix, ContractTypeTemplate)
	plans, err := AugmentResultPlans(result.ResultPlans, store, DefaultGasConfig(), provider, func(name string) (int, bool) {
		return 0, name == assetName
	})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanAssetTo(t, plans[0], "buyer-address", assetName, "4")
	requireResultPlanAssetTo(t, plans[0], addr.MustEncode(), assetName, "96")
	requireNoResultPlanOutputTo(t, plans[0], "bootstrap-address")
}

func TestBackendAMMRefundsExtraResultGas(t *testing.T) {
	assetName := "brc20:f:ooxx"
	contract := NewAMMContract(assetName, "100", 20, "2000")
	deployTx, addr := testTemplateDeployTx(t, contract)
	deployTx.TxOut[1].Value = 20
	deployTx.TxOut[1].Assets = testAsset(assetName, 100)
	gas := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:    gas,
		DeployBaseGas:   1,
		InvokeBaseGas:   1,
		ResultBaseGas:   1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}
	buyTx := testTemplateDefaultInvokeTx(t, addr, 1, testAsset(gas, 5))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, buyTx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			if tx == buyTx {
				return "buyer-address", nil
			}
			return "deployer-address", nil
		},
	})
	require.NoError(t, err)
	require.Len(t, result.ResultPlans, 1)

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{deployTx, buyTx}, TestnetContractPrefix, ContractTypeTemplate)
	plans, err := AugmentResultPlans(result.ResultPlans, store, gasConfig, provider, func(name string) (int, bool) {
		return 0, name == assetName
	})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanAssetTo(t, plans[0], "buyer-address", assetName, "4")
	requireResultPlanAssetTo(t, plans[0], "buyer-address", gas, "4.999")
	requireNoResultPlanAssetTo(t, plans[0], addr.MustEncode(), gas)
}

func TestBackendAMMMultiInvokeOneResult(t *testing.T) {
	assetName := "brc20:f:ooxx"
	contract := NewAMMContract(assetName, "100", 100, "10000")
	deployTx, addr := testTemplateDeployTx(t, contract)
	deployTx.TxOut[1].Value = 100
	deployTx.TxOut[1].Assets = testAsset(assetName, 100)
	gas := DefaultGasConfig().GasAssetName
	fee := testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)
	buyA := testTemplateDefaultInvokeTx(t, addr, 10, testAsset(gas, fee))
	buyB := testTemplateDefaultInvokeTx(t, addr, 10, testAsset(gas, fee))
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, buyA, buyB},
		Store:       store,
		BlockHeight: 100,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			switch tx {
			case buyA:
				return "buyer-a", nil
			case buyB:
				return "buyer-b", nil
			default:
				return "deployer-address", nil
			}
		},
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Deals, 2)
	require.Equal(t, result.SettlementPlans[0].Deals[0].AssetAmt, result.SettlementPlans[0].Deals[1].AssetAmt)
	require.Equal(t, "9.0247452693", result.SettlementPlans[0].Deals[0].AssetAmt)
	require.Len(t, result.ResultPlans, 1)
	require.ElementsMatch(t, []int64{0, 1}, result.ResultPlans[0].ItemIDs)
	require.Contains(t, result.ResultPlans[0].Inputs, OutPoint{TxID: buyA.TxID(), Vout: 0})
	require.Contains(t, result.ResultPlans[0].Inputs, OutPoint{TxID: buyB.TxID(), Vout: 0})
	requireResultPlanAssetTo(t, result.ResultPlans[0], "buyer-a", assetName, "9.0247452693")
	requireResultPlanAssetTo(t, result.ResultPlans[0], "buyer-b", assetName, "9.0247452693")
}

func TestBackendAMMAddLiqRefundsExcess(t *testing.T) {
	assetName := "brc20:f:ooxx"
	contract := NewAMMContract(assetName, "10", 10, "100")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        DefaultGasConfig().DeployBaseGas,
		SubType:         contract.TemplateName(),
		Version:         contract.Version(),
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, deploy.ContractContent, "deployer-address", deploy.DeployNonce)
	require.NoError(t, err)
	gasAssetName := DefaultGasConfig().GasAssetName
	addParam, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: assetName,
		Amt:       "8",
		Value:     6,
	}).Encode()
	require.NoError(t, err)
	addScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPIAddLiquidity,
		Param:     addParam,
	})
	require.NoError(t, err)
	addTx := wire.NewMsgTx(1)
	addTx.AddTxIn(&wire.TxIn{})
	addTx.AddTxOut(wire.NewTxOut(0, nil, addScript))
	addTx.AddTxOut(wire.NewTxOut(6,
		testAssets(assetName, 8, gasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)),
		testTemplateContractScript(addr)))
	store := NewRuntimeStore()
	runtime, err := NewRuntimeWithDeployer(addr, deploy, nil, "deployer-address")
	require.NoError(t, err)
	fundAMMRuntimeWithAsset(t, runtime, assetName, 20, 20)
	store.Add(runtime)
	baseProvider := func(contractAddr ContractAddress) ([]contractframework.UTXO, error) {
		require.True(t, addr.Equal(contractAddr))
		return []contractframework.UTXO{testContractUTXO("previous-result", 0, addr, 20, testAsset(assetName, 20))}, nil
	}

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{addTx},
		Store:         store,
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(baseProvider, []*wire.MsgTx{addTx}, TestnetContractPrefix, ContractTypeTemplate),
		BlockHeight:   100,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			return "alice", nil
		},
		AssetPrecision: func(name string) (int, bool) {
			return 0, name == assetName
		},
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Transfers, 1)
	require.Equal(t, "2", result.SettlementPlans[0].Transfers[0].AssetAmt)

	plans, err := AugmentResultPlans(result.ResultPlans, store, DefaultGasConfig(),
		contractframework.ContractUTXOProviderWithTxOutputs(baseProvider, []*wire.MsgTx{addTx}, TestnetContractPrefix, ContractTypeTemplate),
		func(name string) (int, bool) {
			return 0, name == assetName
		})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	requireResultPlanAssetTo(t, plans[0], "alice", assetName, "2")
	requireResultPlanAssetTo(t, plans[0], addr.MustEncode(), assetName, "26")
	requireResultPlanValueTo(t, plans[0], addr.MustEncode(), 26)
	requireNoResultPlanOutputTo(t, plans[0], "bootstrap-address")
}

func TestContractUTXOProviderWithTxOutputsIncludesDefaultInvoke(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	_, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	defaultTx := testTemplateDefaultInvokeTx(t, addr, 20, testAsset(gasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))

	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{defaultTx}, TestnetContractPrefix, ContractTypeTemplate)
	utxos, err := provider(addr)
	require.NoError(t, err)
	require.Len(t, utxos, 1)
	require.Equal(t, OutPoint{TxID: defaultTx.TxID(), Vout: 0}, utxos[0].OutPoint)
	require.True(t, addr.Equal(utxos[0].Contract))
	require.Equal(t, int64(20), utxos[0].PhysicalValue())
	assets := utxos[0].TxAssets()
	asset, err := assets.Find(wire.NewAssetNameFromString(gasAssetName))
	require.NoError(t, err)
	require.Equal(t, testAsset(gasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas))[0].Amount.String(), asset.Amount.String())
}

func TestBackendIgnoresInvokeBeforeDeploy(t *testing.T) {
	contract := testTemplateContract(t)
	invokeTx := testTemplateLimitOrderInvokeTx(t, contract, OrderTypeBuy)

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{invokeTx}})
	require.NoError(t, err)
	require.Empty(t, result.Records)
	require.Empty(t, result.ResultPlans)
}

func TestBackendIgnoresInvalidDeploy(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        0,
		SubType:         contract.TemplateName(),
		Version:         contract.Version(),
		DeployNonce:     7,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, deploy.ContractContent, "deployer-address", deploy.DeployNonce)
	require.NoError(t, err)
	script, err := DeployNullDataScript(deploy)
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, nil, testTemplateContractScript(addr)))

	store := NewRuntimeStore()
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Store: store})
	require.NoError(t, err)
	require.Empty(t, result.Records)
	require.Empty(t, result.ResultPlans)
	require.False(t, store.Exists(addr))
}

func TestBackendDuplicateDeployDoesNotOverwriteRuntime(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	duplicateTx := deployTx.Copy()
	duplicateTx.LockTime = 1
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, duplicateTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
	require.True(t, store.Exists(addr))
}

func TestBackendNetworkExclusiveDeployRejectsSameConfig(t *testing.T) {
	contract := NewAutopayContract("recipient-address", "ordx:f:test", AutopayScheduleFixed, "10", "", 0)
	deployTx, addr := testTemplateDeployTxWithNonce(t, contract, 7)
	duplicateTx, duplicateAddr := testTemplateDeployTxWithNonce(t, contract, 8)
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, duplicateTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
	require.True(t, store.Exists(addr))
	require.False(t, store.Exists(duplicateAddr))
}

func TestBackendNetworkExclusiveDeployAllowsAfterClose(t *testing.T) {
	contract := NewAutopayContract("recipient-address", "ordx:f:test", AutopayScheduleFixed, "10", "", 0)
	deployTx, addr := testTemplateDeployTxWithNonce(t, contract, 7)
	closeTx := testExchangeCloseTx(t, addr, testAsset(DefaultGasConfig().GasAssetName, 2))
	store := NewRuntimeStore()

	_, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:   []*wire.MsgTx{deployTx, closeTx},
		Store: store,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			return "deployer-address", nil
		},
	})
	require.NoError(t, err)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.Running.Closed)

	redeployTx, redeployAddr := testTemplateDeployTxWithNonce(t, contract, 8)
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{redeployTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.True(t, store.Exists(redeployAddr))
}

func TestBackendRecordsInvalidInvokeParam(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	invokeTx := testTemplateLimitOrderInvokeTx(t, addr, OrderTypeStake)

	store := NewRuntimeStore()
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, invokeTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.ResultPlans, 1)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.Items, 1)
	require.Equal(t, InvokeReasonInvalid, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	require.Equal(t, uint64(1), state.InvokeCount)
	require.Zero(t, state.Running.AssetBInPool)
}

func TestBackendRecordsUnsupportedAMMRefund(t *testing.T) {
	contract := NewAMMContract("ordx:f:test", "100", 10, "1000")
	deployTx, addr := testTemplateDeployTx(t, contract)
	refundTx := testTemplateRefundInvokeTx(t, addr, 1)

	store := NewRuntimeStore()
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, refundTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.ResultPlans, 1)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.Items, 1)
	require.Equal(t, InvokeReasonInvalid, state.Items[0].Reason)
	require.Equal(t, ItemStatusRefunded, state.Items[0].Done)
	require.Equal(t, uint64(1), state.InvokeCount)
	requireDecimalString(t, "100", state.Running.RequiredAssetA)
	requireDecimalString(t, "10", state.Running.RequiredAssetB)
	requireDecimalString(t, "0", state.Running.AssetAInPool)
	requireDecimalString(t, "0", state.Running.AssetBInPool)
}

func testTemplateDeployTx(t *testing.T, contract Contract) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	return testTemplateDeployTxWithNonce(t, contract, 7)
}

func testTemplateDeployTxWithNonce(t *testing.T, contract Contract, nonce uint64) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	return testTemplateDeployTxWithGasLimit(t, contract, nonce, DefaultGasConfig().DeployBaseGas)
}

func testTemplateDeployTxWithGasLimit(t *testing.T, contract Contract, nonce uint64, gasLimit int64) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        gasLimit,
		SubType:         contract.TemplateName(),
		Version:         contract.Version(),
		DeployNonce:     nonce,
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, deploy.ContractContent, "deployer-address", deploy.DeployNonce)
	require.NoError(t, err)
	script, err := DeployNullDataScript(deploy)
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, nil, testTemplateContractScript(addr)))
	return tx, addr
}

func testTemplateLimitOrderInvokeTx(t *testing.T, contract ContractAddress, orderType int) *wire.MsgTx {
	t.Helper()
	return testTemplateLimitOrderInvokeTxWithFunding(t, contract, orderType, 7, nil)
}

func testTemplateLimitOrderInvokeTxWithFunding(t *testing.T, contract ContractAddress, orderType int, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	return testTemplateLimitOrderInvokeTxFor(t, contract, orderType, "ordx:f:test", "10", "2", value, assets)
}

func testTemplateLimitOrderInvokeTxFor(t *testing.T, contract ContractAddress, orderType int,
	assetName, amt, unitPrice string, value int64, assets wire.TxAssets) *wire.MsgTx {

	t.Helper()
	param, err := (&LimitOrderInvokeParam{
		OrderType: orderType,
		AssetName: assetName,
		Amt:       amt,
		UnitPrice: unitPrice,
	}).Encode()
	require.NoError(t, err)
	invokeScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPISwap,
		Param:     param,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(value, assets, testTemplateContractScript(contract)))
	return tx
}

func testTemplateInvalidInvokeTx(t *testing.T, contract ContractAddress, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	invokeScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Action:    "bad-action",
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(value, assets, testTemplateContractScript(contract)))
	return tx
}

func testTemplateDefaultInvokeTx(t *testing.T, contract ContractAddress, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(value, assets, testTemplateContractScript(contract)))
	return tx
}

func testTemplateContractDepositTx(t *testing.T, contract ContractAddress, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(value, assets, testTemplateContractScript(contract)))
	return tx
}

func testTemplateAMMAddLiquidityTx(t *testing.T, contract ContractAddress, assetName, amt string, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	param, err := (&AddLiquidityInvokeParam{
		OrderType: OrderTypeAddLiquidity,
		AssetName: assetName,
		Amt:       amt,
		Value:     value,
	}).Encode()
	require.NoError(t, err)
	invokeScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPIAddLiquidity,
		Param:     param,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(value, assets, testTemplateContractScript(contract)))
	return tx
}

func testTemplateGasFeeAmount(t *testing.T, gas int64) int64 {
	t.Helper()
	fee, err := contractcommon.GasFeeAtHeight(gas, 0)
	require.NoError(t, err)
	return fee
}

func testTemplateRefundInvokeTx(t *testing.T, contract ContractAddress, itemID int64) *wire.MsgTx {
	t.Helper()
	param, err := (&RefundInvokeParam{ItemIDs: []int64{itemID}}).Encode()
	require.NoError(t, err)
	invokeScript, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPIRefund,
		Param:     param,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(7, nil, testTemplateContractScript(contract)))
	return tx
}

func requireResultPlanAsset(t *testing.T, plan ResultPlan, assetName, amount string) {
	t.Helper()
	name := wire.NewAssetNameFromString(assetName)
	require.NotNil(t, name)
	for _, output := range plan.Outputs {
		asset, err := output.Assets.Find(name)
		if err == nil && asset.Amount.String() == amount {
			return
		}
	}
	t.Fatalf("result plan missing asset %s amount %s: %+v", assetName, amount, plan.Outputs)
}

func requireResultPlanValueTo(t *testing.T, plan ResultPlan, to string, value int64) {
	t.Helper()
	for _, output := range plan.Outputs {
		if output.To == to && output.Value == value {
			return
		}
	}
	t.Fatalf("result plan missing value %d to %s: %+v", value, to, plan.Outputs)
}

func requireNoResultPlanAssetTo(t *testing.T, plan ResultPlan, to string, assetName string) {
	t.Helper()
	name := wire.NewAssetNameFromString(assetName)
	require.NotNil(t, name)
	for _, output := range plan.Outputs {
		if output.To != to {
			continue
		}
		asset, err := output.Assets.Find(name)
		require.Error(t, err, "unexpected result asset %s to %s: %+v", assetName, to, asset)
	}
}

func templateRecordsHaveAssetIntents(records []ExecutionRecord) bool {
	for _, record := range records {
		if len(record.AssetIntents) != 0 {
			return true
		}
	}
	return false
}

func testAssets(assetNameA string, amountA int64, assetNameB string, amountB int64) wire.TxAssets {
	assets := testAsset(assetNameA, amountA)
	if err := assets.Merge(testAsset(assetNameB, amountB)); err != nil {
		panic(err)
	}
	return assets
}
