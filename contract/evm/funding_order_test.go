package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBuildBlockResultTxsDeployConstructorSeesCurrentFunding(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	const deployNonce = uint64(3)
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, deployNonce)
	require.NoError(t, err)

	assetName := "ordx:f:deploy-funding"
	initCode := evmPrecompileCallAndReturnTrueCode(
		EncodeTransferAssetCall(assetName, "tb1qdest", "10", nil))
	deployTx := testDeployTx(t, deployNonce, initCode)
	addFundingAssetForTest(t, deployTx, assetName, 10)

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:                       []*wire.MsgTx{deployTx},
		Runtime:                   NewRuntime(nil),
		ContractPrefix:            TestnetContractPrefix,
		GasConfig:                 DefaultGasConfig(),
		Block:                     testBlockContext(100),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
		ResolveScript:             evmTestResultScriptResolver(t, contract),
		ResolveOutput:             evmTestResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Execution.Records[0].Status)
	require.Len(t, result.Execution.Records[0].AssetIntents, 1)
	require.Equal(t, assetName, result.Execution.Records[0].AssetIntents[0].AssetName)
	require.Equal(t, "10", result.Execution.Records[0].AssetIntents[0].Amount.String())
	require.Len(t, result.ResultTxs, 1)

	outputs, err := evmTestResultOutputResolver(contract)(result.ResultTxs[0])
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, "tb1qdest", assetName, "10")
}

func TestBuildBlockResultTxsDoesNotDoubleCountCurrentFunding(t *testing.T) {
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	gasConfig := GasConfig{
		GasAssetName:     DefaultGasConfig().GasAssetName,
		FixedGasPrice:    1,
		ResultPackingFee: 5,
		MaxGasPerBlock:   evmcommon.MaxGasPerBlock,
	}
	assetName := "ordx:f:funding-order"
	invokeTx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(assetName, "tb1qdest", "15", nil),
	}, gasConfig.GasAssetName, evmcommon.InvokeBaseGas)
	addFundingAssetForTest(t, invokeTx, assetName, 10)

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:                       []*wire.MsgTx{invokeTx},
		Runtime:                   runtime,
		ContractPrefix:            TestnetContractPrefix,
		GasConfig:                 gasConfig,
		Block:                     BlockContext{Number: 100, Time: 1, GasLimit: evmcommon.MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller:             fixedCaller(mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qdest"),
		ResolveScript:             evmTestResultScriptResolver(t, contract),
		ResolveOutput:             evmTestResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[0].Status)
	require.Len(t, result.Execution.Records[0].AssetIntents, 1)
	require.Equal(t, assetName, result.Execution.Records[0].AssetIntents[0].AssetName)
	require.Equal(t, "10", result.Execution.Records[0].AssetIntents[0].Amount.String())
	require.Len(t, result.ResultTxs, 1)

	outputs, err := evmTestResultOutputResolver(contract)(result.ResultTxs[0])
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, "tb1qdest", assetName, "10")
}

func TestBuildBlockResultTxsDoesNotExposeLaterFunding(t *testing.T) {
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	gasConfig := GasConfig{
		GasAssetName:     DefaultGasConfig().GasAssetName,
		FixedGasPrice:    1,
		ResultPackingFee: 5,
		MaxGasPerBlock:   evmcommon.MaxGasPerBlock,
	}
	assetName := "ordx:f:future-build-funding"
	first := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(assetName, "tb1qdest", "10", nil),
	}, gasConfig.GasAssetName, evmcommon.InvokeBaseGas)
	second := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 2,
	}, gasConfig.GasAssetName, evmcommon.InvokeBaseGas)
	addFundingAssetForTest(t, second, assetName, 10)

	preloaded := contractframework.ContractUTXOProviderWithTxOutputs(
		nil, []*wire.MsgTx{first, second}, TestnetContractPrefix, ContractTypeEVM)
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:                       []*wire.MsgTx{first, second},
		Runtime:                   runtime,
		ContractPrefix:            TestnetContractPrefix,
		GasConfig:                 gasConfig,
		Block:                     testBlockContext(100),
		ResolveCaller:             fixedCaller(mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qdest"),
		ContractUTXOs:             preloaded,
		ResolveScript:             evmTestResultScriptResolver(t, contract),
		ResolveOutput:             evmTestResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, result.Execution.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[0].Status)
	require.Empty(t, result.Execution.Records[0].AssetIntents)
	require.Equal(t, ResultStatusSuccess, result.Execution.Records[1].Status)
	require.Len(t, result.ResultTxs, 1)
}

func TestExecuteWorkBlockDoesNotExposeLaterFunding(t *testing.T) {
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	gasConfig := GasConfig{
		GasAssetName:   DefaultGasConfig().GasAssetName,
		FixedGasPrice:  1,
		MaxGasPerBlock: evmcommon.MaxGasPerBlock,
	}
	assetName := "ordx:f:future-funding"
	first := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(assetName, "tb1qdest", "10", nil),
	}, gasConfig.GasAssetName, evmcommon.InvokeBaseGas)
	second := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 2,
	}, gasConfig.GasAssetName, evmcommon.InvokeBaseGas)
	addFundingAssetForTest(t, second, assetName, 10)

	// This reproduces the production caller shape before the fix: a provider
	// preloaded with every work transaction in the block.
	preloaded := contractframework.ContractUTXOProviderWithTxOutputs(
		nil, []*wire.MsgTx{first, second}, TestnetContractPrefix, ContractTypeEVM)
	result, err := ExecuteWorkBlock(BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{first, second},
		Runtime:                   runtime,
		ContractPrefix:            TestnetContractPrefix,
		GasConfig:                 gasConfig,
		Block:                     BlockContext{Number: 100, Time: 1, GasLimit: evmcommon.MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller:             fixedCaller(mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qdest"),
		ContractUTXOs:             preloaded,
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
	require.Empty(t, result.Records[0].AssetIntents)
	require.Equal(t, ResultStatusSuccess, result.Records[1].Status)
}

func TestExecuteWorkBlockExposesPriorFundingToLaterCall(t *testing.T) {
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	gasConfig := GasConfig{
		GasAssetName:   DefaultGasConfig().GasAssetName,
		FixedGasPrice:  1,
		MaxGasPerBlock: evmcommon.MaxGasPerBlock,
	}
	assetName := "ordx:f:prior-funding"
	first := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
	}, gasConfig.GasAssetName, evmcommon.InvokeBaseGas)
	addFundingAssetForTest(t, first, assetName, 10)
	second := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 2,
		Param:     EncodeTransferAssetCall(assetName, "tb1qdest", "10", nil),
	}, gasConfig.GasAssetName, evmcommon.InvokeBaseGas)

	result, err := ExecuteWorkBlock(BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{first, second},
		Runtime:                   runtime,
		ContractPrefix:            TestnetContractPrefix,
		GasConfig:                 gasConfig,
		Block:                     BlockContext{Number: 100, Time: 1, GasLimit: evmcommon.MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller:             fixedCaller(mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qdest"),
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.Equal(t, ResultStatusSuccess, result.Records[1].Status)
	require.Len(t, result.Records[1].AssetIntents, 1)
	require.Equal(t, assetName, result.Records[1].AssetIntents[0].AssetName)
	require.Equal(t, "10", result.Records[1].AssetIntents[0].Amount.String())
}

func addFundingAssetForTest(t *testing.T, tx *wire.MsgTx, assetName string, amount int64) {
	t.Helper()
	require.NotNil(t, tx)
	require.GreaterOrEqual(t, len(tx.TxOut), 2)
	name := wire.NewAssetNameFromString(assetName)
	require.NotNil(t, name)
	tx.TxOut[1].Assets = append(tx.TxOut[1].Assets, wire.AssetInfo{
		Name:   *name,
		Amount: *scommon.NewDefaultDecimal(amount),
	})
}
