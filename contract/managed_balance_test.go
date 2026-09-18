package contract

import (
	"encoding/json"
	"math"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func managedTestAsset(name string, amount int64, binding uint32) wire.AssetInfo {
	return wire.AssetInfo{
		Name: *wire.NewAssetNameFromString(name),
		Amount: *scommon.NewDefaultDecimal(amount), BindingSat: binding,
	}
}

func TestManagedBalanceQuantityAccountingAtomic(t *testing.T) {
	asset := managedTestAsset("ordx:f:quantity", 100, 0)
	balance := ManagedBalance{}
	require.NoError(t, balance.Credit(80, wire.TxAssets{asset}))
	before := balance.Clone()
	require.Error(t, balance.Debit(81, nil))
	require.Equal(t, before, balance)
	require.Error(t, balance.Debit(10, wire.TxAssets{managedTestAsset("ordx:f:quantity", 101, 0)}))
	require.Equal(t, before, balance, "a token deficit must not debit sats first")
	require.Error(t, balance.Credit(-1, nil))
	require.Equal(t, before, balance)
	require.NoError(t, balance.Debit(30, wire.TxAssets{managedTestAsset("ordx:f:quantity", 40, 0)}))
	require.Equal(t, int64(50), balance.Value)
	amount, err := balance.AssetAmount("ordx:f:quantity")
	require.NoError(t, err)
	require.Equal(t, "60", amount.String())
	asset.Amount.Value.SetInt64(999)
	require.Equal(t, "60", balance.Assets[0].Amount.String(), "caller-owned amounts must not alias ledger state")
	cloned := balance.Clone()
	cloned.Assets[0].Amount.Value.SetInt64(7)
	require.Equal(t, "60", balance.Assets[0].Amount.String())
}

func TestManagedBalanceCreditMergesSameAssetAcrossCredits(t *testing.T) {
	name := "brc20:f:sgas"
	balance := ManagedBalance{}
	require.NoError(t, balance.Credit(0, wire.TxAssets{managedTestAsset(name, 5, 0)}))
	require.NoError(t, balance.Credit(0, wire.TxAssets{managedTestAsset(name, 7, 0)}))
	require.Len(t, balance.Assets, 1)
	amount, err := balance.AssetAmount(name)
	require.NoError(t, err)
	require.Equal(t, "12", amount.String())
	require.NoError(t, balance.Validate())
}

func TestManagedBalanceRejectsDuplicateIncomingAssets(t *testing.T) {
	name := "brc20:f:sgas"
	balance := ManagedBalance{}
	require.ErrorContains(t, balance.Credit(0, wire.TxAssets{
		managedTestAsset(name, 2, 0), managedTestAsset(name, 3, 0),
	}), "duplicate")
	require.Empty(t, balance.Assets)
}

func TestManagedBalanceOverflowAndBindings(t *testing.T) {
	balance := ManagedBalance{Value: math.MaxInt64}
	require.Error(t, balance.Credit(1, nil))
	require.Equal(t, int64(math.MaxInt64), balance.Value)

	balance = ManagedBalance{Value: 20, Assets: wire.TxAssets{managedTestAsset("ordx:f:bound", 101, 10)}}
	plain, err := balance.AssetAmount(SatoshiAssetName)
	require.NoError(t, err)
	require.Equal(t, "10", plain.String(), "reserve only ten complete binding sats; partial binding stays unbound on L2")
	before := balance.Clone()
	require.ErrorContains(t, balance.Credit(1, wire.TxAssets{managedTestAsset("ordx:f:bound", 1, 0)}), "binding")
	require.ErrorContains(t, balance.Debit(1, wire.TxAssets{managedTestAsset("ordx:f:bound", 1, 0)}), "binding")
	require.Equal(t, before, balance)
}

func TestManagedBalanceEncodingCanonicalAndIndependentOfUTXOs(t *testing.T) {
	a := ManagedBalance{Value: 9, Assets: wire.TxAssets{
		managedTestAsset("brc20:f:z", 20, 0), managedTestAsset("brc20:f:a", 10, 0),
	}}
	b := ManagedBalance{}
	require.NoError(t, b.Credit(4, wire.TxAssets{managedTestAsset("brc20:f:a", 4, 0)}))
	require.NoError(t, b.Credit(5, wire.TxAssets{
		managedTestAsset("brc20:f:z", 20, 0), managedTestAsset("brc20:f:a", 6, 0),
	}))
	encodedA, err := json.Marshal(a)
	require.NoError(t, err)
	encodedB, err := json.Marshal(b)
	require.NoError(t, err)
	require.JSONEq(t, string(encodedA), string(encodedB))
	require.Equal(t, encodedA, encodedB)
	require.NotContains(t, string(encodedA), "outpoint")
	require.NotContains(t, string(encodedA), "txid")
	var decoded ManagedBalance
	require.NoError(t, json.Unmarshal(encodedA, &decoded))
	require.Equal(t, b, decoded)
	before := decoded.Clone()
	require.Error(t, json.Unmarshal([]byte(`{"value":-1}`), &decoded))
	require.Equal(t, before, decoded)
}

func TestContractLifecyclePolicyMatrix(t *testing.T) {
	for _, flags := range []ContractFlags{0, ContractFlagNonClosable} {
		life := ContractLifecycle{Deployer: "owner", Flags: flags}
		require.NoError(t, life.CheckInvoke("call", "user"))
		if flags.Closable() {
			require.NoError(t, life.CheckInvoke(ContractInvokeAPIClose, "owner"))
			require.ErrorIs(t, life.CheckInvoke(ContractInvokeAPIClose, "user"), ErrNotContractDeployer)
		} else {
			require.ErrorIs(t, life.CheckInvoke(ContractInvokeAPIClose, "owner"), ErrContractNonClosable)
		}
		life.Closed = true
		require.ErrorIs(t, life.CheckInvoke("call", "owner"), ErrContractClosed)
		require.ErrorIs(t, life.CheckInvoke(ContractInvokeAPIClose, "owner"), ErrContractClosed)
	}
	require.Error(t, ContractFlags(2).Validate())
	require.False(t, ContractFlags(2).Closable())
	require.Equal(t, int64(7000), DeployerProfitBPS)
	require.Equal(t, int64(3000), BootstrapProfitBPS)
	require.Equal(t, TotalProfitBPS, DeployerProfitBPS+BootstrapProfitBPS)
}
