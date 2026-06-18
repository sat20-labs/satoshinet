package template

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeployPayloadRoundTrip(t *testing.T) {
	want := DeployPayload{
		Type:            ContractTypeTemplate,
		GasLimit:        1000,
		SubType:         TemplateLimitOrder,
		Version:         1,
		DeployNonce:     7,
		ContractContent: []byte{0x01, 0x02, 0x03},
	}
	encoded, err := EncodeDeployPayload(want)
	require.NoError(t, err)
	got, err := DecodeDeployPayload(encoded)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestInvokePayloadRoundTrip(t *testing.T) {
	want := InvokePayload{
		GasLimit:  2000,
		CallNonce: 7,
		Action:    InvokeAPISwap,
		Param:     []byte{0xaa, 0xbb},
	}
	encoded, err := EncodeInvokePayload(want)
	require.NoError(t, err)
	got, err := DecodeInvokePayload(encoded)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestDeployPayloadRejectsMissingRequiredFields(t *testing.T) {
	_, err := EncodeDeployPayload(DeployPayload{})
	require.Error(t, err)
}
