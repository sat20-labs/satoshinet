package evm

import (
	"crypto/sha256"
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

func TestMemoryStateDBStateRootDeterministic(t *testing.T) {
	addr := gethcommon.HexToAddress("0x1111111111111111111111111111111111111111")
	a := NewMemoryStateDB()
	b := NewMemoryStateDB()

	a.SetState(addr, gethcommon.HexToHash("0x01"), gethcommon.HexToHash("0x02"))
	a.SetCode(addr, []byte{0x60, 0x2a}, 0)
	a.AddBalance(addr, uint256.NewInt(10), 0)

	b.AddBalance(addr, uint256.NewInt(10), 0)
	b.SetCode(addr, []byte{0x60, 0x2a}, 0)
	b.SetState(addr, gethcommon.HexToHash("0x01"), gethcommon.HexToHash("0x02"))

	require.Equal(t, a.StateRoot(), b.StateRoot())
}

func TestMemoryStateDBStateRootChanges(t *testing.T) {
	addr := gethcommon.HexToAddress("0x1111111111111111111111111111111111111111")
	state := NewMemoryStateDB()
	before := state.StateRoot()
	state.AddBalance(addr, uint256.NewInt(10), 0)
	after := state.StateRoot()

	require.NotEqual(t, before, after)
}

func TestStateRootUsesVersionedCanonicalEncoding(t *testing.T) {
	state := NewMemoryStateDB()
	state.SetNonce(gethcommon.HexToAddress("0x1234"), 7, 0)
	encoded, err := state.MarshalBinary()
	require.NoError(t, err)
	want := sha256.Sum256(append([]byte("SATOSHINET:EVM_STATE_ROOT:V1\x00"), encoded...))
	require.Equal(t, want, state.StateRoot())
}
