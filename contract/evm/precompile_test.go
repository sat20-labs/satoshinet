package evm

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	gethabi "github.com/ethereum/go-ethereum/accounts/abi"
	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
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

func TestFundingOverlayBalanceOfAddsCurrentFunding(t *testing.T) {
	contract := mustEVMAddress(t, "0x0102030405060708090001020304050607080900")
	other := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	assetName := "brc20:f:ooxx"
	assets, err := NewAssetSet(assetName, scommon.NewDefaultDecimal(7))
	require.NoError(t, err)
	funding := NewFundingAssetView([]contractframework.ContractOutput{
		contractframework.ContractOutputFromFunding(evmcommon.FundingOutput{
			OutPoint: evmcommon.TxOutPoint{
				TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Vout: 0,
			},
			Vout:   0,
			Value:  5,
			Assets: assets,
		}),
	}, "brc20:f:sgas", scommon.NewDefaultDecimal(0))
	base := testAssetBalances{
		contract.String() + ":" + assetName:        mustDefaultDecimal(t, 3),
		contract.String() + ":" + SatoshiAssetName: mustDefaultDecimal(t, 2),
		other.String() + ":" + assetName:           mustDefaultDecimal(t, 11),
	}
	balances := NewFundingOverlayAssetBalanceView(base, funding, contract)

	got, err := balances.AssetBalance(contract, assetName)
	require.NoError(t, err)
	require.Equal(t, "10", got.String())

	got, err = balances.AssetBalance(contract, SatoshiAssetName)
	require.NoError(t, err)
	require.Equal(t, "7", got.String())

	got, err = balances.AssetBalance(other, assetName)
	require.NoError(t, err)
	require.Equal(t, "11", got.String())
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

func TestAssetPrecompileCallerAddress(t *testing.T) {
	precompile := NewAssetPrecompile(nil, nil, "tb1pcaller")

	ret, err := precompile.Run(EncodeCallerAddressCall())
	require.NoError(t, err)
	require.Equal(t, "tb1pcaller", abiRawDynamicString(t, ret))

	_, err = NewAssetPrecompile(nil, nil).Run(EncodeCallerAddressCall())
	require.ErrorContains(t, err, "caller address is not configured")
}

func TestAssetPrecompileTransferAssetABI(t *testing.T) {
	call := EncodeTransferAssetCall("ordx:ticker:0", "tb1ptest", "1000", nil)

	assetName, to, amount, extraData, err := DecodeTransferAssetCall(call)
	require.NoError(t, err)
	require.Equal(t, "ordx:ticker:0", assetName)
	require.Equal(t, "tb1ptest", to)
	require.Equal(t, "1000", amount.String())
	require.Empty(t, extraData)

	precompile := NewAssetPrecompile(nil, nil)
	precompile.Precision = testAssetPrecisionPolicy()
	ret, err := precompile.Run(call)
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

func TestAssetPrecompileTransferAssetsABI(t *testing.T) {
	gas := DefaultGasConfig().GasAssetName
	call := EncodeTransferAssetsCall(
		[]string{SatoshiAssetName, gas, "ordx:f:ooxx", "brc20:f:ooxx", "runes:f:BITCOIN•TESTNET"},
		[]string{"tb1psats", "tb1pgas", "tb1pordx", "tb1pbrc20", "tb1prunes"},
		[]string{"1", "50", "1000", "7.5", "3"},
		[][]byte{nil, nil, nil, nil, nil},
	)

	transfers, err := DecodeTransferAssetsCall(call)
	require.NoError(t, err)
	require.Len(t, transfers, 5)
	require.Equal(t, SatoshiAssetName, transfers[0].AssetName)
	require.Equal(t, gas, transfers[1].AssetName)
	require.Equal(t, "ordx:f:ooxx", transfers[2].AssetName)
	require.Equal(t, "brc20:f:ooxx", transfers[3].AssetName)
	require.Equal(t, "runes:f:BITCOIN•TESTNET", transfers[4].AssetName)
	require.Equal(t, "7.5", transfers[3].Amount.String())
	require.Empty(t, transfers[4].ExtraData)

	precompile := NewAssetPrecompile(nil, nil)
	precompile.Precision = testAssetPrecisionPolicy()
	ret, err := precompile.Run(call)
	require.NoError(t, err)
	require.Equal(t, byte(1), ret[31])
}

func TestAssetPrecompileRequiresExactPrecision(t *testing.T) {
	policy := contractframework.AssetPrecisionPolicy{Resolve: func(assetName string) (int, bool) {
		switch assetName {
		case "asset0":
			return 0, true
		case "asset2":
			return 2, true
		default:
			return 0, false
		}
	}}
	tests := []struct {
		name      string
		assetName string
		amount    string
		wantErr   string
	}{
		{name: "precision zero fraction", assetName: "asset0", amount: "1.9", wantErr: "not exactly representable"},
		{name: "precision zero rounds to zero", assetName: "asset0", amount: "0.9", wantErr: "not exactly representable"},
		{name: "precision two exact", assetName: "asset2", amount: "1.23"},
		{name: "precision two excess", assetName: "asset2", amount: "1.234", wantErr: "not exactly representable"},
		{name: "unknown asset", assetName: "unknown", amount: "1", wantErr: "unknown asset precision"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			precompile := NewAssetPrecompile(nil, nil)
			precompile.Precision = policy
			_, err := precompile.Run(EncodeTransferAssetCall(test.assetName, "tb1pdest", test.amount, nil))
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestAssetPrecompileTransferAssetsSolidityABI(t *testing.T) {
	parsed, err := gethabi.JSON(strings.NewReader(`[{
		"name":"transferAssets",
		"type":"function",
		"inputs":[
			{"name":"assetNames","type":"string[]"},
			{"name":"recipients","type":"string[]"},
			{"name":"amounts","type":"string[]"},
			{"name":"extraData","type":"bytes[]"}
		],
		"outputs":[{"name":"","type":"bool"}]
	}]`))
	require.NoError(t, err)
	input, err := parsed.Pack("transferAssets",
		[]string{SatoshiAssetName, "brc20:f:sgas"},
		[]string{"tb1psats", "tb1pgas"},
		[]string{"1", "50"},
		[][]byte{nil, nil},
	)
	require.NoError(t, err)
	transfers, err := DecodeTransferAssetsCall(input)
	require.NoError(t, err)
	require.Len(t, transfers, 2)
	require.Equal(t, SatoshiAssetName, transfers[0].AssetName)
	require.Equal(t, "tb1psats", transfers[0].To)
	require.Equal(t, "1", transfers[0].Amount.String())
	require.Equal(t, "brc20:f:sgas", transfers[1].AssetName)
	require.Equal(t, "tb1pgas", transfers[1].To)
	require.Equal(t, "50", transfers[1].Amount.String())
	require.Empty(t, transfers[1].ExtraData)
}

func TestAssetTransfersRejectUnsettleableFields(t *testing.T) {
	_, _, _, _, err := DecodeTransferAssetCall(EncodeTransferAssetCall("", "tb1pdest", "1", nil))
	require.ErrorContains(t, err, "asset name is empty")
	_, _, _, _, err = DecodeTransferAssetCall(EncodeTransferAssetCall(SatoshiAssetName, "", "1", nil))
	require.ErrorContains(t, err, "recipient is empty")
	_, _, _, _, err = DecodeTransferAssetCall(EncodeTransferAssetCall(SatoshiAssetName, "tb1pdest", "0", nil))
	require.ErrorContains(t, err, "must be positive")
	_, _, _, _, err = DecodeTransferAssetCall(EncodeTransferAssetCall(SatoshiAssetName, "tb1pdest", "1", []byte{1}))
	require.ErrorContains(t, err, "extra data is not supported")
}

func TestABIDynamicLengthOverflowRejected(t *testing.T) {
	args := make([]byte, 64)
	binary.BigEndian.PutUint64(args[24:32], 32)
	for i := 32; i < 64; i++ {
		args[i] = 0xff
	}
	_, err := abiReadDynamicBytes(args, 0)
	require.Error(t, err)
}

func FuzzABIDynamicReadersNeverPanic(f *testing.F) {
	f.Add([]byte{0})
	f.Add(make([]byte, 64))
	f.Fuzz(func(t *testing.T, args []byte) {
		_, _ = abiReadDynamicBytes(args, 0)
		_, _ = abiReadDynamicArray(args, 0, maxTransferAssetCount, maxABIDynamicBytes)
	})
}

func FuzzAssetPrecompileNeverPanics(f *testing.F) {
	f.Add([]byte{0})
	f.Add(EncodeCompareAmountCall("1:63", "2:63"))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = NewAssetPrecompile(nil, nil).Run(input)
	})
}

func FuzzTriggerPrecompileNeverPanics(f *testing.F) {
	f.Add([]byte{0})
	f.Add(EncodeRegisterHeightTriggerCall("test", 1, 1000, nil))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = NewTriggerPrecompile().Run(input)
	})
}

func TestAssetPrecompileTransferAssetsRejectsInvalidInput(t *testing.T) {
	_, err := DecodeTransferAssetsCall(EncodeTransferAssetsCall(nil, nil, nil, nil))
	require.ErrorContains(t, err, "at least one")

	_, err = DecodeTransferAssetsCall(EncodeTransferAssetsCall(
		[]string{SatoshiAssetName},
		[]string{"tb1pdest", "tb1pextra"},
		[]string{"1"},
		[][]byte{nil},
	))
	require.ErrorContains(t, err, "length mismatch")

	_, err = DecodeTransferAssetsCall(EncodeTransferAssetsCall(
		[]string{SatoshiAssetName},
		[]string{"tb1pdest"},
		[]string{"0"},
		[][]byte{nil},
	))
	require.ErrorContains(t, err, "must be positive")

	assets := make([]string, maxTransferAssetCount+1)
	recipients := make([]string, len(assets))
	amounts := make([]string, len(assets))
	extra := make([][]byte, len(assets))
	for i := range assets {
		assets[i] = SatoshiAssetName
		recipients[i] = "tb1pdest"
		amounts[i] = "1"
	}
	_, err = DecodeTransferAssetsCall(EncodeTransferAssetsCall(assets, recipients, amounts, extra))
	require.ErrorContains(t, err, "length too large")
}

func TestTriggerCalldataLimit(t *testing.T) {
	call := EncodeRegisterHeightTriggerCall("test", 1, 1000, make([]byte, maxTriggerCalldataBytes+1))
	_, err := DecodeTriggerRegistrationCall(call)
	require.ErrorContains(t, err, "out of bounds")
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

	_, err = precompile.Run(EncodeMulAmountCall("1:40", "1:40"))
	require.ErrorContains(t, err, "precision exceeds protocol maximum")
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
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "call-1",
		Input:         EncodeRegisterHeightTriggerCall("vault-release", 100, 50000, []byte{1, 2, 3}),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
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
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "call-1",
		Input:         EncodeRegisterHeightTriggerCall("vault-release", 100, 11, nil),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
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
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "call-1",
		Input:         EncodeRegisterHeightTriggerCall("vault-release", 100, 0, nil),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
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
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "call-1",
		Input:         EncodeRegisterHeightTriggerCall("vault-release", 100, 50000, nil),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
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
	configureTestAssetEffects(runtime)

	result := runtime.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: evmAddressFromGeth(AssetPrecompileAddress).String(),
		CallID:        "call-1",
		Input:         EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, result.Err)
	require.Len(t, runtime.AssetIntents, 1)
	require.Equal(t, "call-1", runtime.AssetIntents[0].CallID)
	require.Equal(t, SatoshiAssetName, runtime.AssetIntents[0].AssetName)
	require.Equal(t, "tb1qdest", runtime.AssetIntents[0].To)
	require.Equal(t, 0, runtime.AssetIntents[0].Amount.Cmp(mustDefaultDecimal(t, 77)))
	require.Equal(t, caller, ContractAddressHash(runtime.AssetIntents[0].From))
}

func TestRuntimeCapturesTransferAssetsIntents(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	runtime := NewRuntime(nil)
	configureTestAssetEffects(runtime)
	gas := DefaultGasConfig().GasAssetName

	result := runtime.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: evmAddressFromGeth(AssetPrecompileAddress).String(),
		CallID:        "call-batch",
		Input: EncodeTransferAssetsCall(
			[]string{SatoshiAssetName, gas, "ordx:f:ooxx", "brc20:f:ooxx", "runes:f:BITCOIN•TESTNET"},
			[]string{"tb1psats", "tb1pgas", "tb1pordx", "tb1pbrc20", "tb1prunes"},
			[]string{"1", "50", "1000", "7.5", "3"},
			[][]byte{nil, nil, nil, nil, nil},
		),
		Gas:   200000,
		Block: BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, result.Err)
	require.Len(t, runtime.AssetIntents, 5)
	require.Equal(t, SatoshiAssetName, runtime.AssetIntents[0].AssetName)
	require.Equal(t, "tb1psats", runtime.AssetIntents[0].To)
	require.Equal(t, "1", runtime.AssetIntents[0].Amount.String())
	require.Equal(t, gas, runtime.AssetIntents[1].AssetName)
	require.Equal(t, "ordx:f:ooxx", runtime.AssetIntents[2].AssetName)
	require.Equal(t, "brc20:f:ooxx", runtime.AssetIntents[3].AssetName)
	require.Equal(t, "7.5", runtime.AssetIntents[3].Amount.String())
	require.Equal(t, "runes:f:BITCOIN•TESTNET", runtime.AssetIntents[4].AssetName)
	require.Empty(t, runtime.AssetIntents[4].ExtraData)
	for _, intent := range runtime.AssetIntents {
		require.Equal(t, "call-batch", intent.CallID)
		require.Equal(t, caller, ContractAddressHash(intent.From))
	}
}

func testAssetPrecisionPolicy() contractframework.AssetPrecisionPolicy {
	return contractframework.AssetPrecisionPolicy{Resolve: func(assetName string) (int, bool) {
		switch assetName {
		case DefaultGasConfig().GasAssetName, "ordx:ticker:0", "ordx:f:ooxx", "runes:f:BITCOIN•TESTNET":
			return 0, true
		case "brc20:f:ooxx":
			return 1, true
		default:
			return 0, false
		}
	}}
}

func configureTestAssetEffects(runtime *Runtime) {
	runtime.AssetPrecision = testAssetPrecisionPolicy()
	runtime.ResolveResultScript = func(ResultOutput) ([]byte, error) { return []byte{0x51}, nil }
}

func TestRuntimeRejectsUnsettleableIntentBeforeCommit(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	runtime := NewRuntime(nil)
	runtime.ResolveResultScript = func(output ResultOutput) ([]byte, error) {
		if output.To == "bad-recipient" {
			return nil, errors.New("invalid recipient")
		}
		return []byte{0x51}, nil
	}
	result := runtime.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: evmAddressFromGeth(AssetPrecompileAddress).String(),
		CallID:        "unsettleable",
		Input:         EncodeTransferAssetCall(SatoshiAssetName, "bad-recipient", "1", nil),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.ErrorContains(t, result.Err, "not settleable")
	require.Empty(t, runtime.AssetIntents)
}

func TestRuntimeCommitRechecksAssetPrecision(t *testing.T) {
	runtime := NewRuntime(nil)
	runtime.AssetPrecision = contractframework.AssetPrecisionPolicy{Resolve: func(assetName string) (int, bool) {
		return 0, assetName == "asset0"
	}}
	runtime.ResolveResultScript = func(ResultOutput) ([]byte, error) { return []byte{0x51}, nil }
	err := runtime.commitCapturedEffects([]AssetIntent{{
		From:      testContract(t),
		To:        "tb1pdest",
		AssetName: "asset0",
		Amount:    mustDecimalString(t, "1.9"),
	}}, nil, nil, 0)
	require.ErrorContains(t, err, "not exactly representable")
	require.Empty(t, runtime.AssetIntents)
}

func TestRuntimeIntentRequiresResultResolver(t *testing.T) {
	runtime := NewRuntime(nil)
	err := runtime.commitCapturedEffects([]AssetIntent{{
		From:      testContract(t),
		To:        "tb1pdest",
		AssetName: SatoshiAssetName,
		Amount:    mustDefaultDecimal(t, 1),
	}}, nil, nil, 0)
	require.ErrorContains(t, err, "missing Result script resolver")
}

func TestRuntimeRejectsTriggerTooFarInFuture(t *testing.T) {
	runtime := NewRuntime(nil)
	err := runtime.commitCapturedEffects(nil, []Trigger{{
		ID:       "far-future",
		Contract: testContract(t),
		Kind:     TriggerAtHeight,
		Height:   MaxEVMTriggerFutureBlocks + 1,
		GasLimit: 1,
	}}, nil, 0)
	require.ErrorContains(t, err, "maximum future range")
}

func TestRuntimeRejectsBlockAssetIntentLimit(t *testing.T) {
	runtime := NewRuntime(nil)
	runtime.AssetIntents = make([]AssetIntent, MaxEVMAssetIntentsPerBlock)
	runtime.ResolveResultScript = func(ResultOutput) ([]byte, error) { return []byte{0x51}, nil }
	err := runtime.commitCapturedEffects([]AssetIntent{{
		From: testContract(t), To: "tb1pdest", AssetName: SatoshiAssetName,
		Amount: mustDefaultDecimal(t, 1),
	}}, nil, nil, 0)
	require.ErrorContains(t, err, "in block")
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
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
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
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
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

func TestRuntimeSatsFundingDoesNotRequireNativeEVMBalance(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	funding := contractframework.ContractOutputFromFunding(evmcommon.FundingOutput{
		OutPoint: evmcommon.TxOutPoint{
			TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Vout: 1,
		},
		Vout:  1,
		Value: 5,
	})
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())

	result := runtime.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "read-sats",
		Input:         EncodeFundingSatsCall(),
		Gas:           100000,
		Value:         5,
		FundingOutput: &funding,
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, result.Err)
}

func TestRuntimeDiscardsAssetIntentOnOuterRevert(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := mustEVMAddress(t, "0x2222222222222222222222222222222222222222")
	runtime := NewRuntime(nil)
	runtime.SetCode(contract, callAssetPrecompileThenRevertCode())

	result := runtime.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: contract.String(),
		CallID:        "call-1",
		Input:         EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.Error(t, result.Err)
	require.Empty(t, runtime.AssetIntents)
}

func TestRuntimeStaticCallCannotTransferAssets(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callPrecompileWithOpcodeCode(AssetPrecompileAddress, vm.STATICCALL))

	for _, input := range [][]byte{
		EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
		EncodeTransferAssetsCall(
			[]string{SatoshiAssetName, "brc20:f:ooxx"},
			[]string{"tb1qsats", "tb1qasset"},
			[]string{"1", "2"},
			[][]byte{nil, nil},
		),
	} {
		result := runtime.Call(CallRequest{
			CallerAddress: caller.String(),
			TargetAddress: contract.MustEncode(),
			CallID:        "static-transfer",
			Input:         input,
			Gas:           100000,
			Block:         BlockContext{GasLimit: 1000000},
		})
		require.NoError(t, result.Err)
		require.Empty(t, runtime.AssetIntents)
	}
}

func TestRuntimeStaticCallCannotClaimFunding(t *testing.T) {
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
	runtime.SetCode(ContractAddressHash(contract), callPrecompileWithOpcodeCode(AssetPrecompileAddress, vm.STATICCALL))

	result := runtime.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "static-claim",
		Input:         EncodeClaimFundingAssetCall(gasAsset, "700"),
		Gas:           100000,
		FundingOutput: &funding,
		GasAssetName:  gasAsset,
		GasFeeReserve: mustDefaultDecimal(t, 50),
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, result.Err)
	require.Zero(t, result.RetainedGasFunding.Sign())
}

func TestRuntimeStaticCallCannotRegisterTrigger(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.SetCode(ContractAddressHash(contract), callPrecompileWithOpcodeCode(TriggerPrecompileAddress, vm.STATICCALL))

	result := runtime.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "static-trigger",
		Input:         EncodeRegisterHeightTriggerCall("readonly", 100, 50000, nil),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, result.Err)
	require.Empty(t, runtime.State.Triggers())
}

func TestRuntimeNestedStaticCallCannotCreateEffects(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	outer := mustEVMAddress(t, "0x2222222222222222222222222222222222222222")
	middle := mustEVMAddress(t, "0x3333333333333333333333333333333333333333")

	for _, opcode := range []vm.OpCode{vm.CALL, vm.DELEGATECALL} {
		runtime := NewRuntime(nil)
		runtime.SetCode(outer, callPrecompileWithOpcodeCode(gethcommon.Address(middle), vm.STATICCALL))
		runtime.SetCode(middle, callPrecompileWithOpcodeCode(AssetPrecompileAddress, opcode))

		result := runtime.Call(CallRequest{
			CallerAddress: caller.String(),
			TargetAddress: outer.String(),
			CallID:        "nested-static",
			Input:         EncodeTransferAssetCall(SatoshiAssetName, "tb1qdest", "77", nil),
			Gas:           200000,
			Block:         BlockContext{GasLimit: 1000000},
		})
		require.NoError(t, result.Err, opcode.String())
		require.Empty(t, runtime.AssetIntents, opcode.String())
	}
}

func TestRuntimeRollsBackStorageWhenEffectCommitFails(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.GasConfig = GasConfig{MaxGasPerTrigger: 10}
	runtime.SetCode(ContractAddressHash(contract), storeThenCallTriggerPrecompileCode())

	result := runtime.Call(CallRequest{
		CallerAddress: caller.String(),
		TargetAddress: contract.MustEncode(),
		CallID:        "call-rollback",
		Input:         EncodeRegisterHeightTriggerCall("vault-release", 100, 11, nil),
		Gas:           100000,
		Block:         BlockContext{GasLimit: 1000000},
	})
	require.ErrorContains(t, result.Err, "trigger gas limit exceeds maximum")
	require.Zero(t, runtime.State.GetState(GethAddress(ContractAddressHash(contract)), gethcommon.Hash{}))
	require.Empty(t, runtime.State.Triggers())
	require.Empty(t, runtime.State.journal)
	require.Empty(t, runtime.State.revisions)
}

func TestRuntimePendingAssetLedgerSpansCalls(t *testing.T) {
	caller := mustEVMAddress(t, "0x1111111111111111111111111111111111111111")
	contract := testContract(t)
	runtime := NewRuntime(nil)
	configureTestAssetEffects(runtime)
	runtime.SetCode(ContractAddressHash(contract), callAssetPrecompileCode())
	runtime.AssetBalances = NewUTXOAssetView([]UTXO{
		mustUTXO(t, OutPoint{TxID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Vout: 0},
			contract, SatoshiAssetName, 100, 1),
	})

	first := runtime.Call(CallRequest{
		CallerAddress: caller.String(), TargetAddress: contract.MustEncode(), CallID: "first",
		Input: EncodeTransferAssetCall(SatoshiAssetName, "tb1qone", "60", nil),
		Gas:   100000, Block: BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, first.Err)
	require.Len(t, runtime.AssetIntents, 1)

	balance := runtime.Call(CallRequest{
		CallerAddress: caller.String(), TargetAddress: evmAddressFromGeth(AssetPrecompileAddress).String(), CallID: "balance",
		Input: EncodeBalanceOfCall(ContractAddressHash(contract), SatoshiAssetName),
		Gas:   100000, Block: BlockContext{GasLimit: 1000000},
	})
	require.NoError(t, balance.Err)
	require.Equal(t, "40", abiRawDynamicString(t, balance.ReturnData))

	second := runtime.Call(CallRequest{
		CallerAddress: caller.String(), TargetAddress: contract.MustEncode(), CallID: "second",
		Input: EncodeTransferAssetCall(SatoshiAssetName, "tb1qtwo", "50", nil),
		Gas:   100000, Block: BlockContext{GasLimit: 1000000},
	})
	require.ErrorContains(t, second.Err, "only 100 is available")
	require.Len(t, runtime.AssetIntents, 1)
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

func storeThenCallTriggerPrecompileCode() []byte {
	code := []byte{
		0x60, 0x01, // PUSH1 value 1
		0x60, 0x00, // PUSH1 slot 0
		0x55, // SSTORE
	}
	return append(code, callTriggerPrecompileCode()...)
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

func callPrecompileWithOpcodeCode(addr gethcommon.Address, opcode vm.OpCode) []byte {
	code := []byte{
		0x36,       // CALLDATASIZE
		0x60, 0x00, // PUSH1 0
		0x60, 0x00, // PUSH1 0
		0x37,       // CALLDATACOPY
		0x60, 0x00, // PUSH1 0, output size
		0x60, 0x00, // PUSH1 0, output offset
		0x36,       // CALLDATASIZE, input size
		0x60, 0x00, // PUSH1 0, input offset
	}
	if opcode == vm.CALL {
		code = append(code, 0x60, 0x00) // PUSH1 0, value
	}
	code = append(code, 0x73) // PUSH20 target address
	code = append(code, addr.Bytes()...)
	code = append(code,
		0x61, 0xc3, 0x50, // PUSH2 50000, gas
		byte(opcode),
		0x50, // POP success
		0x00, // STOP
	)
	return code
}
