package blockchain

import (
	"errors"
	"testing"
	"time"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type testEVMBlockValidator struct {
	called bool
	err    error
}

func (v *testEVMBlockValidator) ValidateEVMBlock(block *btcutil.Block, view *UtxoViewpoint) error {
	v.called = true
	if block == nil {
		return errors.New("missing block")
	}
	if view == nil {
		return errors.New("missing view")
	}
	return v.err
}

func TestValidateEVMBlockHook(t *testing.T) {
	block := btcutil.NewBlock(&wire.MsgBlock{})
	view := NewUtxoViewpoint()
	validator := &testEVMBlockValidator{}
	chain := &BlockChain{evmBlockValidator: validator}

	if err := chain.validateEVMBlock(block, view); err != nil {
		t.Fatal(err)
	}
	if !validator.called {
		t.Fatal("expected EVM validator to be called")
	}

	wantErr := errors.New("evm invalid")
	validator.err = wantErr
	if err := chain.validateEVMBlock(block, view); !errors.Is(err, wantErr) {
		t.Fatalf("got %v want %v", err, wantErr)
	}
}

func TestValidateEVMBlockHookAllowsNilValidator(t *testing.T) {
	chain := &BlockChain{}
	if err := chain.validateEVMBlock(btcutil.NewBlock(&wire.MsgBlock{}), NewUtxoViewpoint()); err != nil {
		t.Fatal(err)
	}
}

func TestEVMBlockExecutionValidatorVerifiesCoinbaseStateRoot(t *testing.T) {
	callerAddr, err := btcutil.NewAddressPubKeyHash(testBytes(20, 0x11), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	caller := testEVMCallerFromBTCAddress(callerAddr)
	deployTx := testEVMDeployTxForCaller(t, 3, testReturn42InitCode(), caller)
	resultTx := testEVMResultTxWithInputs(t, evm.ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})
	blockTime := time.Unix(1710000000, 0)

	executed, err := evm.ExecuteBlock(evm.BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, resultTx},
		Runtime:       evm.NewRuntime(nil),
		Block:         evm.BlockContext{Number: 100, Time: uint64(blockTime.Unix()), GasLimit: 1000000, FixedGasPrice: 1},
		ResolveCaller: fixedTestCaller(caller),
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
		Transactions: []*wire.MsgTx{coinbase, deployTx, resultTx},
	})
	block.SetHeight(100)

	validator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{
		GasConfig: evm.GasConfig{GasAssetName: evm.DefaultGasConfig().GasAssetName, FixedGasPrice: 1, MaxGasPerBlock: 1000000},
	})
	if err := validator.ValidateEVMBlock(block, testPreviousOutputView(t,
		deployTx.TxIn[0].PreviousOutPoint, callerAddr)); err != nil {
		t.Fatal(err)
	}
	postState, ok := validator.EVMBlockPostState(block.Hash())
	if !ok {
		t.Fatal("expected EVM validator to expose post-state")
	}
	if postState.StateRoot() != executed.StateRoot {
		t.Fatal("unexpected validator post-state root")
	}

	var wrongRoot [32]byte
	wrongRoot[0] = 1
	if err := evm.UpsertCoinbaseStateRoot(coinbase, wrongRoot); err != nil {
		t.Fatal(err)
	}
	err = validator.ValidateEVMBlock(block, testPreviousOutputView(t,
		deployTx.TxIn[0].PreviousOutPoint, callerAddr))
	if err == nil {
		t.Fatal("expected wrong EVM state root to be rejected")
	}
	ruleErr, ok := err.(RuleError)
	if !ok || ruleErr.ErrorCode != ErrInvalidEVMBlock {
		t.Fatalf("got %T %[1]v, want ErrInvalidEVMBlock", err)
	}

	missingRootBlock := btcutil.NewBlock(&wire.MsgBlock{
		Header:       wire.BlockHeader{Timestamp: blockTime},
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx(), deployTx, resultTx},
	})
	missingRootBlock.SetHeight(100)
	err = validator.ValidateEVMBlock(missingRootBlock, testPreviousOutputView(t,
		deployTx.TxIn[0].PreviousOutPoint, callerAddr))
	if err == nil {
		t.Fatal("expected missing EVM state root to be rejected")
	}
	ruleErr, ok = err.(RuleError)
	if !ok || ruleErr.ErrorCode != ErrInvalidEVMBlock {
		t.Fatalf("got %T %[1]v, want ErrInvalidEVMBlock", err)
	}
}

func TestEVMBlockExecutionValidatorAllowsBlocksWithoutEVMWork(t *testing.T) {
	block := btcutil.NewBlock(&wire.MsgBlock{
		Transactions: []*wire.MsgTx{testEVMCoinbaseTx()},
	})
	validator := NewEVMBlockExecutionValidator(EVMBlockExecutionConfig{})
	if err := validator.ValidateEVMBlock(block, NewUtxoViewpoint()); err != nil {
		t.Fatal(err)
	}
}

func TestEVMBlockExecutionValidatorResolvesTriggers(t *testing.T) {
	contract := testContractAddressForBlockchain(t)
	runtime := evm.NewRuntime(nil)
	runtime.SetCode(evm.ContractAddressHash(contract), []byte{0x00})
	blockTime := time.Unix(1710000000, 0)
	triggerGasInput := wire.OutPoint{Hash: chainhash.Hash{9}, Index: 0}
	resultTx := testEVMResultTxWithInputs(t, evm.ResultStatusSuccess, 1, []wire.OutPoint{triggerGasInput})
	contractUTXOs := func(got evm.ContractAddress) ([]evm.UTXO, error) {
		if !contract.Equal(got) {
			t.Fatalf("unexpected contract %s", got.MustEncode())
		}
		return []evm.UTXO{
			{
				OutPoint: evm.WireOutPointToEVM(triggerGasInput),
				Contract: contract,
				Assets: wire.TxAssets{{
					Name:   *wire.NewAssetNameFromString(evm.DefaultGasConfig().GasAssetName),
					Amount: *scommon.NewDefaultDecimal(200),
				}},
			},
		}, nil
	}

	executed, err := evm.ExecuteBlock(evm.BlockExecutionRequest{
		Txs:     []*wire.MsgTx{resultTx},
		Runtime: runtime.Clone(),
		Block:   evm.BlockContext{Number: 100, Time: uint64(blockTime.Unix()), GasLimit: 1000000, FixedGasPrice: 1},
		ResolveTriggers: func(evm.TriggerResolutionContext) ([]evm.TriggerCall, error) {
			return []evm.TriggerCall{{
				Trigger:  evm.Trigger{ID: "vault-release", Contract: contract, Kind: evm.TriggerAtHeight, Height: 100},
				GasLimit: 100000,
			}}, nil
		},
		ContractUTXOs: contractUTXOs,
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
		GasConfig: evm.GasConfig{GasAssetName: "ordx:ft:gas", FixedGasPrice: 1, MaxGasPerBlock: 1000000},
		NewRuntime: func(*btcutil.Block, *UtxoViewpoint) (*evm.Runtime, error) {
			return runtime.Clone(), nil
		},
		ResolveTriggers: func(evm.TriggerResolutionContext) ([]evm.TriggerCall, error) {
			return []evm.TriggerCall{{
				Trigger:  evm.Trigger{ID: "vault-release", Contract: contract, Kind: evm.TriggerAtHeight, Height: 100},
				GasLimit: 100000,
			}}, nil
		},
		ContractUTXOs: contractUTXOs,
	})
	view := NewUtxoViewpoint()
	contractScript, err := evm.ContractPkScript(contract)
	if err != nil {
		t.Fatal(err)
	}
	view.Entries()[triggerGasInput] = NewUtxoEntry(wire.NewTxOut(1, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(evm.DefaultGasConfig().GasAssetName),
		Amount: *scommon.NewDefaultDecimal(200),
	}}, contractScript), 1, false)
	if err := validator.ValidateEVMBlock(block, view); err != nil {
		t.Fatal(err)
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
		GasLimit:    200000,
		DeployNonce: nonce,
		InitCode:    initCode,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(evm.DefaultGasConfig().GasAssetName),
		Amount: *scommon.NewDefaultDecimal(300000),
	}}, contractScript))
	return tx
}

func testPreviousOutputView(t *testing.T, outpoint wire.OutPoint, address btcutil.Address) *UtxoViewpoint {
	t.Helper()
	script, err := txscript.PayToAddrScript(address)
	if err != nil {
		t.Fatal(err)
	}
	view := NewUtxoViewpoint()
	view.Entries()[outpoint] = NewUtxoEntry(wire.NewTxOut(1, nil, script), 1, false)
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
	return func(*wire.MsgTx, evm.ParsedTx) (evm.EVMAddress, error) {
		return caller, nil
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
