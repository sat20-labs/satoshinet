package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestValidateInvokeTxBasic(t *testing.T) {
	contract := testContract(t)
	gasAssetName := DefaultGasConfig().GasAssetName
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	invokeScript, err := evmcommon.InvokeNullDataScript(InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas, CallNonce: 1})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(7, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString(gasAssetName),
		Amount: *scommon.NewDefaultDecimal(5),
	}}, testContractScript(contract)))
	tx.AddTxOut(wire.NewTxOut(11, nil, testContractScript(contract)))

	validated, err := ValidateInvokeTxBasic(tx, testContractResolver, func(addr ContractAddress) bool {
		return addr.Equal(contract)
	}, DefaultGasConfig())
	require.NoError(t, err)
	require.Equal(t, int64(18), validated.MsgValue)
	require.Equal(t, DefaultGasConfig().InvokeBaseGas, validated.Payload.GasLimit)
	require.True(t, contract.Equal(validated.Contract))
	require.Len(t, validated.FundingOutputs, 2)
	gasAmount, err := validated.FundingOutputs[0].AssetAmount(gasAssetName)
	require.NoError(t, err)
	require.Equal(t, 0, gasAmount.Cmp(mustDefaultDecimal(t, 5)))
}

func TestValidateInvokeTxBasicRejectsMissingContract(t *testing.T) {
	contract := testContract(t)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	invokeScript, err := evmcommon.InvokeNullDataScript(InvokePayload{GasLimit: DefaultGasConfig().InvokeBaseGas})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(7, nil, testContractScript(contract)))

	_, err = ValidateInvokeTxBasic(tx, testContractResolver, func(ContractAddress) bool { return false }, GasConfig{})
	require.Error(t, err)
}

func TestValidateDeployTxBasic(t *testing.T) {
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	script, err := evmcommon.DeployNullDataScript(DeployPayload{GasLimit: DefaultGasConfig().DeployBaseGas, DeployNonce: 1, InitCode: []byte{0x60, 0x00}})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, script))

	validated, err := ValidateDeployTxBasic(tx, DefaultGasConfig())
	require.NoError(t, err)
	require.Equal(t, DefaultGasConfig().DeployBaseGas, validated.Payload.GasLimit)
}

func TestValidateResultTxBasic(t *testing.T) {
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	script, err := evmcommon.ResultNullDataScript(ResultPayload{Status: ResultStatusSuccess, ResultCount: 1})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, script))

	validated, err := ValidateResultTxBasic(tx)
	require.NoError(t, err)
	require.Equal(t, uint16(1), validated.Payload.ResultCount)
	require.Len(t, validated.Inputs, 1)
}
