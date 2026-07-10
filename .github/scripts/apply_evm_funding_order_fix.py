#!/usr/bin/env python3
from __future__ import annotations

import re
import textwrap
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def replace_once(path: str, old: str, new: str) -> None:
    target = ROOT / path
    text = target.read_text()
    if new in text:
        return
    if old not in text:
        raise RuntimeError(f"expected source block not found in {path}")
    target.write_text(text.replace(old, new, 1))


def patch_backend() -> None:
    replace_once(
        "contract/evm/backend.go",
        """\toverlay := contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{\n\t\tPrefix:       prefix,\n\t\tContractType: ContractTypeEVM,\n\t\tBase:         req.ContractUTXOs,\n\t})\n""",
        """\toverlay := newEVMBlockUTXOOverlay(prefix, req.ContractUTXOs, req.Txs)\n""",
    )
    replace_once(
        "contract/evm/backend.go",
        """\t\tif err := overlay.AddTxOutputs(tx, int64(req.Block.Number)); err != nil {\n\t\t\treturn BlockResultBuildResult{}, err\n\t\t}\n\t\tif err := executor.ExecuteParsedTx(tx, parsed); err != nil {\n\t\t\treturn BlockResultBuildResult{}, err\n\t\t}\n""",
        """\t\tif err := executor.ExecuteParsedTx(tx, parsed); err != nil {\n\t\t\treturn BlockResultBuildResult{}, err\n\t\t}\n\t\tif err := overlay.ApplyTx(tx, int64(req.Block.Number)); err != nil {\n\t\t\treturn BlockResultBuildResult{}, err\n\t\t}\n""",
    )
    path = ROOT / "contract/evm/backend.go"
    text = path.read_text()
    if "func newEVMBlockUTXOOverlay(" not in text:
        old = """func ExecuteWorkBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {\n\texecutor := NewBackend(req)\n\tframeworkExecutor := contractframework.NewExecutor(executor.executorConfig())\n\tif _, err := frameworkExecutor.ExecuteBlock(req.Txs); err != nil {\n\t\treturn BlockExecutionResult{}, err\n\t}\n\treturn executor.FinalizeWork()\n}\n"""
        new = """func newEVMBlockUTXOOverlay(prefix string, base ContractUTXOProvider,\n\ttxs []*wire.MsgTx) *contractframework.ContractUTXOOverlay {\n\n\treturn contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{\n\t\tPrefix:       prefix,\n\t\tContractType: ContractTypeEVM,\n\t\tBase:         withoutEVMBlockOutputs(base, txs),\n\t})\n}\n\n// withoutEVMBlockOutputs makes the execution API defensive against callers that\n// accidentally pass a post-work UTXO view. Current and future work outputs are\n// reintroduced only by the sequential overlay after their transaction executes.\nfunc withoutEVMBlockOutputs(base ContractUTXOProvider, txs []*wire.MsgTx) ContractUTXOProvider {\n\tif base == nil || len(txs) == 0 {\n\t\treturn base\n\t}\n\texcluded := make(map[OutPoint]struct{})\n\tfor _, tx := range txs {\n\t\tif tx == nil {\n\t\t\tcontinue\n\t\t}\n\t\ttxID := tx.TxID()\n\t\tfor vout := range tx.TxOut {\n\t\t\texcluded[OutPoint{TxID: txID, Vout: uint32(vout)}] = struct{}{}\n\t\t}\n\t}\n\tif len(excluded) == 0 {\n\t\treturn base\n\t}\n\treturn func(contractAddr ContractAddress) ([]UTXO, error) {\n\t\tutxos, err := base(contractAddr)\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n\t\tout := make([]UTXO, 0, len(utxos))\n\t\tfor _, utxo := range utxos {\n\t\t\tif _, blocked := excluded[utxo.OutPoint]; blocked {\n\t\t\t\tcontinue\n\t\t\t}\n\t\t\tout = append(out, utxo.Clone())\n\t\t}\n\t\treturn out, nil\n\t}\n}\n\nfunc ExecuteWorkBlock(req BlockExecutionRequest) (BlockExecutionResult, error) {\n\tprefix := req.ContractPrefix\n\tif prefix == \"\" {\n\t\tprefix = TestnetContractPrefix\n\t}\n\toverlay := newEVMBlockUTXOOverlay(prefix, req.ContractUTXOs, req.Txs)\n\treq.ContractPrefix = prefix\n\treq.ContractUTXOs = overlay.Provider\n\texecutor := NewBackend(req)\n\tframeworkExecutor := contractframework.NewExecutor(executor.executorConfig())\n\tfor _, tx := range req.Txs {\n\t\tif err := frameworkExecutor.ExecuteTx(tx); err != nil {\n\t\t\treturn BlockExecutionResult{}, err\n\t\t}\n\t\tif err := overlay.ApplyTx(tx, int64(req.Block.Number)); err != nil {\n\t\t\treturn BlockExecutionResult{}, err\n\t\t}\n\t}\n\tif _, err := executor.FinalizeBlock(contractframework.ExecutionContext{}); err != nil {\n\t\treturn BlockExecutionResult{}, err\n\t}\n\treturn executor.FinalizeWork()\n}\n"""
        if old not in text:
            raise RuntimeError("expected ExecuteWorkBlock block not found")
        path.write_text(text.replace(old, new, 1))

    replace_once(
        "contract/evm/backend.go",
        """\t\tDeployNonce:   validated.Payload.DeployNonce,\n\t\tBlock:         e.Block,\n""",
        """\t\tDeployNonce:      validated.Payload.DeployNonce,\n\t\tExpectedContract: expectedContract,\n\t\tFundingOutputs:   fundingOutputs,\n\t\tBlock:            e.Block,\n""",
    )


def patch_runtime() -> None:
    replace_once(
        "contract/evm/runtime.go",
        """type DeployRequest struct {\n\tCallerAddress string\n\tCallID        string\n\tInitCode      []byte\n\tGas           int64\n\tValue         int64\n\tDeployNonce   uint64\n\tBlock         BlockContext\n}\n""",
        """type DeployRequest struct {\n\tCallerAddress    string\n\tCallID           string\n\tInitCode         []byte\n\tGas              int64\n\tValue            int64\n\tDeployNonce      uint64\n\tExpectedContract ContractAddress\n\tFundingOutputs   []contractframework.ContractOutput\n\tBlock            BlockContext\n}\n""",
    )
    replace_once(
        "contract/evm/runtime.go",
        """\tcapturedIntents := make([]AssetIntent, 0)\n\tcapturedTriggers := make([]Trigger, 0)\n\tconfig := r.configWithSatoshiNetTrace(req.CallID, &capturedIntents, &capturedTriggers, nil)\n""",
        """\tcapturedIntents := make([]AssetIntent, 0)\n\tcapturedTriggers := make([]Trigger, 0)\n\tbalances := AssetBalanceReader(r.AssetBalances)\n\tif len(req.FundingOutputs) != 0 {\n\t\tfunding := NewFundingAssetView(req.FundingOutputs, \"\", nil)\n\t\tbalances = NewFundingOverlayAssetBalanceView(r.AssetBalances, funding,\n\t\t\tContractAddressHash(req.ExpectedContract))\n\t}\n\tconfig := r.configWithSatoshiNetTrace(req.CallID, &capturedIntents, &capturedTriggers, nil)\n""",
    )
    replace_once(
        "contract/evm/runtime.go",
        """\tprecompiles := SatoshiNetPrecompiles(r.AssetBalances, nil, \"\", vm.ActivePrecompiledContracts(rules))\n""",
        """\tprecompiles := SatoshiNetPrecompiles(balances, nil, \"\", vm.ActivePrecompiledContracts(rules))\n""",
    )
    replace_once(
        "contract/evm/runtime.go",
        """\t\terr = r.commitCapturedEffects(capturedIntents, capturedTriggers, r.AssetBalances)\n""",
        """\t\terr = r.commitCapturedEffects(capturedIntents, capturedTriggers, balances)\n""",
    )


def patch_node_services() -> None:
    path = ROOT / "contract/node/services.go"
    text = path.read_text()
    pattern = re.compile(
        r"\n\t\t\toverlay := contractframework\.ContractUTXOProviderWithTxOutputs\(\n"
        r"\t\t\t\tcontractUTXOs, work\.Txs, prefix, evm\.ContractTypeEVM\)"
    )
    matches = len(pattern.findall(text))
    if matches not in (0, 3):
        raise RuntimeError(f"unexpected EVM preloaded overlay count: {matches}")
    if matches == 3:
        text = pattern.sub("", text)
        old = "\t\t\t\tContractUTXOs:             overlay,"
        if text.count(old) != 3:
            raise RuntimeError("unexpected EVM ContractUTXOs overlay field count")
        text = text.replace(old, "\t\t\t\tContractUTXOs:             contractUTXOs,")
        path.write_text(text)


def patch_node_validator() -> None:
    replace_once(
        "contract/node/evm_validation.go",
        """\t\tContractUTXOs:   contractOverlay.Provider,\n""",
        """\t\tContractUTXOs:   v.cfg.ContractUTXOs,\n""",
    )
    replace_once(
        "contract/node/evm_validation.go",
        """\t\tif err := overlay.AddTxOutputs(tx, int64(height)); err != nil {\n""",
        """\t\tif err := overlay.ApplyTx(tx, int64(height)); err != nil {\n""",
    )


def write_tests() -> None:
    path = ROOT / "contract/evm/funding_order_test.go"
    markers = [
        "TestExecuteWorkBlockCountsCurrentFundingOnce",
        "TestExecuteWorkBlockDoesNotExposeLaterFunding",
        "TestExecuteWorkBlockExposesEarlierFunding",
        "TestDeployConstructorSeesCurrentFundingOnce",
    ]
    if path.exists():
        text = path.read_text()
        missing = [marker for marker in markers if marker not in text]
        if missing:
            raise RuntimeError(f"existing funding-order test file is incomplete: {missing}")
        return
    path.write_text(
        textwrap.dedent(
            r'''package evm

import (
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
    require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
    require.Empty(t, result.Records[0].AssetIntents,
        "a transfer above the current funding must not succeed through double counting")
    require.Empty(t, runtime.AssetIntents)
}

func TestExecuteWorkBlockDoesNotExposeLaterFunding(t *testing.T) {
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
    contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
    require.NoError(t, err)
    initCode := evmPrecompileCallAndReturnTrueCode(
        EncodeTransferAssetCall(fundingOrderTestAsset, "tb1qdest", "10", nil))
    deployTx := testDeployTx(t, 3, initCode)
    appendFundingOrderAsset(t, deployTx, fundingOrderTestAsset, 10)
    gasConfig := DefaultGasConfig()

    result, err := BuildBlockResultTxs(BlockResultBuildRequest{
        Txs:            []*wire.MsgTx{deployTx},
        Runtime:        NewRuntime(nil),
        ContractPrefix: TestnetContractPrefix,
        GasConfig:      gasConfig,
        Block:          fundingOrderBlockContext(gasConfig),
        ResolveCaller:  fixedCaller(caller),
        ResolveScript:  evmTestResultScriptResolver(t, contract),
        ResolveOutput:  evmTestResultOutputResolver(contract),
    })
    require.NoError(t, err)
    require.Len(t, result.Execution.Records, 1)
    require.Len(t, result.Execution.Records[0].AssetIntents, 1)
    intent := result.Execution.Records[0].AssetIntents[0]
    require.Equal(t, fundingOrderTestAsset, intent.AssetName)
    require.Equal(t, "10", intent.Amount.String())
    outputs, err := evmTestResultOutputResolver(contract)(result.ResultTxs[0])
    require.NoError(t, err)
    requireResultAssetAmount(t, outputs, "tb1qdest", fundingOrderTestAsset, "10")
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
'''
        ).lstrip()
    )


def main() -> None:
    patch_backend()
    patch_runtime()
    patch_node_services()
    patch_node_validator()
    write_tests()


if __name__ == "__main__":
    main()
