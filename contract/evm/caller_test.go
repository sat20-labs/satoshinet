package evm

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
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

func TestLastInputPreviousOutputCallerResolverUsesPreviousOutputAddress(t *testing.T) {
	firstHash := chainhash.Hash{1}
	lastHash := chainhash.Hash{2}
	lastAddr, err := btcutil.NewAddressPubKeyHash(bytesOf(20, 0x22), &chaincfg.TestNetParams)
	require.NoError(t, err)
	lastScript, err := txscript.PayToAddrScript(lastAddr)
	require.NoError(t, err)
	firstScript := []byte{txscript.OP_TRUE}

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: firstHash, Index: 0}, nil, nil))
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: lastHash, Index: 1}, nil, nil))

	resolver := LastInputPreviousOutputCallerResolver(&chaincfg.TestNetParams,
		func(outpoint wire.OutPoint) ([]byte, bool) {
			switch outpoint {
			case wire.OutPoint{Hash: firstHash, Index: 0}:
				return firstScript, true
			case wire.OutPoint{Hash: lastHash, Index: 1}:
				return lastScript, true
			default:
				return nil, false
			}
		})
	got, err := resolver(tx, ParsedTx{})
	require.NoError(t, err)
	require.Equal(t, btcutil.Hash160([]byte(lastAddr.EncodeAddress())), got[:])
}

func TestLastInputPreviousOutputCallerResolverRejectsMissingPreviousOutput(t *testing.T) {
	pubKey := testCompressedPubKey(0x02)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{Witness: wire.TxWitness{[]byte{1}, pubKey}})

	resolver := LastInputPreviousOutputCallerResolver(&chaincfg.TestNetParams,
		func(wire.OutPoint) ([]byte, bool) { return nil, false })
	_, err := resolver(tx, ParsedTx{})
	require.ErrorContains(t, err, "missing caller previous output address")
}

func TestExtractInputPublicKeyRejectsMissingPublicKey(t *testing.T) {
	_, err := ExtractInputPublicKey(&wire.TxIn{Witness: wire.TxWitness{[]byte{1}}})
	require.Error(t, err)
}

func bytesOf(n int, value byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = value
	}
	return out
}

func testCompressedPubKey(prefix byte) []byte {
	pubKey := make([]byte, 33)
	pubKey[0] = prefix
	for i := 1; i < len(pubKey); i++ {
		pubKey[i] = byte(i)
	}
	return pubKey
}
