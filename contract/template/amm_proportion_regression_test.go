package template

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
)

func TestAMMRemoveLiquidityUsesExactProportions(t *testing.T) {
	for _, precision := range []int{0, 6, 10} {
		t.Run(fmt.Sprintf("asset_precision_%d", precision), func(t *testing.T) {
			state := TemplateRuntimeState{Items: []InvokeItem{{
				ID: 0, Action: InvokeAPIRemoveLiquidity, OrderType: OrderTypeRemoveLiquidity,
				Reason: InvokeReasonNormal, Address: "alice", AssetName: "ordx:f:test",
				Param: mustRemoveLiquidityItemParam(t, "ordx:f:test", "2"),
			}}}
			running := state.AMMData()
			running.AssetAInPool = scommon.NewDecimal(106, precision)
			running.AssetBInPool = scommon.NewDefaultDecimal(106)
			running.TradingReady = true
			running.TotalLPTAmt = parseDecimalOrZero("106")
			running.LPBalances = map[string]*scommon.Decimal{"alice": parseDecimalOrZero("6"), "bob": parseDecimalOrZero("100")}
			running.LPCosts = map[string]int64{"alice": 12, "bob": 200}
			plan := &SettlementPlan{}
			changed, err := applyAMMLiquidity(&state, plan, "foundation")
			require.NoError(t, err)
			require.True(t, changed)
			require.Len(t, plan.Transfers, 1)
			require.Equal(t, "alice", plan.Transfers[0].To)
			requireDecimalString(t, "2", parseDecimalOrZero(plan.Transfers[0].AssetAmt))
			require.EqualValues(t, 2, plan.Transfers[0].SatValue)
			requireDecimalString(t, "104", running.AssetAInPool)
			requireDecimalString(t, "104", running.AssetBInPool)
			require.Equal(t, precision, running.AssetAInPool.Precision)
			requireDecimalString(t, "4", running.LPBalances["alice"])
			requireDecimalString(t, "100", running.LPBalances["bob"])
			require.EqualValues(t, 8, running.LPCosts["alice"])
			require.EqualValues(t, 200, running.LPCosts["bob"])
		})
	}
}

func TestAMMCloseLiquidityUsesExactProportions(t *testing.T) {
	for _, precision := range []int{0, 6, 10} {
		t.Run(fmt.Sprintf("asset_precision_%d", precision), func(t *testing.T) {
			var state TemplateRuntimeState
			running := state.AMMData()
			running.AssetAInPool = scommon.NewDecimal(120, precision)
			running.AssetBInPool = scommon.NewDefaultDecimal(120)
			running.TotalLPTAmt = parseDecimalOrZero("120")
			running.LPBalances = map[string]*scommon.Decimal{"alice": parseDecimalOrZero("100"), "bob": parseDecimalOrZero("20")}
			plan := &SettlementPlan{}
			require.NoError(t, appendAMMLPCloseTransfers(&state, plan, &InvokeItem{ID: 0}, "ordx:f:test"))
			require.Len(t, plan.Transfers, 2)
			require.Equal(t, "alice", plan.Transfers[0].To)
			requireDecimalString(t, "100", parseDecimalOrZero(plan.Transfers[0].AssetAmt))
			require.EqualValues(t, 100, plan.Transfers[0].SatValue)
			require.Equal(t, "bob", plan.Transfers[1].To)
			requireDecimalString(t, "20", parseDecimalOrZero(plan.Transfers[1].AssetAmt))
			require.EqualValues(t, 20, plan.Transfers[1].SatValue)
		})
	}
}

func TestAMMProportionalInt64UsesExactFraction(t *testing.T) {
	for _, test := range []struct {
		name        string
		value, want int64
		part, total *scommon.Decimal
	}{
		{"exact_pool_share", 106, 2, parseDecimalOrZero("2"), parseDecimalOrZero("106")},
		{"exact_cost_share", 12, 4, parseDecimalOrZero("2"), parseDecimalOrZero("6")},
		{"floor_final_fraction", 10, 3, parseDecimalOrZero("1"), parseDecimalOrZero("3")},
		{"fractional_LP", 3, 1, parseDecimalOrZero("0.2"), parseDecimalOrZero("0.4")},
		{"different_LP_precisions", 106, 2, scommon.NewDecimal(2, 0), scommon.NewDecimal(106, 10)},
		{"maximum_int64", 1<<63 - 1, 1<<63 - 1, parseDecimalOrZero("2"), parseDecimalOrZero("2")},
		{"zero_value", 0, 0, parseDecimalOrZero("2"), parseDecimalOrZero("6")},
		{"nil_part", 12, 0, nil, parseDecimalOrZero("6")},
		{"zero_total", 12, 0, parseDecimalOrZero("2"), parseDecimalOrZero("0")},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := proportionalInt64(test.value, test.part, test.total)
			require.NoError(t, err)
			require.Equal(t, test.want, out)
		})
	}
}
