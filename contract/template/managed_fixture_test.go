package template

import (
	"strings"
	"testing"

	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

// creditTestManagedOutput mirrors framework.Executor.acceptOutcome for tests
// that exercise a runtime directly instead of executing a complete work tx.
func creditTestManagedOutput(t *testing.T, runtime *ContractRuntime, output ContractOutput) {
	t.Helper()
	require.NotNil(t, runtime)
	require.NotNil(t, runtime.base)
	require.NoError(t, runtime.base.managed.Credit(output.PhysicalValue(), output.TxAssets()))
}

// managedPhysicalProvider gives direct settlement tests a physical UTXO view
// matching the accepted quantity ledger. Production obtains this view from the
// chain/indexer; unit tests that bypass block execution must provide it too.
func managedPhysicalProvider(t *testing.T, runtime *ContractRuntime) ContractUTXOProvider {
	t.Helper()
	require.NotNil(t, runtime)
	balance := runtime.base.managed.Clone()
	addr := runtime.Address()
	return func(got ContractAddress) ([]contractframework.UTXO, error) {
		require.True(t, addr.Equal(got))
		if balance.IsZero() {
			return nil, nil
		}
		return []contractframework.UTXO{
			testContractUTXO(strings.Repeat("e", 64), 0, addr, balance.Value, balance.Assets),
		}, nil
	}
}
