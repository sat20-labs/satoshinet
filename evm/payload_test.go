package evm

import (
	"bytes"
	"crypto/sha256"
	"testing"

	evmcommon "github.com/sat20-labs/satoshinet/evm/common"
	"github.com/stretchr/testify/require"
)

func TestDeployPayloadRoundTrip(t *testing.T) {
	want := DeployPayload{
		GasLimit:    21000,
		DeployNonce: 7,
		InitCode:    []byte{0x60, 0x2a, 0x60, 0x00},
	}

	got, err := evmcommon.DecodeDeployPayload(evmcommon.EncodeDeployPayload(want))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestInvokePayloadRoundTrip(t *testing.T) {
	want := InvokePayload{
		GasLimit:  50000,
		CallNonce: 9,
		Calldata:  []byte{0xde, 0xad, 0xbe, 0xef},
	}

	got, err := evmcommon.DecodeInvokePayload(evmcommon.EncodeInvokePayload(want))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResultPayloadRoundTrip(t *testing.T) {
	digest := sha256.Sum256([]byte("revert reason"))
	want := ResultPayload{
		Status:       ResultStatusRevert,
		ResultCount:  3,
		ErrorDigest:  digest,
		HasErrorInfo: true,
	}

	got, err := evmcommon.DecodeResultPayload(evmcommon.EncodeResultPayload(want))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestStateRootPayloadRoundTrip(t *testing.T) {
	root := sha256.Sum256([]byte("state"))
	got, err := evmcommon.DecodeStateRootPayload(evmcommon.EncodeStateRootPayload(StateRootPayload{StateRoot: root}))
	require.NoError(t, err)
	require.Equal(t, root, got.StateRoot)
}

func TestPayloadRejectsUnsupportedVersion(t *testing.T) {
	_, err := evmcommon.DecodeInvokePayload([]byte{PayloadVersionV1 + 1})
	require.Error(t, err)
}

func TestStateRootNullDataScriptRoundTrip(t *testing.T) {
	root := sha256.Sum256([]byte("state"))
	script, err := evmcommon.NullDataScript(TxTypeCoinbaseStateRoot, evmcommon.EncodeStateRootPayload(StateRootPayload{StateRoot: root}))
	require.NoError(t, err)

	txType, content, err := evmcommon.ReadNullDataScript(script)
	require.NoError(t, err)
	require.Equal(t, TxTypeCoinbaseStateRoot, txType)
	require.True(t, bytes.HasPrefix(content, []byte{PayloadVersionV1}))
	decoded, err := evmcommon.DecodeStateRootPayload(content)
	require.NoError(t, err)
	require.Equal(t, root, decoded.StateRoot)
}
