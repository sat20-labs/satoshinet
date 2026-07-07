package template

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

func TestRuntimeBaseURLAndStateRoot(t *testing.T) {
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateAMM,
		Version:         1,
		DeployNonce:     7,
		ContractContent: []byte{0x01, 0x02, 0x03},
	}
	addr, _, err := DeriveContractAddress(
		contractcommon.TestnetContractPrefix,
		deploy.ContractContent,
		"deployer-address",
		deploy.DeployNonce,
	)
	require.NoError(t, err)

	runtime, err := NewRuntimeBase(addr, deploy, "deployer-address")
	require.NoError(t, err)
	require.Equal(t, addr.EncodeAddress(), runtime.URL())
	require.Equal(t, TemplateAMM, runtime.TemplateName())
	require.Equal(t, uint32(1), runtime.Version())
	require.Equal(t, "deployer-address", runtime.Deployer())

	before := runtime.StateRoot()
	runtime.SetCurrentBlock(100)
	runtime.IncrementInvokeCount()
	runtime.SetState("pool", []byte("state"))
	after := runtime.StateRoot()
	require.NotEqual(t, before, after)

	got, ok := runtime.GetState("pool")
	require.True(t, ok)
	require.Equal(t, []byte("state"), got)
	got[0] = 'S'
	got2, ok := runtime.GetState("pool")
	require.True(t, ok)
	require.Equal(t, []byte("state"), got2)
}

func TestRuntimeBaseStateRootDeterministic(t *testing.T) {
	deploy := DeployPayload{
		GasLimit:        1000,
		SubType:         TemplateLimitOrder,
		Version:         1,
		DeployNonce:     7,
		ContractContent: []byte{0x01, 0x02, 0x03},
	}
	addr, _, err := DeriveContractAddress(
		contractcommon.TestnetContractPrefix,
		deploy.ContractContent,
		"deployer-address",
		deploy.DeployNonce,
	)
	require.NoError(t, err)
	a, err := NewRuntimeBase(addr, deploy, "deployer-address")
	require.NoError(t, err)
	b, err := NewRuntimeBase(addr, deploy, "deployer-address")
	require.NoError(t, err)

	a.SetState("b", []byte("2"))
	a.SetState("a", []byte("1"))
	b.SetState("a", []byte("1"))
	b.SetState("b", []byte("2"))
	require.Equal(t, a.StateRoot(), b.StateRoot())
}

func TestTemplateStateRootIgnoresFinishedItems(t *testing.T) {
	runtime := testLimitOrderRuntime(t)
	addr := runtime.Address()
	applyLimitOrderInvokeForTest(t, runtime, addr, "buy", "buyer", OrderTypeBuy, "10", "2", 20, nil, 1)
	applyLimitOrderInvokeForTest(t, runtime, addr, "sell", "seller", OrderTypeSell, "10", "2", 0,
		testAsset("ordx:f:test", 10), 1)

	_, err := runtime.SettleBlock(2)
	require.NoError(t, err)
	rootWithFinishedItems := runtime.StateRoot()

	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.Len(t, state.Items, 2)
	state.Items = unfinishedItems(state.Items)
	require.Empty(t, state.Items)
	require.NoError(t, runtime.saveRuntimeState(state))

	require.Equal(t, rootWithFinishedItems, runtime.StateRoot())
}
