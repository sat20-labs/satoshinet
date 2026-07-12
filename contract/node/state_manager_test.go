package node

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPersistedContractStateSizeLimit(t *testing.T) {
	for _, module := range []string{"template", "EVM", "agent"} {
		require.NoError(t, validatePersistedContractStateSize(module,
			make([]byte, MaxPersistedContractStateBytes)))
		require.ErrorContains(t, validatePersistedContractStateSize(module,
			make([]byte, MaxPersistedContractStateBytes+1)), "exceeds")
	}
}
