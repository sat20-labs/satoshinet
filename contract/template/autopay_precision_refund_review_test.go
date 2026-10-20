package template

import (
	"encoding/json"
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutopayHighPrecisionDefaultFundingAndStateRoundTrip(t *testing.T) {
	gas := testAutopayGasConfig()
	runtime := testAutopayRuntime(t, "recipient-address", gas.GasAssetName, "1")
	amount, err := contractframework.ParseDecimalAmountString("50.00000000001")
	require.NoError(t, err)
	assets := testAsset(gas.GasAssetName, 0)
	assets[0].Amount = *amount
	item, err := runtime.ApplyDefaultInvoke(autopayDefaultGasRequest(runtime, "payer", gas.GasAssetName, "precise-default", assets))
	require.NoError(t, err)
	require.NotNil(t, item)
	requireDecimalString(t, amount.String(), item.InAmt)
	store := runtimeStoreWith(runtime)
	root := store.StateRoot()
	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	restored, err := DecodeRuntimeStore(encoded, nil)
	require.NoError(t, err)
	require.Equal(t, root, restored.StateRoot())
	current, _ := restored.Get(runtime.Address())
	state, err := current.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, amount.String(), state.AutopayData().GasBalance)
	requireDecimalString(t, amount.String(), state.Items[0].InAmt)
	jsonItem, err := json.Marshal(state.Items[0])
	require.NoError(t, err)
	var decoded InvokeItem
	require.NoError(t, json.Unmarshal(jsonItem, &decoded))
	require.Equal(t, state.Items[0], decoded)
}

func TestInvokeAssetAmountsPreserveExistingNormalization(t *testing.T) {
	for _, text := range []string{"0", "1", "1.25", "0.1234567890"} {
		amount, err := contractframework.ParseDecimalAmountString(text)
		require.NoError(t, err)
		item := InvokeItem{InAmt: amount}
		require.NoError(t, normalizeCompactInvokeItem(&item))
		if amount.Sign() == 0 {
			require.Nil(t, item.InAmt, "zero quantities retain the existing nil normalization")
			continue
		}
		expected, err := parseStateDecimal("inAmt", text)
		require.NoError(t, err)
		require.Equal(t, expected, item.InAmt, "existing amounts must retain their canonical decimal representation")
	}
}

func TestBackendAutopayRoundedGasMatchesManagedBalance(t *testing.T) {
	gas := testAutopayGasConfig()
	first := testAutopayRuntime(t, "first-recipient", "ordx:f:fee", "1")
	other := testAutopayRuntime(t, "other-recipient", "ordx:f:fee", "1")
	store := runtimeStoreWith(first)
	store.Add(other)
	for n, runtime := range []*ContractRuntime{first, other} {
		state, err := runtime.RuntimeState()
		require.NoError(t, err)
		state.AutopayData().GasBalance = parseDecimalOrZero("1")
		state.AutopayData().FeeBalance = parseDecimalOrZero("10")
		state.AutopayData().NextPayHeight = int64(100 + n)
		state.AutopayData().AutopayDelegates = map[string]AutopayDelegate{
			"payer": {AmountPerBlock: parseDecimalOrZero("1"), Balance: parseDecimalOrZero("10")},
		}
		require.NoError(t, runtime.saveRuntimeState(state))
		creditTestManagedOutput(t, runtime, testContractOutput("pool", 0, runtime.Address(), 0, testAssets("ordx:f:fee", 10, gas.GasAssetName, 1)))
	}
	for height := int64(100); height <= 101; height++ {
		providers := make(map[string]ContractUTXOProvider)
		for n, runtime := range []*ContractRuntime{first, other} {
			addr := runtime.Address()
			current, _ := store.Get(addr)
			provider := managedPhysicalProvider(t, current)
			providers[addr.MustEncode()] = func(addr ContractAddress) ([]UTXO, error) {
				utxos, err := provider(addr)
				for j := range utxos {
					utxos[j].OutPoint.Vout = uint32(n)
				}
				return utxos, err
			}
		}
		result, err := testTemplateExecuteBlock(BlockExecutionRequest{Store: store, GasConfig: gas, BlockHeight: height,
			AssetPrecision: func(string) (int, bool) { return 0, true },
			ContractUTXOs:  func(addr ContractAddress) ([]UTXO, error) { return providers[addr.MustEncode()](addr) },
		})
		require.NoError(t, err, "exhausted operating GAS must pause only its own contract")
		require.Len(t, result.SettlementPlans, 1)
		charged := first
		if height == 101 {
			charged = other
		}
		address := charged.Address()
		require.Equal(t, address.MustEncode(), result.SettlementPlans[0].Contract)
		assert.Equal(t, "1", decimalString(result.SettlementPlans[0].GasFee))
		committed, _ := store.Get(charged.Address())
		state, err := committed.RuntimeState()
		require.NoError(t, err)
		assert.Equal(t, "0", decimalOrZero(state.AutopayData().GasBalance).String())
		assert.Equal(t, AutopayStatusFunding, state.AutopayData().AutopayStatus, "reserve check must use the rounded next fee too")
		managed, _ := store.ManagedBalance(charged.Address())
		actualGas, err := managed.AssetAmount(gas.GasAssetName)
		require.NoError(t, err)
		requireDecimalString(t, "0", actualGas)
	}
}

func TestBackendAutopayCancelRefundsCurrentFunding(t *testing.T) {
	for _, sameAsset := range []bool{false, true} {
		t.Run(fmt.Sprintf("same_asset_%v", sameAsset), func(t *testing.T) {
			gas := testAutopayGasConfig()
			gas.ResultBaseGas = 50000
			feeAsset := "ordx:f:fee"
			if sameAsset {
				feeAsset = gas.GasAssetName
			}
			runtime := testAutopayRuntime(t, "recipient-address", feeAsset, "1")
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			state.AutopayData().AutopayDelegates = map[string]AutopayDelegate{
				"payer": {AmountPerBlock: parseDecimalOrZero("1"), Balance: parseDecimalOrZero("100")},
			}
			state.AutopayData().FeeBalance = parseDecimalOrZero("100")
			require.NoError(t, runtime.saveRuntimeState(state))
			creditTestManagedOutput(t, runtime, testContractOutput("pool", 0, runtime.Address(), 0, testAsset(feeAsset, 100)))
			fee, err := gas.ResultFee(100)
			require.NoError(t, err)
			assets := testAsset(gas.GasAssetName, 0)
			if !sameAsset {
				assets = testAssets(feeAsset, 7, gas.GasAssetName, 0)
			}
			for n := range assets {
				if assets[n].Name.String() == gas.GasAssetName {
					assets[n].Amount = *fee.AddAlignPrecision(parseDecimalOrZero("5"))
				}
			}
			script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: 1, Action: InvokeAPICancel})
			require.NoError(t, err)
			tx := wire.NewMsgTx(1)
			tx.AddTxIn(&wire.TxIn{})
			tx.AddTxOut(wire.NewTxOut(0, nil, script))
			tx.AddTxOut(wire.NewTxOut(0, assets, testTemplateContractScript(runtime.Address())))
			store := runtimeStoreWith(runtime)
			result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Store: store, GasConfig: gas, BlockHeight: 100,
				AssetPrecision: func(string) (int, bool) { return 3, true },
				ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "payer", nil },
				ContractUTXOs:  managedPhysicalProvider(t, runtime),
			})
			require.NoError(t, err)
			require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
			require.Len(t, result.ResultPlans, 1)
			address := runtime.Address()
			received := func(assetName string) *scommon.Decimal {
				total := scommon.NewDefaultDecimal(0)
				for _, output := range result.ResultPlans[0].Outputs {
					if output.To == "payer" {
						for _, asset := range output.Assets {
							if asset.Name.String() == assetName {
								total = total.AddAlignPrecision(&asset.Amount)
							}
						}
					}
				}
				return total
			}
			if sameAsset {
				requireDecimalString(t, "105", received(feeAsset))
			} else {
				requireDecimalString(t, "107", received(feeAsset))
				requireDecimalString(t, "5", received(gas.GasAssetName))
			}
			requireNoResultPlanOutputTo(t, result.ResultPlans[0], address.MustEncode())
			requireDecimalString(t, fee.String(), result.ResultPlans[0].GasFee)
			committed, _ := store.Get(runtime.Address())
			after, err := committed.RuntimeState()
			require.NoError(t, err)
			requireDecimalString(t, "0", after.AutopayData().GasBalance)
			requireDecimalString(t, "0", after.AutopayData().FeeBalance)
			managed, _ := store.ManagedBalance(runtime.Address())
			require.True(t, managed.IsZero(), "all historical and current principal must be refunded")
		})
	}
}

func TestBackendAutopayHighPrecisionLifecycle(t *testing.T) {
	for _, terminal := range []string{InvokeAPICancel, InvokeAPIClose} {
		for _, principalText := range []string{"50", "50.00000000001"} {
			t.Run(terminal+"/"+principalText, func(t *testing.T) {
				gas := testAutopayGasConfig()
				gas.ResultBaseGas = 50000
				runtime := testAutopayRuntime(t, "recipient-address", "ordx:f:fee", "1")
				address := runtime.Address()
				store := runtimeStoreWith(runtime)
				parse := func(text string) *scommon.Decimal {
					t.Helper()
					amount, err := contractframework.ParseDecimalAmountString(text)
					require.NoError(t, err)
					return amount
				}
				principal, rate, operating := parse(principalText), parse("1.00000000001"), parse("100.00000000001")
				execute := func(height int64, action string, param []byte, assets wire.TxAssets) BlockExecutionResult {
					t.Helper()
					current, _ := store.Get(address)
					var txs []*wire.MsgTx
					if action != "" {
						script, err := InvokeNullDataScript(InvokePayload{GasLimit: gas.InvokeBaseGas, CallNonce: uint64(height), Action: action, Param: param})
						require.NoError(t, err)
						tx := wire.NewMsgTx(1)
						tx.AddTxIn(&wire.TxIn{})
						tx.AddTxOut(wire.NewTxOut(0, nil, script))
						tx.AddTxOut(wire.NewTxOut(0, assets, testTemplateContractScript(address)))
						txs = append(txs, tx)
					}
					result, err := testTemplateExecuteBlock(BlockExecutionRequest{Txs: txs, Store: store, GasConfig: gas, BlockHeight: height,
						AssetPrecision: func(string) (int, bool) { return 11, true },
						ResolveInvoker: func(*wire.MsgTx, Tx) (string, error) { return "deployer-address", nil },
						ContractUTXOs:  managedPhysicalProvider(t, current),
					})
					require.NoError(t, err)
					return result
				}
				fee, err := gas.ResultFee(100)
				require.NoError(t, err)
				assets := testAssets("ordx:f:fee", 0, gas.GasAssetName, 0)
				assets[0].Amount = *principal
				assets[1].Amount = *operating.AddAlignPrecision(fee)
				param := mustAutopayFundingParam(t, AutopayConfigInvokeParam{AmountPerBlock: rate.String(), GasFundingAmount: operating.String(), BlobKeyLimit: 1})
				configured := execute(100, InvokeAPIConfig, param, assets)
				require.Equal(t, ResultStatusSuccess, configured.Records[0].Status)
				current, _ := store.Get(address)
				state, err := current.RuntimeState()
				require.NoError(t, err)
				requireDecimalString(t, principal.String(), state.AutopayData().FeeBalance)
				requireDecimalString(t, operating.String(), state.AutopayData().GasBalance)
				encoded, err := store.MarshalBinary()
				require.NoError(t, err)
				store, err = DecodeRuntimeStore(encoded, nil)
				require.NoError(t, err)
				paid := execute(101, "", nil, nil)
				requireResultPlanAssetTo(t, paid.ResultPlans[0], "recipient-address", "ordx:f:fee", rate.String())
				gasFee, err := gas.ContractFundingFee(ExecutionKindTrigger, gas.TriggerBaseGas, true, 101)
				require.NoError(t, err)
				finishAssets := testAsset(gas.GasAssetName, 0)
				finishAssets[0].Amount = *fee
				finished := execute(102, terminal, nil, finishAssets)
				require.Equal(t, ResultStatusSuccess, finished.Records[0].Status)
				requireResultPlanAssetTo(t, finished.ResultPlans[0], "deployer-address", "ordx:f:fee", principal.SubAlignPrecision(rate).String())
				current, _ = store.Get(address)
				state, err = current.RuntimeState()
				require.NoError(t, err)
				requireDecimalString(t, "0", state.AutopayData().FeeBalance)
				if terminal == InvokeAPIClose {
					require.True(t, state.AutopayData().Closed)
					requireResultPlanAssetTo(t, finished.ResultPlans[0], "deployer-address", gas.GasAssetName, operating.SubAlignPrecision(gasFee).String())
					managed, _ := store.ManagedBalance(address)
					require.True(t, managed.IsZero())
				} else {
					requireDecimalString(t, operating.SubAlignPrecision(gasFee).String(), state.AutopayData().GasBalance)
				}
			})
		}
	}
}
