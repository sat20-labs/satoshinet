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
		GasAssetName: gasAssetName, BootstrapAddress: "bootstrap",
		FixedGasPrice: 1, ResultPackingFee: 5, MaxGasPerBlock: evmcommon.MaxGasPerBlock,
	}
	addr, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	deployTx := testDeployTx(t, 3, blockResultInitCode(callAssetPrecompileCode()))
	// Legitimate capital enters through a successful deployment. A separate
	// unsolicited output is an anomaly and cannot finance the later transfer.
	deployTx.TxOut[1].Value = 100
	invokeTx := blockResultInvokeTx(t, addr, InvokePayload{
		GasLimit: evmcommon.InvokeBaseGas, CallNonce: 1,
		Param: EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
	}, gasAssetName, evmcommon.InvokeBaseGas)
	anomaly := OutPoint{TxID: chainhash.Hash{8}.String(), Vout: 0}
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{deployTx, invokeTx}, Runtime: NewRuntime(nil), ContractPrefix: TestnetContractPrefix,
		GasConfig: gasConfig, Block: testBlockContext(100), ResolveCaller: fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, addr.Equal(got))
			return []UTXO{mustUTXO(t, anomaly, addr, SatoshiAssetName, 5, 99)}, nil
		},
		ResolveScript: evmTestResultScriptResolver(t, addr), ResolveOutput: evmTestResultOutputResolver(addr),
		AssetPrecision: func(string) (int, bool) { return 8, true },
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 2)
	require.Equal(t, ExecutionKindDeploy, result.Execution.Records[0].Kind)
	require.Equal(t, ExecutionKindInvoke, result.Execution.Records[1].Kind)
	require.Equal(t, ResultStatusSuccess, result.Execution.Records[1].Status)
	resultTx := result.ResultTxs[0]
	gotInputs := make([]OutPoint, 0, len(resultTx.TxIn))
	for _, input := range resultTx.TxIn {
		gotInputs = append(gotInputs, WireOutPointToEVM(input.PreviousOutPoint))
	}
	require.ElementsMatch(t, []OutPoint{
		{TxID: deployTx.TxID(), Vout: 1}, {TxID: invokeTx.TxID(), Vout: 1},
	}, gotInputs)
	outputs, err := evmTestResultOutputResolver(addr)(resultTx)
	require.NoError(t, err)
	require.Equal(t, int64(77), evmResultSats(outputs, "tb1qdest"))
	require.Equal(t, int64(23), evmResultSats(outputs, addr.MustEncode()))
	require.Equal(t, int64(0), evmResultSats(outputs, "bootstrap"))
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
		GasAssetName: gasAssetName, FixedGasPrice: 1, ResultPackingFee: 5,
		MaxGasPerBlock: evmcommon.MaxGasPerBlock,
	}
	addr, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	deployTx := testDeployTx(t, 3, blockResultInitCode([]byte{0x00}))
	runtime := NewRuntime(nil)
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{deployTx}, Runtime: runtime, ContractPrefix: TestnetContractPrefix,
		GasConfig: gasConfig, Block: testBlockContext(100), ResolveCaller: fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
		ResolveScript:             evmTestResultScriptResolver(t, addr), ResolveOutput: evmTestResultOutputResolver(addr),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ExecutionKindDeploy, result.Execution.Records[0].Kind)
	resultTx := result.ResultTxs[0]
	require.Len(t, resultTx.TxIn, 1)
	require.Equal(t, deployTx.TxHash(), resultTx.TxIn[0].PreviousOutPoint.Hash)
	require.Equal(t, uint32(1), resultTx.TxIn[0].PreviousOutPoint.Index)
	outputs, err := evmTestResultOutputResolver(addr)(resultTx)
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, "deployer", gasAssetName, "4999.995")
	requireNoResultAssetAmount(t, outputs, addr.MustEncode(), gasAssetName)
	owner, ok := runtime.State.ContractDeployer(ContractGethAddress(addr))
	require.True(t, ok)
	require.Equal(t, caller.String(), owner, "authorization uses the actor, not its refund recipient")
}

func TestBuildBlockResultTxsRefundsInvokeBeforeDeploy(t *testing.T) {
	addr := testContract(t)
	invokeTx := blockResultInvokeTx(t, addr, InvokePayload{
		GasLimit: evmcommon.InvokeBaseGas, CallNonce: 1,
	}, DefaultGasConfig().GasAssetName, 100)
	invokeTx.TxOut[1].Value = 7
	runtime := NewRuntime(nil)
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{invokeTx}, Runtime: runtime, ContractPrefix: TestnetContractPrefix,
		GasConfig: DefaultGasConfig(), Block: testBlockContext(1),
		ResolveCaller:             fixedCaller(mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qdest"),
		ResolveScript:             evmTestResultScriptResolver(t, addr), ResolveOutput: evmTestResultOutputResolver(addr),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[0].Status)
	require.False(t, runtime.State.KnownContract(ContractGethAddress(addr)))
	outputs, err := evmTestResultOutputResolver(addr)(result.ResultTxs[0])
	require.NoError(t, err)
	require.Equal(t, int64(7), evmResultSats(outputs, "tb1qdest"))
	requireResultAssetAmount(t, outputs, "tb1qdest", DefaultGasConfig().GasAssetName, "50")
}

func TestEVMResultInvalidStatus(t *testing.T) {
	caller := mustEVMAddress(t, "0x99992233445566778899aabbccddeeff00112233")
	addr := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(addr), evmReturnTrueCode())
	runtime.State.SetContractDeployer(ContractGethAddress(addr), "deployer")
	closeTx := blockResultInvokeTx(t, addr, InvokePayload{
		GasLimit: evmcommon.InvokeBaseGas, CallNonce: 1, Action: evmcommon.ContractInvokeAPIClose,
	}, DefaultGasConfig().GasAssetName, 100)
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{closeTx}, Runtime: runtime, GasConfig: DefaultGasConfig(),
		Block: testBlockContext(1), ResolveCaller: fixedCaller(caller),
		// Matching the refund destination must not impersonate the deployer.
		ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
		ResolveScript:             evmTestResultScriptResolver(t, addr), ResolveOutput: evmTestResultOutputResolver(addr),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[0].Status)
	require.False(t, runtime.State.ContractClosed(ContractGethAddress(addr)))
	parsed, err := ParseTx(result.ResultTxs[0], nil)
	require.NoError(t, err)
	require.Equal(t, ResultStatusInvalid, parsed.Result.Status)
}

func TestEVMCloseProfit(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	gasAssetName := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName: gasAssetName, BootstrapAddress: "bootstrap", FixedGasPrice: 1,
		ResultBaseGas: 5, InvokeBaseGas: evmcommon.InvokeBaseGas,
		DeployBaseGas: evmcommon.DeployBaseGas, MaxGasPerBlock: evmcommon.MaxGasPerBlock,
	}
	addr, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 4)
	require.NoError(t, err)
	const profitAsset = "ordx:ft:profit"
	deployTx := testDeployTx(t, 4, blockResultInitCode(evmReturnTrueCode()))
	profit, err := contractframework.NewAssetSet(profitAsset, scommon.NewDefaultDecimal(100))
	require.NoError(t, err)
	require.NoError(t, deployTx.TxOut[1].Assets.Merge(profit))
	deployTx.TxOut[1].Value = 10
	closeTx := blockResultInvokeTx(t, addr, InvokePayload{
		GasLimit: evmcommon.InvokeBaseGas, CallNonce: 1, Action: evmcommon.ContractInvokeAPIClose,
	}, gasAssetName, 6000)
	anomaly := OutPoint{TxID: chainhash.Hash{9}.String(), Vout: 0}
	runtime := NewRuntime(nil)
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{deployTx, closeTx}, Runtime: runtime, GasConfig: gasConfig,
		Block: testBlockContext(100), ResolveCaller: fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, addr.Equal(got))
			return []UTXO{mustUTXO(t, anomaly, addr, profitAsset, 13, 0)}, nil
		},
		ResolveScript: evmTestResultScriptResolver(t, addr), ResolveOutput: evmTestResultOutputResolver(addr),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 2)
	require.True(t, result.Execution.Records[1].CloseContract)
	require.True(t, runtime.State.ContractClosed(ContractGethAddress(addr)))
	outputs, err := evmTestResultOutputResolver(addr)(result.ResultTxs[0])
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, caller.String(), profitAsset, "70")
	requireResultAssetAmount(t, outputs, "bootstrap", profitAsset, "43")
	require.Equal(t, int64(7), evmResultSats(outputs, caller.String()))
	require.Equal(t, int64(3), evmResultSats(outputs, "bootstrap"))
	balance, ok := runtime.State.ManagedBalance(addr)
	require.True(t, ok)
	require.True(t, balance.IsZero())
}

func TestEVMCloseRequiresCloseHookSuccess(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	addr, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 4)
	require.NoError(t, err)
	deployTx := testDeployTx(t, 4, blockResultInitCode([]byte{0x00}))
	closeTx := blockResultInvokeTx(t, addr, InvokePayload{
		GasLimit: evmcommon.InvokeBaseGas, CallNonce: 1, Action: evmcommon.ContractInvokeAPIClose,
	}, DefaultGasConfig().GasAssetName, 6000)
	runtime := NewRuntime(nil)
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{deployTx, closeTx}, Runtime: runtime, GasConfig: DefaultGasConfig(),
		Block: testBlockContext(100), ResolveCaller: fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
		ResolveScript:             evmTestResultScriptResolver(t, addr), ResolveOutput: evmTestResultOutputResolver(addr),
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 2)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[1].Status)
	require.False(t, result.Execution.Records[1].CloseContract)
	require.False(t, runtime.State.ContractClosed(ContractGethAddress(addr)))
}

func TestEVMCloseHookTransfersBeforeProfit(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	gasAssetName := DefaultGasConfig().GasAssetName
	gasConfig := GasConfig{
		GasAssetName: gasAssetName, BootstrapAddress: "bootstrap", FixedGasPrice: 1,
		ResultBaseGas: 5, InvokeBaseGas: evmcommon.InvokeBaseGas,
		DeployBaseGas: evmcommon.DeployBaseGas, MaxGasPerBlock: evmcommon.MaxGasPerBlock,
	}
	addr, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 4)
	require.NoError(t, err)
	const profitAsset = "ordx:ft:profit"
	closeTx := blockResultInvokeTx(t, addr, InvokePayload{
		GasLimit: evmcommon.InvokeBaseGas, CallNonce: 1, Action: evmcommon.ContractInvokeAPIClose,
	}, gasAssetName, 6000)
	profitInput := OutPoint{TxID: chainhash.Hash{9}.String(), Vout: 0}
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(addr), evmCloseTransferCode(profitAsset, "tb1qdest", "40"))
	runtime.State.SetContractDeployer(ContractGethAddress(addr), caller.String())
	capital := mustUTXO(t, profitInput, addr, profitAsset, 100, 0)
	balance, ok := runtime.State.ManagedBalance(addr)
	require.True(t, ok)
	require.NoError(t, balance.Credit(capital.PhysicalValue(), capital.TxAssets()))
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{closeTx}, Runtime: runtime, GasConfig: gasConfig,
		Block: testBlockContext(100), ResolveCaller: fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("deployer"),
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, addr.Equal(got))
			return []UTXO{capital}, nil
		},
		ResolveScript: evmTestResultScriptResolver(t, addr), ResolveOutput: evmTestResultOutputResolver(addr),
		AssetPrecision: func(name string) (int, bool) { return 0, name == profitAsset || name == gasAssetName },
	})
	require.NoError(t, err)
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusSuccess, result.Execution.Records[0].Status)
	require.True(t, result.Execution.Records[0].CloseContract)
	require.True(t, runtime.State.ContractClosed(ContractGethAddress(addr)))
	outputs, err := evmTestResultOutputResolver(addr)(result.ResultTxs[0])
	require.NoError(t, err)
	requireResultAssetAmount(t, outputs, "tb1qdest", profitAsset, "40")
	requireResultAssetAmount(t, outputs, caller.String(), profitAsset, "42")
	requireResultAssetAmount(t, outputs, "bootstrap", profitAsset, "18")
}

func evmTestResultScriptResolver(t *testing.T, addr ContractAddress) contractframework.ResultRecipientScriptResolver {
	t.Helper()
	return func(output ResultOutput) ([]byte, error) {
		if output.To == addr.MustEncode() {
			return ContractPkScript(addr)
		}
		switch output.To {
		case "deployer":
			return []byte{txscript.OP_2}, nil
		case "bootstrap":
			return []byte{txscript.OP_3}, nil
		case "tb1qdest":
			return []byte{txscript.OP_TRUE}, nil
		default:
			// Use a valid spendable fixture script while preserving the exact
			// recipient text. Raw ASCII can be interpreted as an invalid or
			// unspendable script and disappear from Result output parsing.
			return txscript.NewScriptBuilder().AddData([]byte(output.To)).
				AddOp(txscript.OP_DROP).AddOp(txscript.OP_TRUE).Script()
		}
	}
}

func evmTestResultOutputResolver(addr ContractAddress) contractframework.ResultOutputResolver {
	return func(resultTx *wire.MsgTx) ([]ResultOutput, error) {
		return contractframework.ResultOutputsFromTx(resultTx, TestnetContractPrefix, evmcommon.ParseContractPkScript,
			func(script []byte) (string, bool, error) {
				if len(script) == 1 {
					switch script[0] {
					case txscript.OP_2:
						return "deployer", true, nil
					case txscript.OP_3:
						return "bootstrap", true, nil
					case txscript.OP_TRUE:
						return "tb1qdest", true, nil
					}
				}
				pushes, err := txscript.PushedData(script)
				if err == nil && len(pushes) == 1 {
					return string(pushes[0]), true, nil
				}
				return "", false, err
			})
	}
}

func evmResultSats(outputs []ResultOutput, recipient string) int64 {
	var amount int64
	for _, output := range outputs {
		if output.To == recipient {
			amount += output.Value
		}
	}
	return amount
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
	push2 := func(v int) []byte { return []byte{0x61, byte(v >> 8), byte(v)} }
	code := make([]byte, 0, 64+len(input))
	code = append(code, push2(len(input))...)
	offsetPos := len(code) + 1
	code = append(code, 0x61, 0x00, 0x00)
	code = append(code, 0x60, 0x00, 0x39, 0x60, 0x00, 0x60, 0x00)
	code = append(code, push2(len(input))...)
	code = append(code, 0x60, 0x00, 0x60, 0x00, 0x73)
	code = append(code, AssetPrecompileAddress.Bytes()...)
	code = append(code, 0x5a, 0xf1, 0x50)
	code = append(code, evmReturnTrueCode()...)
	dataOffset := len(code)
	code[offsetPos], code[offsetPos+1] = byte(dataOffset>>8), byte(dataOffset)
	return append(code, input...)
}

func requireResultAssetAmount(t *testing.T, outputs []ResultOutput, to, assetName, amount string) {
	t.Helper()
	total := scommon.NewDefaultDecimal(0)
	found := false
	for _, output := range outputs {
		if output.To != to {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.String() == assetName {
				found = true
				total = total.AddAlignPrecision(asset.Amount.Clone())
			}
		}
	}
	require.True(t, found, "missing asset to=%s asset=%s in %v", to, assetName, outputs)
	require.Equal(t, amount, total.String(), "recipient=%s asset=%s", to, assetName)
}

func requireNoResultAssetAmount(t *testing.T, outputs []ResultOutput, to, assetName string) {
	t.Helper()
	for _, output := range outputs {
		if output.To != to {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.String() == assetName {
				require.Zero(t, asset.Amount.Sign(), "unexpected asset output to=%s asset=%s", to, assetName)
			}
		}
	}
}

func TestContractUTXOOverlayIncludesAndSpendsBlockOutputs(t *testing.T) {
	addr := testContract(t)
	tx := blockResultInvokeTx(t, addr, InvokePayload{GasLimit: 1, CallNonce: 1}, "ordx:ft:gas", 50)
	overlay := contractframework.NewContractUTXOOverlay(contractframework.ContractUTXOOverlayConfig{
		Prefix: TestnetContractPrefix, ContractType: ContractTypeEVM,
	})
	require.NoError(t, overlay.AddTxOutputs(tx, 100))
	utxos, err := overlay.Provider(addr)
	require.NoError(t, err)
	require.Len(t, utxos, 1)
	amount, err := utxos[0].AssetAmount("ordx:ft:gas")
	require.NoError(t, err)
	require.Zero(t, amount.Cmp(mustDefaultDecimal(t, 50)))
	spend := wire.NewMsgTx(2)
	spend.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: tx.TxHash(), Index: 1}, nil, nil))
	require.NoError(t, overlay.ApplyTx(spend, 100))
	utxos, err = overlay.Provider(addr)
	require.NoError(t, err)
	require.Empty(t, utxos)
}

func blockResultInvokeTx(t *testing.T, addr ContractAddress, payload InvokePayload, gasAssetName string, gasAmount int64) *wire.MsgTx {
	t.Helper()
	if payload.Action == "" {
		payload.Action = "call"
	}
	script, err := evmcommon.InvokeNullDataScript(payload)
	require.NoError(t, err)
	contractScript, err := ContractPkScript(addr)
	require.NoError(t, err)
	assetName := wire.NewAssetNameFromString(gasAssetName)
	require.NotNil(t, assetName)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0}})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{Name: *assetName, Amount: *scommon.NewDefaultDecimal(gasAmount)}}, contractScript))
	return tx
}

func blockResultInitCode(runtime []byte) []byte {
	init := []byte{0x60, byte(len(runtime)), 0x60, 0x0c, 0x60, 0x00, 0x39, 0x60, byte(len(runtime)), 0x60, 0x00, 0xf3}
	return append(init, runtime...)
}
