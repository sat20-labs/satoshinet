package template

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBlockExecutorDeployThenInvoke(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	invokeTx := testTemplateLimitOrderInvokeTx(t, addr, OrderTypeBuy)

	result, err := ExecuteBlock(BlockExecutionRequest{
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

func TestBlockExecutorSettlesLimitOrdersOnFinalize(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, SwapInvokeFee, testAssets(gasAssetName, 50, "ordx:f:test", 10))
	buyTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeBuy, 30, nil)

	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, sellTx, buyTx},
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.ResultPlans, 1)
	require.Len(t, result.SettlementPlans[0].Deals, 1)
	require.Equal(t, "10", result.SettlementPlans[0].Deals[0].AssetAmt)
	require.Equal(t, int64(20), result.SettlementPlans[0].Deals[0].SatValue)
	require.ElementsMatch(t, []int64{0, 1}, result.SettlementPlans[0].ItemIDs)
	require.ElementsMatch(t, result.SettlementPlans[0].ItemIDs, result.ResultPlans[0].ItemIDs)
	require.Len(t, result.ResultPlans[0].Inputs, 2)
	require.NotEqual(t, [32]byte{}, result.StateRoot)
}

func TestBlockExecutorSettlesLimitOrdersAcrossStoreReload(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, SwapInvokeFee, testAssets(gasAssetName, 50, "ordx:f:test", 10))
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
	first, err := ExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{deployTx, sellTx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Empty(t, first.ResultPlans)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, "50", state.Running.GasBalance)

	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	store, err = DecodeRuntimeStore(encoded, nil)
	require.NoError(t, err)
	runtime, ok = store.Get(addr)
	require.True(t, ok)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, "50", state.Running.GasBalance)

	buyTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeBuy, 30, testAsset(gasAssetName, 50))
	parsedBuy, err := ParseTx(buyTx, testTemplateContractResolver)
	require.NoError(t, err)
	require.Len(t, parsedBuy.ContractOutputs, 1)
	parsedBuyGas, err := parsedBuy.ContractOutputs[0].AssetAmount(gasAssetName)
	require.NoError(t, err)
	require.Equal(t, "50", parsedBuyGas.String())
	executor := NewBlockExecutor(BlockExecutionRequest{
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 101,
	})
	require.NoError(t, executor.ExecuteTx(buyTx))
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, "100", state.Running.GasBalance)

	second, err := executor.Finalize()
	require.NoError(t, err)
	require.Len(t, second.SettlementPlans, 1)
	require.Len(t, second.ResultPlans, 1)
	require.Len(t, second.SettlementPlans[0].Deals, 1)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.Equal(t, "100", state.Running.GasBalance)
	resultPlans, err := AugmentResultPlans(second.ResultPlans, store, gasConfig, nil)
	require.NoError(t, err)
	requireResultPlanAsset(t, resultPlans[0], gasAssetName, "100")
	currentOnly := ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{sellTx, buyTx}, TestnetContractPrefix)
	resultPlans, err = AugmentResultPlans(second.ResultPlans, store, gasConfig, currentOnly)
	require.NoError(t, err)
	requireResultPlanAsset(t, resultPlans[0], gasAssetName, "99")
}

func TestBlockExecutorDefaultInvokeLimitOrderNoPriceNoOp(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	defaultTx := testTemplateDefaultInvokeTx(t, addr, 20, testAsset(DefaultGasConfig().GasAssetName, int64(DefaultGasConfig().InvokeBaseGas)))

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, defaultTx}, Store: store})
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

func TestBlockExecutorDefaultInvokeLimitOrderBuyAtMarketPrice(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, SwapInvokeFee, testAssets(gasAssetName, int64(DefaultGasConfig().InvokeBaseGas), "ordx:f:test", 10))
	defaultBuyTx := testTemplateDefaultInvokeTx(t, addr, 20, testAsset(gasAssetName, int64(DefaultGasConfig().InvokeBaseGas)))

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, sellTx, defaultBuyTx}, Store: store, BlockHeight: 100})
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

func TestContractUTXOProviderWithTxOutputsIncludesDefaultInvoke(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	_, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	defaultTx := testTemplateDefaultInvokeTx(t, addr, 20, testAsset(gasAssetName, int64(DefaultGasConfig().InvokeBaseGas)))

	provider := ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{defaultTx}, TestnetContractPrefix)
	utxos, err := provider(addr)
	require.NoError(t, err)
	require.Len(t, utxos, 1)
	require.Equal(t, OutPoint{TxID: defaultTx.TxID(), Vout: 0}, utxos[0].OutPoint)
	require.True(t, addr.Equal(utxos[0].Contract))
	require.Equal(t, int64(20), utxos[0].Value)
	asset, err := utxos[0].Assets.Find(wire.NewAssetNameFromString(gasAssetName))
	require.NoError(t, err)
	require.Equal(t, testAsset(gasAssetName, int64(DefaultGasConfig().InvokeBaseGas))[0].Amount.String(), asset.Amount.String())
}

func TestBlockExecutorRejectsInvokeBeforeDeploy(t *testing.T) {
	contract := testTemplateContract(t)
	invokeTx := testTemplateLimitOrderInvokeTx(t, contract, OrderTypeBuy)

	_, err := ExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{invokeTx}})
	require.EqualError(t, err, "invoke target contract does not exist")
}

func TestBlockExecutorSkipsInvalidInvokeParam(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	invokeTx := testTemplateLimitOrderInvokeTx(t, addr, OrderTypeStake)

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, invokeTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.Empty(t, result.SettlementPlans)
	require.Empty(t, result.ResultPlans)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Items)
	require.Zero(t, state.InvokeCount)
}

func TestBlockExecutorSkipsUnsupportedAMMRefund(t *testing.T) {
	contract := NewAMMContract("ordx:f:test", "100", 10, "1000")
	deployTx, addr := testTemplateDeployTx(t, contract)
	refundTx := testTemplateRefundInvokeTx(t, addr, 1)

	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, refundTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.Empty(t, result.SettlementPlans)
	require.Empty(t, result.ResultPlans)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Items)
	require.Zero(t, state.InvokeCount)
	require.Equal(t, "100", state.Running.RequiredAsset)
	require.Equal(t, int64(10), state.Running.RequiredSat)
	require.Empty(t, state.Running.AssetAmtInPool)
	require.Zero(t, state.Running.SatValueInPool)
}

func testTemplateDeployTx(t *testing.T, contract Contract) (*wire.MsgTx, ContractAddress) {
	t.Helper()
	content, err := contract.Encode()
	require.NoError(t, err)
	deploy := DeployPayload{
		GasLimit:        DefaultGasConfig().DeployBaseGas,
		TemplateName:    contract.TemplateName(),
		TemplateVersion: contract.Version(),
		Deployer:        "deployer-address",
		Random:          []byte("random"),
		ContractContent: content,
	}
	addr, _, err := DeriveContractAddress(TestnetContractPrefix, deploy.ContractContent, deploy.Deployer, deploy.Random)
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
	param, err := (&LimitOrderInvokeParam{
		OrderType: orderType,
		AssetName: "ordx:f:test",
		Amt:       "10",
		UnitPrice: "2",
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

func testTemplateDefaultInvokeTx(t *testing.T, contract ContractAddress, value int64, assets wire.TxAssets) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(value, assets, testTemplateContractScript(contract)))
	return tx
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

func testAssets(assetNameA string, amountA int64, assetNameB string, amountB int64) wire.TxAssets {
	assets := testAsset(assetNameA, amountA)
	if err := assets.Merge(testAsset(assetNameB, amountB)); err != nil {
		panic(err)
	}
	return assets
}
