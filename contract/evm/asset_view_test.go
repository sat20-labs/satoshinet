package evm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUTXOAssetViewBalance(t *testing.T) {
	contract := testContract(t)
	otherHash := ContractAddressHash(contract)
	otherHash[0] ^= 1
	other := testContractWithHash(t, otherHash)
	view := NewUTXOAssetView([]UTXO{
		mustUTXO(t, OutPoint{TxID: "a", Vout: 0}, contract, SatoshiAssetName, 10, 0),
		mustUTXO(t, OutPoint{TxID: "b", Vout: 0}, contract, SatoshiAssetName, 20, 0),
		mustUTXO(t, OutPoint{TxID: "c", Vout: 0}, contract, "ordx:ft:gas", 30, 0),
		mustUTXO(t, OutPoint{TxID: "d", Vout: 0}, other, SatoshiAssetName, 40, 0),
	})

	balance, err := view.AssetBalance(ContractAddressHash(contract), SatoshiAssetName)
	require.NoError(t, err)
	require.Equal(t, 0, balance.Cmp(mustDefaultDecimal(t, 30)))
}

func TestUTXOAssetViewRejectsEmptyAsset(t *testing.T) {
	_, err := NewUTXOAssetView(nil).AssetBalance(EVMAddress{}, "")
	require.ErrorIs(t, err, ErrInvalidAsset)
}

func TestContractUTXOAssetViewBalanceUsesProvider(t *testing.T) {
	contract := testContract(t)
	view := NewContractUTXOAssetView(TestnetContractPrefix, func(got ContractAddress) ([]UTXO, error) {
		require.True(t, contract.Equal(got))
		return []UTXO{
			mustUTXO(t, OutPoint{TxID: "a", Vout: 0}, contract, SatoshiAssetName, 25, 0),
			mustUTXO(t, OutPoint{TxID: "b", Vout: 0}, contract, SatoshiAssetName, 17, 0),
		}, nil
	})

	balance, err := view.AssetBalance(ContractAddressHash(contract), SatoshiAssetName)
	require.NoError(t, err)
	require.Equal(t, 0, balance.Cmp(mustDefaultDecimal(t, 42)))
}
