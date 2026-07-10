#!/usr/bin/env python3
from __future__ import annotations

from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def patch_runtime_validation() -> None:
    path = ROOT / "contract/evm/runtime.go"
    text = path.read_text()
    old = "\t\terr = r.commitCapturedEffects(capturedIntents, capturedTriggers, r.AssetBalances)\n"
    new = "\t\terr = r.commitCapturedEffects(capturedIntents, capturedTriggers, balances)\n"
    if old in text:
        path.write_text(text.replace(old, new, 1))
        return
    if text.count(new) >= 2:
        return
    raise RuntimeError("deploy captured-effect balance validation block not found")


def clarify_overlay_comment() -> None:
    path = ROOT / "contract/evm/backend.go"
    text = path.read_text()
    old = """// withoutEVMBlockOutputs makes the execution API defensive against callers that
// accidentally pass a post-work UTXO view. Current and future work outputs are
// reintroduced only by the sequential overlay after their transaction executes.
"""
    new = """// withoutEVMBlockOutputs strips outputs created by this work block from a
// caller-supplied base view. The sequential overlay reintroduces each output
// only after its transaction executes.
"""
    if new in text:
        return
    if old not in text:
        raise RuntimeError("UTXO overlay comment block not found")
    path.write_text(text.replace(old, new, 1))


def add_regression_tests() -> None:
    path = ROOT / "contract/evm/funding_order_test.go"
    text = path.read_text()
    marker = "func TestBuildBlockResultTxsCountsCurrentFundingOnce"
    if marker in text:
        return
    insertion_point = "func appendFundingOrderAsset"
    if insertion_point not in text:
        raise RuntimeError("funding-order helper insertion point not found")
    tests = r'''func TestBuildBlockResultTxsCountsCurrentFundingOnce(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
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

'''
    path.write_text(text.replace(insertion_point, tests + insertion_point, 1))


def main() -> None:
    patch_runtime_validation()
    clarify_overlay_comment()
    add_regression_tests()


if __name__ == "__main__":
    main()
