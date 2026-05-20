package evm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUTXOAssetViewBalance(t *testing.T) {
	contract := testContract(t)
	other := contract
	other.Hash[0] ^= 1
	view := NewUTXOAssetView([]UTXO{
		mustUTXO(t, OutPoint{TxID: "a", Vout: 0}, contract, SatoshiAssetName, 10, 0),
		mustUTXO(t, OutPoint{TxID: "b", Vout: 0}, contract, SatoshiAssetName, 20, 0),
		mustUTXO(t, OutPoint{TxID: "c", Vout: 0}, contract, "ordx:ft:gas", 30, 0),
		mustUTXO(t, OutPoint{TxID: "d", Vout: 0}, other, SatoshiAssetName, 40, 0),
	})

	balance, err := view.AssetBalance(contract.Hash, SatoshiAssetName)
	require.NoError(t, err)
	require.Equal(t, 0, balance.Cmp(mustDefaultDecimal(t, 30)))
}

func TestUTXOAssetViewRejectsEmptyAsset(t *testing.T) {
	_, err := NewUTXOAssetView(nil).AssetBalance(EVMAddress{}, "")
	require.ErrorIs(t, err, ErrInvalidAsset)
}
