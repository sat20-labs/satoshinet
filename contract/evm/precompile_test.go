package evm

import (
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	scommon "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
)

type testAssetBalances map[string]*scommon.Decimal

func (b testAssetBalances) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	return b[owner.String()+":"+assetName], nil
}

func TestAssetPrecompileBalanceOf(t *testing.T) {
	owner := mustEVMAddress(t, "0x0102030405060708090001020304050607080900")
	balances := testAssetBalances{owner.String() + ":" + SatoshiAssetName: mustDefaultDecimal(t, 42)}
	precompile := NewAssetPrecompile(balances)

	ret, err := precompile.Run(EncodeBalanceOfCall(owner, SatoshiAssetName))
	require.NoError(t, err)
	length, err := abiWordToUint64(ret[:32])
	require.NoError(t, err)
	require.Equal(t, uint64(2), length)
	balanceText := string(ret[32 : 32+length])
	require.Equal(t, "42", balanceText)
}

func TestAssetPrecompileTransferAssetABI(t *testing.T) {
	call := EncodeTransferAssetCall("ordx:ticker:0", "tb1ptest", "1000", []byte{1, 2, 3})

	assetName, to, amount, extraData, err := DecodeTransferAssetCall(call)
	require.NoError(t, err)
	require.Equal(t, "ordx:ticker:0", assetName)
	require.Equal(t, "tb1ptest", to)
	require.Equal(t, "1000", amount.String())
	require.Equal(t, []byte{1, 2, 3}, extraData)

	ret, err := NewAssetPrecompile(nil).Run(call)
	require.NoError(t, err)
	require.Equal(t, byte(1), ret[31])
}

func TestAssetPrecompileTransferSatoshiAssetABI(t *testing.T) {
	call := EncodeTransferAssetCall(SatoshiAssetName, "tb1psatoshi", "1", nil)

	assetName, to, amount, extraData, err := DecodeTransferAssetCall(call)
	require.NoError(t, err)
	require.Equal(t, SatoshiAssetName, assetName)
	require.Equal(t, "tb1psatoshi", to)
	require.Equal(t, "1", amount.String())
	require.Empty(t, extraData)
}

func TestTriggerPrecompileRegisterHeightABI(t *testing.T) {
	call := EncodeRegisterHeightTriggerCall("vault-release", 100, 50000, []byte{1, 2, 3})

	trigger, err := DecodeTriggerRegistrationCall(call)
	require.NoError(t, err)
	require.Equal(t, "vault-release", trigger.ID)
	require.Equal(t, TriggerAtHeight, trigger.Kind)
	require.Equal(t, int64(100), trigger.Height)
	require.Equal(t, int64(50000), trigger.GasLimit)
	require.Equal(t, []byte{1, 2, 3}, trigger.Calldata)

	ret, err := NewTriggerPrecompile().Run(call)
	require.NoError(t, err)
	require.Equal(t, byte(1), ret[31])
}

func TestRuntimeCapturesTriggerRegistration(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callTriggerPrecompileCode())

	result := runtime.Call(CallRequest{
		Caller: caller,
		Target: ContractAddressHash(contract),
		CallID: "call-1",
		Input:  EncodeRegisterHeightTriggerCall("vault-release", 100, 50000, []byte{1, 2, 3}),
		Gas:    100000,
		Block:  BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, result.Err)
	triggers := runtime.State.Triggers()
	require.Len(t, triggers, 1)
	require.Equal(t, "vault-release", triggers[0].ID)
	require.True(t, triggers[0].Contract.Equal(contract))
	require.Equal(t, int64(100), triggers[0].Height)
	require.Equal(t, int64(50000), triggers[0].GasLimit)
	require.Equal(t, []byte{1, 2, 3}, triggers[0].Calldata)
}

func TestRuntimeDiscardsTriggerRegistrationOnOuterRevert(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callTriggerPrecompileThenRevertCode())

	result := runtime.Call(CallRequest{
		Caller: caller,
		Target: ContractAddressHash(contract),
		CallID: "call-1",
		Input:  EncodeRegisterHeightTriggerCall("vault-release", 100, 50000, nil),
		Gas:    100000,
		Block:  BlockContext{GasLimit: 1000000},
	})
	require.Error(t, result.Err)
	require.Empty(t, runtime.State.Triggers())
}

func TestAssetPrecompileRejectsUint256Overflow(t *testing.T) {
	call := EncodeTransferAssetCall(SatoshiAssetName, "tb1pdest", "1", nil)
	call[4+64] = 1

	_, _, _, _, err := DecodeTransferAssetCall(call)
	require.Error(t, err)
}

func TestAssetPrecompileRejectsMalformedDynamicOffset(t *testing.T) {
	call := EncodeTransferAssetCall(SatoshiAssetName, "tb1pdest", "1", nil)
	call[4+31] = 0xff

	_, _, _, _, err := DecodeTransferAssetCall(call)
	require.Error(t, err)
}

func TestRuntimeCapturesTransferAssetIntent(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	runtime := NewRuntime(nil)

	result := runtime.Call(CallRequest{
		Caller: caller,
		Target: evmAddressFromGeth(AssetPrecompileAddress),
		CallID: "call-1",
		Input:  EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
		Gas:    100000,
		Block:  BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, result.Err)
	require.Len(t, runtime.AssetIntents, 1)
	require.Equal(t, "call-1", runtime.AssetIntents[0].CallID)
	require.Equal(t, SatoshiAssetName, runtime.AssetIntents[0].AssetName)
	require.Equal(t, "tb1qdest", runtime.AssetIntents[0].To)
	require.Equal(t, 0, runtime.AssetIntents[0].Amount.Cmp(mustDefaultDecimal(t, 77)))
	require.Equal(t, caller, ContractAddressHash(runtime.AssetIntents[0].From))
}

func TestRuntimeDiscardsAssetIntentOnOuterRevert(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := mustEVMAddress(t, "0x2222222222222222222222222222222222222222")
	runtime := NewRuntime(nil)
	runtime.SetCode(contract, callAssetPrecompileThenRevertCode())

	result := runtime.Call(CallRequest{
		Caller: caller,
		Target: contract,
		CallID: "call-1",
		Input:  EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
		Gas:    100000,
		Block:  BlockContext{GasLimit: 1000000},
	})
	require.Error(t, result.Err)
	require.Empty(t, runtime.AssetIntents)
}

func evmAddressFromGeth(addr [20]byte) EVMAddress {
	var out EVMAddress
	copy(out[:], addr[:])
	return out
}

func mustEVMAddress(t *testing.T, s string) EVMAddress {
	t.Helper()
	addr, err := ParseEVMAddressHex(s)
	require.NoError(t, err)
	return addr
}

func callAssetPrecompileThenRevertCode() []byte {
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
		0xf1,       // CALL
		0x50,       // POP
		0x60, 0x00, // PUSH1 0
		0x60, 0x00, // PUSH1 0
		0xfd, // REVERT
	)
	return code
}

func callTriggerPrecompileCode() []byte {
	return callPrecompileCode(TriggerPrecompileAddress, false)
}

func callTriggerPrecompileThenRevertCode() []byte {
	return callPrecompileCode(TriggerPrecompileAddress, true)
}

func callPrecompileCode(addr gethcommon.Address, revert bool) []byte {
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
	code = append(code, addr.Bytes()...)
	code = append(code,
		0x61, 0xc3, 0x50, // PUSH2 50000, gas
		0xf1, // CALL
	)
	if revert {
		code = append(code,
			0x50,       // POP
			0x60, 0x00, // PUSH1 0
			0x60, 0x00, // PUSH1 0
			0xfd, // REVERT
		)
		return code
	}
	code = append(code, 0x00) // STOP
	return code
}
