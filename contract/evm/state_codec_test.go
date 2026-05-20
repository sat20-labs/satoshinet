package evm

import (
	"testing"

	gethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"
)

func TestMemoryStateDBMarshalBinaryRoundTrip(t *testing.T) {
	state := NewMemoryStateDB()
	addr := gethcommon.HexToAddress("0x11112233445566778899aabbccddeeff00112233")
	slot := gethcommon.HexToHash("0x01")
	value := gethcommon.HexToHash("0x02")

	state.SetNonce(addr, 7, 0)
	state.AddBalance(addr, uint256.NewInt(123), 0)
	state.SetCode(addr, []byte{0x60, 0x2a, 0x00}, 0)
	state.SetState(addr, slot, value)
	require.NoError(t, state.RegisterTrigger(Trigger{
		ID:       "vault-release",
		Contract: testContract(t),
		Kind:     TriggerAtHeight,
		Height:   100,
		GasLimit: 50000,
		Calldata: []byte{0x01, 0x02},
	}))

	encoded, err := state.MarshalBinary()
	require.NoError(t, err)

	decoded, err := DecodeMemoryStateDB(encoded)
	require.NoError(t, err)
	require.Equal(t, state.StateRoot(), decoded.StateRoot())
	require.Equal(t, uint64(7), decoded.GetNonce(addr))
	require.Equal(t, uint256.NewInt(123), decoded.GetBalance(addr))
	require.Equal(t, []byte{0x60, 0x2a, 0x00}, decoded.GetCode(addr))
	require.Equal(t, value, decoded.GetState(addr, slot))
	require.Equal(t, state.Triggers(), decoded.Triggers())
}

func TestMemoryStateDBMarshalBinaryDeterministic(t *testing.T) {
	a := NewMemoryStateDB()
	b := NewMemoryStateDB()
	addr1 := gethcommon.HexToAddress("0x11112233445566778899aabbccddeeff00112233")
	addr2 := gethcommon.HexToAddress("0xffff2233445566778899aabbccddeeff00112233")
	slot1 := gethcommon.HexToHash("0x01")
	slot2 := gethcommon.HexToHash("0x02")

	a.SetState(addr2, slot2, gethcommon.HexToHash("0x22"))
	a.SetState(addr1, slot1, gethcommon.HexToHash("0x11"))
	require.NoError(t, a.RegisterTrigger(Trigger{ID: "b", Contract: testContract(t), Kind: TriggerAtHeight, Height: 2, GasLimit: 10}))
	require.NoError(t, a.RegisterTrigger(Trigger{ID: "a", Contract: testContract(t), Kind: TriggerAtHeight, Height: 1, GasLimit: 10}))
	b.SetState(addr1, slot1, gethcommon.HexToHash("0x11"))
	b.SetState(addr2, slot2, gethcommon.HexToHash("0x22"))
	require.NoError(t, b.RegisterTrigger(Trigger{ID: "a", Contract: testContract(t), Kind: TriggerAtHeight, Height: 1, GasLimit: 10}))
	require.NoError(t, b.RegisterTrigger(Trigger{ID: "b", Contract: testContract(t), Kind: TriggerAtHeight, Height: 2, GasLimit: 10}))

	encodedA, err := a.MarshalBinary()
	require.NoError(t, err)
	encodedB, err := b.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, encodedA, encodedB)
}

func TestMemoryStateDBUnmarshalRejectsMalformedState(t *testing.T) {
	_, err := DecodeMemoryStateDB([]byte("bad"))
	require.Error(t, err)

	state := NewMemoryStateDB()
	encoded, err := state.MarshalBinary()
	require.NoError(t, err)
	encoded = append(encoded, 0x00)
	_, err = DecodeMemoryStateDB(encoded)
	require.Error(t, err)
}

func TestMemoryStateDBCloneIsIndependent(t *testing.T) {
	state := NewMemoryStateDB()
	addr := gethcommon.HexToAddress("0x11112233445566778899aabbccddeeff00112233")
	slot := gethcommon.HexToHash("0x01")
	state.SetState(addr, slot, gethcommon.HexToHash("0x11"))
	require.NoError(t, state.RegisterTrigger(Trigger{ID: "vault-release", Contract: testContract(t), Kind: TriggerAtHeight, Height: 100, GasLimit: 10}))

	cloned := state.Clone()
	cloned.SetState(addr, slot, gethcommon.HexToHash("0x22"))
	cloned.RemoveTrigger(testContract(t), "vault-release")

	require.Equal(t, gethcommon.HexToHash("0x11"), state.GetState(addr, slot))
	require.Equal(t, gethcommon.HexToHash("0x22"), cloned.GetState(addr, slot))
	require.Len(t, state.Triggers(), 1)
	require.Empty(t, cloned.Triggers())
}
