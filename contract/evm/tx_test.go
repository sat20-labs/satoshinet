package evm

import (
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	evmcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestParseInvokeTxFindsContractOutputs(t *testing.T) {
	contract := testContract(t)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	invokeScript, err := evmcommon.InvokeNullDataScript(InvokePayload{GasLimit: 1000, CallNonce: 9, Param: []byte{1, 2, 3}})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(10, wire.TxAssets{{
		Name:   *wire.NewAssetNameFromString("ordx:ft:gas"),
		Amount: *scommon.NewDefaultDecimal(20),
	}}, testContractScript(contract)))

	parsed, err := ParseTx(tx, testContractResolver)
	require.NoError(t, err)
	require.Equal(t, TxTypeInvoke, parsed.Type)
	require.NotNil(t, parsed.Invoke)
	require.Equal(t, int64(1000), parsed.Invoke.GasLimit)
	require.Len(t, parsed.ContractOutputs, 1)
	require.True(t, contract.Equal(parsed.ContractOutputs[0].Contract))
	require.Equal(t, uint32(1), parsed.ContractOutputs[0].Vout)
	require.Equal(t, int64(10), parsed.ContractOutputs[0].PhysicalValue())
	amount, err := parsed.ContractOutputs[0].AssetAmount("ordx:ft:gas")
	require.NoError(t, err)
	require.Equal(t, 0, amount.Cmp(mustDefaultDecimal(t, 20)))
}

func TestParseDeployTxCombinesMultipleOPReturns(t *testing.T) {
	initCode := make([]byte, evmcommon.MaxNullDataPayloadLen+11)
	for i := range initCode {
		initCode[i] = byte(255 - i)
	}
	scripts, err := evmcommon.DeployNullDataScripts(DeployPayload{
		GasLimit:        5000,
		DeployNonce:     3,
		ContractContent: initCode,
	})
	require.NoError(t, err)
	require.Len(t, scripts, 2)

	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	for _, script := range scripts {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}

	parsed, err := ParseTx(tx, testContractResolver)
	require.NoError(t, err)
	require.Equal(t, TxTypeDeploy, parsed.Type)
	require.NotNil(t, parsed.Deploy)
	require.Equal(t, int64(5000), parsed.Deploy.GasLimit)
	require.Equal(t, uint64(3), parsed.Deploy.DeployNonce)
	require.Equal(t, initCode, parsed.Deploy.ContractContent)
}

func TestParseInvokeTxRejectsMultipleContracts(t *testing.T) {
	contract := testContract(t)
	otherHash := ContractAddressHash(contract)
	otherHash[0] ^= 1
	other := testContractWithHash(t, otherHash)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	invokeScript, err := evmcommon.InvokeNullDataScript(InvokePayload{GasLimit: 1000})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(10, nil, testContractScript(contract)))
	tx.AddTxOut(wire.NewTxOut(10, nil, testContractScript(other)))

	_, err = ParseTx(tx, testContractResolver)
	require.Error(t, err)
}

func TestParseTxRejectsMixedEVMOPReturns(t *testing.T) {
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	deployScript, err := evmcommon.DeployNullDataScript(DeployPayload{GasLimit: 1000, DeployNonce: 1, ContractContent: []byte{1}})
	require.NoError(t, err)
	invokeScript, err := evmcommon.InvokeNullDataScript(InvokePayload{GasLimit: 1000, CallNonce: 1, Param: []byte{2}})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, deployScript))
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))

	_, err = ParseTx(tx, testContractResolver)
	require.Error(t, err)
}

func TestParseResultRequiresOPReturnLast(t *testing.T) {
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	resultScript, err := evmcommon.ResultNullDataScript(ResultPayload{Status: ResultStatusSuccess, ResultCount: 1})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))
	tx.AddTxOut(wire.NewTxOut(10, nil, []byte{0x51}))

	_, err = ParseTx(tx, testContractResolver)
	require.Error(t, err)
}

func TestParseResultRejectsMultipleOPReturns(t *testing.T) {
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	resultScript, err := evmcommon.ResultNullDataScript(ResultPayload{Status: ResultStatusSuccess, ResultCount: 1})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))

	_, err = ParseTx(tx, testContractResolver)
	require.Error(t, err)
}

func TestInvokeCallBindings(t *testing.T) {
	contract := testContract(t)
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(&wire.TxIn{})
	invokeScript, err := evmcommon.InvokeNullDataScript(InvokePayload{GasLimit: 1000})
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, invokeScript))
	tx.AddTxOut(wire.NewTxOut(10, nil, testContractScript(contract)))

	bindings, err := InvokeCallBindings(tx, testContractResolver)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	require.Equal(t, tx.TxID(), bindings[0].InvokeTxID)
	require.Equal(t, uint32(1), bindings[0].FundingInput.Vout)
	require.NotEmpty(t, bindings[0].CallID)
}

func testContractScript(contract ContractAddress) []byte {
	script, err := ContractPkScript(contract)
	if err != nil {
		panic(err)
	}
	return script
}

func testContractResolver(pkScript []byte) (ContractAddress, bool, error) {
	return ParseContractPkScript(pkScript, TestnetContractPrefix)
}
