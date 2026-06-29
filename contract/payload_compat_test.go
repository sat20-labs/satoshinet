package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPayloadGoldenBytes(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want string
	}{
		{
			name: "evm deploy",
			got: EncodeDeployPayload(DeployPayload{
				Type:            ContractTypeEVM,
				SubType:         "sol",
				GasLimit:        21000,
				DeployNonce:     7,
				ContractContent: []byte{0x60, 0x00},
			}),
			want: "010203736f6c0088a40107026000",
		},
		{
			name: "evm invoke",
			got: EncodeInvokePayload(InvokePayload{
				GasLimit:  2,
				CallNonce: 3,
				Action:    "call",
				Param:     []byte{0xbb, 0xcc},
			}),
			want: "0102030463616c6c02bbcc",
		},
		{
			name: "template deploy",
			got: EncodeDeployPayload(DeployPayload{
				Type:            ContractTypeTemplate,
				GasLimit:        1,
				SubType:         "x",
				Version:         1,
				DeployNonce:     7,
				ContractContent: []byte{0xaa},
			}),
			want: "0101017801010701aa",
		},
		{
			name: "template invoke",
			got: EncodeInvokePayload(InvokePayload{
				GasLimit:  2,
				CallNonce: 3,
				Action:    "a",
				Param:     []byte{0xbb, 0xcc},
			}),
			want: "010203016102bbcc",
		},
		{
			name: "agent deploy",
			got: EncodeDeployPayload(DeployPayload{
				Type:            ContractTypeAgent,
				GasLimit:        1,
				SubType:         "p",
				Version:         1,
				DeployNonce:     7,
				ContractContent: []byte{0xaa},
			}),
			want: "0103017001010701aa",
		},
		{
			name: "agent invoke",
			got: EncodeInvokePayload(InvokePayload{
				GasLimit:  2,
				CallNonce: 3,
				Action:    "a",
				Param:     []byte{0xbb, 0xcc},
			}),
			want: "010203016102bbcc",
		},
		{
			name: "result",
			got: EncodeResultPayload(ResultPayload{
				Status:      ResultStatusSuccess,
				ResultCount: 2,
			}),
			want: "01000200",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, hex.EncodeToString(test.got))
		})
	}
}

func TestStateRootPayloadGoldenBytes(t *testing.T) {
	root := sha256.Sum256([]byte("state-root"))
	payload := EncodeStateRootPayload(StateRootPayload{StateRoot: root})
	require.Equal(t, "01"+hex.EncodeToString(root[:]), hex.EncodeToString(payload))
}

func TestCombineStateRootsGolden(t *testing.T) {
	templateRoot := sha256.Sum256([]byte("template"))
	evmRoot := sha256.Sum256([]byte("evm"))
	agentRoot := sha256.Sum256([]byte("agent"))

	combined := CombineStateRoots(templateRoot, evmRoot, agentRoot)
	manual := sha256.Sum256(append(append(templateRoot[:], evmRoot[:]...), agentRoot[:]...))
	require.Equal(t, manual, combined)
}
