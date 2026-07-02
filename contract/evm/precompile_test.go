package evm

import (
	"encoding/binary"
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

type testAssetBalances map[string]*scommon.Decimal

func (b testAssetBalances) AssetBalance(owner EVMAddress, assetName string) (*scommon.Decimal, error) {
	return b[owner.String()+":"+assetName], nil
}

func TestAssetPrecompileBalanceOf(t *testing.T) {
	owner := mustEVMAddress(t, "0x0102030405060708090001020304050607080900")
	balances := testAssetBalances{owner.String() + ":" + SatoshiAssetName: mustDefaultDecimal(t, 42)}
	precompile := NewAssetPrecompile(balances, nil)

	ret, err := precompile.Run(EncodeBalanceOfCall(owner, SatoshiAssetName))
	require.NoError(t, err)
	length, err := abiWordToUint64(ret[:32])
	require.NoError(t, err)
	require.Equal(t, uint64(2), length)
	balanceText := string(ret[32 : 32+length])
	require.Equal(t, "42", balanceText)
}

func TestAssetPrecompileFundingAssetAmount(t *testing.T) {
	gasAsset := "brc20:f:sgas"
	assetSet, err := NewAssetSet(gasAsset, scommon.NewDefaultDecimal(1050))
	require.NoError(t, err)
	otherSet, err := NewAssetSet("brc20:f:ooxx", scommon.NewDefaultDecimal(7))
	require.NoError(t, err)
	funding := NewFundingAssetView([]contractframework.ContractOutput{
		contractframework.ContractOutputFromFunding(evmcommon.FundingOutput{
			OutPoint: evmcommon.TxOutPoint{
				TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Vout: 0,
			},
			Vout:   0,
			Value:  25,
			Assets: assetSet,
		}),
		contractframework.ContractOutputFromFunding(evmcommon.FundingOutput{
			OutPoint: evmcommon.TxOutPoint{
				TxID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Vout: 1,
			},
			Vout:   1,
			Assets: otherSet,
		}),
	}, gasAsset, scommon.NewDefaultDecimal(50))
	precompile := NewAssetPrecompile(nil, funding)

	ret, err := precompile.Run(EncodeFundingAssetAmountCall(gasAsset))
	require.NoError(t, err)
	require.Equal(t, "1000", abiRawDynamicString(t, ret))

	ret, err = precompile.Run(EncodeFundingAssetAmountCall("brc20:f:ooxx"))
	require.NoError(t, err)
	require.Equal(t, "7", abiRawDynamicString(t, ret))

	ret, err = precompile.Run(EncodeFundingAssetAmountCall(SatoshiAssetName))
	require.NoError(t, err)
	require.Equal(t, "25", abiRawDynamicString(t, ret))

	ret, err = precompile.Run(EncodeFundingSatsCall())
	require.NoError(t, err)
	require.Equal(t, uint64(25), binary.BigEndian.Uint64(ret[24:32]))
	require.Zero(t, funding.ClaimedAssetAmount(gasAsset).Sign())

	ret, err = precompile.Run(EncodeClaimFundingAssetCall(gasAsset, "600"))
	require.NoError(t, err)
	require.Equal(t, byte(1), ret[31])
	require.Equal(t, "600", funding.ClaimedAssetAmount(gasAsset).String())

	_, err = precompile.Run(EncodeClaimFundingAssetCall(gasAsset, "401"))
	require.ErrorContains(t, err, "exceeds available")
}

func TestAssetPrecompileTransferAssetABI(t *testing.T) {
	call := EncodeTransferAssetCall("ordx:ticker:0", "tb1ptest", "1000", []byte{1, 2, 3})

	assetName, to, amount, extraData, err := DecodeTransferAssetCall(call)
	require.NoError(t, err)
	require.Equal(t, "ordx:ticker:0", assetName)
	require.Equal(t, "tb1ptest", to)
	require.Equal(t, "1000", amount.String())
	require.Equal(t, []byte{1, 2, 3}, extraData)

	ret, err := NewAssetPrecompile(nil, nil).Run(call)
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

func TestAssetPrecompileAmountCompare(t *testing.T) {
	precompile := NewAssetPrecompile(nil, nil)

	ret, err := precompile.Run(EncodeCompareAmountCall("1.2", "1.20"))
	require.NoError(t, err)
	require.Equal(t, int64(0), abiInt256Small(t, ret))

	ret, err = precompile.Run(EncodeCompareAmountCall("1.2000000001", "1.2"))
	require.NoError(t, err)
	require.Equal(t, int64(1), abiInt256Small(t, ret))

	ret, err = precompile.Run(EncodeCompareAmountCall("0.9", "1"))
	require.NoError(t, err)
	require.Equal(t, int64(-1), abiInt256Small(t, ret))
}

func TestAssetPrecompileAmountArithmetic(t *testing.T) {
	precompile := NewAssetPrecompile(nil, nil)

	ret, err := precompile.Run(EncodeAddAmountCall("1.2", "0.03"))
	require.NoError(t, err)
	require.Equal(t, "1.23", abiRawDynamicString(t, ret))

	ret, err = precompile.Run(EncodeSubAmountCall("1.2", "0.03"))
	require.NoError(t, err)
	require.Equal(t, "1.17", abiRawDynamicString(t, ret))

	ret, err = precompile.Run(EncodeMulAmountCall("1.5", "2"))
	require.NoError(t, err)
	require.Equal(t, "3", abiRawDynamicString(t, ret))

	ret, err = precompile.Run(EncodeDivAmountCall("3", "2"))
	require.NoError(t, err)
	require.Equal(t, "1.5", abiRawDynamicString(t, ret))
}

func TestAssetPrecompileAmountArithmeticRejectsInvalidResults(t *testing.T) {
	precompile := NewAssetPrecompile(nil, nil)

	_, err := precompile.Run(EncodeSubAmountCall("1", "2"))
	require.ErrorContains(t, err, "underflows")

	_, err = precompile.Run(EncodeDivAmountCall("1", "0"))
	require.ErrorContains(t, err, "division by zero")
}

func TestAssetPrecompileAmountUintConversions(t *testing.T) {
	precompile := NewAssetPrecompile(nil, nil)

	ret, err := precompile.Run(EncodeUintToAmountCall(123))
	require.NoError(t, err)
	require.Equal(t, "123", abiRawDynamicString(t, ret))

	ret, err = precompile.Run(EncodeAmountToUintFloorCall("123.999"))
	require.NoError(t, err)
	require.Equal(t, uint64(123), binary.BigEndian.Uint64(ret[24:32]))

	ret, err = precompile.Run(EncodeAmountToUintFloorCall("0.999"))
	require.NoError(t, err)
	require.Equal(t, uint64(0), binary.BigEndian.Uint64(ret[24:32]))

	ret, err = precompile.Run(EncodeAmountToUintCeilCall("123"))
	require.NoError(t, err)
	require.Equal(t, uint64(123), binary.BigEndian.Uint64(ret[24:32]))

	ret, err = precompile.Run(EncodeAmountToUintCeilCall("123.001"))
	require.NoError(t, err)
	require.Equal(t, uint64(124), binary.BigEndian.Uint64(ret[24:32]))
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

func TestRuntimeRejectsOverLimitTriggerRegistration(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.GasConfig = GasConfig{MaxGasPerTrigger: 10}
	runtime.SetCode(ContractAddressHash(contract), callTriggerPrecompileCode())

	result := runtime.Call(CallRequest{
		Caller: caller,
		Target: ContractAddressHash(contract),
		CallID: "call-1",
		Input:  EncodeRegisterHeightTriggerCall("vault-release", 100, 11, nil),
		Gas:    100000,
		Block:  BlockContext{GasLimit: 1000000},
	})
	require.ErrorContains(t, result.Err, "trigger gas limit exceeds maximum")
	require.Equal(t, ResultStatusInvalid, result.Status)
	require.Empty(t, runtime.State.Triggers())
}

func TestRuntimeRejectsZeroTriggerRegistration(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callTriggerPrecompileCode())

	result := runtime.Call(CallRequest{
		Caller: caller,
		Target: ContractAddressHash(contract),
		CallID: "call-1",
		Input:  EncodeRegisterHeightTriggerCall("vault-release", 100, 0, nil),
		Gas:    100000,
		Block:  BlockContext{GasLimit: 1000000},
	})
	require.ErrorContains(t, result.Err, "trigger gas limit must be positive")
	require.Equal(t, ResultStatusInvalid, result.Status)
	require.Empty(t, runtime.State.Triggers())
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

func TestRuntimeRetainsOnlyClaimedGasFunding(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	gasAsset := "brc20:f:sgas"
	assets, err := NewAssetSet(gasAsset, scommon.NewDefaultDecimal(1050))
	require.NoError(t, err)
	funding := contractframework.ContractOutputFromFunding(evmcommon.FundingOutput{
		OutPoint: evmcommon.TxOutPoint{
			TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Vout: 1,
		},
		Vout:     1,
		Contract: contract,
		Assets:   assets,
	})
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	readResult := runtime.Call(CallRequest{
		Caller:        caller,
		Target:        ContractAddressHash(contract),
		CallID:        "read",
		Input:         EncodeFundingAssetAmountCall(gasAsset),
		Gas:           100000,
		FundingOutput: &funding,
		GasAssetName:  gasAsset,
		GasFeeReserve: mustDefaultDecimal(t, 50),
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, readResult.Err)
	require.Zero(t, readResult.RetainedGasFunding.Sign())

	claimResult := runtime.Call(CallRequest{
		Caller:        caller,
		Target:        ContractAddressHash(contract),
		CallID:        "claim",
		Input:         EncodeClaimFundingAssetCall(gasAsset, "700"),
		Gas:           100000,
		FundingOutput: &funding,
		GasAssetName:  gasAsset,
		GasFeeReserve: mustDefaultDecimal(t, 50),
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, claimResult.Err)
	require.Equal(t, "700", claimResult.RetainedGasFunding.String())
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

func abiRawDynamicString(t *testing.T, ret []byte) string {
	t.Helper()
	require.GreaterOrEqual(t, len(ret), 32)
	length, err := abiWordToUint64(ret[:32])
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(ret), 32+int(length))
	return string(ret[32 : 32+length])
}

func abiInt256Small(t *testing.T, ret []byte) int64 {
	t.Helper()
	require.Len(t, ret, 32)
	if ret[0]&0x80 == 0 {
		for _, b := range ret[:24] {
			require.Zero(t, b)
		}
		return int64(binary.BigEndian.Uint64(ret[24:32]))
	}
	for _, b := range ret[:24] {
		require.Equal(t, byte(0xff), b)
	}
	return int64(binary.BigEndian.Uint64(ret[24:32]))
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
