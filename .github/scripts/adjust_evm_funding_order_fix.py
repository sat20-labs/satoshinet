#!/usr/bin/env python3
from __future__ import annotations

import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def replace_once(path: str, old: str, new: str) -> None:
    target = ROOT / path
    text = target.read_text()
    if new in text:
        return
    if old not in text:
        raise RuntimeError(f"expected adjustment block not found in {path}")
    target.write_text(text.replace(old, new, 1))


def main() -> None:
    replace_once(
        "contract/evm/backend.go",
        """\treq.ContractPrefix = prefix\n\treq.ContractUTXOs = overlay.Provider\n\texecutor := NewBackend(req)\n""",
        """\treq.ContractPrefix = prefix\n\tif req.ContractUTXOs != nil {\n\t\treq.ContractUTXOs = overlay.Provider\n\t}\n\texecutor := NewBackend(req)\n""",
    )

    replace_once(
        "contract/evm/funding_order_test.go",
        """\trequire.Equal(t, ResultStatusSuccess, result.Records[0].Status)\n\trequire.Empty(t, result.Records[0].AssetIntents,\n\t\t\"a transfer above the current funding must not succeed through double counting\")\n""",
        """\trequire.Equal(t, ResultStatusInvalid, result.Records[0].Status)\n\trequire.Empty(t, result.Records[0].AssetIntents,\n\t\t\"a transfer above the current funding must not succeed through double counting\")\n""",
    )

    path = ROOT / "contract/evm/funding_order_test.go"
    text = path.read_text()
    replacement = r'''func TestDeployConstructorSeesCurrentFundingOnce(t *testing.T) {
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
	result := runtime.Deploy(DeployRequest{
		CallerAddress:    caller.String(),
		CallID:           "deploy-with-funding",
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

func appendFundingOrderAsset'''
    pattern = re.compile(
        r"func TestDeployConstructorSeesCurrentFundingOnce\(t \*testing\.T\) \{.*?\n\}\n\nfunc appendFundingOrderAsset",
        re.S,
    )
    if "CallID:           \"deploy-with-funding\"" not in text:
        text, count = pattern.subn(replacement, text, count=1)
        if count != 1:
            raise RuntimeError("deploy funding test block not found")
        path.write_text(text)


if __name__ == "__main__":
    main()
