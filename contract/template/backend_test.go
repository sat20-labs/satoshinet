package template

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func testTemplateExecuteBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {
	if req.ResolveInvoker == nil {
		req.ResolveInvoker = testTemplateInvokerResolver
	}
	if err := ensureTemplateTestDeployResultGas(&req); err != nil {
		return BlockExecutionResult{}, err
	}
	return ExecuteBlock(req)
}

func ensureTemplateTestDeployResultGas(req *BlockExecutionRequest) error {
	if req == nil {
		return nil
	}
	cfg := req.GasConfig.Normalize()
	fee, err := cfg.ResultFee(req.BlockHeight)
	if err != nil {
		return err
	}
	policy := contractframework.AssetPrecisionPolicy{
		Fallback: contractcommon.GasFeePrecision,
		Resolve:  req.AssetPrecision,
	}
	fee = policy.NormalizeUp(cfg.GasAssetName, fee)
	prefix := req.ContractPrefix
	if prefix == "" {
		prefix = TestnetContractPrefix
	}
	name := wire.NewAssetNameFromString(cfg.GasAssetName)
	if name == nil {
		return fmt.Errorf("invalid test gas asset %s", cfg.GasAssetName)
	}
	for _, tx := range req.Txs {
		typ, found, err := contractcommon.ClassifyTxPayloadType(tx)
		if err != nil {
			return err
		}
		if !found || typ != TxTypeDeploy {
			continue
		}
		for _, txOut := range tx.TxOut {
			addr, ok, err := ParseContractPkScript(txOut.PkScript, prefix)
			if err != nil {
				return err
			}
			if !ok || addr.ContractType() != ContractTypeTemplate {
				continue
			}
			asset, _ := txOut.Assets.Find(name)
			current := scommon.NewDefaultDecimal(0)
			if asset != nil {
				current = asset.Amount.Clone()
			}
			if current.Cmp(fee) >= 0 {
				continue
			}
			missing := fee.SubAlignPrecision(current)
			if err := txOut.Assets.Merge(wire.TxAssets{{Name: *name, Amount: *missing}}); err != nil {
				return err
			}
		}
	}
	return nil
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
	store := NewRuntimeStore()
	_, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deployTx, invokeTx}, Store: store,
		Registry: testTemplateRegistryWithBaseGas(baseGas),
	})
	require.ErrorContains(t, err, "invoke gas limit below invoke base gas")
	require.Empty(t, store.runtimes, "admission failure must not commit candidate block state")
}

func TestDeployRequiresResult(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx}, BlockHeight: 100})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	record := result.Records[0]
	require.Equal(t, TxTypeDeploy, record.Type)
	require.True(t, addr.Equal(record.Contract))
	require.True(t, record.RequiresResult)
	require.NotNil(t, record.GasFee)
	require.Positive(t, record.GasFee.Sign())
	require.Len(t, result.ResultPlans, 1)
	plan := result.ResultPlans[0]
	require.Equal(t, addr.MustEncode(), plan.Contract)
	require.Empty(t, plan.ItemIDs)
	require.Len(t, plan.Inputs, 1)
	require.NotNil(t, plan.GasFee)
	require.Zero(t, plan.GasFee.Cmp(record.GasFee))
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
	requireDecimalString(t, "20", state.LimitOrderData().AssetBInPool)

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
	require.Empty(t, result.SettlementPlans)
	require.Len(t, result.ResultPlans, 1)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Items)
	require.Empty(t, state.LimitOrderData().AssetAInPool)
	require.Zero(t, state.LimitOrderData().AssetBInPool)
	requireDecimalString(t, "0", state.LimitOrderData().GasBalance)

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
	require.Empty(t, state.Items)
}

func TestBackendSettlesLimitOrdersAcrossStoreReload(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	gasAssetName := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeSell, 0,
		testAssets(gasAssetName, 50, "ordx:f:test", 10))
	store := NewRuntimeStore()
	gasConfig := GasConfig{
		GasAssetName: gasAssetName, BootstrapAddress: "bootstrap-address",
		DeployBaseGas: 1, InvokeBaseGas: 1, ResultBaseGas: 1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}
	first, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deployTx, sellTx}, Store: store, GasConfig: gasConfig, BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, first.ResultPlans, 1)
	firstResultTx := mustBuildTemplateResultTx(t, first.ResultPlans)

	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	store, err = DecodeRuntimeStore(encoded, nil)
	require.NoError(t, err)

	buyTx := testTemplateLimitOrderInvokeTxWithFunding(t, addr, OrderTypeBuy, 20, testAsset(gasAssetName, 50))
	parentProvider := contractframework.ContractUTXOProviderWithTxOutputs(nil,
		[]*wire.MsgTx{firstResultTx}, TestnetContractPrefix, ContractTypeTemplate)
	second, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{buyTx}, Store: store, GasConfig: gasConfig, BlockHeight: 101,
		ContractUTXOs: parentProvider, ResolveInvoker: testTemplateInvokerResolver,
	})
	require.NoError(t, err)
	require.Len(t, second.SettlementPlans, 1)
	require.Len(t, second.SettlementPlans[0].Deals, 1)
	require.Len(t, second.ResultPlans, 1)
	requireResultPlanAssetTo(t, second.ResultPlans[0], "invoker-address", gasAssetName, "49.999")
	requireNoResultPlanAssetTo(t, second.ResultPlans[0], addr.MustEncode(), gasAssetName)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "0", state.LimitOrderData().GasBalance)
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
	require.Empty(t, result.SettlementPlans)
	require.Len(t, result.ResultPlans, 1)

	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.LimitOrderData().AssetAInPool)
	require.Zero(t, state.LimitOrderData().AssetBInPool)
	requireDecimalString(t, "0", state.LimitOrderData().GasBalance)

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
	require.True(t, state.LimitOrderData().Closed)

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
		GasAssetName: gas, BootstrapAddress: "bootstrap-address",
		DeployBaseGas: 1, InvokeBaseGas: 1, ResultBaseGas: 1,
		MaxGasPerInvoke: DefaultGasConfig().MaxGasPerInvoke,
	}
	deployTx.TxOut[1].Value = 20
	deployTx.TxOut[1].Assets = testAssets(gas, 5, contract.AssetName, 100)
	addTx := testTemplateAMMAddLiquidityTx(t, addr, contract.AssetName, "100", 20,
		testAssets(gas, 5, contract.AssetName, 100))
	closeTx := testExchangeCloseTx(t, addr, testAsset(gas, 2))
	profitTx := testTemplateContractDepositTx(t, addr, 10, testAsset("ordx:f:profit", 100))
	profitProvider := contractframework.ContractUTXOProviderWithTxOutputs(nil,
		[]*wire.MsgTx{profitTx}, TestnetContractPrefix, ContractTypeTemplate)
	store := NewRuntimeStore()
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deployTx, addTx, closeTx}, Store: store, GasConfig: gasConfig,
		ContractUTXOs: profitProvider,
		ResolveInvoker: func(tx *wire.MsgTx, contractTx Tx) (string, error) {
			if contractTx.Kind == TxTypeDeploy || tx == closeTx {
				return "deployer-address", nil
			}
			return "lp-address", nil
		},
		BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.ResultPlans, 1)
	plan := result.ResultPlans[0]
	requireResultPlanAssetTo(t, plan, "deployer-address", contract.AssetName, "100")
	requireResultPlanValueTo(t, plan, "deployer-address", 20)
	requireResultPlanAssetTo(t, plan, "lp-address", contract.AssetName, "100")
	requireResultPlanValueTo(t, plan, "lp-address", 20)
	requireResultPlanAssetTo(t, plan, "lp-address", gas, "4.999")
	requireResultPlanAssetTo(t, plan, "deployer-address", gas, "6.998")
	// Unsolicited physical surplus is exceptional foundation custody, not
	// contract profit. It never participates in the 70/30 profit split.
	requireResultPlanValueTo(t, plan, "bootstrap-address", 10)
	requireResultPlanAssetTo(t, plan, "bootstrap-address", "ordx:f:profit", "100")
	requireNoResultPlanAssetTo(t, plan, "deployer-address", "ordx:f:profit")
	requireNoResultPlanOutputTo(t, plan, addr.MustEncode())
}

func TestBackendDefaultInvokeLimitOrderNoPriceNoOp(t *testing.T) {
	contract := NewLimitOrderContract("ordx:f:test")
	deployTx, addr := testTemplateDeployTx(t, contract)
	defaultTx := testTemplateDefaultInvokeTx(t, addr, 20,
		testAsset(DefaultGasConfig().GasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))
	store := NewRuntimeStore()
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, defaultTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, TxTypeDeploy, result.Records[0].Type)
	require.Equal(t, TxTypeInvoke, result.Records[1].Type)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
	require.True(t, result.Records[1].RequiresResult)
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
	require.Empty(t, state.Items)
	require.Equal(t, 2, state.LimitOrderData().TotalDealCount)
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
		Txs:            []*wire.MsgTx{deployTx, buyTx, sellTx},
		Store:          store,
		BlockHeight:    100,
		AssetPrecision: func(name string) (int, bool) { return 0, name == assetName },
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
		Txs:            []*wire.MsgTx{deployTx, buyTx},
		Store:          store,
		BlockHeight:    100,
		AssetPrecision: func(name string) (int, bool) { return 0, name == assetName },
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

func TestBackendAMMSellUsesFundingAsInputAndAmtAsMinimumReceive(t *testing.T) {
	const assetName = "ordx:f:qqcom"
	contract := NewAMMContract(assetName, "2", 17, "34")
	deployTx, addr := testTemplateDeployTx(t, contract)
	deployTx.TxOut[1].Value = 17
	deployTx.TxOut[1].Assets = testAsset(assetName, 2)
	gas := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxFor(t, addr, OrderTypeSell, assetName, "4", "4", 0,
		testAssets(assetName, 1, gas, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deployTx, sellTx}, Store: NewRuntimeStore(), BlockHeight: 100,
		ResolveInvoker: func(tx *wire.MsgTx, _ Tx) (string, error) {
			if tx == sellTx {
				return "seller-address", nil
			}
			return "deployer-address", nil
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusSuccess, result.Records[1].Status)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Deals, 1)
	require.Equal(t, "1", result.SettlementPlans[0].Deals[0].AssetAmt)
	require.Equal(t, int64(5), result.SettlementPlans[0].Deals[0].SatValue)
	requireResultPlanValueTo(t, result.ResultPlans[0], "seller-address", 5)
}

func TestBackendAMMSellSlippageIsNotFundingInvalid(t *testing.T) {
	const assetName = "ordx:f:qqcom"
	contract := NewAMMContract(assetName, "2", 17, "34")
	deployTx, addr := testTemplateDeployTx(t, contract)
	deployTx.TxOut[1].Value = 17
	deployTx.TxOut[1].Assets = testAsset(assetName, 2)
	gas := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxFor(t, addr, OrderTypeSell, assetName, "6", "6", 0,
		testAssets(assetName, 1, gas, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deployTx, sellTx}, Store: NewRuntimeStore(), BlockHeight: 100,
		ResolveInvoker: func(tx *wire.MsgTx, _ Tx) (string, error) {
			if tx == sellTx {
				return "seller-address", nil
			}
			return "deployer-address", nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, ResultStatusSuccess, result.Records[1].Status)
	require.Empty(t, result.SettlementPlans[0].Deals)
	requireResultPlanAssetTo(t, result.ResultPlans[0], "seller-address", assetName, "1")
}

func TestBackendAMMSellStillRejectsMissingContractAsset(t *testing.T) {
	const assetName = "ordx:f:qqcom"
	contract := NewAMMContract(assetName, "2", 17, "34")
	deployTx, addr := testTemplateDeployTx(t, contract)
	deployTx.TxOut[1].Value = 17
	deployTx.TxOut[1].Assets = testAsset(assetName, 2)
	gas := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxFor(t, addr, OrderTypeSell, assetName, "4", "4", 0,
		testAsset(gas, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deployTx, sellTx}, Store: NewRuntimeStore(), BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
	require.Empty(t, result.SettlementPlans)
	require.Len(t, result.ResultPlans, 1)
}

func TestBackendLimitOrderSellStillRequiresDeclaredAssetAmount(t *testing.T) {
	const assetName = "ordx:f:qqcom"
	contract := NewLimitOrderContract(assetName)
	deployTx, addr := testTemplateDeployTx(t, contract)
	gas := DefaultGasConfig().GasAssetName
	sellTx := testTemplateLimitOrderInvokeTxFor(t, addr, OrderTypeSell, assetName, "4", "4", 0,
		testAssets(assetName, 1, gas, testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas)))

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deployTx, sellTx}, Store: NewRuntimeStore(), BlockHeight: 100,
	})
	require.NoError(t, err)
	require.Equal(t, ResultStatusInvalid, result.Records[1].Status)
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
		Txs:            []*wire.MsgTx{deployTx, buyTx},
		Store:          store,
		GasConfig:      gasConfig,
		BlockHeight:    100,
		AssetPrecision: func(name string) (int, bool) { return 0, name == assetName },
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
	buyB.TxIn[0].PreviousOutPoint.Index = 1 // distinct txid/outpoint in the same block
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
	require.Equal(t, "9.0247452692", result.SettlementPlans[0].Deals[0].AssetAmt)
	require.Equal(t, "7.5256381498", result.SettlementPlans[0].Deals[1].AssetAmt)
	require.Len(t, result.ResultPlans, 1)
	require.ElementsMatch(t, []int64{0, 1}, result.ResultPlans[0].ItemIDs)
	require.Contains(t, result.ResultPlans[0].Inputs, OutPoint{TxID: buyA.TxID(), Vout: 0})
	require.Contains(t, result.ResultPlans[0].Inputs, OutPoint{TxID: buyB.TxID(), Vout: 0})
	requireResultPlanAssetTo(t, result.ResultPlans[0], "buyer-a", assetName, "9.0247452692")
	requireResultPlanAssetTo(t, result.ResultPlans[0], "buyer-b", assetName, "7.5256381498")
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
	creditTestManagedOutput(t, runtime, testContractOutput("previous-result", 0, addr, 20, testAsset(assetName, 20)))
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
	requireResultPlanAssetTo(t, plans[0], addr.MustEncode(), assetName, "6")
	requireResultPlanValueTo(t, plans[0], addr.MustEncode(), 6)
	require.Len(t, plans[0].Inputs, 1)
	require.Equal(t, int64(26), plans[0].ManagedRemainder.Value)
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
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
	require.True(t, result.Records[0].RequiresResult)
	require.Len(t, result.ResultPlans, 1)
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
	_, err = testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Store: store})
	require.ErrorContains(t, err, "deploy gas limit is zero")
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

func TestBackendAutopayDeployAllowsSameConfig(t *testing.T) {
	contract := NewAutopayContract("dkvs", "recipient-address", "ordx:f:test", "10")
	deployTx, addr := testTemplateDeployTxWithNonce(t, contract, 7)
	duplicateTx, duplicateAddr := testTemplateDeployTxWithNonce(t, contract, 8)
	store := NewRuntimeStore()

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{deployTx, duplicateTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.Equal(t, ResultStatusSuccess, result.Records[1].Status)
	require.True(t, store.Exists(addr))
	require.True(t, store.Exists(duplicateAddr))
}

func TestBackendAutopayDeployAllowsAfterClose(t *testing.T) {
	contract := NewAutopayContract("dkvs", "recipient-address", "ordx:f:test", "10")
	deployTx, addr := testTemplateDeployTxWithNonce(t, contract, 7)
	closeTx := testExchangeCloseTx(t, addr, testAsset(DefaultGasConfig().GasAssetName,
		testTemplateGasFeeAmount(t, DefaultGasConfig().ResultBaseGas)))
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
	require.True(t, state.AutopayData().Closed)

	redeployTx, redeployAddr := testTemplateDeployTxWithNonce(t, contract, 8)
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{redeployTx}, Store: store})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.True(t, store.Exists(redeployAddr))
}

func TestBackendAutopayDelegateLimitRefundsInvoke(t *testing.T) {
	param, err := (&AutopayConfigInvokeParam{AmountPerBlock: "1"}).Encode()
	require.NoError(t, err)

	for _, defaultInvoke := range []bool{false, true} {
		t.Run(fmt.Sprintf("default=%t", defaultInvoke), func(t *testing.T) {
			runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:test", "1")
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			state.AutopayData().AutopayDelegates = make(map[string]AutopayDelegate, AutopayMaxDelegates)
			for i := 0; i < AutopayMaxDelegates; i++ {
				state.AutopayData().AutopayDelegates[fmt.Sprintf("delegate-%d", i)] = AutopayDelegate{}
			}
			require.NoError(t, runtime.saveRuntimeState(state))
			store := runtimeStoreWith(runtime)
			gasAsset := DefaultGasConfig().GasAssetName
			funding := testAssets("ordx:f:test", 10, gasAsset,
				testTemplateGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas))

			var tx *wire.MsgTx
			if defaultInvoke {
				tx = testTemplateDefaultInvokeTx(t, runtime.Address(), 0, funding)
			} else {
				script, err := InvokeNullDataScript(InvokePayload{
					GasLimit:  DefaultGasConfig().InvokeBaseGas,
					CallNonce: 1,
					Action:    InvokeAPIConfig,
					Param:     param,
				})
				require.NoError(t, err)
				tx = wire.NewMsgTx(1)
				tx.AddTxIn(&wire.TxIn{})
				tx.AddTxOut(wire.NewTxOut(0, nil, script))
				tx.AddTxOut(wire.NewTxOut(0, funding, testTemplateContractScript(runtime.Address())))
			}

			result, err := testTemplateExecuteBlock(BlockExecutionRequest{
				Txs:         []*wire.MsgTx{tx},
				Store:       store,
				BlockHeight: 100,
				ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) {
					return "new-delegate", nil
				},
				AssetPrecision: func(name string) (int, bool) {
					return 0, name == "ordx:f:test"
				},
			})
			require.NoError(t, err)
			require.Len(t, result.Records, 1)
			require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
			require.True(t, result.Records[0].RequiresResult)
			state, err = runtime.RuntimeState()
			require.NoError(t, err)
			require.NotContains(t, state.AutopayData().AutopayDelegates, "new-delegate")
		})
	}
}

func TestBackendAutopayConfigFundsDelegate(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", gasConfig.GasAssetName, "1")
	store := runtimeStoreWith(runtime)
	param, err := (&AutopayConfigInvokeParam{AmountPerBlock: "10"}).Encode()
	require.NoError(t, err)
	script, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPIConfig,
		Param:     param,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, testAsset(gasConfig.GasAssetName, 100),
		testTemplateContractScript(runtime.Address())))

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{tx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) {
			return "delegate-address", nil
		},
		AssetPrecision: func(name string) (int, bool) {
			return 0, name == gasConfig.GasAssetName
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)

	expectedBalance := scommon.NewDefaultDecimal(100).
		SubAlignPrecision(result.Records[0].GasFee)
	runtime, ok := store.Get(runtime.Address())
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	delegate, ok := state.AutopayData().AutopayDelegates["delegate-address"]
	require.True(t, ok)
	requireDecimalString(t, "10", delegate.AmountPerBlock)
	requireDecimalString(t, expectedBalance.String(), delegate.Balance)
	requireDecimalString(t, expectedBalance.String(), state.AutopayData().FeeBalance)
	requireDecimalString(t, "0", state.AutopayData().GasBalance)
	require.Equal(t, AutopayStatusFunding, state.AutopayData().AutopayStatus)
}

func TestBackendAutopayExplicitOperatingGasFunding(t *testing.T) {
	for _, tc := range []struct {
		name, caller, amount, gas string
		fund                      int64
		valid                     bool
	}{
		{"pure", "deployer-address", "", "400", 450, true},
		{"mixed", "deployer-address", "10", "400", 550, true},
		{"unauthorized", "user-address", "", "400", 450, false},
		{"over-net", "deployer-address", "", "401", 450, false},
		{"pure-with-business", "deployer-address", "", "400", 550, false},
		{"negative", "deployer-address", "10", "-1", 450, false},
		{"mixed-over-precision", "deployer-address", "10", "400.5", 550, false},
		{"pure-over-precision", "deployer-address", "", "400.5", 450, false},
		{"exact-decimal", "deployer-address", "", "400.0", 450, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gas := testAutopayGasConfig()
			gas.TriggerBaseGas, gas.ResultBaseGas = 150000, 50000
			runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "10")
			store := runtimeStoreWith(runtime)
			param, err := (&AutopayConfigInvokeParam{AmountPerBlock: tc.amount, GasFundingAmount: tc.gas}).Encode()
			require.NoError(t, err)
			script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: InvokeAPIConfig, Param: param})
			require.NoError(t, err)
			tx := wire.NewMsgTx(1)
			tx.AddTxIn(&wire.TxIn{})
			tx.AddTxOut(wire.NewTxOut(0, nil, script))
			tx.AddTxOut(wire.NewTxOut(0, testAsset(gas.GasAssetName, tc.fund), testTemplateContractScript(runtime.Address())))
			result, err := testTemplateExecuteBlock(BlockExecutionRequest{
				Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
				ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return tc.caller, nil },
				AssetPrecision: func(name string) (int, bool) { return 0, name == gas.GasAssetName },
			})
			require.NoError(t, err)
			require.Len(t, result.Records, 1)
			runtime, ok := store.Get(runtime.Address())
			require.True(t, ok)
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			expectedPrincipal := int64(0)
			if tc.valid {
				require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
				requireDecimalString(t, "400", state.AutopayData().GasBalance)
				if tc.amount != "" {
					expectedPrincipal = 100
					requireDecimalString(t, "100", state.AutopayData().AutopayDelegates[tc.caller].Balance)
				} else {
					require.Empty(t, state.AutopayData().AutopayDelegates)
				}
			} else {
				require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
				requireDecimalString(t, "0", state.AutopayData().GasBalance)
				require.Empty(t, state.AutopayData().AutopayDelegates)
			}
			requireDecimalString(t, fmt.Sprint(expectedPrincipal), state.AutopayData().FeeBalance)
			provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{tx}, TestnetContractPrefix, ContractTypeTemplate)
			plans, err := AugmentResultPlans(result.ResultPlans, store, gas, provider, nil)
			require.NoError(t, err)
			require.Len(t, plans, 1)
			requireDecimalString(t, "50", plans[0].GasFee)
			contractAddress := runtime.Address()
			if tc.valid {
				requireResultPlanAssetTo(t, plans[0], contractAddress.MustEncode(), gas.GasAssetName, fmt.Sprint(tc.fund-50))
				requireNoResultPlanAssetTo(t, plans[0], tc.caller, gas.GasAssetName)
			} else {
				requireResultPlanAssetTo(t, plans[0], tc.caller, gas.GasAssetName, fmt.Sprint(tc.fund-50))
				requireNoResultPlanAssetTo(t, plans[0], contractAddress.MustEncode(), gas.GasAssetName)
				// Backend prunes terminal items after building their refund plans.
				require.Empty(t, state.Items)
				next, err := runtime.SettleBlockWithGasConfig(101, gas)
				require.NoError(t, err)
				require.Empty(t, next.Transfers)
				require.Empty(t, next.Inputs)
				require.Empty(t, next.ItemIDs)
				requireDecimalString(t, "0", next.GasFee)
			}
		})
	}
}

func TestBackendAutopayInvalidDifferentAssetRefundPreservesPool(t *testing.T) {
	for _, tc := range []struct{ name, caller, funding string }{
		{"unauthorized", "caller-address", "400"},
		{"over-net", "deployer-address", "401"},
	} {
		for _, seeded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/seeded=%t", tc.name, seeded), func(t *testing.T) {
				gas := testAutopayGasConfig()
				gas.TriggerBaseGas, gas.ResultBaseGas = 150000, 50000
				const fee = "ordx:f:fee"
				runtime := testAutopayRuntime(t, "recipient-address", fee, "10")
				if seeded {
					require.NoError(t, runtime.ApplyFunding(testContractOutput("old-pool", 0, runtime.Address(), 0,
						testAssets(fee, 1000, gas.GasAssetName, 600)), gas.GasAssetName))
					state, err := runtime.RuntimeState()
					require.NoError(t, err)
					state.AutopayData().NextPayHeight = 200
					require.NoError(t, runtime.saveRuntimeState(state))
				}
				before, err := runtime.RuntimeState()
				require.NoError(t, err)
				param, err := (&AutopayConfigInvokeParam{AmountPerBlock: "10", GasFundingAmount: tc.funding}).Encode()
				require.NoError(t, err)
				script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: InvokeAPIConfig, Param: param})
				require.NoError(t, err)
				tx := wire.NewMsgTx(1)
				tx.AddTxIn(&wire.TxIn{})
				tx.AddTxOut(wire.NewTxOut(0, nil, script))
				tx.AddTxOut(wire.NewTxOut(0, testAssets(fee, 100, gas.GasAssetName, 450), testTemplateContractScript(runtime.Address())))
				store := runtimeStoreWith(runtime)
				result, err := testTemplateExecuteBlock(BlockExecutionRequest{
					Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
					ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return tc.caller, nil },
					AssetPrecision: func(string) (int, bool) { return 0, true },
				})
				require.NoError(t, err)
				require.Len(t, result.Records, 1)
				require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
				after, err := runtime.RuntimeState()
				require.NoError(t, err)
				require.Equal(t, before.AutopayData().AutopayDelegates, after.AutopayData().AutopayDelegates)
				requireDecimalString(t, decimalOrZero(before.AutopayData().FeeBalance).String(), after.AutopayData().FeeBalance)
				requireDecimalString(t, decimalOrZero(before.AutopayData().GasBalance).String(), after.AutopayData().GasBalance)
				provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, []*wire.MsgTx{tx}, TestnetContractPrefix, ContractTypeTemplate)
				plans, err := AugmentResultPlans(result.ResultPlans, store, gas, provider, nil)
				require.NoError(t, err)
				require.Len(t, plans, 1)
				requireDecimalString(t, "50", plans[0].GasFee)
				requireResultPlanAssetTo(t, plans[0], tc.caller, fee, "100")
				requireResultPlanAssetTo(t, plans[0], tc.caller, gas.GasAssetName, "400")
				addr := runtime.Address()
				requireNoResultPlanAssetTo(t, plans[0], addr.MustEncode(), fee)
				requireNoResultPlanAssetTo(t, plans[0], addr.MustEncode(), gas.GasAssetName)
				next, err := runtime.SettleBlockWithGasConfig(101, gas)
				require.NoError(t, err)
				require.Empty(t, next.Transfers)
				require.Empty(t, next.Inputs)
			})
		}
	}
}

func TestBackendAutopayConfigFundsDelegateAndGas(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	const feeAsset = "ordx:f:fee"
	runtime := testAutopayRuntime(t, "recipient-address", feeAsset, "1")
	store := runtimeStoreWith(runtime)
	param, err := (&AutopayConfigInvokeParam{AmountPerBlock: "10"}).Encode()
	require.NoError(t, err)
	script, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPIConfig,
		Param:     param,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, testAssets(feeAsset, 100, gasConfig.GasAssetName, 100),
		testTemplateContractScript(runtime.Address())))

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{tx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) {
			return "delegate-address", nil
		},
		AssetPrecision: func(name string) (int, bool) {
			return 0, name == feeAsset
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)

	runtime, ok := store.Get(runtime.Address())
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	delegate, ok := state.AutopayData().AutopayDelegates["delegate-address"]
	require.True(t, ok)
	requireDecimalString(t, "10", delegate.AmountPerBlock)
	requireDecimalString(t, "100", delegate.Balance)
	requireDecimalString(t, "100", state.AutopayData().FeeBalance)
	expectedGas := scommon.NewDefaultDecimal(100).SubAlignPrecision(result.Records[0].GasFee)
	requireDecimalString(t, expectedGas.String(), state.AutopayData().GasBalance)
}

func TestBackendAutopayExplicitDifferentAssetGasNotDoubleCounted(t *testing.T) {
	gas := testAutopayGasConfig()
	gas.ResultBaseGas = 50000
	runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "10")
	store := runtimeStoreWith(runtime)
	param, err := (&AutopayConfigInvokeParam{AmountPerBlock: "10", GasFundingAmount: "400"}).Encode()
	require.NoError(t, err)
	script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: InvokeAPIConfig, Param: param})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, testAssets("ordx:f:fee", 100, gas.GasAssetName, 550), testTemplateContractScript(runtime.Address())))
	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "deployer-address", nil },
		AssetPrecision: func(string) (int, bool) { return 0, true },
	})
	require.NoError(t, err)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	runtime, ok := store.Get(runtime.Address())
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "100", state.AutopayData().FeeBalance)
	// 550 - result50 = explicit400 + legacy separate-asset remainder100.
	requireDecimalString(t, "500", state.AutopayData().GasBalance)
}

func TestBackendAutopayInvalidConfigDoesNotFundDelegate(t *testing.T) {
	gasConfig := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", gasConfig.GasAssetName, "10")
	store := runtimeStoreWith(runtime)
	param, err := (&AutopayConfigInvokeParam{AmountPerBlock: "1"}).Encode()
	require.NoError(t, err)
	script, err := InvokeNullDataScript(InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
		Action:    InvokeAPIConfig,
		Param:     param,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, testAsset(gasConfig.GasAssetName, 100),
		testTemplateContractScript(runtime.Address())))

	result, err := testTemplateExecuteBlock(BlockExecutionRequest{
		Txs:         []*wire.MsgTx{tx},
		Store:       store,
		GasConfig:   gasConfig,
		BlockHeight: 100,
		ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) {
			return "delegate-address", nil
		},
		AssetPrecision: func(name string) (int, bool) {
			return 0, name == gasConfig.GasAssetName
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Records[0].Status)

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.NotContains(t, state.AutopayData().AutopayDelegates, "delegate-address")
	requireDecimalString(t, "0", state.AutopayData().FeeBalance)
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
	require.Empty(t, result.SettlementPlans)
	require.Len(t, result.ResultPlans, 1)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Items)
	require.Zero(t, state.InvokeCount)
	require.Zero(t, state.LimitOrderData().AssetBInPool)
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
	require.Empty(t, result.SettlementPlans)
	require.Len(t, result.ResultPlans, 1)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Empty(t, state.Items)
	require.Zero(t, state.InvokeCount)
	requireDecimalString(t, "100", state.AMMData().RequiredAssetA)
	requireDecimalString(t, "10", state.AMMData().RequiredAssetB)
	requireDecimalString(t, "0", state.AMMData().AssetAInPool)
	requireDecimalString(t, "0", state.AMMData().AssetBInPool)
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
	return testTemplateLimitOrderInvokeTxWithFunding(t, contract, orderType, 7,
		testAsset(DefaultGasConfig().GasAssetName, testTemplateGasFeeAmount(t, DefaultGasConfig().ResultBaseGas)))
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
	tx.AddTxOut(wire.NewTxOut(7,
		testAsset(DefaultGasConfig().GasAssetName,
			testTemplateGasFeeAmount(t, DefaultGasConfig().ResultBaseGas)),
		testTemplateContractScript(contract)))
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
