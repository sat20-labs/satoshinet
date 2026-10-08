package base

import (
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDescendingRequiresOrdinaryInputSubject(t *testing.T) {
	tx := &common.Transaction{Inputs: []*common.Input{{}}, Outputs: []*common.Output{{}}}
	require.NotPanics(t, func() {
		_, err := GenDescend(tx, 0, 1, "txid")
		require.Error(t, err)
	})
}

func TestAscendingTickerUsesVerifiedPrecisionAndBinding(t *testing.T) {
	b, _, _ := membershipIndexer(t)
	btc := "::-2100000000000000-0-1"
	require.NoError(t, b.applyAscendingTicker(&common.AscendData{Value: 100}, []byte(btc)))
	require.NoError(t, b.applyAscendingTicker(&common.AscendData{Value: 100}, []byte(btc)))
	require.Error(t, b.applyAscendingTicker(&common.AscendData{Value: 100}, []byte("::-2100000000000000-1-1")))
	require.Error(t, b.applyAscendingTicker(&common.AscendData{Value: 100}, []byte("::-2100000000000000-0-4294967297")))
	asset := wire.TxAssets{{Name: *indexer.NewAssetNameFromString("brc20:f:test"), Amount: *indexer.NewDecimal(100, 2), BindingSat: 3}}
	require.NoError(t, b.applyAscendingTicker(&common.AscendData{Assets: asset}, []byte("brc20:f:test-1000000-2-3")))
	require.NoError(t, b.applyAscendingTicker(&common.AscendData{Assets: asset}, []byte("brc20:f:test-1000000-2-3")))
	require.Error(t, b.applyAscendingTicker(&common.AscendData{Assets: asset}, []byte("brc20:f:test-1000000-1-3")))
	require.Error(t, b.applyAscendingTicker(&common.AscendData{Assets: asset}, []byte("brc20:f:test-1000000-2-4")))
}
