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
	cfg := evm.GasConfig{
		GasAssetName:     gasAsset,
		FixedGasPrice:    1,
		ResultPackingFee: 100,
		MaxGasPerInvoke:  500000,
		MaxGasPerBlock:   1000000,
	}
	blockCtx := evm.BlockContext{
		Number:        100,
		Time:          1710000000,
		GasLimit:      1000000,
		FixedGasPrice: cfg.FixedGasPrice,
	}

	runtimeCode := e2ECallAssetPrecompileCode()
	contract, err := evm.DeriveCreateContractAddress(
		evm.TestnetContractPrefix, caller, deployNonce,
	)
	require.NoError(t, err)

	deployTx := e2EDeployTx(t, contract, deployNonce, e2EInitCode(runtimeCode), 500000, gasAsset)
	probeDeployResultTx := e2EResultTx(t, evm.ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})
	invokeTx := e2EInvokeTx(t, contract, evm.InvokePayload{
		GasLimit:  200000,
		CallNonce: 1,
		Calldata:  evm.EncodeTransferAssetCall(evm.SatoshiAssetName, "tb1qe2edest", "77", nil),
	}, 500000, gasAsset)
	probeInvokeResultTx := e2EResultTx(t, evm.ResultStatusSuccess, 1, []wire.OutPoint{{
		Hash:  invokeTx.TxHash(),
		Index: 1,
	}})

	probeRuntime := evm.NewRuntime(nil)
	probe, err := evm.ExecuteBlock(evm.BlockExecutionRequest{
		Txs: []*wire.MsgTx{
			deployTx, invokeTx, probeDeployResultTx, probeInvokeResultTx,
		},
		Runtime:        probeRuntime,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          blockCtx,
		ResolveCaller:  e2EFixedCaller(caller),
	})
	require.NoError(t, err)
	require.Len(t, probe.Records, 2)
	require.Equal(t, evm.ExecutionKindDeploy, probe.Records[0].Kind)
	require.Equal(t, evm.ExecutionKindInvoke, probe.Records[1].Kind)
	require.True(t, probe.Records[1].RequiresResult)
	require.Len(t, probe.Records[1].AssetIntents, 1)

	invokeRecord := probe.Records[1]
	assetInputHash := chainhash.Hash{0xaa}
	deployGasAssets, err := evm.NewAssetSet(gasAsset, scommon.NewDefaultDecimal(500000))
	require.NoError(t, err)
	invokeGasAssets, err := evm.NewAssetSet(gasAsset, scommon.NewDefaultDecimal(500000))
	require.NoError(t, err)
	available := []evm.UTXO{
		{
			OutPoint: evm.OutPoint{TxID: deployTx.TxID(), Vout: 1},
			Contract: contract,
			Assets:   deployGasAssets,
			Height:   100,
		},
		{
			OutPoint: evm.OutPoint{TxID: invokeTx.TxID(), Vout: 1},
			Contract: contract,
			Assets:   invokeGasAssets,
			Height:   100,
		},
		{
			OutPoint: evm.OutPoint{TxID: assetInputHash.String(), Vout: 0},
			Contract: contract,
			Value:    100,
			Height:   99,
		},
	}
	deployResultTx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status:    evm.ResultStatusSuccess,
		Records:   []evm.ExecutionRecord{probe.Records[0]},
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveScript: e2EResultScriptResolver(t, contract),
	})
	require.NoError(t, err)
	invokeResultTx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status:    evm.ResultStatusSuccess,
		Records:   []evm.ExecutionRecord{invokeRecord},
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveScript: e2EResultScriptResolver(t, contract),
	})
	require.NoError(t, err)

	resultVerifier := evm.CanonicalResultVerifier{
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveOutput: func(tx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(
				tx, evm.TestnetContractPrefix, evmcommon.ParseContractPkScript, e2ERecipientResolver)
		},
	}

	coinbase := e2ECoinbaseTx()
	replayRuntime := evm.NewRuntime(nil)
	replayed, err := evm.ExecuteBlock(evm.BlockExecutionRequest{
		Txs: []*wire.MsgTx{
			coinbase, deployTx, invokeTx, deployResultTx, invokeResultTx,
		},
		Runtime:        replayRuntime,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          blockCtx,
		ResolveCaller:  e2EFixedCaller(caller),
		VerifyResult:   resultVerifier.Verify,
	})
	require.NoError(t, err)
	require.NotEqual(t, [32]byte{}, replayed.StateRoot)
	require.NoError(t, evm.UpsertCoinbaseStateRoot(coinbase, replayed.StateRoot))

	verifiedRuntime := evm.NewRuntime(nil)
	verified, err := evm.ExecuteBlockAndVerifyStateRoot(evm.BlockExecutionRequest{
		Txs: []*wire.MsgTx{
			coinbase, deployTx, invokeTx, deployResultTx, invokeResultTx,
		},
		CoinbaseTx:     coinbase,
		Runtime:        verifiedRuntime,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          blockCtx,
		ResolveCaller:  e2EFixedCaller(caller),
		VerifyResult:   resultVerifier.Verify,
	})
	require.NoError(t, err)
	require.Equal(t, replayed.StateRoot, verified.StateRoot)

	store := contractnode.NewEVMStateStore(e2EDatabase(t))
	parentHash := chainhash.Hash{0x01}
	parentState := evm.NewMemoryStateDB()
	require.NoError(t, store.StoreBlockState(&parentHash, parentState))

	mainHash := chainhash.Hash{0x02}
	require.NoError(t, store.StoreBlockState(&mainHash, replayRuntime.State))

	altBlock := btcutil.NewBlock(&wire.MsgBlock{
		Header: wire.BlockHeader{PrevBlock: parentHash},
		Transactions: []*wire.MsgTx{
			e2ECoinbaseTx(),
		},
	})
	altRuntime, err := store.RuntimeFactory()(altBlock, nil)
	require.NoError(t, err)
	require.Equal(t, parentState.StateRoot(), altRuntime.State.StateRoot())

	loadedMain, err := store.LoadBlockState(&mainHash)
	require.NoError(t, err)
	require.Equal(t, replayed.StateRoot, loadedMain.StateRoot())
}

func TestEVMEndToEndDecimalAssetTransfer(t *testing.T) {
	const (
		gasAsset      = "ordx:ft:gas"
		transferAsset = "ordx:ft:usd"
	)
	caller := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract, err := evm.DeriveCreateContractAddress(evm.TestnetContractPrefix, caller, 8)
	require.NoError(t, err)
	blockCtx := evm.BlockContext{Number: 100, Time: 1710000000, GasLimit: 1000000, FixedGasPrice: 1}
	cfg := evm.GasConfig{
		GasAssetName:     gasAsset,
		FixedGasPrice:    1,
		ResultPackingFee: 100,
		MaxGasPerInvoke:  500000,
		MaxGasPerBlock:   1000000,
	}

	deployTx := e2EDeployTx(t, contract, 8, e2EInitCode(e2ECallAssetPrecompileCode()), 500000, gasAsset)
	invokeTx := e2EInvokeTx(t, contract, evm.InvokePayload{
		GasLimit:  200000,
		CallNonce: 1,
		Calldata:  evm.EncodeTransferAssetCall(transferAsset, "tb1qe2edest", "1.25", nil),
	}, 500000, gasAsset)
	runtime := evm.NewRuntime(nil)
	executor := evm.NewBackend(evm.BlockExecutionRequest{
		Runtime:        runtime,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          blockCtx,
		ResolveCaller:  e2EFixedCaller(caller),
	})
	require.NoError(t, executor.ExecuteTx(deployTx))
	deployPending := executor.PendingRecords()
	require.Len(t, deployPending, 1)
	deployResultTx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status:    evm.ResultStatusSuccess,
		Records:   deployPending,
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return []evm.UTXO{{
				OutPoint: evm.OutPoint{TxID: deployTx.TxID(), Vout: 1},
				Contract: contract,
				Assets:   e2EAsset(gasAsset, 500000),
				Height:   100,
			}}, nil
		},
		ResolveScript: e2EResultScriptResolver(t, contract),
	})
	require.NoError(t, err)
	require.NoError(t, executor.ExecuteTx(deployResultTx))
	require.NoError(t, executor.ExecuteTx(invokeTx))
	pending := executor.PendingRecords()
	require.Len(t, pending, 1)

	assetAmount, err := evm.ParseDecimalAmountString("2.5")
	require.NoError(t, err)
	available := []evm.UTXO{
		{
			OutPoint: evm.OutPoint{TxID: deployTx.TxID(), Vout: 1},
			Contract: contract,
			Assets:   e2EAsset(gasAsset, 500000),
			Height:   100,
		},
		{
			OutPoint: evm.OutPoint{TxID: invokeTx.TxID(), Vout: 1},
			Contract: contract,
			Assets:   e2EAsset(gasAsset, 500000),
			Height:   100,
		},
		{
			OutPoint: evm.OutPoint{TxID: chainhash.Hash{0xbb}.String(), Vout: 0},
			Contract: contract,
			Assets:   e2EDecimalAsset(transferAsset, assetAmount),
			Height:   99,
		},
	}
	resultTx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status:    evm.ResultStatusSuccess,
		Records:   pending,
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveScript: e2EResultScriptResolver(t, contract),
	})
	require.NoError(t, err)
	outputs, err := contractframework.ResultOutputsFromTx(resultTx, evm.TestnetContractPrefix, evmcommon.ParseContractPkScript, e2ERecipientResolver)
	require.NoError(t, err)
	require.NotEmpty(t, outputs)
	wantAmount, err := evm.ParseDecimalAmountString("1.25")
	require.NoError(t, err)
	var foundTransfer bool
	for _, output := range outputs {
		if output.To != "tb1qe2edest" {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.String() == transferAsset &&
				asset.Amount.Cmp(wantAmount) == 0 {
				foundTransfer = true
			}
		}
	}
	require.True(t, foundTransfer)

	verifier := evm.CanonicalResultVerifier{
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveOutput: func(tx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(tx, evm.TestnetContractPrefix, evmcommon.ParseContractPkScript, e2ERecipientResolver)
		},
	}
	replayRuntime := evm.NewRuntime(nil)
	_, err = evm.ExecuteBlock(evm.BlockExecutionRequest{
		Txs:            []*wire.MsgTx{deployTx, invokeTx, deployResultTx, resultTx},
		Runtime:        replayRuntime,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          blockCtx,
		ResolveCaller:  e2EFixedCaller(caller),
		VerifyResult:   verifier.Verify,
	})
	require.NoError(t, err)
}

func TestEVMEndToEndRejectsWrongResultOutput(t *testing.T) {
	const gasAsset = "ordx:ft:gas"
	caller := mustE2EEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract, err := evm.DeriveCreateContractAddress(evm.TestnetContractPrefix, caller, 9)
	require.NoError(t, err)
	blockCtx := evm.BlockContext{Number: 100, Time: 1710000000, GasLimit: 1000000, FixedGasPrice: 1}
	cfg := evm.GasConfig{
		GasAssetName:     gasAsset,
		FixedGasPrice:    1,
		ResultPackingFee: 100,
		MaxGasPerInvoke:  500000,
		MaxGasPerBlock:   1000000,
	}
	deployTx := e2EDeployTx(t, contract, 9, e2EInitCode(e2ECallAssetPrecompileCode()), 500000, gasAsset)
	invokeTx := e2EInvokeTx(t, contract, evm.InvokePayload{
		GasLimit:  200000,
		CallNonce: 1,
		Calldata:  evm.EncodeTransferAssetCall(evm.SatoshiAssetName, "tb1qe2edest", "77", nil),
	}, 500000, gasAsset)

	runtime := evm.NewRuntime(nil)
	executor := evm.NewBackend(evm.BlockExecutionRequest{
		Runtime:        runtime,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          blockCtx,
		ResolveCaller:  e2EFixedCaller(caller),
	})
	require.NoError(t, executor.ExecuteTx(deployTx))
	deployPending := executor.PendingRecords()
	require.Len(t, deployPending, 1)
	deployResultTx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status:    evm.ResultStatusSuccess,
		Records:   deployPending,
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return []evm.UTXO{{
				OutPoint: evm.OutPoint{TxID: deployTx.TxID(), Vout: 1},
				Contract: contract,
				Assets:   e2EAsset(gasAsset, 500000),
				Height:   100,
			}}, nil
		},
		ResolveScript: e2EResultScriptResolver(t, contract),
	})
	require.NoError(t, err)
	require.NoError(t, executor.ExecuteTx(deployResultTx))
	require.NoError(t, executor.ExecuteTx(invokeTx))
	pending := executor.PendingRecords()
	require.Len(t, pending, 1)
	available := []evm.UTXO{
		{
			OutPoint: evm.OutPoint{TxID: deployTx.TxID(), Vout: 1},
			Contract: contract,
			Assets:   e2EAsset(gasAsset, 500000),
			Height:   100,
		},
		{
			OutPoint: evm.OutPoint{TxID: invokeTx.TxID(), Vout: 1},
			Contract: contract,
			Assets:   e2EAsset(gasAsset, 500000),
			Height:   100,
		},
		{
			OutPoint: evm.OutPoint{TxID: chainhash.Hash{0xcc}.String(), Vout: 0},
			Contract: contract,
			Value:    100,
			Height:   99,
		},
	}
	resultTx, err := contractframework.BuildCanonicalResultTx(contractframework.CanonicalResultTxRequest{
		Status:    evm.ResultStatusSuccess,
		Records:   pending,
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveScript: e2EResultScriptResolver(t, contract),
	})
	require.NoError(t, err)
	resultTx.TxOut[0].Value = 78

	verifier := evm.CanonicalResultVerifier{
		GasConfig: cfg,
		UTXOs: func(got evm.ContractAddress) ([]evm.UTXO, error) {
			require.True(t, contract.Equal(got))
			return available, nil
		},
		ResolveOutput: func(tx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(tx, evm.TestnetContractPrefix, evmcommon.ParseContractPkScript, e2ERecipientResolver)
		},
	}
	replayRuntime := evm.NewRuntime(nil)
	_, err = evm.ExecuteBlock(evm.BlockExecutionRequest{
		Txs:            []*wire.MsgTx{deployTx, invokeTx, deployResultTx, resultTx},
		Runtime:        replayRuntime,
		ContractPrefix: evm.TestnetContractPrefix,
		GasConfig:      cfg,
		Block:          blockCtx,
		ResolveCaller:  e2EFixedCaller(caller),
		VerifyResult:   verifier.Verify,
	})
	require.Error(t, err)
}

func e2EDeployTx(t *testing.T, contract evm.ContractAddress, nonce uint64, initCode []byte, gasAmount int64, gasAsset string) *wire.MsgTx {
	t.Helper()
	contractScript, err := evm.ContractPkScript(contract)
	require.NoError(t, err)
	script, err := evmcommon.DeployNullDataScript(evm.DeployPayload{
		GasLimit:    300000,
		DeployNonce: nonce,
		InitCode:    initCode,
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
	return func(*wire.MsgTx, evm.ParsedTx) (evm.EVMAddress, error) {
		return caller, nil
	}
}

func e2EResultScriptResolver(t *testing.T, contract evm.ContractAddress) evm.ResultRecipientScriptResolver {
	t.Helper()
	return func(output evm.ResultOutput) ([]byte, error) {
		if output.To == contract.MustEncode() {
			return evm.ContractPkScript(contract)
		}
		return []byte{0x51}, nil
	}
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
