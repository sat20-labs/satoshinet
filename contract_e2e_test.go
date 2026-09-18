//go:build legacy_evm_e2e

package main

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	contractnode "github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/database"
	_ "github.com/sat20-labs/satoshinet/database/ffldb"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestEVMEndToEndDeployInvokeReplayAndReorg(t *testing.T) {
	const (
		deployNonce = 7
		gasAsset    = "ordx:ft:gas"
	)
	caller := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	cfg := evm.DefaultGasConfig()
	cfg.GasAssetName = gasAsset
	cfg.FixedGasPrice = 1
	cfg.ResultBaseGas = 100
	blockCtx := evm.BlockContext{Number: 100, Time: 1710000000, GasLimit: cfg.MaxGasPerBlock, FixedGasPrice: 1}
	runtimeCode := e2ECallAssetPrecompileCode()
	contract, err := evm.DeriveCreateContractAddress(evm.TestnetContractPrefix, caller, deployNonce)
	require.NoError(t, err)
	deployTx := e2EDeployTx(t, contract, deployNonce, e2EInitCode(runtimeCode), 500000, gasAsset)
	invokeTx := e2EInvokeTx(t, contract, evm.InvokePayload{
		GasLimit: 200000, Action: evmcommon.ContractInvokeAPICall,
		Param: e2ETransferAssetParam(t, evm.SatoshiAssetName, "tb1qe2edest", "77"),
	}, 500000, gasAsset)
	invokeTx.TxOut[1].Value = 100
	work := []*wire.MsgTx{deployTx, invokeTx}

	builderRuntime := evm.NewRuntime(nil)
	built, err := evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
		Txs: work, Runtime: builderRuntime, ContractPrefix: evm.TestnetContractPrefix,
		GasConfig: cfg, Block: blockCtx, ResolveCaller: e2EFixedCaller(caller),
		ResolveGasRefundRecipient: e2EFixedRefundRecipient("tb1qe2edest"),
		ResolveScript: e2EResultScriptResolver(t, contract),
		ResolveOutput: e2EResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, built.Execution.Records, 2)
	require.Len(t, built.ResultTxs, 1)
	require.Equal(t, evm.ExecutionKindDeploy, built.Execution.Records[0].Kind)
	require.Equal(t, evm.ExecutionKindInvoke, built.Execution.Records[1].Kind)

	replayRuntime := evm.NewRuntime(nil)
	replayed, err := evm.ExecuteWorkBlock(evm.BlockExecutionRequest{
		Txs: work, Runtime: replayRuntime, ContractPrefix: evm.TestnetContractPrefix,
		GasConfig: cfg, Block: blockCtx, ResolveCaller: e2EFixedCaller(caller),
		ResolveGasRefundRecipient: e2EFixedRefundRecipient("tb1qe2edest"),
		ResolveResultScript: e2EResultScriptResolver(t, contract),
	})
	require.NoError(t, err)
	require.Equal(t, built.Execution.StateRoot, replayed.StateRoot)
	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, work, evm.TestnetContractPrefix, evm.ContractTypeEVM)
	verifier := evm.CanonicalResultVerifier{
		GasConfig: cfg, UTXOs: provider, ResolveOutput: e2EResultOutputResolver(contract),
		ResolveScript: e2EResultScriptResolver(t, contract),
	}
	require.NoError(t, evm.VerifyResultTxs(evm.ResultVerifyRequest{
		ResultTxs: built.ResultTxs, Execution: replayed, VerifyResult: verifier.Verify,
	}))

	coinbase := e2ECoinbaseTx()
	require.NoError(t, evm.UpsertCoinbaseStateRoot(coinbase, replayed.StateRoot))
	require.NoError(t, evm.VerifyCoinbaseStateRoot(coinbase, replayed.StateRoot))

	store := contractnode.NewEVMStateStore(e2EDatabase(t))
	parentHash := chainhash.Hash{0x01}
	parentState := evm.NewMemoryStateDB()
	require.NoError(t, store.StoreBlockState(&parentHash, parentState))
	mainHash := chainhash.Hash{0x02}
	require.NoError(t, store.StoreBlockState(&mainHash, replayRuntime.State))
	altBlock := btcutil.NewBlock(&wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: parentHash}, Transactions: []*wire.MsgTx{e2ECoinbaseTx()}})
	altRuntime, err := store.RuntimeFactory()(altBlock, nil)
	require.NoError(t, err)
	require.Equal(t, parentState.StateRoot(), altRuntime.State.StateRoot())
	loadedMain, err := store.LoadBlockState(&mainHash)
	require.NoError(t, err)
	require.Equal(t, replayed.StateRoot, loadedMain.StateRoot())
}

func TestEVMEndToEndDecimalAssetTransfer(t *testing.T) {
	const (
		gasAsset = "ordx:ft:gas"
		transferAsset = "ordx:ft:usd"
	)
	caller := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract, err := evm.DeriveCreateContractAddress(evm.TestnetContractPrefix, caller, 8)
	require.NoError(t, err)
	cfg := evm.DefaultGasConfig()
	cfg.GasAssetName = gasAsset
	cfg.FixedGasPrice = 1
	cfg.ResultBaseGas = 100
	blockCtx := evm.BlockContext{Number: 100, Time: 1710000000, GasLimit: cfg.MaxGasPerBlock, FixedGasPrice: 1}
	deployTx := e2EDeployTx(t, contract, 8, e2EInitCode(e2ECallAssetPrecompileCode()), 500000, gasAsset)
	invokeTx := e2EInvokeTx(t, contract, evm.InvokePayload{
		GasLimit: 200000, Action: evmcommon.ContractInvokeAPICall,
		Param: e2ETransferAssetParam(t, transferAsset, "tb1qe2edest", "1.25"),
	}, 500000, gasAsset)
	amount, err := evm.ParseDecimalAmountString("2.5")
	require.NoError(t, err)
	require.NoError(t, invokeTx.TxOut[1].Assets.Merge(e2EDecimalAsset(transferAsset, amount)))
	work := []*wire.MsgTx{deployTx, invokeTx}
	runtime := evm.NewRuntime(nil)
	built, err := evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
		Txs: work, Runtime: runtime, ContractPrefix: evm.TestnetContractPrefix,
		GasConfig: cfg, Block: blockCtx, ResolveCaller: e2EFixedCaller(caller),
		ResolveGasRefundRecipient: e2EFixedRefundRecipient("tb1qe2edest"),
		ResolveScript: e2EResultScriptResolver(t, contract), ResolveOutput: e2EResultOutputResolver(contract),
		AssetPrecision: func(name string) (int, bool) { return 2, name == transferAsset },
	})
	require.NoError(t, err)
	require.Len(t, built.ResultTxs, 1)
	outputs, err := e2EResultOutputResolver(contract)(built.ResultTxs[0])
	require.NoError(t, err)
	want, err := evm.ParseDecimalAmountString("1.25")
	require.NoError(t, err)
	var external, retained bool
	for _, output := range outputs {
		for _, asset := range output.Assets {
			if asset.Name.String() != transferAsset || asset.Amount.Cmp(want) != 0 { continue }
			if output.To == "tb1qe2edest" { external = true }
			if output.To == contract.MustEncode() { retained = true }
		}
	}
	require.True(t, external)
	require.True(t, retained)

	replayRuntime := evm.NewRuntime(nil)
	replayed, err := evm.ExecuteWorkBlock(evm.BlockExecutionRequest{
		Txs: work, Runtime: replayRuntime, ContractPrefix: evm.TestnetContractPrefix,
		GasConfig: cfg, Block: blockCtx, ResolveCaller: e2EFixedCaller(caller),
		ResolveGasRefundRecipient: e2EFixedRefundRecipient("tb1qe2edest"),
		ResolveResultScript: e2EResultScriptResolver(t, contract),
		AssetPrecision: func(name string) (int, bool) { return 2, name == transferAsset },
	})
	require.NoError(t, err)
	require.Equal(t, evm.ResultStatusSuccess, replayed.Records[1].Status)
	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, work, evm.TestnetContractPrefix, evm.ContractTypeEVM)
	verifier := evm.CanonicalResultVerifier{GasConfig: cfg, UTXOs: provider,
		ResolveOutput: e2EResultOutputResolver(contract), ResolveScript: e2EResultScriptResolver(t, contract)}
	require.NoError(t, evm.VerifyResultTxs(evm.ResultVerifyRequest{ResultTxs: built.ResultTxs, Execution: replayed, VerifyResult: verifier.Verify}))
}

func TestEVMEndToEndRejectsWrongResultOutput(t *testing.T) {
	const gasAsset = "ordx:ft:gas"
	caller := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract, err := evm.DeriveCreateContractAddress(evm.TestnetContractPrefix, caller, 9)
	require.NoError(t, err)
	cfg := evm.DefaultGasConfig()
	cfg.GasAssetName = gasAsset
	cfg.FixedGasPrice = 1
	cfg.ResultBaseGas = 100
	blockCtx := evm.BlockContext{Number: 100, Time: 1710000000, GasLimit: cfg.MaxGasPerBlock, FixedGasPrice: 1}
	deployTx := e2EDeployTx(t, contract, 9, e2EInitCode(e2ECallAssetPrecompileCode()), 500000, gasAsset)
	invokeTx := e2EInvokeTx(t, contract, evm.InvokePayload{
		GasLimit: 200000, Action: evmcommon.ContractInvokeAPICall,
		Param: e2ETransferAssetParam(t, evm.SatoshiAssetName, "tb1qe2edest", "77"),
	}, 500000, gasAsset)
	invokeTx.TxOut[1].Value = 100
	work := []*wire.MsgTx{deployTx, invokeTx}
	built, err := evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
		Txs: work, Runtime: evm.NewRuntime(nil), ContractPrefix: evm.TestnetContractPrefix,
		GasConfig: cfg, Block: blockCtx, ResolveCaller: e2EFixedCaller(caller),
		ResolveGasRefundRecipient: e2EFixedRefundRecipient("tb1qe2edest"),
		ResolveScript: e2EResultScriptResolver(t, contract), ResolveOutput: e2EResultOutputResolver(contract),
	})
	require.NoError(t, err)
	require.Len(t, built.ResultTxs, 1)
	bad := built.ResultTxs[0].Copy()
	require.NotEmpty(t, bad.TxOut)
	bad.TxOut[0].Value++

	replayRuntime := evm.NewRuntime(nil)
	replayed, err := evm.ExecuteWorkBlock(evm.BlockExecutionRequest{
		Txs: work, Runtime: replayRuntime, ContractPrefix: evm.TestnetContractPrefix,
		GasConfig: cfg, Block: blockCtx, ResolveCaller: e2EFixedCaller(caller),
		ResolveGasRefundRecipient: e2EFixedRefundRecipient("tb1qe2edest"),
		ResolveResultScript: e2EResultScriptResolver(t, contract),
	})
	require.NoError(t, err)
	provider := contractframework.ContractUTXOProviderWithTxOutputs(nil, work, evm.TestnetContractPrefix, evm.ContractTypeEVM)
	verifier := evm.CanonicalResultVerifier{GasConfig: cfg, UTXOs: provider,
		ResolveOutput: e2EResultOutputResolver(contract), ResolveScript: e2EResultScriptResolver(t, contract)}
	err = evm.VerifyResultTxs(evm.ResultVerifyRequest{ResultTxs: []*wire.MsgTx{bad}, Execution: replayed, VerifyResult: verifier.Verify})
	require.Error(t, err)
}

func e2EDeployTx(t *testing.T, contract evm.ContractAddress, nonce uint64, initCode []byte, gasAmount int64, gasAsset string) *wire.MsgTx {
	t.Helper()
	contractScript, err := evm.ContractPkScript(contract)
	require.NoError(t, err)
	script, err := evmcommon.DeployNullDataScript(evm.DeployPayload{
		GasLimit:        evm.DefaultGasConfig().DeployBaseGas,
		DeployNonce:     nonce,
		ContractContent: initCode,
	})
	require.NoError(t, err)

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{0x10}, Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, e2EAsset(gasAsset, gasAmount), contractScript))
	return tx
}

func e2EInvokeTx(t *testing.T, contract evm.ContractAddress, payload evm.InvokePayload, gasAmount int64, gasAsset string) *wire.MsgTx {
	t.Helper()
	contractScript, err := evm.ContractPkScript(contract)
	require.NoError(t, err)
	invokeScript, err := evmcommon.InvokeNullDataScript(payload)
	require.NoError(t, err)

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{0x20}, Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(0, e2EAsset(gasAsset, gasAmount), contractScript))
	return tx
}

func e2ETransferAssetParam(t *testing.T, assetName, to, amount string) []byte {
	t.Helper()
	return evm.EncodeTransferAssetCall(assetName, to, amount, nil)
}

func e2EResultTx(t *testing.T, status evm.ResultStatus, count uint16, inputs []wire.OutPoint) *wire.MsgTx {
	t.Helper()
	script, err := evmcommon.ResultNullDataScript(evm.ResultPayload{
		Status:      status,
		ResultCount: count,
	})
	require.NoError(t, err)

	tx := wire.NewMsgTx(2)
	for i := range inputs {
		tx.AddTxIn(wire.NewTxIn(&inputs[i], nil, nil))
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx
}

func e2ECoinbaseTx() *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{
		Hash:  chainhash.Hash{},
		Index: wire.MaxPrevOutIndex,
	}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, []byte{0x51}))
	return tx
}

func e2EFixedCaller(caller evm.EVMAddress) evm.CallerResolver {
	return func(*wire.MsgTx, evmcommon.Tx) (string, error) {
		return caller.String(), nil
	}
}

func e2EResultScriptResolver(t *testing.T, contract evm.ContractAddress) evm.ResultRecipientScriptResolver {
	t.Helper()
	return func(output evm.ResultOutput) ([]byte, error) {
		if output.To == contract.MustEncode() { return evm.ContractPkScript(contract) }
		return []byte{0x51}, nil
	}
}

func e2EResultOutputResolver(contract evm.ContractAddress) evm.ResultOutputResolver {
	return func(tx *wire.MsgTx) ([]evm.ResultOutput, error) {
		return contractframework.ResultOutputsFromTx(tx, evm.TestnetContractPrefix,
			evmcommon.ParseContractPkScript, e2ERecipientResolver)
	}
}

func e2EFixedRefundRecipient(recipient string) evm.GasRefundRecipientResolver {
	return func(*wire.MsgTx, evmcommon.Tx) (string, bool, error) { return recipient, true, nil }
}

func e2ERecipientResolver(pkScript []byte) (string, bool, error) {
	if len(pkScript) == 1 && pkScript[0] == 0x51 {
		return "tb1qe2edest", true, nil
	}
	return "", false, nil
}

func e2EInitCode(runtime []byte) []byte {
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

func e2ECallAssetPrecompileCode() []byte {
	code := []byte{
		0x36,
		0x60, 0x00,
		0x60, 0x00,
		0x37,
		0x60, 0x00,
		0x60, 0x00,
		0x36,
		0x60, 0x00,
		0x60, 0x00,
		0x73,
	}
	code = append(code, evm.AssetPrecompileAddress.Bytes()...)
	code = append(code, 0x61, 0xc3, 0x50, 0xf1, 0x00)
	return code
}

func e2EAsset(name string, amount int64) wire.TxAssets {
	assetName := wire.NewAssetNameFromString(name)
	return wire.TxAssets{{
		Name:   *assetName,
		Amount: *scommon.NewDefaultDecimal(amount),
	}}
}

func e2EDecimalAsset(name string, amount *scommon.Decimal) wire.TxAssets {
	assetName := wire.NewAssetNameFromString(name)
	return wire.TxAssets{{
		Name:   *assetName,
		Amount: *amount.Clone(),
	}}
}

func e2EUTXO(outpoint evm.OutPoint, contractAddr evm.ContractAddress, value int64,
	assets wire.TxAssets, height int64) evm.UTXO {

	return contractframework.UTXOFromTxOutput(outpoint, contractAddr, height, &wire.TxOut{
		Value:  value,
		Assets: assets,
	})
}

func mustE2EEVMAddress(t *testing.T, address string) evm.EVMAddress {
	t.Helper()
	parsed, err := evm.ParseEVMAddressHex(address)
	require.NoError(t, err)
	return parsed
}

func e2EDatabase(t *testing.T) database.DB {
	t.Helper()
	db, err := database.Create("ffldb", t.TempDir(), wire.TestNet)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})
	return db
}
