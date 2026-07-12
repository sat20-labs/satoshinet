package evm

import (
	"strings"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const fundingOrderTestAsset = "ordx:f:funding-order"

func TestExecuteWorkBlockCountsCurrentFundingOnce(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	gasConfig := DefaultGasConfig()
	tx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "15", nil),
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
	require.Equal(t, ResultStatusInvalid, result.Records[0].Status)
	require.Empty(t, result.Records[0].AssetIntents,
		"a transfer above the current funding must not succeed through double counting")
	require.Empty(t, runtime.AssetIntents)
}

func TestExecuteWorkBlockDoesNotExposeLaterFunding(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	gasConfig := DefaultGasConfig()
	spend := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "10", nil),
	}, gasConfig.GasAssetName, gasConfig.InvokeBaseGas)
	laterFunding := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 2,
	}, gasConfig.GasAssetName, gasConfig.InvokeBaseGas)
	appendFundingOrderAsset(t, laterFunding, fundingOrderTestAsset, 10)
	txs := []*wire.MsgTx{spend, laterFunding}

	result, err := ExecuteWorkBlock(BlockExecutionRequest{
		Txs:            txs,
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          fundingOrderBlockContext(gasConfig),
		ResolveCaller:  fixedCaller(caller),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(nil,
			txs, TestnetContractPrefix, ContractTypeEVM),
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Empty(t, result.Records[0].AssetIntents,
		"an earlier call must not spend funding created by a later transaction")
	require.Empty(t, runtime.AssetIntents)
}

func TestExecuteWorkBlockExposesEarlierFunding(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	gasConfig := DefaultGasConfig()
	funding := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
	}, gasConfig.GasAssetName, gasConfig.InvokeBaseGas)
	appendFundingOrderAsset(t, funding, fundingOrderTestAsset, 10)
	spend := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 2,
		Param:     EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "10", nil),
	}, gasConfig.GasAssetName, gasConfig.InvokeBaseGas)
	txs := []*wire.MsgTx{funding, spend}

	result, err := ExecuteWorkBlock(BlockExecutionRequest{
		Txs:            txs,
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          fundingOrderBlockContext(gasConfig),
		ResolveCaller:  fixedCaller(caller),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(nil,
			txs, TestnetContractPrefix, ContractTypeEVM),
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Empty(t, result.Records[0].AssetIntents)
	require.Len(t, result.Records[1].AssetIntents, 1)
	intent := result.Records[1].AssetIntents[0]
	require.Equal(t, fundingOrderTestAsset, intent.AssetName)
	require.Equal(t, "tb1qdest", intent.To)
	require.Equal(t, "10", intent.Amount.String())
}

func TestDeployConstructorSeesCurrentFundingOnce(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	expectedContract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	assets, err := NewAssetSet(fundingOrderTestAsset, scommon.NewDefaultDecimal(10))
	require.NoError(t, err)
	funding := contractframework.ContractOutputFromFunding(evmcommon.FundingOutput{
		OutPoint: evmcommon.TxOutPoint{
			TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Vout: 1,
		},
		Vout:     1,
		Contract: expectedContract,
		Assets:   assets,
	})
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	result := runtime.Deploy(DeployRequest{
		CallerAddress: caller.String(),
		CallID:        "deploy-with-funding",
		InitCode: evmPrecompileCallAndReturnTrueCode(
			EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "10", nil)),
		Gas:              500000,
		DeployNonce:      3,
		ExpectedContract: expectedContract,
		FundingOutputs:   []contractframework.ContractOutput{funding},
		Block: BlockContext{
			Number:        100,
			Time:          1,
			GasLimit:      1000000,
			FixedGasPrice: 1,
		},
	})
	require.NoError(t, result.Err)
	require.Equal(t, ResultStatusSuccess, result.Status)
	require.True(t, result.Contract.Equal(expectedContract))
	require.Len(t, runtime.AssetIntents, 1)
	intent := runtime.AssetIntents[0]
	require.Equal(t, fundingOrderTestAsset, intent.AssetName)
	require.Equal(t, "tb1qdest", intent.To)
	require.Equal(t, "10", intent.Amount.String())
}

func TestDeployConstructorSeesPendingBalance(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	expectedContract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	assets, err := NewAssetSet(fundingOrderTestAsset, scommon.NewDefaultDecimal(10))
	require.NoError(t, err)
	funding := contractframework.ContractOutputFromFunding(evmcommon.FundingOutput{
		OutPoint: evmcommon.TxOutPoint{TxID: strings.Repeat("c", 64), Vout: 1},
		Vout:     1,
		Contract: expectedContract,
		Assets:   assets,
	})
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	result := runtime.Deploy(DeployRequest{
		CallerAddress: caller.String(),
		CallID:        "deploy-pending-balance",
		InitCode: constructorTransferThenBalanceCode(
			EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "6", nil),
			EncodeBalanceOfCall(ContractAddressHash(expectedContract), fundingOrderTestAsset)),
		Gas:              500000,
		DeployNonce:      3,
		ExpectedContract: expectedContract,
		FundingOutputs:   []contractframework.ContractOutput{funding},
		Block:            BlockContext{Number: 100, Time: 1, GasLimit: 1000000, FixedGasPrice: 1},
	})
	require.NoError(t, result.Err)
	require.Equal(t, "4", abiRawDynamicString(t, result.RuntimeCode))
	require.Len(t, runtime.AssetIntents, 1)
	require.Equal(t, "6", runtime.AssetIntents[0].Amount.String())
}

func TestBuildBlockResultTxsCountsCurrentFundingOnce(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	gasConfig := DefaultGasConfig()
	tx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "15", nil),
	}, gasConfig.GasAssetName, gasConfig.InvokeBaseGas)
	appendFundingOrderAsset(t, tx, fundingOrderTestAsset, 10)

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{tx},
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          fundingOrderBlockContext(gasConfig),
		ResolveCaller:  fixedCaller(caller),
		ResolveScript:  evmTestResultScriptResolver(t, contract),
		ResolveOutput:  evmTestResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[0].Status)
	require.Empty(t, result.Execution.Records[0].AssetIntents)
}

func TestBuildBlockResultTxsDoesNotExposeLaterFunding(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	gasConfig := DefaultGasConfig()
	spend := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "10", nil),
	}, gasConfig.GasAssetName, gasConfig.InvokeBaseGas)
	laterFunding := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  gasConfig.InvokeBaseGas,
		CallNonce: 2,
	}, gasConfig.GasAssetName, gasConfig.InvokeBaseGas)
	appendFundingOrderAsset(t, laterFunding, fundingOrderTestAsset, 10)
	txs := []*wire.MsgTx{spend, laterFunding}

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            txs,
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          fundingOrderBlockContext(gasConfig),
		ResolveCaller:  fixedCaller(caller),
		ContractUTXOs: contractframework.ContractUTXOProviderWithTxOutputs(nil,
			txs, TestnetContractPrefix, ContractTypeEVM),
		ResolveScript: evmTestResultScriptResolver(t, contract),
		ResolveOutput: evmTestResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, result.Execution.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[0].Status)
	require.Empty(t, result.Execution.Records[0].AssetIntents)
}

func TestDeployConstructorRejectsOverspendCurrentFunding(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	expectedContract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	assets, err := NewAssetSet(fundingOrderTestAsset, scommon.NewDefaultDecimal(10))
	require.NoError(t, err)
	funding := contractframework.ContractOutputFromFunding(evmcommon.FundingOutput{
		OutPoint: evmcommon.TxOutPoint{
			TxID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Vout: 1,
		},
		Vout:     1,
		Contract: expectedContract,
		Assets:   assets,
	})
	runtime := NewRuntime(nil)
	configureFundingOrderRuntime(runtime)
	result := runtime.Deploy(DeployRequest{
		CallerAddress: caller.String(),
		CallID:        "deploy-overspend-funding",
		InitCode: evmPrecompileCallAndReturnTrueCode(
			EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "15", nil)),
		Gas:              500000,
		DeployNonce:      3,
		ExpectedContract: expectedContract,
		FundingOutputs:   []contractframework.ContractOutput{funding},
		Block: BlockContext{
			Number:        100,
			Time:          1,
			GasLimit:      1000000,
			FixedGasPrice: 1,
		},
	})
	require.Error(t, result.Err)
	require.Equal(t, ResultStatusInvalid, result.Status)
	require.Empty(t, runtime.AssetIntents)
}

func configureFundingOrderRuntime(runtime *Runtime) {
	runtime.AssetPrecision = contractframework.AssetPrecisionPolicy{Resolve: func(assetName string) (int, bool) {
		return 0, assetName == fundingOrderTestAsset
	}}
	runtime.ResolveResultScript = func(ResultOutput) ([]byte, error) { return []byte{0x51}, nil }
}

func constructorTransferThenBalanceCode(transfer, balance []byte) []byte {
	push2 := func(v int) []byte { return []byte{0x61, byte(v >> 8), byte(v)} }
	code := make([]byte, 0, 128+len(transfer)+len(balance))
	appendCall := func(input []byte, offsetPos *int) {
		code = append(code, push2(len(input))...)
		*offsetPos = len(code) + 1
		code = append(code, 0x61, 0x00, 0x00, 0x60, 0x00, 0x39)
		code = append(code, 0x60, 0x80, 0x60, 0x00)
		code = append(code, push2(len(input))...)
		code = append(code, 0x60, 0x00, 0x60, 0x00, 0x73)
		code = append(code, AssetPrecompileAddress.Bytes()...)
		code = append(code, 0x5a, 0xf1, 0x50)
	}
	var transferOffsetPos, balanceOffsetPos int
	appendCall(transfer, &transferOffsetPos)
	appendCall(balance, &balanceOffsetPos)
	code = append(code,
		0x3d, 0x60, 0x00, 0x60, 0x00, 0x3e,
		0x3d, 0x60, 0x00, 0xf3,
	)
	transferOffset := len(code)
	balanceOffset := transferOffset + len(transfer)
	code[transferOffsetPos], code[transferOffsetPos+1] = byte(transferOffset>>8), byte(transferOffset)
	code[balanceOffsetPos], code[balanceOffsetPos+1] = byte(balanceOffset>>8), byte(balanceOffset)
	code = append(code, transfer...)
	return append(code, balance...)
}

func appendFundingOrderAsset(t *testing.T, tx *wire.MsgTx, assetName string, amount int64) {
	t.Helper()
	require.NotNil(t, tx)
	require.Greater(t, len(tx.TxOut), 1)
	name := wire.NewAssetNameFromString(assetName)
	require.NotNil(t, name)
	tx.TxOut[1].Assets = append(tx.TxOut[1].Assets, wire.AssetInfo{
		Name:   *name,
		Amount: *scommon.NewDefaultDecimal(amount),
	})
}

func fundingOrderBlockContext(gasConfig GasConfig) BlockContext {
	gasConfig = gasConfig.Normalize()
	return BlockContext{
		Number:        100,
		Time:          1,
		GasLimit:      evmcommon.MaxGasPerBlock,
		FixedGasPrice: gasConfig.FixedGasPrice,
	}
}
