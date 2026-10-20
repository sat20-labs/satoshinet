package framework

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestResultAggregationPreservesOrderDuplicatesAndIsolation(t *testing.T) {
	a, b, c := OutPoint{TxID: "a"}, OutPoint{TxID: "b"}, OutPoint{TxID: "c"}
	coin := UTXOFromTxOutput(a, testContractAddress(t, ModuleTemplate, 7), 10, wire.NewTxOut(1, nil, []byte{0x51}))
	first := ResultPlan{
		Contract: "one", ItemIDs: []int64{2, 2, 1}, Inputs: []OutPoint{a, a},
		CallFunding: []OutPoint{a, a}, RefundFunding: []OutPoint{a, a},
		InputUTXOs: []UTXO{coin, coin}, GasFee: scommon.NewDefaultDecimal(3), SatoshiFee: 2,
		InputScope: ResultInputScopeExplicit, ManagedRemainder: &contract.ManagedBalance{Value: 1},
		Outputs: []ResultOutput{{To: "first", ExtraData: []byte{1}}},
	}
	other := ResultPlan{Contract: "two", Height: 9, Inputs: []OutPoint{b, b}, ManagedRemainder: &contract.ManagedBalance{Value: 2}}
	moreCoin := coin.Clone()
	moreCoin.OutPoint = b
	plans := []ResultPlan{first, {Outputs: []ResultOutput{{To: "anonymous"}}}, other, {
		Contract: "one", Height: 11, ResultCount: 2, ItemIDs: []int64{1, 3, 3},
		Inputs: []OutPoint{b, a}, CallFunding: []OutPoint{c, a}, RefundFunding: []OutPoint{b, a},
		InputUTXOs: []UTXO{coin, moreCoin, moreCoin}, GasFee: scommon.NewDefaultDecimal(4), SatoshiFee: 5,
		Outputs: []ResultOutput{{To: "second"}}, FeeOutputs: []ResultOutput{{To: "fee"}},
	}, {Contract: "one", ItemIDs: []int64{4, 3}, Inputs: []OutPoint{c}}}
	before := CloneResultPlans(plans)
	got := MergeResultPlansByContract(plans)
	require.Len(t, got, 3)
	require.Equal(t, "one", got[0].Contract)
	require.Empty(t, got[1].Contract)
	require.Equal(t, "two", got[2].Contract)
	require.Equal(t, 4, got[0].ResultCount)
	require.EqualValues(t, 11, got[0].Height)
	require.Equal(t, []int64{2, 2, 1, 3, 4}, got[0].ItemIDs)
	require.Equal(t, []OutPoint{a, b, c}, got[0].Inputs)
	require.Equal(t, []OutPoint{a, c}, got[0].CallFunding)
	require.Equal(t, []OutPoint{a, b}, got[0].RefundFunding)
	require.Equal(t, []UTXO{coin, coin, moreCoin}, got[0].InputUTXOs)
	require.Equal(t, "7", got[0].GasFee.String())
	require.EqualValues(t, 7, got[0].SatoshiFee)
	require.Equal(t, ResultInputScopeAllContractUTXOs, got[0].InputScope)
	require.Nil(t, got[0].ManagedRemainder)
	require.Equal(t, []ResultOutput{{To: "first", ExtraData: []byte{1}}, {To: "second"}}, got[0].Outputs)
	require.Equal(t, []ResultOutput{{To: "fee"}}, got[0].FeeOutputs)
	require.Equal(t, []OutPoint{b, b}, got[2].Inputs, "unmerged plan is cloned without normalization")
	require.EqualValues(t, 2, got[2].ManagedRemainder.Value)
	got[0].Outputs[0].ExtraData[0]++
	got[0].InputUTXOs[2].TxOutput.OutValue.Value++
	got[2].ManagedRemainder.Value++
	require.Equal(t, before, CloneResultPlans(plans), "aggregation must not mutate caller-owned data")
}

func BenchmarkResultPlanAggregation(b *testing.B) {
	for _, n := range []int{128, 512, 1001} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			plans := make([]ResultPlan, n)
			for i := range plans {
				point := OutPoint{TxID: "work", Vout: uint32(i)}
				plans[i] = ResultPlan{Contract: "one", ItemIDs: []int64{int64(i)}, Inputs: []OutPoint{point}, CallFunding: []OutPoint{point}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MergeResultPlansByContract(plans)
			}
		})
	}
}
