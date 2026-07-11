package node

import (
	"strings"
	"testing"
	"time"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/mining"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestEVMBlockExecutionValidatorVerifiesCoinbaseStateRoot(t *testing.T) {
	callerAddr, err := btcutil.NewAddressPubKeyHash(testBytes(20, 0x11), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	caller := testEVMCallerFromBTCAddress(callerAddr)
	deployTx := testEVMDeployTxForCaller(t, 3, testReturn42InitCode(), caller)
	blockTime := time.Unix(1710000000, 0)
	prevView := testPreviousOutputView(t, deployTx.TxIn[0].PreviousOutPoint, callerAddr)

	built, err := evm.BuildBlockResultTxs(evm.BlockResultBuildRequest{
		Txs:           []*wire.MsgTx{deployTx},
		Runtime:       evm.NewRuntime(nil),
		Block:         evm.BlockContext{Number: 100, Time: uint64(blockTime.Unix()), GasLimit: evm.DefaultGasConfig().MaxGasPerBlock, FixedGasPrice: 1},
		ResolveCaller: fixedTestCaller(caller),
		ResolveGasRefundRecipient: evm.LastInputPreviousOutputGasRefundRecipientResolver(&chaincfg.TestNetParams,
			previousOutputScriptResolver(prevView)),
		GasConfig: evm.GasConfig{GasAssetName: evm.DefaultGasConfig().GasAssetName, FixedGasPrice: 1, MaxGasPerBlock: evm.DefaultGasConfig().MaxGasPerBlock},
		ResolveScript: func(output evm.ResultOutput) ([]byte, error) {
			contract, err := evm.DecodeContractAddress(output.To)
			if err == nil {
				return evm.ContractPkScript(contract)
			}
			if output.To == callerAddr.EncodeAddress() {
				return txscript.PayToAddrScript(callerAddr)
			}
			return []byte{txscript.OP_TRUE}, nil
		},
		ResolveOutput: func(resultTx *wire.MsgTx) ([]evm.ResultOutput, error) {
			return contractframework.ResultOutputsFromTx(resultTx, evm.TestnetContractPrefix, evmcommon.ParseContractPkScript,
				func(pkScript []byte) (string, bool, error) {
					if script, err := txscript.PayToAddrScript(callerAddr); err == nil && string(script) == string(pkScript) {
						return callerAddr.EncodeAddress(), true, nil
					}
					if len(pkScript) == 1 && pkScript[0] == txscript.OP_TRUE {
						return "test-recipient", true, nil
					}
					return "", false, nil
				})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	coinbase := testEVMCoinbaseTx()
	if err := evm.UpsertCoinbaseStateRoot(coinbase, built.Execution.StateRoot); err != nil {
		t.Fatal(err)
	}
	blockTxs := append([]*wire.MsgTx{coinbase, deployTx}, built.ResultTxs...)
	block := btcutil.NewBlock(&wire.MsgBlock{Header: wire.BlockHeader{Timestamp: blockTime}, Transactions: blockTxs})
	block.SetHeight(100)

	validator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{
		GasConfig:    evm.GasConfig{GasAssetName: evm.DefaultGasConfig().GasAssetName, FixedGasPrice: 1, MaxGasPerBlock: evm.DefaultGasConfig().MaxGasPerBlock},
		VerifyResult: func(*wire.MsgTx, []evm.ExecutionRecord) error { return nil },
	})
	if err := validator.ValidateEVMBlock(block, prevView); err != nil {
		t.Fatal(err)
	}
	postState, ok := validator.EVMBlockPostState(block.Hash())
	if !ok {
		t.Fatal("expected EVM validator to expose post-state")
	}
	if postState.StateRoot() != built.Execution.StateRoot {
		t.Fatal("unexpected validator post-state root")
	}

	var wrongRoot [32]byte
	wrongRoot[0] = 1
	if err := evm.UpsertCoinbaseStateRoot(coinbase, wrongRoot); err != nil {
		t.Fatal(err)
	}
	err = validator.ValidateEVMBlock(block, prevView)
	if err == nil {
		t.Fatal("expected wrong EVM state root to be rejected")
	}
	ruleErr, ok := err.(blockchain.RuleError)
	if !ok || ruleErr.ErrorCode != blockchain.ErrInvalidEVMBlock {
		t.Fatalf("got %T %[1]v, want blockchain.ErrInvalidEVMBlock", err)
	}

	missingRootBlock := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: blockTime},
		Transactions: append([]*wire.MsgTx{testEVMCoinbaseTx(), deployTx}, built.ResultTxs...),
	})
	missingRootBlock.SetHeight(100)
	err = validator.ValidateEVMBlock(missingRootBlock, prevView)
	if err == nil {
		t.Fatal("expected missing EVM state root to be rejected")
	}
	ruleErr, ok = err.(blockchain.RuleError)
	if !ok || ruleErr.ErrorCode != blockchain.ErrInvalidEVMBlock {
		t.Fatalf("got %T %[1]v, want blockchain.ErrInvalidEVMBlock", err)
	}
}

func TestEVMResultUsesPrevCaller(t *testing.T) {
	callerAddr, err := btcutil.NewAddressPubKeyHash(testBytes(20, 0x22), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	caller := testEVMCallerFromBTCAddress(callerAddr)
	deployTx := testEVMDeployTxForCaller(t, 7, testReturn42InitCode(), caller)
	view := testPreviousAssetOutputView(t, deployTx.TxIn[0].PreviousOutPoint, callerAddr)
	db := testEVMStateDB(t)
	builder, err := NewResultBuilder(Config{
		DB:               db,
		ChainParams:      &chaincfg.TestNetParams,
		BootstrapAddress: "tb1pbootstrap",
		EVMResolveRecipient: func(pkScript []byte) (string, bool, error) {
			if script, err := txscript.PayToAddrScript(callerAddr); err == nil && string(script) == string(pkScript) {
				return callerAddr.EncodeAddress(), true, nil
			}
			return "", false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := builder(mining.ContractBuildRequest{
		Txs:       []*btcutil.Tx{btcutil.NewTx(deployTx)},
		Height:    100,
		Timestamp: time.Unix(1710000000, 0),
		UtxoView:  view,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ResultTxs) == 0 {
		t.Fatal("expected EVM deploy result tx")
	}
	if result.StateRoot == ([32]byte{}) {
		t.Fatal("expected EVM state root")
	}
}

func TestEVMBlockExecutionValidatorAllowsBlocksWithoutEVMWork(t *testing.T) {
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx()},
	})
	validator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{})
	if err := validator.ValidateEVMBlock(block, blockchain.NewUtxoViewpoint()); err != nil {
		t.Fatal(err)
	}
}

func TestEVMBlockExecutionValidatorResolvesTriggers(t *testing.T) {
	contract := testContractAddressForBlockchain(t)
	runtime := evm.NewRuntime(nil)
	runtime.SetCode(evm.ContractAddressHash(contract), []byte{0x00})
	if err := runtime.State.RegisterTrigger(evm.Trigger{
		ID:       "vault-release",
		Contract: contract,
		Kind:     evm.TriggerAtHeight,
		Height:   100,
		GasLimit: evm.DefaultGasConfig().TriggerBaseGas,
	}); err != nil {
		t.Fatal(err)
	}
	blockTime := time.Unix(1710000000, 0)
	triggerGasInput := wire.OutPoint{Hash: chainhash.Hash{9}, Index: 0}
	gasAsset := evm.DefaultGasConfig().GasAssetName
	gasConfig := evm.DefaultGasConfig()
	gasConfig.FixedGasPrice = 1
	triggerGasFee := testEVMValidatorContractFundingFee(t,
		evm.ExecutionKindTrigger, evm.DefaultGasConfig().TriggerBaseGas, true, 100)
	resultTx := testEVMResultTxWithInputs(t, evm.ResultStatusSuccess, 1, []wire.OutPoint{triggerGasInput})
	contractScript, err := evm.ContractPkScript(contract)
	if err != nil {
		t.Fatal(err)
	}
	resultFee, err := evm.DefaultGasConfig().ResultFee(100)
	if err != nil {
		t.Fatal(err)
	}
	change := triggerGasFee.SubAlignPrecision(resultFee)
	resultTx.TxOut = append([]*wire.TxOut{wire.NewTxOut(0, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(gasAsset),
		Amount: *change,
	}}, contractScript)}, resultTx.TxOut...)
	contractUTXOs := func(got evm.ContractAddress) ([]evm.UTXO, error) {
		if !contract.Equal(got) {
			t.Fatalf("unexpected contract %s", got.MustEncode())
		}
		outpoint := evm.WireOutPointToEVM(triggerGasInput)
		return []evm.UTXO{
			contractframework.UTXOFromTxOutput(outpoint, contract, 0, &wire.TxOut{
				Assets: wire.TxAssets{{
					Name:   *wire.NewAssetNameFromString(gasAsset),
					Amount: *triggerGasFee,
				}},
			}),
		}, nil
	}

	executed, err := evm.ExecuteBlock(evm.BlockExecutionRequest{
		Runtime:   runtime.Clone(),
		GasConfig: gasConfig,
		Block:     evm.BlockContext{Number: 100, Time: uint64(blockTime.Unix()), GasLimit: evm.DefaultGasConfig().MaxGasPerBlock, FixedGasPrice: 1},
		ResolveTriggers: func(evm.TriggerResolutionContext) ([]evm.TriggerCall, error) {
			return []evm.TriggerCall{{
				Trigger:  evm.Trigger{ID: "vault-release", Contract: contract, Kind: evm.TriggerAtHeight, Height: 100},
				GasLimit: evm.DefaultGasConfig().TriggerBaseGas,
			}}, nil
		},
		ContractUTXOs: contractUTXOs,
		AssetPrecision: func(string) (int, bool) {
			return 0, true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	coinbase := testEVMCoinbaseTx()
	if err := evm.UpsertCoinbaseStateRoot(coinbase, executed.StateRoot); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: blockTime},
		Transactions: []*wire.MsgTx{coinbase, resultTx},
	})
	block.SetHeight(100)

	validator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{
		GasConfig: gasConfig,
		NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*evm.Runtime, error) {
			return runtime.Clone(), nil
		},
		ResolveTriggers: func(evm.TriggerResolutionContext) ([]evm.TriggerCall, error) {
			return []evm.TriggerCall{{
				Trigger:  evm.Trigger{ID: "vault-release", Contract: contract, Kind: evm.TriggerAtHeight, Height: 100},
				GasLimit: evm.DefaultGasConfig().TriggerBaseGas,
			}}, nil
		},
		ContractUTXOs: contractUTXOs,
		AssetPrecision: func(string) (int, bool) {
			return 0, true
		},
	})
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[triggerGasInput] = blockchain.NewUtxoEntry(wire.NewTxOut(1, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(gasAsset),
		Amount: *triggerGasFee,
	}}, contractScript), 1, false)
	if err := validator.ValidateEVMBlock(block, view); err != nil {
		t.Fatal(err)
	}
}

func TestEVMBlockExecutionValidatorAdvancesDueTriggerWithoutResult(t *testing.T) {
	contract := testContractAddressForBlockchain(t)
	runtime := evm.NewRuntime(nil)
	runtime.SetCode(evm.ContractAddressHash(contract), []byte{0x00})
	if err := runtime.State.RegisterTrigger(evm.Trigger{
		ID:       "height-trigger",
		Contract: contract,
		Kind:     evm.TriggerAtHeight,
		Height:   100,
		GasLimit: evm.DefaultGasConfig().TriggerBaseGas,
	}); err != nil {
		t.Fatal(err)
	}
	parentRoot := runtime.State.StateRoot()
	blockTime := time.Unix(1710000000, 0)
	gasConfig := evm.DefaultGasConfig()
	gasConfig.FixedGasPrice = 1
	contractUTXOs := func(evm.ContractAddress) ([]evm.UTXO, error) {
		return nil, nil
	}
	executed, err := evm.ExecuteBlock(evm.BlockExecutionRequest{
		Runtime:       runtime.Clone(),
		GasConfig:     gasConfig,
		Block:         evm.BlockContext{Number: 100, Time: uint64(blockTime.Unix()), GasLimit: gasConfig.MaxGasPerBlock, FixedGasPrice: 1},
		ContractUTXOs: contractUTXOs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(executed.Records) != 0 {
		t.Fatalf("expected state-only trigger, got records: %#v", executed.Records)
	}
	if executed.StateRoot != parentRoot {
		t.Fatal("gas-starved trigger must remain pending without changing state root")
	}

	missingRootBlock := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: blockTime},
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx()},
	})
	missingRootBlock.SetHeight(100)
	validator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{
		GasConfig: gasConfig,
		NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*evm.Runtime, error) {
			return runtime.Clone(), nil
		},
		ContractUTXOs: contractUTXOs,
	})
	err = validator.ValidateEVMBlock(missingRootBlock, blockchain.NewUtxoViewpoint())
	if err == nil || !strings.Contains(err.Error(), "missing EVM state root commitment") {
		t.Fatalf("unexpected missing root error: %v", err)
	}

	coinbase := testEVMCoinbaseTx()
	if err := evm.UpsertCoinbaseStateRoot(coinbase, executed.StateRoot); err != nil {
		t.Fatal(err)
	}
	block := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: blockTime},
		Transactions: []*wire.MsgTx{coinbase},
	})
	block.SetHeight(100)
	if err := validator.ValidateEVMBlock(block, blockchain.NewUtxoViewpoint()); err != nil {
		t.Fatal(err)
	}
	postState, ok := validator.EVMBlockPostState(block.Hash())
	if !ok {
		t.Fatal("expected EVM validator to expose post-state")
	}
	if postState.StateRoot() != executed.StateRoot {
		t.Fatal("unexpected EVM post-state root")
	}
}

func testEVMCoinbaseTx() *wire.MsgTx {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{
		Hash:  chainhash.Hash{},
		Index: wire.MaxPrevOutIndex,
	}, nil, nil))
	return tx
}

func testEVMDeployTx(t *testing.T, nonce uint64, initCode []byte) *wire.MsgTx {
	t.Helper()
	caller := mustTestEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	return testEVMDeployTxForCaller(t, nonce, initCode, caller)
}

func testEVMDeployTxForCaller(t *testing.T, nonce uint64, initCode []byte, caller evm.EVMAddress) *wire.MsgTx {
	t.Helper()
	contract, err := evm.DeriveCreateContractAddress(evm.TestnetContractPrefix, caller, nonce)
	if err != nil {
		t.Fatal(err)
	}
	contractScript, err := evm.ContractPkScript(contract)
	if err != nil {
		t.Fatal(err)
	}
	script, err := evmcommon.DeployNullDataScript(evm.DeployPayload{
		Type:            evm.ContractTypeEVM,
		SubType:         "sol",
		GasLimit:        evm.DefaultGasConfig().DeployBaseGas,
		DeployNonce:     nonce,
		ContractContent: initCode,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(evm.DefaultGasConfig().GasAssetName),
		Amount: *testEVMValidatorGasFee(t, evm.DefaultGasConfig().DeployBaseGas),
	}}, contractScript))
	return tx
}

func testEVMValidatorGasFee(t *testing.T, gas int64) *scommon.Decimal {
	t.Helper()
	fee, err := evmcommon.GasFeeDecimalAtHeight(gas, 0)
	if err != nil {
		t.Fatalf("gas fee failed: %v", err)
	}
	return fee
}

func testEVMValidatorContractFundingFee(t *testing.T,
	kind evm.ExecutionKind, gas int64, needsResult bool, height uint64) *scommon.Decimal {

	t.Helper()
	fee, err := evm.DefaultGasConfig().ContractFundingFee(kind, gas, needsResult, height)
	if err != nil {
		t.Fatalf("contract funding fee failed: %v", err)
	}
	return fee
}

func testPreviousOutputView(t *testing.T, outpoint wire.OutPoint, address btcutil.Address) *blockchain.UtxoViewpoint {
	t.Helper()
	script, err := txscript.PayToAddrScript(address)
	if err != nil {
		t.Fatal(err)
	}
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[outpoint] = blockchain.NewUtxoEntry(wire.NewTxOut(1, nil, script), 1, false)
	return view
}

func testPreviousAssetOutputView(t *testing.T, outpoint wire.OutPoint, address btcutil.Address) *blockchain.UtxoViewpoint {
	t.Helper()
	script, err := txscript.PayToAddrScript(address)
	if err != nil {
		t.Fatal(err)
	}
	view := blockchain.NewUtxoViewpoint()
	view.Entries()[outpoint] = blockchain.NewUtxoEntry(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(evm.DefaultGasConfig().GasAssetName),
		Amount: *scommon.NewDefaultDecimal(20000),
	}}, script), 1, false)
	return view
}

func testEVMCallerFromBTCAddress(address btcutil.Address) evm.EVMAddress {
	var out evm.EVMAddress
	hash := btcutil.Hash160([]byte(address.EncodeAddress()))
	copy(out[:], hash)
	return out
}

func testBytes(n int, value byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = value
	}
	return out
}

func testEVMResultTx(t *testing.T, status evm.ResultStatus, count uint16) *wire.MsgTx {
	return testEVMResultTxWithInputs(t, status, count, []wire.OutPoint{
		{Hash: chainhash.Hash{2}, Index: 0},
	})
}

func testEVMResultTxWithInputs(t *testing.T, status evm.ResultStatus, count uint16, inputs []wire.OutPoint) *wire.MsgTx {
	t.Helper()
	script, err := evmcommon.ResultNullDataScript(evm.ResultPayload{
		Status:      status,
		ResultCount: count,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	for i := range inputs {
		tx.AddTxIn(wire.NewTxIn(&inputs[i], nil, nil))
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx
}

func mustTestEVMAddress(t *testing.T, address string) evm.EVMAddress {
	t.Helper()
	parsed, err := evm.ParseEVMAddressHex(address)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func fixedTestCaller(caller evm.EVMAddress) evm.CallerResolver {
	return func(*wire.MsgTx, evmcommon.Tx) (string, error) {
		return caller.String(), nil
	}
}

func testReturn42InitCode() []byte {
	return []byte{
		0x60, 0x0a,
		0x60, 0x0c,
		0x60, 0x00,
		0x39,
		0x60, 0x0a,
		0x60, 0x00,
		0xf3,
		0x60, 0x2a,
		0x60, 0x00,
		0x52,
		0x60, 0x20,
		0x60, 0x00,
		0xf3,
	}
}

func testContractAddressForBlockchain(t *testing.T) evm.ContractAddress {
	t.Helper()
	addr := mustTestEVMAddress(t, "0x2222222222222222222222222222222222222222")
	contract, err := evm.NewContractAddress(evm.TestnetContractPrefix, evm.AddressVersionV1, evm.ContractTypeEVM, addr)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func callAssetPrecompileCodeForBlockchain() []byte {
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
