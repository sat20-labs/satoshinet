package anchortx

import (
	"testing"

	common "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBTCLuckyAnchorRejectsUnsignedAndMalformedMarker(t *testing.T) {
	// A Lucky annotation must never use the invoice format that does not bind
	// outputs. Duplicate/malformed annotations must fail even with a valid Core
	// signature over the ordinary funding invoice.
	for _, mode := range []string{"unbound", "duplicate", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			tx, _ := reviewAnchor(t, nil, 2000)
			payload := make([]byte, 37)
			payload[0] = 1
			payload[1] = 1
			payload[5] = 1
			if mode == "truncated" {
				payload = payload[:2]
			}
			script, err := common.NullDataScript(txscript.OP_DATA_10, payload)
			require.NoError(t, err)
			tx.AddTxOut(wire.NewTxOut(0, nil, script))
			if mode == "duplicate" {
				tx.AddTxOut(wire.NewTxOut(0, nil, script))
			}
			_, err = CheckAnchorTxValid(tx, true, false)
			require.Error(t, err, "Lucky metadata must be validated and covered by the invoice signature")
		})
	}
}
