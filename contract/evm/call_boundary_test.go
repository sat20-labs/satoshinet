package evm

import (
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestReviewRejectsCrossContractCodeBeforeExecution(t *testing.T) {
	for _, opcode := range []vm.OpCode{vm.CALL, vm.STATICCALL, vm.DELEGATECALL, vm.CALLCODE} {
		t.Run(opcode.String(), func(t *testing.T) {
			runtime := NewRuntime(nil)
			a := ContractAddressHash(testContract(t))
			b := mustEVMAddress(t, "0x2222222222222222222222222222222222222222")
			// A changes its own storage, then ignores the nested call's success flag.
			code := append([]byte{0x60, 1, 0x60, 0, 0x55}, callPrecompileWithOpcodeCode(GethAddress(b), opcode)...)
			runtime.SetCode(a, code)
			runtime.SetCode(b, []byte{0x60, 42, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3})
			nestedOpcodes := 0
			runtime.Config.Tracer = &tracing.Hooks{OnOpcode: func(_ uint64, _ byte, _, _ uint64, _ tracing.OpContext, _ []byte, depth int, _ error) {
				if depth > 1 {
					nestedOpcodes++
				}
			}}
			before := runtime.State.StateRoot()
			result := runtime.Call(CallRequest{TargetAddress: a.String(), Gas: 200000, Block: testBlockContext(1)})
			require.ErrorContains(t, result.Err, "cross-contract EVM calls are unsupported")
			require.Zero(t, nestedOpcodes, "B bytecode must never execute, including read-only bytecode")
			require.Equal(t, before, runtime.State.StateRoot(), "A cannot catch or ignore the boundary violation")
			require.Empty(t, runtime.AssetIntents)
		})
	}
}

func TestReviewCrossContractCallDiscardsCapturedEffects(t *testing.T) {
	for _, trigger := range []bool{false, true} {
		t.Run(map[bool]string{false: "asset_intent", true: "registered_trigger"}[trigger], func(t *testing.T) {
			runtime := NewRuntime(nil)
			a := ContractAddressHash(testContract(t))
			b := mustEVMAddress(t, "0x2222222222222222222222222222222222222222")
			configureTestAssetEffects(runtime)
			runtime.AssetBalances = testAssetBalances{a.String() + ":" + SatoshiAssetName: scommon.NewDefaultDecimal(11), b.String() + ":" + SatoshiAssetName: scommon.NewDefaultDecimal(11)}
			precompile := AssetPrecompileAddress
			input := EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "7", nil)
			if trigger {
				precompile = TriggerPrecompileAddress
				input = EncodeRegisterHeightTriggerCall("later", 10, 50000, nil)
			}
			firstCall := callPrecompileWithOpcodeCode(precompile, vm.CALL)
			code := append(firstCall[:len(firstCall)-1], callPrecompileWithOpcodeCode(GethAddress(b), vm.CALL)...)
			runtime.SetCode(a, code)
			runtime.SetCode(b, callAssetPrecompileCode())
			before := runtime.State.StateRoot()
			result := runtime.Call(CallRequest{TargetAddress: a.String(), Input: input, Gas: 200000, Block: testBlockContext(1)})
			require.ErrorContains(t, result.Err, "cross-contract EVM calls are unsupported")
			require.Empty(t, runtime.AssetIntents)
			require.Empty(t, runtime.State.Triggers())
			require.Equal(t, before, runtime.State.StateRoot())
		})
	}
}

func TestReviewConstructorCannotCallAnotherContract(t *testing.T) {
	runtime := NewRuntime(nil)
	b := mustEVMAddress(t, "0x2222222222222222222222222222222222222222")
	runtime.SetCode(b, []byte{0})
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	expected, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	before := runtime.State.StateRoot()
	result := runtime.Deploy(DeployRequest{CallerAddress: caller.String(), ExpectedContract: expected, DeployNonce: 3, Gas: 200000, InitCode: callPrecompileWithOpcodeCode(GethAddress(b), vm.CALL), Block: testBlockContext(1)})
	require.ErrorContains(t, result.Err, "cross-contract EVM calls are unsupported")
	require.Equal(t, before, runtime.State.StateRoot())
}

func TestReviewCallBoundaryKeepsDirectCallsAndCodeInspection(t *testing.T) {
	runtime := NewRuntime(nil)
	a := ContractAddressHash(testContract(t))
	b := mustEVMAddress(t, "0x2222222222222222222222222222222222222222")
	runtime.SetCode(b, []byte{0x60, 42, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3})
	direct := runtime.Call(CallRequest{TargetAddress: b.String(), Gas: 100000, Block: testBlockContext(1)})
	require.NoError(t, direct.Err)
	require.Equal(t, byte(42), direct.ReturnData[31])
	// EXTCODECOPY is inspection, not execution of B. Return B's first opcode.
	code := []byte{0x60, 1, 0x60, 0, 0x60, 0, 0x73}
	code = append(code, GethAddress(b).Bytes()...)
	code = append(code, 0x3c, 0x60, 1, 0x60, 0, 0xf3)
	runtime.SetCode(a, code)
	result := runtime.Call(CallRequest{TargetAddress: a.String(), Gas: 100000, Block: testBlockContext(1)})
	require.NoError(t, result.Err)
	require.Equal(t, []byte{0x60}, result.ReturnData)
	require.Equal(t, gethcommon.Hash{}, runtime.State.GetState(GethAddress(b), gethcommon.Hash{}))
}

func TestReviewCrossContractFailureProducesRefundResult(t *testing.T) {
	runtime := NewRuntime(nil)
	a := testContract(t)
	b := mustEVMAddress(t, "0x2222222222222222222222222222222222222222")
	runtime.SetCode(ContractAddressHash(a), callPrecompileWithOpcodeCode(GethAddress(b), vm.CALL))
	runtime.SetCode(b, []byte{0})
	before := runtime.State.StateRoot()
	cfg := DefaultGasConfig()
	limit := int64(200000)
	tx := testInvokeTx(t, a, InvokePayload{GasLimit: limit, CallNonce: 1})
	fee, err := cfg.TotalUserBudgetFee(ExecutionKindInvoke, limit, true, 1)
	require.NoError(t, err)
	tx.TxOut[1].Assets[0].Amount = *fee
	tx.TxOut[1].Value = 7
	result, err := BuildBlockResultTxs(BlockResultBuildRequest{
		Txs: []*wire.MsgTx{tx}, Runtime: runtime, GasConfig: cfg, Block: testBlockContext(1),
		ResolveCaller:             fixedCaller(mustEVMAddress(t, "0x1111111111111111111111111111111111111111")),
		ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qdest"),
		ResolveScript:             evmTestResultScriptResolver(t, a), ResolveOutput: evmTestResultOutputResolver(a),
	})
	require.NoError(t, err, "the rejected call must not abort block Result construction")
	require.Len(t, result.ResultTxs, 1)
	require.Len(t, result.Execution.Records, 1)
	require.Equal(t, ResultStatusInvalid, result.Execution.Records[0].Status)
	require.Equal(t, before, runtime.State.StateRoot())
	outputs, err := evmTestResultOutputResolver(a)(result.ResultTxs[0])
	require.NoError(t, err)
	refunded := int64(0)
	for _, out := range outputs {
		if out.To == "tb1qdest" {
			refunded += out.Value
		}
	}
	require.Equal(t, int64(7), refunded)
}

func TestReviewCallBoundaryAllowsSameContractCall(t *testing.T) {
	runtime := NewRuntime(nil)
	a := ContractAddressHash(testContract(t))
	// A's first invocation has calldata. Its self-call has no calldata and
	// returns immediately, proving that the boundary does not ban CALL itself.
	nested := []byte{0x60, 0, 0x60, 0, 0x60, 0, 0x60, 0, 0x60, 0, 0x73}
	nested = append(nested, GethAddress(a).Bytes()...)
	nested = append(nested, 0x61, 0xc3, 0x50, 0xf1, 0x50, 0x00)
	code := []byte{0x36, 0x15, 0x60, byte(5 + len(nested)), 0x57}
	code = append(code, nested...)
	code = append(code, 0x5b, 0x00)
	runtime.SetCode(a, code)
	nestedOpcodes := 0
	runtime.Config.Tracer = &tracing.Hooks{OnOpcode: func(_ uint64, _ byte, _, _ uint64, _ tracing.OpContext, _ []byte, depth int, _ error) {
		if depth > 1 {
			nestedOpcodes++
		}
	}}
	result := runtime.Call(CallRequest{TargetAddress: a.String(), Input: []byte{1}, Gas: 100000, Block: testBlockContext(1)})
	require.NoError(t, result.Err)
	require.Positive(t, nestedOpcodes)
}
