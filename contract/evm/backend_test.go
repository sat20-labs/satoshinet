package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func executeWorkAndVerifyResults(t *testing.T, req BlockExecutionRequest,
	resultTxs ...*wire.MsgTx) BlockExecutionResult {

	t.Helper()
	executed, err := ExecuteBlock(req)
	require.NoError(t, err)
	require.NoError(t, VerifyResultTxs(ResultVerifyRequest{
		ResultTxs:      resultTxs,
		Execution:      executed,
		ContractPrefix: req.ContractPrefix,
		VerifyResult:   req.VerifyResult,
	}), "execution records: %+v", executed.Records)
	return executed
}

func TestBackendDeployResultThenInvokeRequiresFeeResult(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	refundRecipient := "tb1qrefund"
	deployTx := testDeployTx(t, 3, return42InitCode())
	deployResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})

	executed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient(refundRecipient),
	}, deployResultTx)
	require.Len(t, executed.Records, 1)
	require.Equal(t, TxTypeDeploy, executed.Records[0].Type)
	require.Equal(t, refundRecipient, executed.Records[0].GasRefundRecipient)

	contract := executed.Records[0].Contract
	invokeTx := testInvokeTx(t, contract, InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1})
	missingInvokeResult, err := ExecuteBlock(BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx, invokeTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient(refundRecipient),
	})
	require.NoError(t, err)
	err = VerifyResultTxs(ResultVerifyRequest{
		ResultTxs:    []*wire.MsgTx{deployResultTx},
		Execution:    missingInvokeResult,
		VerifyResult: nil,
	})
	require.Error(t, err)

	combinedResultTx := testResultTx(t, ResultStatusSuccess, 2, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
		{Hash: invokeTx.TxHash(), Index: 1},
	})
	executed = executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx, invokeTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient(refundRecipient),
	}, combinedResultTx)
	require.Len(t, executed.Records, 2)
	require.True(t, executed.Records[1].RequiresResult)
	require.Equal(t, refundRecipient, executed.Records[1].GasRefundRecipient)
	require.NotEqual(t, [32]byte{}, executed.StateRoot)
}

func TestBackendDefaultInvokeEmptyCall(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, return42InitCode())
	deployResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})

	deployed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx},
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	}, deployResultTx)
	require.Len(t, deployed.Records, 1)
	defaultTx := testDefaultInvokeTx(t, deployed.Records[0].Contract, 100, testEVMGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas))

	missingDefaultResult, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx, defaultTx},
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.NoError(t, err)
	err = VerifyResultTxs(ResultVerifyRequest{
		ResultTxs: []*wire.MsgTx{deployResultTx},
		Execution: missingDefaultResult,
	})
	require.Error(t, err)

	defaultResultTx := testResultTx(t, ResultStatusSuccess, 2, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
		{Hash: defaultTx.TxHash(), Index: 0},
	})
	executed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx, defaultTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qrefund"),
	}, defaultResultTx)
	require.Len(t, executed.Records, 2)
	require.Equal(t, TxTypeInvoke, executed.Records[1].Type)
	require.True(t, executed.Records[1].RequiresResult)
	require.Equal(t, ResultFeeModePlainTxFee, executed.Records[1].ResultFeeMode)
	require.Equal(t, "tb1qrefund", executed.Records[1].GasRefundRecipient)
}

func TestBackendDefaultInvokeUnsupportedRefundsFunding(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, revertRuntimeInitCode())
	deployResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})

	deployed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx},
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	}, deployResultTx)
	require.Len(t, deployed.Records, 1)

	defaultTx := testDefaultInvokeTx(t, deployed.Records[0].Contract, 100,
		testEVMGasFeeAmount(t, DefaultGasConfig().InvokeBaseGas))
	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:                       []*wire.MsgTx{deployTx, defaultTx},
		Runtime:                   NewRuntime(nil),
		Block:                     testBlockContext(1),
		ResolveCaller:             fixedCaller(caller),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qrefund"),
	})
	require.NoError(t, err)
	require.Len(t, executed.Records, 2)
	record := executed.Records[1]
	require.Equal(t, TxTypeInvoke, record.Type)
	require.NotEqual(t, ResultStatusSuccess, record.Status)
	require.Equal(t, "tb1qrefund", record.GasRefundRecipient)
	require.Len(t, record.AssetIntents, 1)
	require.Equal(t, "tb1qrefund", record.AssetIntents[0].To)
	require.Equal(t, SatoshiAssetName, record.AssetIntents[0].AssetName)
	require.Equal(t, "100", record.AssetIntents[0].Amount.String())
}

func TestBackendIgnoresInvalidDeploy(t *testing.T) {
	tx := testDeployTx(t, 3, return42InitCode())
	script, err := evmcommon.DeployNullDataScript(DeployPayload{
		GasLimit:        0,
		DeployNonce:     3,
		ContractContent: return42InitCode(),
	})
	require.NoError(t, err)
	tx.TxOut[0].PkScript = script

	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{tx},
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")),
	})
	require.NoError(t, err)
	require.Empty(t, executed.Records)
}

func TestBackendRejectsMissingDeployResult(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, return42InitCode())

	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx},
		Runtime:       NewRuntime(nil),
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.NoError(t, err)
	err = VerifyResultTxs(ResultVerifyRequest{Execution: executed})
	require.Error(t, err)
}

func TestBackendRejectsSplitResultTransactions(t *testing.T) {
	contract := testContract(t)
	execution := BlockExecutionResult{PendingRecords: []ExecutionRecord{
		{Contract: contract, Status: ResultStatusSuccess, RequiresResult: true},
		{Contract: contract, Status: ResultStatusSuccess, RequiresResult: true},
	}}
	first := testResultTx(t, ResultStatusSuccess, 1, nil)
	second := testResultTx(t, ResultStatusSuccess, 1, nil)
	err := VerifyResultTxs(ResultVerifyRequest{
		ResultTxs: []*wire.MsgTx{first, second},
		Execution: execution,
	})
	require.ErrorContains(t, err, "got 2 want 1")
}

func TestBackendRejectsExternalResult(t *testing.T) {
	resultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: chainhash.Hash{1}, Index: 0},
	})
	executor := NewBackend(BlockExecutionRequest{})
	require.ErrorContains(t, executor.ExecuteTx(resultTx), "not accepted as external input")

	_, err := ExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{resultTx}})
	require.ErrorContains(t, err, "not accepted as external input")
}

func TestBackendRejectsBlockGasLimitExceeded(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, return42InitCode())

	_, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx},
		Runtime:       NewRuntime(nil),
		GasConfig:     GasConfig{MaxGasPerBlock: 1},
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.Error(t, err)
}

func TestBackendInvokeRevertRequiresFundingInputResult(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), []byte{0x60, 0x00, 0x60, 0x00, 0xfd})

	invokeTx := testInvokeTx(t, contract, InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1})
	resultTx := testResultTx(t, ResultStatusRevert, 1, []wire.OutPoint{
		{Hash: invokeTx.TxHash(), Index: 1},
	})

	executed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	}, resultTx)
	require.Len(t, executed.Records, 1)
	require.Equal(t, ResultStatusRevert, executed.Records[0].Status)
	require.True(t, executed.Records[0].RequiresResult)
}

func TestBackendRejectsResultMissingInvokeFundingInput(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), []byte{0x60, 0x00, 0x60, 0x00, 0xfd})

	invokeTx := testInvokeTx(t, contract, InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1})
	resultTx := testResultTx(t, ResultStatusRevert, 1, []wire.OutPoint{
		{Hash: chainhash.Hash{9, 9, 9}, Index: 0},
	})

	executed, err := ExecuteBlock(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	})
	require.NoError(t, err)
	err = VerifyResultTxs(ResultVerifyRequest{
		ResultTxs: []*wire.MsgTx{resultTx},
		Execution: executed,
	})
	require.Error(t, err)
}

func TestBackendAssetIntentRequiresResultAndVerifier(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	invokeTx := testInvokeTx(t, contract, InvokePayload{
		GasLimit:  DefaultGasConfig().InvokeBaseGas,
		CallNonce: 1,
		Param:     EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
	})
	resultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: invokeTx.TxHash(), Index: 1},
	})
	var verifierCalled bool
	executed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{mustUTXO(t, OutPoint{TxID: chainhash.Hash{7}.String(), Vout: 0}, contract, SatoshiAssetName, 100, 1)}, nil
		},
		VerifyResult: func(resultTx *wire.MsgTx, settled []ExecutionRecord) error {
			verifierCalled = true
			require.Len(t, settled, 1)
			require.Len(t, settled[0].AssetIntents, 1)
			require.Equal(t, SatoshiAssetName, settled[0].AssetIntents[0].AssetName)
			require.Equal(t, 0, settled[0].AssetIntents[0].Amount.Cmp(mustDefaultDecimal(t, 77)))
			return nil
		},
	}, resultTx)
	require.True(t, verifierCalled)
	require.Len(t, executed.Records, 1)
	require.True(t, executed.Records[0].RequiresResult)
}

func TestExecuteBlockSettlesTriggersAfterInvokes(t *testing.T) {
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
	executed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx},
		Runtime:       runtime,
		Block:         testBlockContext(100),
		ResolveCaller: fixedCaller(caller),
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, OutPoint{TxID: chainhash.Hash{9}.String(), Vout: 0},
					contract, DefaultGasConfig().GasAssetName, 100000000, 1),
				mustUTXO(t, OutPoint{TxID: chainhash.Hash{9}.String(), Vout: 1},
					contract, SatoshiAssetName, 100, 1),
			}, nil
		},
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
	}, resultTx)
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
	executed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:           []*wire.MsgTx{invokeTx},
		Runtime:       runtime,
		Block:         testBlockContext(100),
		ResolveCaller: fixedCaller(caller),
		ContractUTXOs: func(got ContractAddress) ([]UTXO, error) {
			require.True(t, contract.Equal(got))
			return []UTXO{
				mustUTXO(t, OutPoint{TxID: chainhash.Hash{10}.String(), Vout: 0},
					contract, DefaultGasConfig().GasAssetName, 100000000, 1),
				mustUTXO(t, OutPoint{TxID: chainhash.Hash{10}.String(), Vout: 1},
					contract, SatoshiAssetName, 100, 1),
			}, nil
		},
		VerifyResult: func(resultTx *wire.MsgTx, settled []ExecutionRecord) error {
			require.Len(t, settled, 2)
			require.Equal(t, ExecutionKindInvoke, settled[0].Kind)
			require.Equal(t, ExecutionKindTrigger, settled[1].Kind)
			return nil
		},
	}, resultTx)
	require.Len(t, executed.Records, 2)
	require.Equal(t, ExecutionKindInvoke, executed.Records[0].Kind)
	require.Equal(t, ExecutionKindTrigger, executed.Records[1].Kind)
	require.Empty(t, runtime.State.Triggers())
}

func TestBackendTriggerRequiresResultWithoutInvokeFunding(t *testing.T) {
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

	executor := NewBackend(BlockExecutionRequest{
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
	require.NoError(t, executor.VerifyAndSettleResultTx(resultTx, nil))
	executed, err := executor.Finalize()
	require.NoError(t, err)
	require.Len(t, executed.Records, 1)
	require.Equal(t, ExecutionKindTrigger, executed.Records[0].Kind)
}

func TestBackendKeepsTriggerWhenContractGasIsInsufficient(t *testing.T) {
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

	executor := NewBackend(BlockExecutionRequest{
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
	require.Len(t, runtime.State.Triggers(), 1)
}

func TestBackendRejectsOverLimitTriggerExecution(t *testing.T) {
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	require.NoError(t, runtime.State.RegisterTrigger(Trigger{
		ID:       "vault-release",
		Contract: contract,
		Kind:     TriggerAtHeight,
		Height:   100,
		GasLimit: 11,
	}))
	executor := NewBackend(BlockExecutionRequest{
		Runtime:   runtime,
		GasConfig: GasConfig{MaxGasPerTrigger: 10},
		Block:     testBlockContext(100),
	})
	err := executor.ExecuteTrigger(TriggerCall{
		Trigger: Trigger{
			ID:       "vault-release",
			Contract: contract,
			Kind:     TriggerAtHeight,
			Height:   100,
		},
		GasLimit: 11,
	})
	require.ErrorContains(t, err, "trigger gas limit exceeds maximum")
	require.Empty(t, executor.pending)
	require.Empty(t, executor.records)
}

func TestBackendVerifiesCoinbaseStateRoot(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	deployTx := testDeployTx(t, 3, return42InitCode())
	deployResultTx := testResultTx(t, ResultStatusSuccess, 1, []wire.OutPoint{
		{Hash: deployTx.TxHash(), Index: 1},
	})
	runtime := NewRuntime(nil)

	executed := executeWorkAndVerifyResults(t, BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx},
		Runtime:       runtime,
		Block:         testBlockContext(1),
		ResolveCaller: fixedCaller(caller),
	}, deployResultTx)

	coinbaseTx := wire.NewMsgTx(2)
	coinbaseTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.MaxPrevOutIndex}})
	require.NoError(t, UpsertCoinbaseStateRoot(coinbaseTx, executed.StateRoot))

	_, err := ExecuteBlockAndVerifyStateRoot(BlockExecutionRequest{
		Txs:           []*wire.MsgTx{deployTx},
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
		Txs:           []*wire.MsgTx{deployTx},
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
		GasLimit:        evmcommon.DeployBaseGas,
		DeployNonce:     nonce,
		ContractContent: initCode,
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
	if payload.Action == "" {
		payload.Action = "call"
	}
	script, err := evmcommon.InvokeNullDataScript(payload)
	require.NoError(t, err)
	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{2}, Index: 0}})
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(DefaultGasConfig().GasAssetName),
		Amount: *testEVMGasFee(t, int64(payload.GasLimit)),
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

func revertRuntimeInitCode() []byte {
	runtime := []byte{0x60, 0x00, 0x60, 0x00, 0xfd}
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

func testEVMGasFee(t *testing.T, gas int64) *scommon.Decimal {
	t.Helper()
	fee, err := evmcommon.GasFeeDecimalAtHeight(gas, 0)
	require.NoError(t, err)
	return fee
}

func testEVMGasFeeAmount(t *testing.T, gas int64) int64 {
	t.Helper()
	fee, err := evmcommon.GasFeeAtHeight(gas, 0)
	require.NoError(t, err)
	return fee
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
	return func(*wire.MsgTx, Tx) (string, error) {
		return caller.String(), nil
	}
}

func fixedGasRefundRecipient(recipient string) GasRefundRecipientResolver {
	return func(*wire.MsgTx, Tx) (string, bool, error) {
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
