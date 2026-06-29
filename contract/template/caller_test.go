package template

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestLastInputPreviousOutputInvokerResolverUsesPreviousOutputAddress(t *testing.T) {
	firstHash := chainhash.Hash{1}
	lastHash := chainhash.Hash{2}
	lastAddr, err := btcutil.NewAddressPubKeyHash(repeatedBytes(20, 0x33), &chaincfg.TestNetParams)
	require.NoError(t, err)
	lastScript, err := txscript.PayToAddrScript(lastAddr)
	require.NoError(t, err)

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: firstHash, Index: 0}, nil, nil))
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: lastHash, Index: 1}, nil, nil))

	resolver := LastInputPreviousOutputInvokerResolver(&chaincfg.TestNetParams,
		func(outpoint wire.OutPoint) ([]byte, bool) {
			if outpoint == (wire.OutPoint{Hash: lastHash, Index: 1}) {
				return lastScript, true
			}
			return nil, false
		})
	got, err := resolver(tx, Tx{})
	require.NoError(t, err)
	require.Equal(t, lastAddr.EncodeAddress(), got)
}

func TestLastInputPreviousOutputInvokerResolverRejectsMissingPreviousOutput(t *testing.T) {
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{Witness: wire.TxWitness{[]byte{1}, testTemplatePubKey(0x02)}})

	resolver := LastInputPreviousOutputInvokerResolver(&chaincfg.TestNetParams,
		func(wire.OutPoint) ([]byte, bool) { return nil, false })
	_, err := resolver(tx, Tx{})
	require.ErrorContains(t, err, "missing template invoker previous output address")
}

func repeatedBytes(n int, value byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = value
	}
	return out
}

func testTemplatePubKey(prefix byte) []byte {
	pubKey := make([]byte, 33)
	pubKey[0] = prefix
	for i := 1; i < len(pubKey); i++ {
		pubKey[i] = byte(i)
	}
	return pubKey
}
