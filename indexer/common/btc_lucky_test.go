package common

import (
	"encoding/binary"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBTCLuckyMetadataCanonicalEncoding(t *testing.T) {
	m := BTCLuckyMetadata{Version: 1, BlockHeight: 0x01020304, BlockHash: chainhash.Hash{1, 2, 3}}
	script, err := m.Script()
	require.NoError(t, err)
	kind, payload, err := ReadDataFromNullDataScript(script)
	require.NoError(t, err)
	require.EqualValues(t, CONTENT_TYPE_BTC_LUCKY, kind)
	require.Len(t, payload, 37)
	require.EqualValues(t, 0x01020304, binary.LittleEndian.Uint32(payload[1:5]))
	require.Equal(t, m.BlockHash[:], payload[5:])
	parsed, err := BTCLuckyMetadataFromOutputs([]*wire.TxOut{wire.NewTxOut(0, nil, script)})
	require.NoError(t, err)
	require.Equal(t, &m, parsed)
	for _, mode := range []string{"version", "height", "hash", "short", "trailing", "nonminimal", "value", "assets", "duplicate", "missing-push"} {
		t.Run(mode, func(t *testing.T) {
			data := append([]byte(nil), payload...)
			switch mode {
			case "version":
				data[0] = 2
			case "height":
				clear(data[1:5])
			case "hash":
				clear(data[5:])
			case "short":
				data = data[:36]
			}
			bad, err := NullDataScript(CONTENT_TYPE_BTC_LUCKY, data)
			require.NoError(t, err)
			if mode == "trailing" {
				bad = append(bad, txscript.OP_0)
			}
			if mode == "nonminimal" {
				bad = append(append([]byte(nil), bad[:3]...), append([]byte{txscript.OP_PUSHDATA1, 37}, data...)...)
			}
			if mode == "missing-push" {
				bad = bad[:3]
			}
			out := wire.NewTxOut(0, nil, bad)
			if mode == "value" {
				out.Value = 1
			}
			if mode == "assets" {
				out.Assets = wire.TxAssets{{}}
			}
			outputs := []*wire.TxOut{out}
			if mode == "duplicate" {
				outputs = append(outputs, out)
			}
			_, err = BTCLuckyMetadataFromOutputs(outputs)
			require.Error(t, err)
		})
	}
	ordinary, err := BTCLuckyMetadataFromOutputs([]*wire.TxOut{wire.NewTxOut(10, nil, []byte{txscript.OP_TRUE})})
	require.NoError(t, err)
	require.Nil(t, ordinary)
}
