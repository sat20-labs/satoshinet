package contract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNullDataScriptsRoundTripSingleByteFinalChunk(t *testing.T) {
	for _, last := range []byte{0x00, 0x01, 0x10, 0x81} {
		content := make([]byte, MaxNullDataPayloadLen+1)
		for i := 0; i < len(content)-1; i++ {
			content[i] = byte(i + 17)
		}
		content[len(content)-1] = last
		scripts, err := NullDataScripts(TxTypeDeploy, content)
		require.NoError(t, err)
		require.Len(t, scripts, 2)
		var decoded []byte
		for i, script := range scripts {
			typ, part, err := ReadNullDataScript(script)
			require.NoError(t, err)
			require.Equal(t, TxTypeDeploy, typ)
			if i == 1 { require.Len(t, part, 1) }
			decoded = append(decoded, part...)
		}
		require.Equal(t, content, decoded)
	}
}
