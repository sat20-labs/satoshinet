package template

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

func TestGasResultAggregationPreservesCoverageAndRecordAccounting(t *testing.T) {
	addr := testTemplateContract(t)
	a, b, c := OutPoint{TxID: testHash(1)}, OutPoint{TxID: testHash(2)}, OutPoint{TxID: testHash(3)}
	plans := []ResultPlan{{Contract: addr.MustEncode(), ItemIDs: []int64{9, 9}, Inputs: []OutPoint{a, a},
		GasFee: scommon.NewDefaultDecimal(2), InputScope: framework.ResultInputScopeExplicit}}
	records := []ExecutionRecord{
		{Contract: addr, RequiresResult: true, ItemIDs: []int64{9}, GasFee: scommon.NewDefaultDecimal(100)},
		{Contract: addr, RequiresResult: true, ItemIDs: []int64{1, 1}, FundingInputs: []OutPoint{b, b},
			GasFee: scommon.NewDefaultDecimal(3), ResultFeeMode: framework.ResultFeeModeGasAsset, GasRefundRecipient: "caller", CallID: "first"},
		// This shares an item with the preceding record, but is not covered by
		// the original settlement plan. Its fee and refund must still be kept.
		{Contract: addr, RequiresResult: true, ItemIDs: []int64{1}, FundingInputs: []OutPoint{c, b},
			GasFee: scommon.NewDefaultDecimal(4), ResultFeeMode: framework.ResultFeeModeGasAsset, GasRefundRecipient: "caller", CallID: "second"},
		{Contract: addr, RequiresResult: true, Status: ResultStatusSuccess, ItemIDs: []int64{2}},
	}
	before := framework.CloneResultPlans(plans)
	got := AddMissingGasResultPlans(plans, records)
	require.Len(t, got, 1)
	require.Equal(t, []int64{9, 9, 1, 2}, got[0].ItemIDs)
	require.Equal(t, []OutPoint{a, b, c}, got[0].Inputs)
	require.Equal(t, "9", got[0].GasFee.String())
	require.Equal(t, framework.ResultInputScopeAllContractUTXOs, got[0].InputScope)
	require.Len(t, got[0].GasRefunds, 2)
	require.Equal(t, "first", got[0].GasRefunds[0].CallID)
	require.Equal(t, "second", got[0].GasRefunds[1].CallID)
	require.Equal(t, before, framework.CloneResultPlans(plans))
	untouched := AddMissingGasResultPlans(plans, records[:1])
	require.Equal(t, []OutPoint{a, a}, untouched[0].Inputs, "covered-only plan retains its original duplicates")
}

func BenchmarkMissingGasAggregation(b *testing.B) {
	addr, err := contract.NewContractAddressFromHash(TestnetContractPrefix, AddressVersionV1, ContractTypeTemplate, make([]byte, AddressHashLen))
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range []int{128, 512, 1001} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			records := make([]ExecutionRecord, n)
			for i := range records {
				records[i] = ExecutionRecord{Contract: addr, RequiresResult: true, ItemIDs: []int64{int64(i)}, FundingInputs: []OutPoint{{TxID: "work", Vout: uint32(i)}}}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				AddMissingGasResultPlans(nil, records)
			}
		})
	}
}
