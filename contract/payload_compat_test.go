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
				GasLimit:    21000,
				DeployNonce: 7,
				InitCode:    []byte{0x60, 0x00},
			}),
			want: "0188a401076000",
		},
		{
			name: "evm invoke",
			got: EncodeInvokePayload(InvokePayload{
				GasLimit:  2,
				CallNonce: 3,
				Calldata:  []byte{0xbb, 0xcc},
			}),
			want: "010203bbcc",
		},
		{
			name: "template deploy",
			got: mustTemplateDeployPayload(t, TemplateDeployPayload{
				GasLimit:        1,
				TemplateName:    "x",
				TemplateVersion: 1,
				Deployer:        "d",
				Random:          []byte("r"),
				ContractContent: []byte{0xaa},
			}),
			want: "01010178010164017201aa",
		},
		{
			name: "template invoke",
			got: mustTemplateInvokePayload(t, TemplateInvokePayload{
				GasLimit:  2,
				CallNonce: 3,
				Action:    "a",
				Param:     []byte{0xbb, 0xcc},
			}),
			want: "010203016102bbcc",
		},
		{
			name: "agent deploy",
			got: mustAgentDeployPayload(t, AgentDeployPayload{
				GasLimit:        1,
				Subtype:         "p",
				AgentVersion:    1,
				Deployer:        "d",
				Random:          []byte("r"),
				ContractContent: []byte{0xaa},
			}),
			want: "01010170010164017201aa",
		},
		{
			name: "agent invoke",
			got: mustAgentInvokePayload(t, AgentInvokePayload{
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

func mustTemplateDeployPayload(t *testing.T, payload TemplateDeployPayload) []byte {
	t.Helper()
	encoded, err := EncodeTemplateDeployPayload(payload)
	require.NoError(t, err)
	return encoded
}

func mustTemplateInvokePayload(t *testing.T, payload TemplateInvokePayload) []byte {
	t.Helper()
	encoded, err := EncodeTemplateInvokePayload(payload)
	require.NoError(t, err)
	return encoded
}

func mustAgentDeployPayload(t *testing.T, payload AgentDeployPayload) []byte {
	t.Helper()
	encoded, err := EncodeAgentDeployPayload(payload)
	require.NoError(t, err)
	return encoded
}

func mustAgentInvokePayload(t *testing.T, payload AgentInvokePayload) []byte {
	t.Helper()
	encoded, err := EncodeAgentInvokePayload(payload)
	require.NoError(t, err)
	return encoded
}
