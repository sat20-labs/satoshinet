package evm

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestEVMAddressFromPublicKey(t *testing.T) {
	pubKey := testCompressedPubKey(0x02)
	addr, err := EVMAddressFromPublicKey(pubKey)
	require.NoError(t, err)
	require.Equal(t, btcutil.Hash160(pubKey), addr[:])
}

func TestExtractInputPublicKeyFromWitness(t *testing.T) {
	pubKey := testCompressedPubKey(0x03)
	txIn := wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}, nil, nil)
	txIn.Witness = wire.TxWitness{[]byte{0x30, 0x01}, pubKey}

	got, err := ExtractInputPublicKey(txIn)
	require.NoError(t, err)
	require.Equal(t, pubKey, got)
}

func TestExtractInputPublicKeyFromSignatureScript(t *testing.T) {
	pubKey := testCompressedPubKey(0x02)
	script, err := txscript.NewScriptBuilder().
		AddData([]byte{0x30, 0x01}).
		AddData(pubKey).
		Script()
	require.NoError(t, err)
	txIn := wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}, script, nil)

	got, err := ExtractInputPublicKey(txIn)
	require.NoError(t, err)
	require.Equal(t, pubKey, got)
}

func TestLastInputCallerResolverUsesLastInput(t *testing.T) {
	first := testCompressedPubKey(0x02)
	last := testCompressedPubKey(0x03)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{Witness: wire.TxWitness{[]byte{1}, first}})
	tx.AddTxIn(&wire.TxIn{Witness: wire.TxWitness{[]byte{1}, last}})

	got, err := LastInputCallerResolver(tx, ParsedTx{})
	require.NoError(t, err)
	want, err := EVMAddressFromPublicKey(last)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestExtractInputPublicKeyRejectsMissingPublicKey(t *testing.T) {
	_, err := ExtractInputPublicKey(&wire.TxIn{Witness: wire.TxWitness{[]byte{1}}})
	require.Error(t, err)
}

func testCompressedPubKey(prefix byte) []byte {
	pubKey := make([]byte, 33)
	pubKey[0] = prefix
	for i := 1; i < len(pubKey); i++ {
		pubKey[i] = byte(i)
	}
	return pubKey
}
