package template

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestReviewSettlementItemIDsAreContractLocal(t *testing.T) {
	a := testTemplateContract(t)
	hash := make([]byte, AddressHashLen)
	hash[0] = 2
	b, err := contractcommon.NewContractAddressFromHash(TestnetContractPrefix, AddressVersionV1, ContractTypeTemplate, hash)
	require.NoError(t, err)
	records := []ExecutionRecord{
		{Contract: a, ItemIDs: []int64{0}, FundingInputs: []OutPoint{{TxID: testHash(1)}}, GasFee: scommon.NewDefaultDecimal(1), RequiresResult: true},
		{Contract: b, ItemIDs: []int64{0}, FundingInputs: []OutPoint{{TxID: testHash(2)}}, GasFee: scommon.NewDefaultDecimal(2), RequiresResult: true},
	}
	plans := []*SettlementPlan{{Contract: a.MustEncode(), ItemIDs: []int64{0}, Transfers: []SettlementTransfer{{ItemID: 0, To: "buyer-a", AssetName: "ordx:f:test", AssetAmt: "1"}}}}
	t.Run("funding_and_fee", func(t *testing.T) {
		results, err := BuildSettlementResultPlans(plans, records, nil)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, records[0].FundingInputs, results[0].Inputs)
		require.Equal(t, "1", results[0].GasFee.String())
	})
	t.Run("missing_result", func(t *testing.T) {
		results := AddMissingGasResultPlans([]ResultPlan{{Contract: a.MustEncode(), ItemIDs: []int64{0}}}, records)
		require.Len(t, results, 2)
		require.Equal(t, b.MustEncode(), results[1].Contract)
		require.Equal(t, records[1].FundingInputs, results[1].Inputs)
	})
	t.Run("asset_intents", func(t *testing.T) {
		backend := &Backend{records: records}
		got, err := backend.recordsWithSettlementAssetIntents(plans)
		require.NoError(t, err)
		require.Len(t, got[0].AssetIntents, 1)
		require.Empty(t, got[1].AssetIntents)
	})
}

func TestReviewExchangeIndivisibleAssetRefund(t *testing.T) {
	contract := testExchangeContract()
	deploy, addr := testTemplateDeployTx(t, contract)
	fund := testTemplateDefaultInvokeTx(t, addr, 0, testAsset(contract.AssetAName, 100))
	buy := testTemplateDefaultInvokeTx(t, addr, 0, testAsset(contract.AssetBName, 1))
	store := NewRuntimeStore()
	result, err := ExecuteBlock(BlockExecutionRequest{
		Txs: []*wire.MsgTx{deploy, fund, buy}, Store: store, BlockHeight: 10,
		GasConfig: exchangeTestGasConfig(DefaultGasConfig().GasAssetName),
		AssetPrecision: func(name string) (int, bool) {
			if name == contract.AssetAName {
				return 0, true
			}
			return 2, true
		},
		ResolveInvoker: func(tx *wire.MsgTx, parsed Tx) (string, error) {
			if parsed.Kind == TxTypeDeploy {
				return "deployer-address", nil
			}
			if tx == buy {
				return "buyer-address", nil
			}
			return "funder-address", nil
		},
	})
	require.NoError(t, err)
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	requireDecimalString(t, "100", state.ExchangeData().AssetAInPool)
	require.Len(t, result.SettlementPlans, 1)
	require.Len(t, result.SettlementPlans[0].Transfers, 1)
	transfer := result.SettlementPlans[0].Transfers[0]
	require.Equal(t, "buyer-address", transfer.To)
	require.Equal(t, contract.AssetBName, transfer.AssetName)
	require.Equal(t, "0.99", transfer.AssetAmt)
}

func TestReviewExchangePrecisionConservesPartialFill(t *testing.T) {
	contract := testExchangeContract()
	precision := func(name string) (int, bool) {
		if name == contract.AssetAName {
			return 0, true
		}
		return 2, true
	}
	quote := exchangeQuote(contract, 10, "0", parseDecimalOrZero("100"), parseDecimalOrZero("3.99"), precision)
	requireDecimalString(t, "1", quote.OutA)
	requireDecimalString(t, "2", quote.SpentB)
	// Rounded payment must cover the actual integer output at the tier price.
	contract.Steps[0].BPerA = "0.333"
	quote = exchangeQuote(contract, 10, "0", parseDecimalOrZero("100"), parseDecimalOrZero("1"), precision)
	requireDecimalString(t, "3", quote.OutA)
	requireDecimalString(t, "1", quote.SpentB)
}
