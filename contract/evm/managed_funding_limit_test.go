package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestManagedCurrentFundingCannotOverspendDirectCall(t *testing.T) {
	const asset = fundingOrderTestAsset
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	addr := testContract(t)
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	runtime.SetCode(ContractAddressHash(addr), callAssetPrecompileCode())
	backend := NewBackend(BlockExecutionRequest{
		Runtime: runtime, GasConfig: DefaultGasConfig(), Block: fundingOrderBlockContext(DefaultGasConfig()),
		ResolveCaller: fixedCaller(caller),
		ResolveResultScript: func(ResultOutput) ([]byte, error) { return []byte{0x51}, nil },
	})
	funding := contractframework.ContractOutputFromFunding(contractcommon.FundingOutput{
		OutPoint: contractcommon.TxOutPoint{TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Vout: 0},
		Vout: 0, Contract: addr,
		Assets: wire.TxAssets{{Name: *wire.NewAssetNameFromString(asset), Amount: *scommon.NewDefaultDecimal(10)}},
	})
	result := backend.Runtime.Call(CallRequest{
		CallerAddress: caller.String(), TargetAddress: addr.MustEncode(), CallID: "overspend-current-funding",
		Input: EncodeTransferAssetCall(asset, "tb1qdest", "15", nil), Gas: DefaultGasConfig().InvokeBaseGas,
		FundingOutput: &funding, Block: fundingOrderBlockContext(DefaultGasConfig()),
	})
	require.Error(t, result.Err)
	require.NotEqual(t, ResultStatusSuccess, result.Status)
	require.Empty(t, backend.Runtime.AssetIntents)
}
