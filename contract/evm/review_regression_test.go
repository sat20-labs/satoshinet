package evm

import (
	"bytes"
	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
)

func TestReviewDuplicateDeployIsRefundableFailure(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	first := testDeployTx(t, 3, return42InitCode())
	second := testDeployTx(t, 3, return42InitCode())
	second.TxIn[0].PreviousOutPoint.Index++
	result, err := ExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{first, second}, Runtime: NewRuntime(nil), Block: testBlockContext(1), ResolveCaller: fixedCaller(caller), ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qrefund")})
	require.NoError(t, err)
	require.Len(t, result.Records, 2)
	require.Equal(t, ResultStatusSuccess, result.Records[0].Status)
	require.NotEqual(t, ResultStatusSuccess, result.Records[1].Status)
	require.True(t, result.Records[0].Contract.Equal(result.Records[1].Contract))
	require.True(t, result.Records[1].RequiresResult)
}

func TestReviewNativeValueAndUTXOBalance(t *testing.T) {
	contract := testContract(t)
	funding := contractframework.ContractOutputFromFunding(contractcommon.FundingOutput{Value: 7})
	for _, tc := range []struct {
		name   string
		opcode byte
		want   int64
	}{{"callvalue", 0x34, 7}, {"selfbalance", 0x47, 18}} {
		t.Run(tc.name, func(t *testing.T) {
			runtime := NewRuntime(nil)
			runtime.AssetBalances = testAssetBalances{ContractAddressHash(contract).String() + ":" + SatoshiAssetName: scommon.NewDefaultDecimal(11)}
			runtime.SetCode(ContractAddressHash(contract), []byte{tc.opcode, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3})
			result := runtime.Call(CallRequest{CallerAddress: "0x1111111111111111111111111111111111111111", TargetAddress: contract.MustEncode(), FundingOutput: &funding, Gas: 100000, Block: testBlockContext(1)})
			require.NoError(t, result.Err)
			require.Len(t, result.ReturnData, 32)
			require.Equal(t, byte(tc.want), result.ReturnData[31])
			require.True(t, runtime.State.GetBalance(ContractGethAddress(contract)).IsZero(), "UTXO balance must not create a second persistent ledger")
		})
	}
}

func TestReviewConstructorReadsFundingValue(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	contract, err := DeriveCreateContractAddress(TestnetContractPrefix, caller, 3)
	require.NoError(t, err)
	funding := contractframework.ContractOutputFromFunding(contractcommon.FundingOutput{Value: 7})
	runtime := NewRuntime(nil)
	result := runtime.Deploy(DeployRequest{CallerAddress: caller.String(), ExpectedContract: contract, DeployNonce: 3, FundingOutput: &funding, Gas: 100000, Block: testBlockContext(1), InitCode: []byte{0x34, 0x60, 0, 0x55, 0x60, 0, 0x60, 0, 0xf3}})
	require.NoError(t, result.Err)
	require.Equal(t, gethcommon.Hash{31: 7}, runtime.State.GetState(ContractGethAddress(contract), gethcommon.Hash{}))
	require.True(t, runtime.State.GetBalance(ContractGethAddress(contract)).IsZero())
}

func TestReviewNativeTransferCannotCreateUnsettledBalance(t *testing.T) {
	contract := testContract(t)
	runtime := NewRuntime(nil)
	runtime.AssetBalances = testAssetBalances{ContractAddressHash(contract).String() + ":" + SatoshiAssetName: scommon.NewDefaultDecimal(18)}
	// CALL another address with 1 sat, then return its success flag.
	code := []byte{0x60, 0, 0x60, 0, 0x60, 0, 0x60, 0, 0x60, 1, 0x73}
	code = append(code, bytes.Repeat([]byte{0x44}, 20)...)
	code = append(code, 0x61, 0x27, 0x10, 0xf1, 0x60, 0, 0x52, 0x60, 32, 0x60, 0, 0xf3)
	runtime.SetCode(ContractAddressHash(contract), code)
	result := runtime.Call(CallRequest{TargetAddress: contract.MustEncode(), Gas: 100000, Block: testBlockContext(1)})
	require.NoError(t, result.Err)
	require.Equal(t, make([]byte, 32), result.ReturnData)
	require.Empty(t, runtime.AssetIntents)
	require.True(t, runtime.State.GetBalance(gethcommon.BytesToAddress(bytes.Repeat([]byte{0x44}, 20))).IsZero())

	code = append([]byte{0x73}, bytes.Repeat([]byte{0x44}, 20)...)
	code = append(code, 0xff)
	runtime.SetCode(ContractAddressHash(contract), code)
	before := runtime.State.StateRoot()
	result = runtime.Call(CallRequest{TargetAddress: contract.MustEncode(), Gas: 100000, Block: testBlockContext(1)})
	require.ErrorIs(t, result.Err, ErrNativeTransferUnsupported)
	require.Equal(t, before, runtime.State.StateRoot())
	require.Empty(t, runtime.AssetIntents)
}

func TestReviewFailedConstructorRefundsNonGasFunding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   []byte
		status ResultStatus
	}{
		{"revert", []byte{0x60, 0, 0x60, 0, 0xfd}, ResultStatusRevert},
		{"out_of_gas", []byte{0x5b, 0x60, 0, 0x56}, ResultStatusOutOfGas},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
			tx := testDeployTx(t, 3, tc.code)
			tx.TxOut[1].Value = 7
			tx.TxOut[1].Assets = append(tx.TxOut[1].Assets, wire.AssetInfo{Name: *wire.NewAssetNameFromString("ordx:f:other"), Amount: *scommon.NewDefaultDecimal(9)})
			result, err := ExecuteBlock(BlockExecutionRequest{Txs: []*wire.MsgTx{tx}, Runtime: NewRuntime(nil), Block: testBlockContext(1), ResolveCaller: fixedCaller(caller), ResolveGasRefundRecipient: fixedGasRefundRecipient("tb1qrefund")})
			require.NoError(t, err)
			require.Len(t, result.Records, 1)
			record := result.Records[0]
			require.Equal(t, tc.status, record.Status)
			require.Len(t, record.AssetIntents, 2)
			require.Equal(t, SatoshiAssetName, record.AssetIntents[0].AssetName)
			require.Equal(t, "7", record.AssetIntents[0].Amount.String())
			require.Equal(t, "ordx:f:other", record.AssetIntents[1].AssetName)
			require.Equal(t, "9", record.AssetIntents[1].Amount.String())
			for _, intent := range record.AssetIntents {
				require.Equal(t, "tb1qrefund", intent.To)
				require.True(t, intent.From.Equal(record.Contract))
			}
		})
	}
}
