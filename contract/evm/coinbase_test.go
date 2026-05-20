package evm

import (
	"crypto/sha256"
	"testing"

	evmcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestFindCoinbaseStateRoot(t *testing.T) {
	root := sha256.Sum256([]byte("state"))
	script, err := evmcommon.StateRootNullDataScript(StateRootPayload{StateRoot: root})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxOut(wire.NewTxOut(0, nil, []byte{0x51}))
	tx.AddTxOut(wire.NewTxOut(0, nil, script))

	payload, found, err := FindCoinbaseStateRoot(tx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, root, payload.StateRoot)
	require.NoError(t, VerifyCoinbaseStateRoot(tx, root))
}

func TestVerifyCoinbaseStateRootRejectsMismatch(t *testing.T) {
	root := sha256.Sum256([]byte("state"))
	other := sha256.Sum256([]byte("other"))
	script, err := evmcommon.StateRootNullDataScript(StateRootPayload{StateRoot: root})
	require.NoError(t, err)
	tx := wire.NewMsgTx(1)
	tx.AddTxOut(wire.NewTxOut(0, nil, script))

	require.Error(t, VerifyCoinbaseStateRoot(tx, other))
}

func TestUpsertCoinbaseStateRoot(t *testing.T) {
	root := sha256.Sum256([]byte("state"))
	tx := wire.NewMsgTx(1)
	tx.AddTxOut(wire.NewTxOut(0, nil, []byte{0x51}))

	require.NoError(t, UpsertCoinbaseStateRoot(tx, root))
	require.Len(t, tx.TxOut, 2)
	require.NoError(t, VerifyCoinbaseStateRoot(tx, root))

	other := sha256.Sum256([]byte("other"))
	require.NoError(t, UpsertCoinbaseStateRoot(tx, other))
	require.Len(t, tx.TxOut, 2)
	require.NoError(t, VerifyCoinbaseStateRoot(tx, other))
}

func TestUpsertCoinbaseStateRootRejectsDuplicateExistingRoots(t *testing.T) {
	root := sha256.Sum256([]byte("state"))
	script, err := evmcommon.StateRootNullDataScript(StateRootPayload{StateRoot: root})
	require.NoError(t, err)

	tx := wire.NewMsgTx(1)
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	tx.AddTxOut(wire.NewTxOut(0, nil, script))

	require.Error(t, UpsertCoinbaseStateRoot(tx, root))
}
