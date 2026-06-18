package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBuildDeployTx(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	prev := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	tx, contract, err := BuildDeployTx(DeployTxBuildRequest{
		ContractPrefix: TestnetContractPrefix,
		Caller:         caller,
		GasLimit:       100000,
		DeployNonce:    3,
		InitCode:       []byte{0x60, 0x00},
		Funding: wire.TxOut{Assets: wire.TxAssets{{
			Name:   *wire.NewAssetNameFromString("ordx:ft:gas"),
			Amount: *mustDefaultDecimal(t, 100000),
		}}},
		Inputs: []wire.OutPoint{prev},
	})
	require.NoError(t, err)
	require.Len(t, tx.TxIn, 1)
	require.Equal(t, prev, tx.TxIn[0].PreviousOutPoint)
	require.Len(t, tx.TxOut, 2)

	parsed, err := ParseTx(tx, StandardContractScriptResolver(TestnetContractPrefix))
	require.NoError(t, err)
	require.Equal(t, TxTypeDeploy, parsed.Type)
	require.Equal(t, int64(100000), parsed.Deploy.GasLimit)

	funding, err := FindContractOutputsForContract(tx,
		StandardContractScriptResolver(TestnetContractPrefix), contract)
	require.NoError(t, err)
	require.Len(t, funding, 1)
	require.Equal(t, uint32(1), funding[0].Vout)
	require.Len(t, tx.TxOut[1].Assets, 1)
	require.Equal(t, "ordx:ft:gas", tx.TxOut[1].Assets[0].Name.String())
}

func TestBuildDeployTxSplitsLargeInitCode(t *testing.T) {
	caller := mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	initCode := make([]byte, evmcommon.MaxNullDataPayloadLen+33)
	for i := range initCode {
		initCode[i] = byte(i)
	}
	tx, contract, err := BuildDeployTx(DeployTxBuildRequest{
		ContractPrefix: TestnetContractPrefix,
		Caller:         caller,
		GasLimit:       100000,
		DeployNonce:    4,
		InitCode:       initCode,
		Funding: wire.TxOut{Assets: wire.TxAssets{{
			Name:   *wire.NewAssetNameFromString("ordx:ft:gas"),
			Amount: *mustDefaultDecimal(t, 100000),
		}}},
		Inputs: []wire.OutPoint{{Hash: chainhash.Hash{4}, Index: 0}},
	})
	require.NoError(t, err)
	require.Len(t, tx.TxOut, 3)

	parsed, err := ParseTx(tx, StandardContractScriptResolver(TestnetContractPrefix))
	require.NoError(t, err)
	require.Equal(t, initCode, parsed.Deploy.Code)
	funding, err := FindContractOutputsForContract(tx, StandardContractScriptResolver(TestnetContractPrefix), contract)
	require.NoError(t, err)
	require.Len(t, funding, 1)
	require.Equal(t, uint32(2), funding[0].Vout)
}

func TestBuildInvokeTx(t *testing.T) {
	contract := testContract(t)
	tx, err := BuildInvokeTx(InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  100000,
		CallNonce: 9,
		Calldata:  []byte{0xde, 0xad, 0xbe, 0xef},
		Funding: wire.TxOut{
			Value: 77,
			Assets: wire.TxAssets{{
				Name:   *wire.NewAssetNameFromString("ordx:ft:gas"),
				Amount: *mustDefaultDecimal(t, 100000),
			}},
		},
		Inputs: []wire.OutPoint{{Hash: chainhash.Hash{2}, Index: 0}},
	})
	require.NoError(t, err)
	require.Len(t, tx.TxOut, 2)

	parsed, err := ParseTx(tx, StandardContractScriptResolver(TestnetContractPrefix))
	require.NoError(t, err)
	require.Equal(t, TxTypeInvoke, parsed.Type)
	require.Len(t, parsed.ContractOutputs, 1)
	require.True(t, contract.Equal(parsed.ContractOutputs[0].Contract))
	require.Equal(t, int64(77), parsed.ContractOutputs[0].Value)
	require.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, parsed.Invoke.Data)
}

func TestBuildEVMTxRejectsInvalidFunding(t *testing.T) {
	_, _, err := BuildDeployTx(DeployTxBuildRequest{
		Caller:      mustEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233"),
		GasLimit:    1,
		DeployNonce: 1,
		InitCode:    []byte{0x60, 0x00},
		Funding:     wire.TxOut{},
	})
	require.Error(t, err)

	_, err = BuildInvokeTx(InvokeTxBuildRequest{
		Contract:  testContract(t),
		GasLimit:  1,
		CallNonce: 1,
		Funding: wire.TxOut{Assets: wire.TxAssets{{
			Name:   *wire.NewAssetNameFromString(SatoshiAssetName),
			Amount: *scommon.NewDefaultDecimal(1),
		}}},
	})
	require.Error(t, err)
}

func TestMsgTxHex(t *testing.T) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, []byte{0x51}))
	encoded, err := MsgTxHex(tx)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)
}
