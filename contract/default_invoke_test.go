package contract

import (
	"testing"

	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func defaultOutputFixture(t *testing.T, typ byte) *wire.TxOut {
	t.Helper()
	hash := make([]byte, 20)
	hash[0] = typ
	addr, err := NewContractAddressFromHash(TestnetContractPrefix, AddressVersionV1, typ, hash)
	require.NoError(t, err)
	script, err := ContractPkScript(addr)
	require.NoError(t, err)
	return wire.NewTxOut(7, nil, script)
}

func TestDefaultInvokeOutputOrderAndModuleFilter(t *testing.T) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(defaultOutputFixture(t, ContractTypeEVM))
	tx.AddTxOut(defaultOutputFixture(t, ContractTypeTemplate))
	tx.AddTxOut(defaultOutputFixture(t, ContractTypeEVM))
	tx.AddTxOut(wire.NewTxOut(3, nil, []byte{txscript.OP_TRUE}))
	all, err := FindDefaultInvokeOutputs(tx, TestnetContractPrefix, 0)
	require.NoError(t, err)
	require.Len(t, all, 3)
	for i, output := range all {
		require.Equal(t, uint32(i), output.Vout)
		require.Equal(t, tx.TxID(), output.TxID)
	}
	evm, err := FindDefaultInvokeOutputs(tx, TestnetContractPrefix, ContractTypeEVM)
	require.NoError(t, err)
	require.Len(t, evm, 2)
	require.Equal(t, uint32(0), evm[0].Vout)
	require.Equal(t, uint32(2), evm[1].Vout)
	all[0].PkScript[0] ^= 1
	require.NotEqual(t, all[0].PkScript, tx.TxOut[0].PkScript)
}

func TestDefaultInvokeAllowsOrdinaryMemo(t *testing.T) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{})
	memo, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddData([]byte("memo")).Script()
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, memo))
	tx.AddTxOut(defaultOutputFixture(t, ContractTypeTemplate))
	outputs, err := FindDefaultInvokeOutputs(tx, TestnetContractPrefix, 0)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, uint32(1), outputs[0].Vout)
}

func TestDefaultInvokeExcludesEveryExplicitContractEnvelope(t *testing.T) {
	for _, typ := range []TxType{TxTypeDeploy, TxTypeInvoke, TxTypeResult, TxTypeCoinbaseStateRoot} {
		t.Run(string(rune('A'+typ)), func(t *testing.T) {
			tx := wire.NewMsgTx(2)
			tx.AddTxIn(&wire.TxIn{})
			tx.AddTxOut(defaultOutputFixture(t, ContractTypeTemplate))
			script, err := NullDataScript(typ, []byte{0x42})
			require.NoError(t, err)
			tx.AddTxOut(wire.NewTxOut(0, nil, script))
			outputs, err := FindDefaultInvokeOutputs(tx, TestnetContractPrefix, 0)
			require.NoError(t, err)
			require.Empty(t, outputs)
		})
	}
}

func TestMalformedContractEnvelopeCannotBecomeDefaultInvoke(t *testing.T) {
	for _, typ := range []byte{ContentTypeContractDeploy, ContentTypeContractInvoke, ContentTypeContractResult, ContentTypeContractStateRoot} {
		tx := wire.NewMsgTx(2)
		tx.AddTxIn(&wire.TxIn{})
		tx.AddTxOut(defaultOutputFixture(t, ContractTypeEVM))
		script, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddOp(sat20MagicNumber).AddInt64(int64(typ)).Script()
		require.NoError(t, err)
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
		_, _, err = ClassifyTxPayloadType(tx)
		require.ErrorContains(t, err, "malformed contract payload")
		outputs, err := FindDefaultInvokeOutputs(tx, TestnetContractPrefix, 0)
		require.Error(t, err)
		require.Empty(t, outputs)
	}
}

func TestDefaultInvokeRejectsNilInputsBeforeHashing(t *testing.T) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(nil)
	tx.AddTxOut(defaultOutputFixture(t, ContractTypeEVM))
	require.NotPanics(t, func() {
		_, err := FindDefaultInvokeOutputs(tx, TestnetContractPrefix, 0)
		require.ErrorContains(t, err, "nil input")
	})
}
