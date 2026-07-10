package evm

import (
	"testing"

	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestExecuteWorkBlockExposesCurrentFundingOnce(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	gasConfig := DefaultGasConfig()
	tx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "10", nil),
	}, gasConfig.GasAssetName, gasConfig.InvokeBaseGas)
	appendFundingOrderAsset(t, tx, fundingOrderTestAsset, 10)

	result, err := ExecuteWorkBlock(BlockExecutionRequest{
		Txs:            []*wire.MsgTx{tx},
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          fundingOrderBlockContext(gasConfig),
		ResolveCaller:  fixedCaller(caller),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(nil,
			[]*wire.MsgTx{tx}, TestnetContractPrefix, ContractTypeEVM),
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.Len(t, result.Records[0].AssetIntents, 1)
	intent := result.Records[0].AssetIntents[0]
	require.Equal(t, fundingOrderTestAsset, intent.AssetName)
	require.Equal(t, "tb1qdest", intent.To)
	require.Equal(t, "10", intent.Amount.String())
}
