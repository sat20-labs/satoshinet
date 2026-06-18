package framework

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestContractUTXOOverlayAddsOutputsAndRemovesSpentInputs(t *testing.T) {
	addr := testContractAddress(t, ModuleTemplate, 1)
	baseHash := chainhash.Hash{9}
	baseOut := OutPoint{TxID: baseHash.String(), Vout: 0}
	overlay := NewContractUTXOOverlay(ContractUTXOOverlayConfig{
		Prefix:       contract.TestnetContractPrefix,
		ContractType: byte(ModuleTemplate),
		Base: func(got contract.ContractAddress) ([]UTXO, error) {
			require.True(t, addr.Equal(got))
			return []UTXO{{
				OutPoint: baseOut,
				Contract: addr,
				Value:    50,
			}}, nil
		},
	})

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: baseHash, Index: 0}, nil, nil))
	tx.AddTxOut(testContractTxOut(t, addr))

	require.NoError(t, overlay.ApplyTx(tx, 100))

	utxos, err := overlay.Provider(addr)
	require.NoError(t, err)
	require.Len(t, utxos, 1)
	require.Equal(t, tx.TxID(), utxos[0].OutPoint.TxID)
	require.Equal(t, uint32(0), utxos[0].OutPoint.Vout)
	require.Equal(t, int64(100), utxos[0].Height)
}
