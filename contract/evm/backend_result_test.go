package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBuildBlockResultTxsDeployInvoke(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	gasAssetName := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:     gasAssetName,
		FixedGasPrice:    1,
		ResultPackingFee: 5,
		MaxGasPerInvoke:  0,
		MaxGasPerBlock:   evmcommon.MaxGasPerBlock,
	}
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)

	deployTx := testDeployTx(t, 3, blockResultInitCode(callAssetPrecompileCode()))
	invokeTx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
	}, gasAssetName, evmcommon.InvokeBaseGas)
	assetHash := chainhash.Hash{8}
	assetInput := OutPoint{TxID: assetHash.String(), Vout: 0}

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{deployTx, invokeTx},
		Runtime:        NewRuntime(nil),
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          BlockContext{Number: 100, Time: 1, GasLimit: evmcommon.MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller:  fixedCaller(caller),
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, assetInput, contract, SatoshiAssetName, 100, 99),
			}, nil
		},
		ResolveScript: func(output ResultOutput) ([]byte, error) {
			if output.To == contract.MustEncode() {
				return ContractPkScript(contract)
			}
			return []byte{txscript.OP_TRUE}, nil
		},
		ResolveOutput: func(resultTx *wire.MsgTx) ([]ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(resultTx, TestnetContractPrefix, evmcommon.ParseContractPkScript, func(pkScript []byte) (string, bool, error) {
				if len(pkScript) == 1 && pkScript[0] == txscript.OP_TRUE {
					return "tb1qdest", true, nil
				}
				return "", false, nil
			})
		},
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 2)
	require.Len(t, result.Execution.Records, 2)
	require.Equal(t, ExecutionKindDeploy, result.Execution.Records[0].Kind)
	require.Equal(t, ExecutionKindInvoke, result.Execution.Records[1].Kind)

	deployResult := result.ResultTxs[0]
	require.Len(t, deployResult.TxIn, 1)
	require.Equal(t, deployTx.TxHash(), deployResult.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, uint32(1), deployResult.TxIn[0].PreviousOutPoint.Index)
	deployParsed, err := ParseTx(deployResult, nil)
	require.NoError(t, err)
	require.Equal(t, TxTypeResult, deployParsed.Type)

	invokeResult := result.ResultTxs[1]
	require.Len(t, invokeResult.TxIn, 2)
	require.Equal(t, invokeTx.TxHash(), invokeResult.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, uint32(1), invokeResult.TxIn[0].PreviousOutPoint.Index)
	require.Equal(t, assetHash, invokeResult.TxIn[1].PreviousOutPoint.Hash)
	require.NotEqual(t, [32]byte{}, result.Execution.StateRoot)
}

func TestBuildBlockResultTxsIgnoresInvokeBeforeDeploy(t *testing.T) {
	contract := testContract(t)
	invokeTx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
	}, DefaultGasConfig().GasAssetName, evmcommon.InvokeBaseGas)

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{invokeTx},
		Runtime:        NewRuntime(nil),
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      DefaultGasConfig(),
		Block:          testBlockContext(1),
		ResolveCaller:  fixedCaller(mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")),
	})
	require.NoError(t, err)
	require.Empty(t, result.ResultTxs)
	require.Empty(t, result.Execution.Records)
}

func TestContractUTXOOverlayIncludesAndSpendsBlockOutputs(t *testing.T) {
	contract := testContract(t)
	tx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  1,
		CallNonce: 1,
	}, "ordx:ft:gas", 50)
	overlay := contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
		Prefix:       TestnetContractPrefix,
		ContractType: ContractTypeEVM,
	})
	require.NoError(t, overlay.AddTxOutputs(tx, 100))

	utxos, err := overlay.Provider(contract)
	require.NoError(t, err)
	require.Len(t, utxos, 1)
	amount, err := utxos[0].AssetAmount("ordx:ft:gas")
	require.NoError(t, err)
	require.Equal(t, 0, amount.Cmp(mustDefaultDecimal(t, 50)))

	spend := wire.NewMsgTx(2)
	spend.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: tx.TxHash(), Index: 1}, nil, nil))
	require.NoError(t, overlay.ApplyTx(spend, 100))
	utxos, err = overlay.Provider(contract)
	require.NoError(t, err)
	require.Empty(t, utxos)
}

func blockResultInvokeTx(t *testing.T, contract ContractAddress, payload InvokePayload, gasAssetName string, gasAmount int64) *wire.MsgTx {
	t.Helper()
	script, err := evmcommon.InvokeNullDataScript(payload)
	require.NoError(t, err)
	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)
	assetName := wire.NewAssetNameFromString(gasAssetName)
	require.NotNil(t, assetName)

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0}})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *assetName,
		Amount: *scommon.NewDefaultDecimal(gasAmount),
	}}, contractScript))
	return tx
}

func blockResultInitCode(runtime []byte) []byte {
	init := []byte{
		0x60, byte(len(runtime)),
		0x60, 0x0c,
		0x60, 0x00,
		0x39,
		0x60, byte(len(runtime)),
		0x60, 0x00,
		0xf3,
	}
	return append(init, runtime...)
}
