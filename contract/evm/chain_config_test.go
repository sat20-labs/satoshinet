package evm

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSatoshiNetEVMRulesV1AreFixedAtParis(t *testing.T) {
	config := newSatoshiNetChainConfigV1()
	rules := config.Rules(big.NewInt(1), true, 1)
	require.Equal(t, uint32(1), EVMRulesVersion)
	require.True(t, rules.IsLondon)
	require.True(t, rules.IsMerge)
	require.False(t, rules.IsShanghai)
	require.False(t, rules.IsCancun)
}

func TestRuntimeBlockContextUsesSatoshiNetHashes(t *testing.T) {
	runtime := NewRuntime(nil)
	parent := [32]byte{1}
	older := [32]byte{2}
	ctx := runtime.blockContext(BlockContext{
		Number:      10,
		ParentHash:  parent,
		BlockHashes: map[uint64][32]byte{8: older},
	})
	require.Equal(t, parent, [32]byte(ctx.GetHash(9)))
	require.Equal(t, older, [32]byte(ctx.GetHash(8)))
	require.Zero(t, ctx.GetHash(7))
	require.NotNil(t, ctx.Random)
	require.Equal(t, parent, [32]byte(*ctx.Random))
}
