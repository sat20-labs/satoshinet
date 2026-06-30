package contract

import (
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBuildUnifiedEVMDeployTx(t *testing.T) {
	caller := mustTestEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	prev := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	tx, contract, err := BuildDeployTx(DeployTxBuildRequest{
		ContractPrefix:  TestnetContractPrefix,
		Type:            ContractTypeEVM,
		Deployer:        caller.String(),
		GasLimit:        100000,
		DeployNonce:     3,
		ContractContent: []byte{0x60, 0x00},
		Funding: wire.TxOut{Assets: wire.TxAssets{{
			Name:   *wire.NewAssetNameFromString("ordx:ft:gas"),
			Amount: *indexercommon.NewDefaultDecimal(100000),
		}}},
		Inputs: []wire.OutPoint{prev},
	})
	require.NoError(t, err)
	require.Len(t, tx.TxIn, 1)
	require.Equal(t, prev, tx.TxIn[0].PreviousOutPoint)
	require.Len(t, tx.TxOut, 2)

	txType, payload, err := ReadNullDataScript(tx.TxOut[0].PkScript)
	require.NoError(t, err)
	require.Equal(t, TxTypeDeploy, txType)
	deploy, err := DecodeDeployPayload(payload)
	require.NoError(t, err)
	require.Equal(t, int64(100000), deploy.GasLimit)
	require.Equal(t, uint64(3), deploy.DeployNonce)
	require.Equal(t, []byte{0x60, 0x00}, deploy.ContractContent)

	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)
	require.Equal(t, contractScript, tx.TxOut[1].PkScript)
	require.Len(t, tx.TxOut[1].Assets, 1)
	require.Equal(t, "ordx:ft:gas", tx.TxOut[1].Assets[0].Name.String())
}

func TestBuildUnifiedEVMDeployTxSplitsLargeInitCode(t *testing.T) {
	caller := mustTestEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	initCode := make([]byte, MaxNullDataPayloadLen+33)
	for i := range initCode {
		initCode[i] = byte(i)
	}
	tx, _, err := BuildDeployTx(DeployTxBuildRequest{
		Type:            ContractTypeEVM,
		Deployer:        caller.String(),
		GasLimit:        100000,
		DeployNonce:     4,
		ContractContent: initCode,
		Funding: wire.TxOut{Assets: wire.TxAssets{{
			Name:   *wire.NewAssetNameFromString("ordx:ft:gas"),
			Amount: *indexercommon.NewDefaultDecimal(100000),
		}}},
		Inputs: []wire.OutPoint{{Hash: chainhash.Hash{4}, Index: 0}},
	})
	require.NoError(t, err)
	require.Len(t, tx.TxOut, 3)

	var encoded []byte
	for i := 0; i < 2; i++ {
		txType, part, err := ReadNullDataScript(tx.TxOut[i].PkScript)
		require.NoError(t, err)
		require.Equal(t, TxTypeDeploy, txType)
		encoded = append(encoded, part...)
	}
	deploy, err := DecodeDeployPayload(encoded)
	require.NoError(t, err)
	require.Equal(t, initCode, deploy.ContractContent)
}

func TestBuildUnifiedEVMInvokeTx(t *testing.T) {
	contract, err := NewContractAddress(TestnetContractPrefix, AddressVersionV1, ContractTypeEVM, EVMAddress{1, 2, 3})
	require.NoError(t, err)
	tx, err := BuildInvokeTx(InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  100000,
		CallNonce: 9,
		Action:    "call",
		Param:     []byte{0xde, 0xad, 0xbe, 0xef},
		Funding: wire.TxOut{
			Value: 77,
			Assets: wire.TxAssets{{
				Name:   *wire.NewAssetNameFromString("ordx:ft:gas"),
				Amount: *indexercommon.NewDefaultDecimal(100000),
			}},
		},
		Inputs: []wire.OutPoint{{Hash: chainhash.Hash{2}, Index: 0}},
	})
	require.NoError(t, err)
	require.Len(t, tx.TxOut, 2)

	txType, payload, err := ReadNullDataScript(tx.TxOut[0].PkScript)
	require.NoError(t, err)
	require.Equal(t, TxTypeInvoke, txType)
	invoke, err := DecodeInvokePayload(payload)
	require.NoError(t, err)
	require.Equal(t, int64(100000), invoke.GasLimit)
	require.Equal(t, uint64(9), invoke.CallNonce)
	require.Equal(t, "call", invoke.Action)
	require.Equal(t, []byte{0xde, 0xad, 0xbe, 0xef}, invoke.Param)

	contractScript, err := ContractPkScript(contract)
	require.NoError(t, err)
	require.Equal(t, contractScript, tx.TxOut[1].PkScript)
	require.Equal(t, int64(77), tx.TxOut[1].Value)
}

func TestBuildUnifiedEVMInvokeTxDefaultsCallAction(t *testing.T) {
	contract, err := NewContractAddress(TestnetContractPrefix, AddressVersionV1, ContractTypeEVM, EVMAddress{1, 2, 3})
	require.NoError(t, err)
	tx, err := BuildInvokeTx(InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  100000,
		CallNonce: 9,
		Funding: wire.TxOut{
			Assets: wire.TxAssets{{
				Name:   *wire.NewAssetNameFromString("ordx:ft:gas"),
				Amount: *indexercommon.NewDefaultDecimal(100000),
			}},
		},
		Inputs: []wire.OutPoint{{Hash: chainhash.Hash{2}, Index: 0}},
	})
	require.NoError(t, err)
	txType, payload, err := ReadNullDataScript(tx.TxOut[0].PkScript)
	require.NoError(t, err)
	require.Equal(t, TxTypeInvoke, txType)
	invoke, err := DecodeInvokePayload(payload)
	require.NoError(t, err)
	require.Equal(t, ContractInvokeAPICall, invoke.Action)
}

func TestBuildEVMTxRejectsInvalidFunding(t *testing.T) {
	caller := mustTestEVMAddress(t, "0x11112233445566778899aabbccddeeff00112233")
	_, _, err := BuildDeployTx(DeployTxBuildRequest{
		Type:            ContractTypeEVM,
		Deployer:        caller.String(),
		GasLimit:        1,
		DeployNonce:     1,
		ContractContent: []byte{0x60, 0x00},
		Funding:         wire.TxOut{},
	})
	require.Error(t, err)

	contract, err := NewContractAddress(TestnetContractPrefix, AddressVersionV1, ContractTypeEVM, EVMAddress{1})
	require.NoError(t, err)
	_, err = BuildInvokeTx(InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  1,
		CallNonce: 1,
		Funding: wire.TxOut{Assets: wire.TxAssets{{
			Name:   *wire.NewAssetNameFromString(SatoshiAssetName),
			Amount: *indexercommon.NewDefaultDecimal(1),
		}}},
	})
	require.Error(t, err)
}

func mustTestEVMAddress(t *testing.T, s string) EVMAddress {
	t.Helper()
	addr, err := ParseEVMAddressHex(s)
	require.NoError(t, err)
	return addr
}
