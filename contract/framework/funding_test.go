package framework

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	l2common "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestSummarizeBusinessFundingExcludesGas(t *testing.T) {
	out := testFundingOutput(100, testFundingAsset("brc20:f:sgas", 50), testFundingBoundAsset("ordx:f:test", 20, 1))

	summary, err := SummarizeBusinessFunding([]ContractOutput{out}, "brc20:f:sgas")
	require.NoError(t, err)
	require.Equal(t, int64(80), summary.PlainSat)
	require.Len(t, summary.Assets, 1)
	require.Equal(t, 0, summary.AssetAmount("ordx:f:test").Cmp(scommon.NewDefaultDecimal(20)))
	require.Equal(t, 0, summary.AssetAmount("brc20:f:sgas").Cmp(ZeroDecimal()))
}

func TestBusinessFundingSummaryRequiresDeclaredAmounts(t *testing.T) {
	out := testFundingOutput(200, testFundingBoundAsset("ordx:f:test", 100, 1))
	summary, err := SummarizeBusinessFunding([]ContractOutput{out}, "brc20:f:sgas")
	require.NoError(t, err)

	require.NoError(t, summary.RequirePlainSat(100, "add liquidity"))
	require.NoError(t, summary.RequireAsset("ordx:f:test", scommon.NewDefaultDecimal(100), "add liquidity"))
	require.Error(t, summary.RequirePlainSat(101, "add liquidity"))
	require.Error(t, summary.RequireAsset("ordx:f:test", scommon.NewDefaultDecimal(101), "add liquidity"))
}

func TestSummarizeBusinessFundingTreatsSatsAsGasWhenGasIsSats(t *testing.T) {
	out := testFundingOutput(200, testFundingBoundAsset("ordx:f:test", 100, 1))
	summary, err := SummarizeBusinessFunding([]ContractOutput{out}, contract.SatoshiAssetName)
	require.NoError(t, err)

	require.Equal(t, int64(0), summary.PlainSat)
	require.Equal(t, 0, summary.AssetAmount("ordx:f:test").Cmp(scommon.NewDefaultDecimal(100)))
}

func TestNonGasFundingRefundIntents(t *testing.T) {
	contractAddr := testFundingContractAddress(t)
	out := testFundingOutput(100,
		testFundingAsset("brc20:f:sgas", 50),
		testFundingBoundAsset("ordx:f:test", 20, 1))

	intents, err := NonGasFundingRefundIntents(contractAddr,
		[]ContractOutput{out}, "brc20:f:sgas", "tb1qrefund")
	require.NoError(t, err)
	require.Len(t, intents, 2)
	require.Equal(t, contract.SatoshiAssetName, intents[0].AssetName)
	require.Equal(t, "80", intents[0].Amount.String())
	require.Equal(t, "ordx:f:test", intents[1].AssetName)
	require.Equal(t, "20", intents[1].Amount.String())
}

func TestNonGasFundingRefundIntentsSkipsSatsWhenSatsIsGas(t *testing.T) {
	contractAddr := testFundingContractAddress(t)
	out := testFundingOutput(100, testFundingBoundAsset("ordx:f:test", 20, 1))

	intents, err := NonGasFundingRefundIntents(contractAddr,
		[]ContractOutput{out}, contract.SatoshiAssetName, "tb1qrefund")
	require.NoError(t, err)
	require.Len(t, intents, 1)
	require.Equal(t, "ordx:f:test", intents[0].AssetName)
	require.Equal(t, "20", intents[0].Amount.String())
}

func testFundingOutput(value int64, assets ...wire.AssetInfo) ContractOutput {
	outpoint := OutPoint{TxID: "tx", Vout: 1}
	return ContractOutput{
		OutPoint: outpoint,
		Vout:     outpoint.Vout,
		TxOutput: &l2common.TxOutput{
			OutPointStr: outpoint.String(),
			OutValue: wire.TxOut{
				Value:  value,
				Assets: assets,
			},
		},
	}
}

func testFundingAsset(name string, amount int64) wire.AssetInfo {
	return wire.AssetInfo{
		Name:   *wire.NewAssetNameFromString(name),
		Amount: *scommon.NewDefaultDecimal(amount),
	}
}

func testFundingBoundAsset(name string, amount int64, bindingSat uint32) wire.AssetInfo {
	asset := testFundingAsset(name, amount)
	asset.BindingSat = bindingSat
	return asset
}

func testFundingContractAddress(t *testing.T) contract.ContractAddress {
	t.Helper()
	addr, err := contract.NewContractAddress(contract.TestnetContractPrefix,
		contract.AddressVersionV1, contract.ContractTypeEVM, contract.EVMAddress{1})
	require.NoError(t, err)
	return addr
}
