package contract_e2e

import (
	"encoding/json"
	"fmt"
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const scenarioAssetA = "ordx:f:scenarioa"
const scenarioAssetB = "ordx:f:scenariob"

func TestNetworkTemplateLifecycleMatrix(t *testing.T) {
	for _, name := range []string{tmplcontract.TemplateLimitOrder, tmplcontract.TemplateAMM, tmplcontract.TemplateExchange, tmplcontract.TemplateAutopay} {
		t.Run(name, func(t *testing.T) {
			f := newSignedTemplateFixture(t, map[string]int64{scenarioAssetA: 10000, scenarioAssetB: 10000})
			makeContract := func() tmplcontract.Contract {
				switch name {
				case tmplcontract.TemplateLimitOrder:
					return tmplcontract.NewLimitOrderContract(scenarioAssetA)
				case tmplcontract.TemplateAMM:
					return tmplcontract.NewAMMContract(scenarioAssetA, "100", 100, "10000")
				case tmplcontract.TemplateExchange:
					return tmplcontract.NewExchangeContract(scenarioAssetA, scenarioAssetB, tmplcontract.ExchangePriceModeHeight, []tmplcontract.ExchangePriceStep{{Threshold: "0", BPerA: "2"}})
				default:
					return tmplcontract.NewAutopayContract("scenario", f.traderBActor.address, scenarioAssetA, "1")
				}
			}
			gas := tmplcontract.DefaultGasConfig().GasAssetName
			address := scenarioDeploy(t, f, makeContract(), name+"-closable", 0, scenarioFunding(t, 0, map[string]int64{gas: 10000}))
			reject := func(t *testing.T, action string, param []byte, owner bool) {
				t.Helper()
				signer, actor := f.traderB, f.traderBActor
				if owner {
					signer, actor = f.traderA, f.traderAActor
				}
				before := scenarioState(t, f.bootstrapNode, address)["invokeCount"]
				outputs := scenarioInvoke(t, f, signer, address, action, param, scenarioFunding(t, 7, map[string]int64{scenarioAssetA: 1}))
				require.Equal(t, before, scenarioState(t, f.bootstrapNode, address)["invokeCount"], "rejected action must not enter runtime history")
				requireTemplateResultAssetAmount(t, outputs, actor.address, scenarioAssetA, "1")
				requireTemplateResultValue(t, outputs, actor.address, 7)
			}
			t.Run("unknown_action_refunds_without_mutation", func(t *testing.T) { reject(t, "not-an-api", nil, true) })
			t.Run("non_deployer_cannot_close", func(t *testing.T) { reject(t, tmplcontract.InvokeAPIClose, nil, false) })
			for _, action := range contractcommon.TemplateInvokeActions(name) {
				t.Run("malformed_"+action+"_refunds_without_mutation", func(t *testing.T) { reject(t, action, []byte{txscript.OP_PUSHDATA1}, true) })
			}
			t.Run("deployer_closes", func(t *testing.T) {
				before := scenarioState(t, f.bootstrapNode, address)["invokeCount"].(float64)
				scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIClose, nil, scenarioFunding(t, 0, nil))
				require.Equal(t, before+1, scenarioState(t, f.bootstrapNode, address)["invokeCount"])
			})
			t.Run("repeat_close_cannot_pay_twice", func(t *testing.T) { reject(t, tmplcontract.InvokeAPIClose, nil, true) })
			t.Run("closed_default_funding_is_refunded", func(t *testing.T) { reject(t, "", nil, false) })
			for _, action := range contractcommon.TemplateInvokeActions(name) {
				if action == tmplcontract.InvokeAPIClose {
					continue
				}
				t.Run("closed_rejects_"+action, func(t *testing.T) {
					var param []byte
					switch action {
					case tmplcontract.InvokeAPISwap:
						param = templateLimitOrderParam(t, scenarioAssetA, tmplcontract.OrderTypeBuy, "1", "10")
					case tmplcontract.InvokeAPIRefund:
						param = templateRefundParam(t, nil)
					case tmplcontract.InvokeAPIAddLiquidity:
						param = templateAddLiquidityParam(t, scenarioAssetA, "1", 1)
					case tmplcontract.InvokeAPIRemoveLiquidity:
						param = templateRemoveLiquidityParam(t, scenarioAssetA, "1")
					case tmplcontract.InvokeAPIExchange:
						var err error
						param, err = (&tmplcontract.ExchangeInvokeParam{MinOutA: "1"}).Encode()
						require.NoError(t, err)
					case tmplcontract.InvokeAPIConfig:
						var err error
						param, err = (&tmplcontract.AutopayConfigInvokeParam{AmountPerBlock: "1", BlobKeyLimit: 1}).Encode()
						require.NoError(t, err)
					}
					reject(t, action, param, false)
				})
			}
			t.Run("non_closable_flag_survives_restart", func(t *testing.T) {
				address = scenarioDeploy(t, f, makeContract(), name+"-permanent", contractcommon.ContractFlagNonClosable, scenarioFunding(t, 0, map[string]int64{gas: 10000}))
				reject(t, tmplcontract.InvokeAPIClose, nil, true)
				before := scenarioState(t, f.coreNode, address)
				require.NoError(t, f.coreNode.Restart(false))
				require.NoError(t, rpctest.ConnectNode(f.coreNode, f.bootstrapNode))
				require.NoError(t, rpctest.JoinNodes(f.nodes, rpctest.Blocks))
				after := scenarioState(t, f.coreNode, address)
				delete(before, "currentBlock")
				delete(after, "currentBlock")
				require.Equal(t, before, after, "cold node must recover the template runtime")
				reject(t, tmplcontract.InvokeAPIClose, nil, true)
			})
			t.Run("RPC_list_and_history", func(t *testing.T) { requireScenarioQueries(t, f, address, name) })
			f.requireNodesSynced(t)
		})
	}
}

func TestNetworkTemplateLimitOrderScenarioMatrix(t *testing.T) {
	f := newSignedTemplateFixture(t, map[string]int64{scenarioAssetA: 10000, scenarioAssetB: 10000})
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	address := scenarioDeploy(t, f, tmplcontract.NewLimitOrderContract(scenarioAssetA), "limit-scenarios", 0, scenarioFunding(t, 0, map[string]int64{gas: 10000}))
	for _, tc := range []struct {
		name, asset, amount, price string
		funding                    int64
	}{
		{"zero_amount", scenarioAssetA, "0", "10", 2},
		{"negative_amount", scenarioAssetA, "-1", "10", 2},
		{"zero_price", scenarioAssetA, "2", "0", 2},
		{"wrong_asset", scenarioAssetB, "2", "10", 2},
		{"insufficient_sell_funding", scenarioAssetA, "3", "10", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := scenarioState(t, f.bootstrapNode, address)["activeSellCount"]
			outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPISwap,
				templateLimitOrderParam(t, tc.asset, tmplcontract.OrderTypeSell, tc.amount, tc.price), scenarioFunding(t, 0, map[string]int64{scenarioAssetA: tc.funding}))
			require.Equal(t, before, scenarioState(t, f.bootstrapNode, address)["activeSellCount"])
			requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetA, fmt.Sprint(tc.funding))
		})
	}
	t.Run("open_sell_order", func(t *testing.T) {
		scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPISwap,
			templateLimitOrderParam(t, scenarioAssetA, tmplcontract.OrderTypeSell, "10", "10"), scenarioFunding(t, 0, map[string]int64{scenarioAssetA: 10}))
		require.EqualValues(t, 1, scenarioState(t, f.bootstrapNode, address)["activeSellCount"])
	})
	t.Run("another_actor_cannot_refund_orders", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIRefund, templateRefundParam(t, nil), scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "")
		require.EqualValues(t, 1, scenarioState(t, f.bootstrapNode, address)["activeSellCount"])
	})
	t.Run("partial_fill_refunds_price_improvement_and_overpayment", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPISwap,
			templateLimitOrderParam(t, scenarioAssetA, tmplcontract.OrderTypeBuy, "4", "12"), scenarioFunding(t, 60, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "4")
		requireTemplateResultValue(t, outputs, f.traderAActor.address, 40)
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 20)
		require.EqualValues(t, 1, scenarioState(t, f.bootstrapNode, address)["activeSellCount"])
	})
	t.Run("owner_refunds_remaining_six", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIRefund, templateRefundParam(t, nil), scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetA, "6")
		require.EqualValues(t, 0, scenarioState(t, f.bootstrapNode, address)["activeSellCount"])
	})
	t.Run("repeated_refund_does_not_duplicate_principal", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIRefund, templateRefundParam(t, nil), scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetA, "")
	})
	t.Run("non_crossing_buy_is_retained", func(t *testing.T) {
		scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPISwap,
			templateLimitOrderParam(t, scenarioAssetA, tmplcontract.OrderTypeSell, "2", "10"), scenarioFunding(t, 0, map[string]int64{scenarioAssetA: 2}))
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPISwap,
			templateLimitOrderParam(t, scenarioAssetA, tmplcontract.OrderTypeBuy, "2", "9"), scenarioFunding(t, 18, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "")
		state := scenarioState(t, f.bootstrapNode, address)
		require.EqualValues(t, 1, state["activeBuyCount"])
		require.EqualValues(t, 1, state["activeSellCount"])
	})
	t.Run("close_refunds_both_sides_to_their_owners", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIClose, nil, scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetA, "2")
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 18)
		state := scenarioState(t, f.bootstrapNode, address)
		require.EqualValues(t, 0, state["activeBuyCount"])
		require.EqualValues(t, 0, state["activeSellCount"])
	})
}

func TestNetworkTemplateAMMLiquidityScenarioMatrix(t *testing.T) {
	f := newSignedTemplateFixture(t, map[string]int64{scenarioAssetA: 10000})
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	address := scenarioDeploy(t, f, tmplcontract.NewAMMContract(scenarioAssetA, "100", 100, "10000"), "amm-scenarios", 0,
		scenarioFunding(t, 100, map[string]int64{gas: 10000, scenarioAssetA: 100}))
	t.Run("initial_pool_and_LP_owner", func(t *testing.T) {
		view := scenarioAMMView(t, f, address)
		require.True(t, view.TradingReady)
		require.Equal(t, "100", view.AssetAInPool)
		require.Equal(t, "100", view.AssetBInPool)
		require.Equal(t, "10000", view.K)
		require.Len(t, view.LPBalances, 1)
		require.NotEmpty(t, view.LPBalances[f.traderAActor.address])
	})
	for _, tc := range []struct {
		name, action string
		param        func(*testing.T) []byte
		value, asset int64
	}{
		{"unsupported_refund", tmplcontract.InvokeAPIRefund, func(t *testing.T) []byte { return templateRefundParam(t, nil) }, 0, 0},
		{"remove_without_LP", tmplcontract.InvokeAPIRemoveLiquidity, func(t *testing.T) []byte { return templateRemoveLiquidityParam(t, scenarioAssetA, "1") }, 0, 0},
		{"add_insufficient_asset", tmplcontract.InvokeAPIAddLiquidity, func(t *testing.T) []byte { return templateAddLiquidityParam(t, scenarioAssetA, "3", 2) }, 2, 2},
		{"add_insufficient_satoshis", tmplcontract.InvokeAPIAddLiquidity, func(t *testing.T) []byte { return templateAddLiquidityParam(t, scenarioAssetA, "2", 3) }, 2, 2},
		{"buy_min_output_unmet", tmplcontract.InvokeAPISwap, func(t *testing.T) []byte {
			return templateLimitOrderParam(t, scenarioAssetA, tmplcontract.OrderTypeBuy, "50", "100")
		}, 10, 0},
		{"sell_min_price_unmet", tmplcontract.InvokeAPISwap, func(t *testing.T) []byte {
			return templateLimitOrderParam(t, scenarioAssetA, tmplcontract.OrderTypeSell, "2", "1000")
		}, 0, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := scenarioAMMView(t, f, address)
			outputs := scenarioInvoke(t, f, f.traderB, address, tc.action, tc.param(t), scenarioFunding(t, tc.value, map[string]int64{scenarioAssetA: tc.asset}))
			after := scenarioAMMView(t, f, address)
			require.Equal(t, before.AssetAInPool, after.AssetAInPool)
			require.Equal(t, before.AssetBInPool, after.AssetBInPool)
			require.Equal(t, before.K, after.K)
			require.Equal(t, before.LPBalances, after.LPBalances)
			want := ""
			if tc.asset > 0 {
				want = fmt.Sprint(tc.asset)
			}
			requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, want)
			requireTemplateResultValue(t, outputs, f.traderBActor.address, tc.value)
		})
	}
	t.Run("unbalanced_add_refunds_excess_asset", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIAddLiquidity, templateAddLiquidityParam(t, scenarioAssetA, "8", 6), scenarioFunding(t, 6, map[string]int64{scenarioAssetA: 8}))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "2")
		view := scenarioAMMView(t, f, address)
		require.Equal(t, "106", view.AssetAInPool)
		require.Equal(t, "106", view.AssetBInPool)
		require.Len(t, view.LPBalances, 2)
	})
	t.Run("partial_remove_preserves_remaining_LP", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIRemoveLiquidity, templateRemoveLiquidityParam(t, scenarioAssetA, "2"), scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "2")
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 2)
		view := scenarioAMMView(t, f, address)
		require.Equal(t, "4", view.LPBalances[f.traderBActor.address])
		require.Equal(t, "100", view.LPBalances[f.traderAActor.address])
		require.Equal(t, "104", view.AssetAInPool)
		require.Equal(t, "104", view.AssetBInPool)
	})
	t.Run("remove_exceeding_owned_LP_caps_at_balance", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIRemoveLiquidity, templateRemoveLiquidityParam(t, scenarioAssetA, "1000000"), scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "4")
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 4)
		after := scenarioAMMView(t, f, address)
		require.NotContains(t, after.LPBalances, f.traderBActor.address)
		require.Equal(t, "100", after.LPBalances[f.traderAActor.address])
		require.Equal(t, "100", after.AssetAInPool)
		require.Equal(t, "100", after.AssetBInPool)
		require.Equal(t, "10000", after.K)
	})
	t.Run("repeated_remove_cannot_pay_twice", func(t *testing.T) {
		before := scenarioAMMView(t, f, address)
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIRemoveLiquidity, templateRemoveLiquidityParam(t, scenarioAssetA, "1"), scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "")
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 0)
		after := scenarioAMMView(t, f, address)
		require.Equal(t, before.LPBalances, after.LPBalances)
		require.Equal(t, before.K, after.K)
	})
	t.Run("remove_all_own_LP_returns_exact_contribution", func(t *testing.T) {
		scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIAddLiquidity, templateAddLiquidityParam(t, scenarioAssetA, "6", 6), scenarioFunding(t, 6, map[string]int64{scenarioAssetA: 6}))
		view := scenarioAMMView(t, f, address)
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIRemoveLiquidity, templateRemoveLiquidityParam(t, scenarioAssetA, view.LPBalances[f.traderBActor.address]), scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "6")
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 6)
		after := scenarioAMMView(t, f, address)
		require.Equal(t, "100", after.AssetAInPool)
		require.Equal(t, "100", after.AssetBInPool)
	})
	t.Run("close_returns_initial_LP_and_clears_pool", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIClose, nil, scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetA, "100")
		requireTemplateResultValue(t, outputs, f.traderAActor.address, 100)
		view := scenarioAMMView(t, f, address)
		require.True(t, view.Closed)
		require.False(t, view.TradingReady)
		require.Empty(t, view.LPBalances)
		require.Contains(t, []string{"", "0"}, view.AssetAInPool)
		require.Contains(t, []string{"", "0"}, view.AssetBInPool)
	})
}

func TestNetworkTemplateExchangeScenarioMatrix(t *testing.T) {
	f := newSignedTemplateFixture(t, map[string]int64{scenarioAssetA: 10000, scenarioAssetB: 10000})
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	for _, tc := range []struct {
		name, minOut          string
		stock, payment        int64
		out, proceeds, refund string
	}{
		{"explicit_minimum_exactly_met", "12", 100, 24, "12", "24", ""},
		{"minimum_not_met_refunds_all", "13", 100, 24, "", "", "24"},
		{"inventory_exhaustion_refunds_unused_payment", "", 5, 24, "5", "10", "14"},
		{"empty_inventory_refunds_all", "", 0, 24, "", "", "24"},
		{"integer_asset_rounding_refunds_dust", "", 100, 3, "1", "2", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address := scenarioDeploy(t, f, tmplcontract.NewExchangeContract(scenarioAssetA, scenarioAssetB, tmplcontract.ExchangePriceModeHeight,
				[]tmplcontract.ExchangePriceStep{{Threshold: "0", BPerA: "2"}}), tc.name, 0,
				scenarioFunding(t, 0, map[string]int64{gas: 10000, scenarioAssetA: tc.stock}))
			param, err := (&tmplcontract.ExchangeInvokeParam{MinOutA: tc.minOut}).Encode()
			require.NoError(t, err)
			outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIExchange, param, scenarioFunding(t, 0, map[string]int64{scenarioAssetB: tc.payment}))
			requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, tc.out)
			requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetB, tc.proceeds)
			requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetB, tc.refund)
		})
	}
	t.Run("sold_amount_crosses_multiple_tiers_in_one_call", func(t *testing.T) {
		address := scenarioDeploy(t, f, tmplcontract.NewExchangeContract(scenarioAssetA, scenarioAssetB, tmplcontract.ExchangePriceModeSoldA,
			[]tmplcontract.ExchangePriceStep{{Threshold: "0", BPerA: "2"}, {Threshold: "10", BPerA: "3"}}), "sold-cross", 0,
			scenarioFunding(t, 0, map[string]int64{gas: 10000, scenarioAssetA: 100}))
		outputs := scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 0, map[string]int64{scenarioAssetB: 50}))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "20")
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetB, "50")
		outputs = scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 0, map[string]int64{scenarioAssetB: 30}))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "10")
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetB, "30")
	})
	t.Run("sold_amount_exact_threshold_uses_next_price", func(t *testing.T) {
		address := scenarioDeploy(t, f, tmplcontract.NewExchangeContract(scenarioAssetA, scenarioAssetB, tmplcontract.ExchangePriceModeSoldA,
			[]tmplcontract.ExchangePriceStep{{Threshold: "0", BPerA: "2"}, {Threshold: "10", BPerA: "3"}}), "sold-boundary", 0,
			scenarioFunding(t, 0, map[string]int64{gas: 10000, scenarioAssetA: 100}))
		outputs := scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 0, map[string]int64{scenarioAssetB: 20}))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "10")
		outputs = scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 0, map[string]int64{scenarioAssetB: 3}))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "1")
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetB, "3")
	})
	t.Run("height_threshold_switches_price", func(t *testing.T) {
		height, err := f.bootstrapNode.Client.GetBlockCount()
		require.NoError(t, err)
		threshold := height + 6
		address := scenarioDeploy(t, f, tmplcontract.NewExchangeContract(scenarioAssetA, scenarioAssetB, tmplcontract.ExchangePriceModeHeight,
			[]tmplcontract.ExchangePriceStep{{Threshold: "0", BPerA: "2"}, {Threshold: fmt.Sprint(threshold), BPerA: "3"}}), "height-boundary", 0,
			scenarioFunding(t, 0, map[string]int64{gas: 10000, scenarioAssetA: 100}))
		outputs := scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 0, map[string]int64{scenarioAssetB: 20}))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "10")
		for {
			current, err := f.bootstrapNode.Client.GetBlockCount()
			require.NoError(t, err)
			if current >= threshold {
				break
			}
			scenarioMineBlocks(t, f, 1)
		}
		outputs = scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 0, map[string]int64{scenarioAssetB: 18}))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "6")
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetB, "18")
	})
	t.Run("satoshi_payment_and_change", func(t *testing.T) {
		address := scenarioDeploy(t, f, tmplcontract.NewExchangeContract(scenarioAssetA, tmplcontract.SatoshiAssetName, tmplcontract.ExchangePriceModeHeight,
			[]tmplcontract.ExchangePriceStep{{Threshold: "0", BPerA: "2"}}), "satoshi-payment", 0,
			scenarioFunding(t, 0, map[string]int64{gas: 10000, scenarioAssetA: 5}))
		outputs := scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 13, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "5")
		requireTemplateResultValue(t, outputs, f.traderAActor.address, 10)
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 3)
	})
}

func TestNetworkTemplateAutopayScenarioMatrix(t *testing.T) {
	f := newSignedTemplateFixture(t, nil)
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	funding := func(amount int64) wire.TxOut { return scenarioFunding(t, 0, map[string]int64{gas: amount}) }
	address := scenarioDeploy(t, f, tmplcontract.NewAutopayContract("gas-isolation", f.traderBActor.address, gas, "10"), "autopay-scenarios", 0, funding(50))
	config := func(t *testing.T, amount, reserve string, limit uint32) []byte {
		param, err := (&tmplcontract.AutopayConfigInvokeParam{AmountPerBlock: amount, GasFundingAmount: reserve, BlobKeyLimit: limit}).Encode()
		require.NoError(t, err)
		return param
	}
	waitBlocks := func(t *testing.T, count int64) {
		scenarioMineBlocks(t, f, int(count))
	}
	t.Run("default_funding_registers_delegate_and_preserves_principal", func(t *testing.T) {
		scenarioInvoke(t, f, f.traderB, address, "", nil, funding(1050))
		view := scenarioAutopayView(t, f.bootstrapNode, address)
		require.Equal(t, "1000", view.Delegates[f.traderBActor.address].Balance)
		require.Equal(t, "10", view.Delegates[f.traderBActor.address].AmountPerBlock)
		require.EqualValues(t, 1, view.Delegates[f.traderBActor.address].BlobKeyLimit)
	})
	t.Run("principal_never_pays_operating_gas", func(t *testing.T) {
		waitBlocks(t, 2)
		view := scenarioAutopayView(t, f.bootstrapNode, address)
		require.Zero(t, view.PaidBlocks)
		require.Equal(t, "1000", view.FeeBalance)
		require.Contains(t, []string{"", "0"}, view.GasBalance)
	})
	for _, tc := range []struct {
		name, amount, reserve string
		limit                 uint32
	}{
		{"zero_rate", "0", "", 1}, {"below_minimum", "9", "", 1},
		{"blob_limit_exceeds_1024", "10", "", 1025}, {"fractional_integer_asset_rate", "10.5", "", 1},
		{"gas_only_cannot_change_blob_limit", "", "200", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := scenarioAutopayView(t, f.bootstrapNode, address)
			scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIConfig, config(t, tc.amount, tc.reserve, tc.limit), funding(50))
			after := scenarioAutopayView(t, f.bootstrapNode, address)
			require.Equal(t, before.Delegates, after.Delegates)
			require.Equal(t, before.GasBalance, after.GasBalance)
			require.Equal(t, before.InvokeCount, after.InvokeCount)
		})
	}
	t.Run("maximum_blob_limit_and_minimum_rate_are_accepted", func(t *testing.T) {
		scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIConfig, config(t, "10", "", 1024), funding(50))
		view := scenarioAutopayView(t, f.bootstrapNode, address)
		require.EqualValues(t, 1024, view.Delegates[f.traderBActor.address].BlobKeyLimit)
		require.Equal(t, "1000", view.Delegates[f.traderBActor.address].Balance)
	})
	t.Run("non_owner_cannot_fund_operating_gas", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIConfig, config(t, "", "200", 0), funding(250))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, gas, "200")
		require.Zero(t, scenarioAutopayView(t, f.bootstrapNode, address).PaidBlocks)
	})
	t.Run("one_trigger_budget_pays_exactly_one_block", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIConfig, config(t, "", "200", 0), funding(250))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, gas, "10")
		view := scenarioAutopayView(t, f.bootstrapNode, address)
		require.EqualValues(t, 1, view.PaidBlocks)
		require.Equal(t, "990", view.FeeBalance)
		require.Equal(t, "10", view.Delegates[f.traderBActor.address].TotalPaid)
	})
	t.Run("exhaustion_pauses_and_survives_restart", func(t *testing.T) {
		waitBlocks(t, 2)
		before := scenarioAutopayView(t, f.coreNode, address)
		require.Equal(t, scenarioAutopayView(t, f.bootstrapNode, address), before)
		require.NoError(t, f.coreNode.Restart(false))
		// Check the restarted node before reconnecting or advancing the chain.
		// Peer synchronization must not conceal incomplete local recovery.
		recovered := scenarioAutopayView(t, f.coreNode, address)
		require.Equal(t, before, recovered, "core must recover the complete persisted Autopay state")
		require.NoError(t, rpctest.ConnectNode(f.coreNode, f.bootstrapNode))
		require.NoError(t, rpctest.JoinNodes(f.nodes, rpctest.Blocks))
		waitBlocks(t, 1)
		after := scenarioAutopayView(t, f.coreNode, address)
		require.Equal(t, before.PaidBlocks, after.PaidBlocks)
		require.Equal(t, before.Delegates, after.Delegates)
		require.Equal(t, before.Status, after.Status)
		require.Equal(t, before.GasBalance, after.GasBalance)
		require.Equal(t, "990", after.FeeBalance)
		require.Equal(t, scenarioAutopayView(t, f.bootstrapNode, address), after)
	})
	t.Run("refill_resumes_without_catching_up_missed_blocks", func(t *testing.T) {
		scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIConfig, config(t, "", "200", 0), funding(250))
		view := scenarioAutopayView(t, f.bootstrapNode, address)
		require.EqualValues(t, 2, view.PaidBlocks)
		require.Equal(t, "980", view.FeeBalance)
	})
	t.Run("cancel_returns_only_own_unused_balance", func(t *testing.T) {
		scenarioInvoke(t, f, f.traderA, address, "", nil, funding(1050))
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPICancel, nil, funding(50))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, gas, "980")
		require.Equal(t, tmplcontract.AutopayStatusClosed, scenarioAutopayView(t, f.bootstrapNode, address).Delegates[f.traderBActor.address].Status)
		require.Equal(t, "1000", scenarioAutopayView(t, f.bootstrapNode, address).Delegates[f.traderAActor.address].Balance)
	})
	t.Run("repeat_cancel_cannot_refund_twice", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPICancel, nil, funding(50))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, gas, "")
	})
	t.Run("config_and_refund_after_cancel_reactivates_delegate", func(t *testing.T) {
		scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIConfig, config(t, "20", "", 1), funding(1050))
		view := scenarioAutopayView(t, f.bootstrapNode, address)
		require.Equal(t, "20", view.Delegates[f.traderBActor.address].AmountPerBlock)
		require.Equal(t, "1000", view.Delegates[f.traderBActor.address].Balance)
		require.Equal(t, "1000", view.Delegates[f.traderAActor.address].Balance)
		require.Equal(t, "2000", view.FeeBalance)
	})
	t.Run("close_returns_delegate_principal", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIClose, nil, funding(50))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, gas, "1000")
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, gas, "1000")
		view := scenarioAutopayView(t, f.bootstrapNode, address)
		require.True(t, view.Closed)
		require.Equal(t, tmplcontract.AutopayStatusClosed, view.Status)
	})
}

func TestNetworkTemplateAMMDefaultSwapAndCloseOwners(t *testing.T) {
	f := newSignedTemplateFixture(t, map[string]int64{scenarioAssetA: 10000})
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	address := scenarioDeploy(t, f, tmplcontract.NewAMMContract(scenarioAssetA, "100", 100, "10000"), "amm-default-roundtrip", 0,
		scenarioFunding(t, 100, map[string]int64{gas: 10000, scenarioAssetA: 100}))
	t.Run("default_buy_has_exact_constant_product_output", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 100, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "49")
		view := scenarioAMMView(t, f, address)
		require.Equal(t, "51", view.AssetAInPool)
		require.Equal(t, "200", view.AssetBInPool)
		require.Equal(t, "10200", view.K)
	})
	t.Run("default_sell_reprices_using_updated_reserves", func(t *testing.T) {
		outputs := scenarioInvoke(t, f, f.traderB, address, "", nil, scenarioFunding(t, 0, map[string]int64{scenarioAssetA: 49}))
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 97)
		view := scenarioAMMView(t, f, address)
		require.Equal(t, "100", view.AssetAInPool)
		require.Equal(t, "103", view.AssetBInPool)
		require.Equal(t, "10300", view.K)
	})
	t.Run("two_LP_close_refunds_each_owner", func(t *testing.T) {
		// Use an independent, untraded pool so the exact principal assertion is
		// separate from the fee/profit arithmetic already exercised above.
		address := scenarioDeploy(t, f, tmplcontract.NewAMMContract(scenarioAssetA, "100", 100, "10000"), "amm-two-owner-close", 0,
			scenarioFunding(t, 100, map[string]int64{gas: 10000, scenarioAssetA: 100}))
		scenarioInvoke(t, f, f.traderB, address, tmplcontract.InvokeAPIAddLiquidity, templateAddLiquidityParam(t, scenarioAssetA, "20", 20),
			scenarioFunding(t, 20, map[string]int64{scenarioAssetA: 20}))
		outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIClose, nil, scenarioFunding(t, 0, nil))
		requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetA, "100")
		requireTemplateResultAssetAmount(t, outputs, f.traderBActor.address, scenarioAssetA, "20")
		requireTemplateResultValue(t, outputs, f.traderAActor.address, 100)
		requireTemplateResultValue(t, outputs, f.traderBActor.address, 20)
		require.True(t, scenarioAMMView(t, f, address).Closed)
	})
}

func TestNetworkTemplateDeploymentRejectsInvalidContent(t *testing.T) {
	f := newSignedTemplateFixture(t, map[string]int64{scenarioAssetA: 10000})
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	for _, tc := range []struct {
		name     string
		contract tmplcontract.Contract
	}{
		{"limit_invalid_asset", tmplcontract.NewLimitOrderContract("not-an-asset")},
		{"amm_inconsistent_K", tmplcontract.NewAMMContract(scenarioAssetA, "100", 100, "9999")},
		{"amm_zero_reserve", tmplcontract.NewAMMContract(scenarioAssetA, "0", 100, "0")},
		{"exchange_same_assets", tmplcontract.NewExchangeContract(scenarioAssetA, scenarioAssetA, tmplcontract.ExchangePriceModeHeight, []tmplcontract.ExchangePriceStep{{Threshold: "0", BPerA: "2"}})},
		{"exchange_unsorted_thresholds", tmplcontract.NewExchangeContract(scenarioAssetA, scenarioAssetB, tmplcontract.ExchangePriceModeHeight, []tmplcontract.ExchangePriceStep{{Threshold: "10", BPerA: "2"}, {Threshold: "0", BPerA: "3"}})},
		{"autopay_zero_minimum", tmplcontract.NewAutopayContract("invalid", f.traderBActor.address, scenarioAssetA, "0")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			funding := scenarioFunding(t, 7, map[string]int64{gas: 10000, scenarioAssetA: 2})
			inputs := scenarioSelectFunding(t, f, f.traderAActor, funding, contractcommon.DeployBaseGas)
			content, err := tc.contract.Encode()
			if autopay, ok := tc.contract.(*tmplcontract.AutopayContract); ok {
				// The public constructor rejects this content locally. Build the
				// same script bytes explicitly to exercise node-side rejection.
				content, err = txscript.NewScriptBuilder().
					AddData([]byte(autopay.ServiceName)).AddData([]byte(autopay.Recipient)).
					AddData([]byte(autopay.FeeAssetName)).AddData([]byte(autopay.MinAmountPerBlock)).Script()
			}
			require.NoError(t, err)
			tx, address, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
				ContractPrefix: tmplcontract.TestnetContractPrefix, Type: contractcommon.ContractTypeTemplate,
				SubType: tc.contract.TemplateName(), Version: tc.contract.Version(), ContractContent: content,
				Deployer: f.traderAActor.address, DeployNonce: deployNonceFromBytes([]byte(tc.name)),
				GasLimit: networkTemplateDeployGasLimit(), Inputs: inputs, Funding: funding,
			})
			require.NoError(t, err)
			signScenarioInputsWithChange(t, f, tx, f.traderA, contractcommon.DeployBaseGas, 0)
			f.sendAndWaitTx(t, tx)
			outputs := requireScenarioResult(t, f, tx, address)
			requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetA, "2")
			requireTemplateResultValue(t, outputs, f.traderAActor.address, 7)
			param, err := json.Marshal(address.MustEncode())
			require.NoError(t, err)
			raw, err := f.bootstrapNode.Client.RawRequest("getcontractstate", []json.RawMessage{param})
			require.NoError(t, err)
			var response struct {
				State   interface{} `json:"state"`
				Details struct {
					Exists bool `json:"exists"`
				} `json:"details"`
			}
			require.NoError(t, json.Unmarshal(raw, &response))
			require.False(t, response.Details.Exists, "invalid deployment must not create a callable runtime")
			require.Nil(t, response.State)
		})
	}
}

// A non-numeric opcode must not be silently interpreted as order ID zero.
// Keep this regression in the default gate to protect refund decoding.
func TestNetworkTemplateRefundRejectsNonNumericOpcode(t *testing.T) {
	f := newSignedTemplateFixture(t, map[string]int64{scenarioAssetA: 10000})
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	address := scenarioDeploy(t, f, tmplcontract.NewLimitOrderContract(scenarioAssetA), "refund-opcode", 0,
		scenarioFunding(t, 0, map[string]int64{gas: 10000}))
	scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPISwap,
		templateLimitOrderParam(t, scenarioAssetA, tmplcontract.OrderTypeSell, "2", "10"), scenarioFunding(t, 0, map[string]int64{scenarioAssetA: 2}))
	before := scenarioState(t, f.bootstrapNode, address)
	outputs := scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIRefund, []byte{0xff}, scenarioFunding(t, 7, map[string]int64{scenarioAssetA: 1}))
	after := scenarioState(t, f.bootstrapNode, address)
	require.Equal(t, before["invokeCount"], after["invokeCount"], "invalid opcode must be rejected before applying a refund")
	require.EqualValues(t, 1, after["activeSellCount"], "malformed refund must preserve the existing order")
	requireTemplateResultAssetAmount(t, outputs, f.traderAActor.address, scenarioAssetA, "1")
	requireTemplateResultValue(t, outputs, f.traderAActor.address, 7)
}
