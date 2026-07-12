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
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 2)
	require.Equal(t, ExecutionKindDeploy, result.Execution.Records[0].Kind)
	require.Equal(t, ExecutionKindInvoke, result.Execution.Records[1].Kind)

	resultTx := result.ResultTxs[0]
	require.Len(t, resultTx.TxIn, 3)
	require.Equal(t, deployTx.TxHash(), resultTx.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, uint32(1), resultTx.TxIn[0].PreviousOutPoint.Index)
	require.Equal(t, invokeTx.TxHash(), resultTx.TxIn[1].PreviousOutPoint.Hash)
	require.Equal(t, uint32(1), resultTx.TxIn[1].PreviousOutPoint.Index)
	require.Equal(t, assetHash, resultTx.TxIn[2].PreviousOutPoint.Hash)
	parsed, err := ParseTx(resultTx, nil)
	require.NoError(t, err)
	require.Equal(t, TxTypeResult, parsed.Type)
	require.Equal(t, uint16(2), parsed.Result.ResultCount)
	require.NotEqual(t, [32]byte{}, result.Execution.StateRoot)
}

func TestBuildBlockResultTxsDeployOnly(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	gasAssetName := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:     gasAssetName,
		FixedGasPrice:    1,
		ResultPackingFee: 5,
		MaxGasPerBlock:   evmcommon.MaxGasPerBlock,
	}
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	deployTx := testDeployTx(t, 3, blockResultInitCode([]byte{0x00}))

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{deployTx},
		Runtime:        NewRuntime(nil),
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          BlockContext{Number: 100, Time: 1, GasLimit: evmcommon.MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller:  fixedCaller(caller),
		ResolveGasRefundRecipient: func(*wire.MsgTx, evmcommon.Tx) (string, bool, error) {
			return "deployer", true, nil
		},
		ResolveScript: evmTestResultScriptResolver(t, contract),
		ResolveOutput: evmTestResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ExecutionKindDeploy, result.Execution.Records[0].Kind)

	resultTx := result.ResultTxs[0]
	require.Len(t, resultTx.TxIn, 1)
	require.Equal(t, deployTx.TxHash(), resultTx.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, uint32(1), resultTx.TxIn[0].PreviousOutPoint.Index)
	outputs, err := evmTestResultOutputResolver(contract)(resultTx)
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, "deployer", gasAssetName, "4999.995")
	requireNoResultAssetAmount(t, outputs, contract.MustEncode(), gasAssetName)
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

func TestEVMResultInvalidStatus(t *testing.T) {
	caller := mustEVMAddress(t, "0x99992233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), []byte{0x00})
	runtime.State.SetContractDeployer(ContractGethAddress(contract), "deployer")
	closeTx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
		Action:    evmcommon.ContractInvokeAPIClose,
	}, DefaultGasConfig().GasAssetName, evmcommon.InvokeBaseGas)

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{closeTx},
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      DefaultGasConfig(),
		Block:          testBlockContext(1),
		ResolveCaller:  fixedCaller(caller),
		ResolveScript:  evmTestResultScriptResolver(t, contract),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[0].Status)
	parsed, err := ParseTx(result.ResultTxs[0], nil)
	require.NoError(t, err)
	require.Equal(t, ResultStatusInvalid, parsed.Result.Status)
}

func TestEVMCloseProfit(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	gasAssetName := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:     gasAssetName,
		BootstrapAddress: "bootstrap",
		FixedGasPrice:    1,
		ResultBaseGas:    5,
		InvokeBaseGas:    evmcommon.InvokeBaseGas,
		DeployBaseGas:    evmcommon.DeployBaseGas,
		MaxGasPerBlock:   evmcommon.MaxGasPerBlock,
	}
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 4)
	require.NoError(t, err)
	deployTx := testDeployTx(t, 4, blockResultInitCode(evmReturnTrueCode()))
	closeTx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
		Action:    evmcommon.ContractInvokeAPIClose,
	}, gasAssetName, 6000)
	profitHash := chainhash.Hash{9}
	profitInput := OutPoint{TxID: profitHash.String(), Vout: 0}
	const profitAsset = "ordx:ft:profit"
	runtime := NewRuntime(nil)

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{deployTx, closeTx},
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          BlockContext{Number: 100, Time: 1, GasLimit: evmcommon.MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller:  fixedCaller(caller),
		ResolveGasRefundRecipient: func(*wire.MsgTx, evmcommon.Tx) (string, bool, error) {
			return "deployer", true, nil
		},
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, profitInput, contract, profitAsset, 100, 0),
			}, nil
		},
		ResolveScript: evmTestResultScriptResolver(t, contract),
		ResolveOutput: evmTestResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 2)
	require.True(t, result.Execution.Records[1].CloseContract)
	require.True(t, runtime.State.ContractClosed(ContractGethAddress(contract)))

	outputs, err := evmTestResultOutputResolver(contract)(result.ResultTxs[0])
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, "deployer", profitAsset, "60")
	requireResultAssetAmount(t, outputs, "bootstrap", profitAsset, "40")
}

func TestEVMCloseRequiresCloseHookSuccess(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	gasAssetName := DefaultGasConfig().GasAssetName
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 4)
	require.NoError(t, err)
	deployTx := testDeployTx(t, 4, blockResultInitCode([]byte{0x00}))
	closeTx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
		Action:    evmcommon.ContractInvokeAPIClose,
	}, gasAssetName, 6000)
	runtime := NewRuntime(nil)

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{deployTx, closeTx},
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      DefaultGasConfig(),
		Block:          BlockContext{Number: 100, Time: 1, GasLimit: evmcommon.MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller:  fixedCaller(caller),
		ResolveGasRefundRecipient: func(*wire.MsgTx, evmcommon.Tx) (string, bool, error) {
			return "deployer", true, nil
		},
		ResolveScript: evmTestResultScriptResolver(t, contract),
		ResolveOutput: evmTestResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[1].Status)
	require.False(t, result.Execution.Records[1].CloseContract)
	require.False(t, runtime.State.ContractClosed(ContractGethAddress(contract)))
}

func TestEVMCloseHookTransfersBeforeProfit(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	gasAssetName := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName:     gasAssetName,
		BootstrapAddress: "bootstrap",
		FixedGasPrice:    1,
		ResultBaseGas:    5,
		InvokeBaseGas:    evmcommon.InvokeBaseGas,
		DeployBaseGas:    evmcommon.DeployBaseGas,
		MaxGasPerBlock:   evmcommon.MaxGasPerBlock,
	}
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 4)
	require.NoError(t, err)
	const profitAsset = "ordx:ft:profit"
	closeTx := blockResultInvokeTx(t, contract, InvokePayload{
		GasLimit:  evmcommon.InvokeBaseGas,
		CallNonce: 1,
		Action:    evmcommon.ContractInvokeAPIClose,
	}, gasAssetName, 6000)
	profitHash := chainhash.Hash{9}
	profitInput := OutPoint{TxID: profitHash.String(), Vout: 0}
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), evmCloseTransferCode(profitAsset, "tb1qdest", "40"))
	runtime.State.SetContractDeployer(ContractGethAddress(contract), "deployer")

	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs:            []*wire.MsgTx{closeTx},
		Runtime:        runtime,
		ContractPrefix: TestnetContractPrefix,
		GasConfig:      gasConfig,
		Block:          BlockContext{Number: 100, Time: 1, GasLimit: evmcommon.MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller:  fixedCaller(caller),
		ResolveGasRefundRecipient: func(*wire.MsgTx, evmcommon.Tx) (string, bool, error) {
			return "deployer", true, nil
		},
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, profitInput, contract, profitAsset, 100, 0),
			}, nil
		},
		ResolveScript: evmTestResultScriptResolver(t, contract),
		ResolveOutput: evmTestResultOutputResolver(contract),
		AssetPrecision: func(assetName string) (int, bool) {
			return 0, assetName == profitAsset || assetName == gasAssetName
		},
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Execution.Records[0].Status)
	require.True(t, result.Execution.Records[0].CloseContract)
	require.True(t, runtime.State.ContractClosed(ContractGethAddress(contract)))

	outputs, err := evmTestResultOutputResolver(contract)(result.ResultTxs[0])
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, "tb1qdest", profitAsset, "40")
	requireResultAssetAmount(t, outputs, "deployer", profitAsset, "36")
	requireResultAssetAmount(t, outputs, "bootstrap", profitAsset, "24")
}

func evmTestResultScriptResolver(t *testing.T, contract ContractAddress) contractframework.ResultRecipientScriptResolver {
	t.Helper()
	return func(output ResultOutput) ([]byte, error) {
		if output.To == contract.MustEncode() {
			return ContractPkScript(contract)
		}
		switch output.To {
		case "deployer":
			return []byte{txscript.OP_2}, nil
		case "bootstrap":
			return []byte{txscript.OP_3}, nil
		default:
			return []byte{txscript.OP_TRUE}, nil
		}
	}
}

func evmTestResultOutputResolver(contract ContractAddress) contractframework.ResultOutputResolver {
	return func(resultTx *wire.MsgTx) ([]ResultOutput, error) {
		return contractframework.ResultOutputsFromTx(resultTx, TestnetContractPrefix, evmcommon.ParseContractPkScript,
			func(pkScript []byte) (string, bool, error) {
				if len(pkScript) != 1 {
					return "", false, nil
				}
				switch pkScript[0] {
				case txscript.OP_2:
					return "deployer", true, nil
				case txscript.OP_3:
					return "bootstrap", true, nil
				case txscript.OP_TRUE:
					return "tb1qdest", true, nil
				default:
					return "", false, nil
				}
			})
	}
}

func evmReturnTrueCode() []byte {
	return []byte{
		0x60, 0x01, // PUSH1 1
		0x60, 0x00, // PUSH1 0
		0x52,       // MSTORE
		0x60, 0x20, // PUSH1 32
		0x60, 0x00, // PUSH1 0
		0xf3, // RETURN
	}
}

func evmCloseTransferCode(assetName, to, amount string) []byte {
	return evmPrecompileCallAndReturnTrueCode(EncodeTransferAssetCall(assetName, to, amount, nil))
}

func evmPrecompileCallAndReturnTrueCode(input []byte) []byte {
	push2 := func(v int) []byte {
		return []byte{0x61, byte(v >> 8), byte(v)}
	}
	code := make([]byte, 0, 64+len(input))
	code = append(code, push2(len(input))...)
	offsetPos := len(code) + 1
	code = append(code, 0x61, 0x00, 0x00) // PUSH2 data offset
	code = append(code,
		0x60, 0x00, // PUSH1 0
		0x39,       // CODECOPY
		0x60, 0x00, // PUSH1 0, output size
		0x60, 0x00, // PUSH1 0, output offset
	)
	code = append(code, push2(len(input))...)
	code = append(code,
		0x60, 0x00, // PUSH1 0, input offset
		0x60, 0x00, // PUSH1 0, value
		0x73, // PUSH20 precompile address
	)
	code = append(code, AssetPrecompileAddress.Bytes()...)
	code = append(code,
		0x5a, // GAS
		0xf1, // CALL
		0x50, // POP
	)
	code = append(code, evmReturnTrueCode()...)
	dataOffset := len(code)
	code[offsetPos] = byte(dataOffset >> 8)
	code[offsetPos+1] = byte(dataOffset)
	return append(code, input...)
}

func requireResultAssetAmount(t *testing.T, outputs []ResultOutput, to, assetName, amount string) {
	t.Helper()
	for _, output := range outputs {
		if output.To != to {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.String() == assetName {
				require.Equal(t, amount, asset.Amount.String())
				return
			}
		}
	}
	t.Fatalf("missing asset output to=%s asset=%s amount=%s outputs=%v", to, assetName, amount, outputs)
}

func requireNoResultAssetAmount(t *testing.T, outputs []ResultOutput, to, assetName string) {
	t.Helper()
	for _, output := range outputs {
		if output.To != to {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.String() == assetName && asset.Amount.Sign() != 0 {
				t.Fatalf("unexpected asset output to=%s asset=%s amount=%s outputs=%v",
					to, assetName, asset.Amount.String(), outputs)
			}
		}
	}
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
	if payload.Action == "" {
		payload.Action = "call"
	}
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
