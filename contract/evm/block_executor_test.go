package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBlockExecutorDeployResultThenInvokeRequiresFeeResult(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	refundRecipient := "tb1qrefund"
	deployTx := testDeployTx(t, 3, return42InitCode())
	deployResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})

	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx, deployResultTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient(refundRecipient),
	})
	require.NoError(t, err)
	require.Len(t, executed.Records, 1)
	require.Equal(t, TxTypeDeploy, executed.Records[0].Type)
	require.Equal(t, refundRecipient, executed.Records[0].GasRefundRecipient)

	contract := executed.Records[0].Contract
	invokeTx := testInvokeTx(t, contract, InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1})
	_, err = ExecuteBlock(BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx, deployResultTx, invokeTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient(refundRecipient),
	})
	require.Error(t, err)

	invokeResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: invokeTx.TxHash(), Index: 1},
	})
	executed, err = ExecuteBlock(BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx, deployResultTx, invokeTx, invokeResultTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient(refundRecipient),
	})
	require.NoError(t, err)
	require.Len(t, executed.Records, 2)
	require.True(t, executed.Records[1].RequiresResult)
	require.Equal(t, refundRecipient, executed.Records[1].GasRefundRecipient)
	require.NotEqual(t, [32]byte{}, executed.StateRoot)
}

func TestBlockExecutorDefaultInvokeEmptyCall(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, return42InitCode())
	deployResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})

	deployed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, deployResultTx},
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.NoError(t, err)
	require.Len(t, deployed.Records, 1)
	defaultTx := testDefaultInvokeTx(t, deployed.Records[0].Contract, 100, testEVMGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas))

	_, err = ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, deployResultTx, defaultTx},
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.Error(t, err)

	defaultResultTx := testResultTx(t, ResultStatusInvalid, 1, []wire.OutPoint{
		{Hash: defaultTx.TxHash(), Index: 0},
	})
	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx, deployResultTx, defaultTx, defaultResultTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qrefund"),
	})
	require.NoError(t, err)
	require.Len(t, executed.Records, 2)
	require.Equal(t, TxTypeInvoke, executed.Records[1].Type)
	require.True(t, executed.Records[1].RequiresResult)
	require.Equal(t, ResultFeeModePlainTxFee, executed.Records[1].ResultFeeMode)
	require.Empty(t, executed.Records[1].GasRefundRecipient)
}

func TestBlockExecutorRejectsMissingDeployResult(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, return42InitCode())

	_, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx},
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.Error(t, err)
}

func TestBlockExecutorRejectsBlockGasLimitExceeded(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, return42InitCode())
	deployResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})

	_, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, deployResultTx},
		Runtime:       NewRuntime(nil),
		GasConfig:     GasConfig{MaxGasPerBlock: 1},
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.Error(t, err)
}

func TestBlockExecutorInvokeRevertRequiresFundingInputResult(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), []byte{0x60, 0x00, 0x60, 0x00, 0xfd})

	invokeTx := testInvokeTx(t, contract, InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1})
	resultTx := testResultTx(t, ResultStatusRevert, 1, []wire.OutPoint{
		{Hash: invokeTx.TxHash(), Index: 1},
	})

	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx, resultTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.NoError(t, err)
	require.Len(t, executed.Records, 1)
	require.Equal(t, ResultStatusRevert, executed.Records[0].Status)
	require.True(t, executed.Records[0].RequiresResult)
}

func TestBlockExecutorRejectsResultMissingInvokeFundingInput(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), []byte{0x60, 0x00, 0x60, 0x00, 0xfd})

	invokeTx := testInvokeTx(t, contract, InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1})
	resultTx := testResultTx(t, ResultStatusRevert, 1, []wire.OutPoint{
		{Hash: chainhash.Hash{9, 9, 9}, Index: 0},
	})

	_, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx, resultTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.Error(t, err)
}

func TestBlockExecutorAssetIntentRequiresResultAndVerifier(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	invokeTx := testInvokeTx(t, contract, InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Calldata:  EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
	})
	resultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: invokeTx.TxHash(), Index: 1},
	})
	var verifierCalled bool
	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx, resultTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
		VerifyResult: func(resultTx *wire.MsgTx, settled []ExecutionRecord) error {
			verifierCalled = true
			require.Len(t, settled, 1)
			require.Len(t, settled[0].AssetIntents, 1)
			require.Equal(t, SatoshiAssetName, settled[0].AssetIntents[0].AssetName)
			require.Equal(t, 0, settled[0].AssetIntents[0].Amount.Cmp(mustDefaultDecimal(t, 77)))
			return nil
		},
	})
	require.NoError(t, err)
	require.True(t, verifierCalled)
	require.Len(t, executed.Records, 1)
	require.True(t, executed.Records[0].RequiresResult)
}

func TestExecuteBlockSettlesTriggersAfterInvokes(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	invokeTx := testInvokeTx(t, contract, InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
	})
	resultTx := testResultTx(t, ResultStatusSuccess, 2, []wire.OutPoint{
		{Hash: invokeTx.TxHash(), Index: 1},
	})
	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx, resultTx},
		Runtime:       runtime,
		Block:         testBlockContext(100),
		ResolveCaller: fixedCaller(caller),
		ResolveTriggers: func(ctx TriggerResolutionContext) ([]TriggerCall, error) {
			return []TriggerCall{{
				Trigger: Trigger{
					ID:       "vault-release",
					Contract: contract,
					Kind:     TriggerAtHeight,
					Height:   100,
				},
				GasLimit: DefaultGasConfig().TriggerBaseGas,
				Calldata: EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
			}}, nil
		},
		VerifyResult: func(resultTx *wire.MsgTx, settled []ExecutionRecord) error {
			require.Len(t, settled, 2)
			require.Equal(t, ExecutionKindInvoke, settled[0].Kind)
			require.Equal(t, ExecutionKindTrigger, settled[1].Kind)
			return nil
		},
	})
	require.NoError(t, err)
	require.Len(t, executed.Records, 2)
	require.Equal(t, ExecutionKindInvoke, executed.Records[0].Kind)
	require.Equal(t, ExecutionKindTrigger, executed.Records[1].Kind)
}

func TestExecuteBlockSettlesStateRegisteredTrigger(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	require.NoError(t, runtime.State.RegisterTrigger(Trigger{
		ID:       "vault-release",
		Contract: contract,
		Kind:     TriggerAtHeight,
		Height:   100,
		GasLimit: DefaultGasConfig().TriggerBaseGas,
		Calldata: EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
	}))

	invokeTx := testInvokeTx(t, contract, InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
	})
	resultTx := testResultTx(t, ResultStatusSuccess, 2, []wire.OutPoint{
		{Hash: invokeTx.TxHash(), Index: 1},
	})
	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx, resultTx},
		Runtime:       runtime,
		Block:         testBlockContext(100),
		ResolveCaller: fixedCaller(caller),
		VerifyResult: func(resultTx *wire.MsgTx, settled []ExecutionRecord) error {
			require.Len(t, settled, 2)
			require.Equal(t, ExecutionKindInvoke, settled[0].Kind)
			require.Equal(t, ExecutionKindTrigger, settled[1].Kind)
			return nil
		},
	})
	require.NoError(t, err)
	require.Len(t, executed.Records, 2)
	require.Equal(t, ExecutionKindInvoke, executed.Records[0].Kind)
	require.Equal(t, ExecutionKindTrigger, executed.Records[1].Kind)
	require.Empty(t, runtime.State.Triggers())
}

func TestBlockExecutorTriggerRequiresResultWithoutInvokeFunding(t *testing.T) {
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	executor := NewBlockExecutor(BlockExecutionRequest{
		Runtime: runtime,
		Block:   testBlockContext(100),
	})
	err := executor.ExecuteTrigger(TriggerCall{
		Trigger: Trigger{
			ID:       "vault-release",
			Contract: contract,
			Kind:     TriggerAtHeight,
			Height:   100,
		},
		GasLimit: DefaultGasConfig().TriggerBaseGas,
		Calldata: EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
	})
	require.NoError(t, err)
	require.Len(t, executor.pending, 1)
	require.Equal(t, ExecutionKindTrigger, executor.pending[0].Kind)
	require.Empty(t, executor.pending[0].FundingInputs)

	resultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: chainhash.Hash{7}, Index: 0},
	})
	require.NoError(t, executor.ExecuteTx(resultTx))
	executed, err := executor.Finalize()
	require.NoError(t, err)
	require.Len(t, executed.Records, 1)
	require.Equal(t, ExecutionKindTrigger, executed.Records[0].Kind)
}

func TestBlockExecutorTerminatesTriggerWhenContractGasIsInsufficient(t *testing.T) {
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	require.NoError(t, runtime.State.RegisterTrigger(Trigger{
		ID:       "vault-release",
		Contract: contract,
		Kind:     TriggerAtHeight,
		Height:   100,
		GasLimit: DefaultGasConfig().TriggerBaseGas,
	}))

	executor := NewBlockExecutor(BlockExecutionRequest{
		Runtime: runtime,
		GasConfig: GasConfig{
			GasAssetName:  "ordx:ft:gas",
			FixedGasPrice: 1,
			ResultBaseGas: 10,
		},
		Block: testBlockContext(100),
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return nil, nil
		},
	})
	err := executor.ExecuteTrigger(runtime.State.DueTriggerCalls(BlockEnvironment{Height: 100})[0])
	require.NoError(t, err)
	require.Empty(t, executor.pending)
	require.Empty(t, executor.records)
	require.Empty(t, runtime.State.Triggers())
}

func TestBlockExecutorVerifiesCoinbaseStateRoot(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, return42InitCode())
	deployResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})
	runtime := NewRuntime(nil)

	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, deployResultTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.NoError(t, err)

	coinbaseTx := wire.NewMsgTx(2)
	coinbaseTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.MaxPrevOutIndex}})
	require.NoError(t, UpsertCoinbaseStateRoot(coinbaseTx, executed.StateRoot))

	_, err = ExecuteBlockAndVerifyStateRoot(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, deployResultTx},
		CoinbaseTx:    coinbaseTx,
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.NoError(t, err)

	var wrongRoot [32]byte
	wrongRoot[0] = 1
	require.NoError(t, UpsertCoinbaseStateRoot(coinbaseTx, wrongRoot))
	_, err = ExecuteBlockAndVerifyStateRoot(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, deployResultTx},
		CoinbaseTx:    coinbaseTx,
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.Error(t, err)
}

func testDeployTx(t *testing.T, nonce uint64, initCode []byte) *wire.MsgTx {
	t.Helper()
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, nonce)
	require.NoError(t, err)
	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)
	script, err := evmcommon.DeployNullDataScript(DeployPayload{
		GasLimit:    evmcommon.DeployBaseGas,
		DeployNonce: nonce,
		InitCode:    initCode,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(DefaultGasConfig().GasAssetName),
		Amount: *testEVMGasFee(t, evmcommon.DeployBaseGas),
	}}, contractScript))
	return tx
}

func testInvokeTx(t *testing.T, contract ContractAddress, payload InvokePayload) *wire.MsgTx {
	t.Helper()
	script, err := evmcommon.InvokeNullDataScript(payload)
	require.NoError(t, err)
	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0}})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(DefaultGasConfig().GasAssetName),
		Amount: *testEVMGasFee(t, payload.GasLimit),
	}}, contractScript))
	return tx
}

func testDefaultInvokeTx(t *testing.T, contract ContractAddress, value int64, gasAmount int64) *wire.MsgTx {
	t.Helper()
	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{3}, Index: 0}})
	tx.AddTxOut(wire.NewTxOut(value, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(DefaultGasConfig().GasAssetName),
		Amount: *scommon.NewDefaultDecimal(gasAmount),
	}}, contractScript))
	return tx
}

func testEVMGasFee(t *testing.T, gas uint64) *scommon.Decimal {
	t.Helper()
	fee, err := evmcommon.GasFeeDecimalAtHeight(gas, 0)
	require.NoError(t, err)
	return fee
}

func testEVMGasFeeAmount(t *testing.T, gas uint64) int64 {
	t.Helper()
	fee, err := evmcommon.GasFeeAtHeight(gas, 0)
	require.NoError(t, err)
	require.LessOrEqual(t, fee, uint64(1<<63-1))
	return int64(fee)
}

func testBlockContext(number uint64) BlockContext {
	return BlockContext{
		Number:        number,
		Time:          1,
		GasLimit:      DefaultGasConfig().MaxGasPerBlock,
		FixedGasPrice: 1,
	}
}

func testResultTx(t *testing.T, status ResultStatus, count uint16, inputs []wire.OutPoint) *wire.MsgTx {
	t.Helper()
	script, err := evmcommon.ResultNullDataScript(ResultPayload{
		Status:      status,
		ResultCount: count,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	if len(inputs) == 0 {
		inputs = []wire.OutPoint{{Hash: chainhash.Hash{3}, Index: 0}}
	}
	for i := range inputs {
		tx.AddTxIn(wire.NewTxIn(&inputs[i], nil, nil))
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx
}

func testCanonicalResultTx(t *testing.T, inputs []wire.OutPoint, outputs []wire.TxOut) *wire.MsgTx {
	t.Helper()
	script, err := evmcommon.ResultNullDataScript(ResultPayload{
		Status:      ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	for i := range inputs {
		tx.AddTxIn(wire.NewTxIn(&inputs[i], nil, nil))
	}
	for i := range outputs {
		output := outputs[i]
		tx.AddTxOut(&output)
	}
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx
}

func fixedCaller(caller EVMAddress) CallerResolver {
	return func(*wire.MsgTx, ParsedTx) (EVMAddress, error) {
		return caller, nil
	}
}

func fixedGasRefundRecipient(recipient string) GasRefundRecipientResolver {
	return func(*wire.MsgTx, ParsedTx) (string, bool, error) {
		return recipient, recipient != "", nil
	}
}

func callAssetPrecompileCode() []byte {
	code := []byte{
		0x36,       // CALLDATASIZE
		0x60, 0x00, // PUSH1 0
		0x60, 0x00, // PUSH1 0
		0x37,       // CALLDATACOPY
		0x60, 0x00, // PUSH1 0, output size
		0x60, 0x00, // PUSH1 0, output offset
		0x36,       // CALLDATASIZE, input size
		0x60, 0x00, // PUSH1 0, input offset
		0x60, 0x00, // PUSH1 0, value
		0x73, // PUSH20 precompile address
	}
	code = append(code, AssetPrecompileAddress.Bytes()...)
	code = append(code,
		0x61, 0xc3, 0x50, // PUSH2 50000, gas
		0xf1, // CALL
		0x00, // STOP
	)
	return code
}
