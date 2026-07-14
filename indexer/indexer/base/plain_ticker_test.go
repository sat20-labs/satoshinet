package base

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const plainTickerPayload = "::-21000000000000000-0-1"

func TestPlainTickerRequiresPersistedAscend(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	base := &BaseIndexer{
		db:          database,
		tickInfoMap: make(map[string]*common.TickerInfo),
	}
	rpc := &RpcIndexer{BaseIndexer: *base}

	require.Nil(t, base.GetTickerInfo(&indexer.ASSET_PLAIN_SAT))
	require.Nil(t, rpc.GetTickerInfo(&indexer.ASSET_PLAIN_SAT))
	require.NotContains(t, rpc.GetTickerMap(), indexer.ASSET_PLAIN_SAT.String())
}

func TestPlainTickerLoadsPersistedTotals(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	persistTicker(t, database, &common.TickerInfo{
		AssetName:       indexer.ASSET_PLAIN_SAT,
		N:               1,
		Divisibility:    0,
		MaxSupply:       decimal(t, "21000000000000000", 0),
		TotalAscendAmt:  decimal(t, "40000", 0),
		TotalDescendAmt: decimal(t, "0", 0),
	})

	base := &BaseIndexer{
		db:          database,
		tickInfoMap: make(map[string]*common.TickerInfo),
	}
	info := base.GetTickerInfo(&indexer.ASSET_PLAIN_SAT)
	require.NotNil(t, info)
	require.Equal(t, "40000", info.TotalAscendAmt.String())

	rpc := &RpcIndexer{BaseIndexer: BaseIndexer{
		db:          database,
		tickInfoMap: make(map[string]*common.TickerInfo),
	}}
	info = rpc.GetTickerInfo(&indexer.ASSET_PLAIN_SAT)
	require.NotNil(t, info)
	require.Equal(t, "40000", info.TotalAscendAmt.String())
	require.Contains(t, rpc.GetTickerMap(), indexer.ASSET_PLAIN_SAT.String())
}

func TestPlainTickerSurvivesFlushBeforeDescend(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	base := &BaseIndexer{
		db:          database,
		tickInfoMap: make(map[string]*common.TickerInfo),
	}

	require.NoError(t, base.applyAscendingTicker(&common.AscendData{Value: 40000}, []byte(plainTickerPayload)))
	persistTicker(t, database, base.tickInfoMap[indexer.ASSET_PLAIN_SAT.String()])
	base.tickInfoMap = make(map[string]*common.TickerInfo)

	require.NoError(t, base.applyDescendingTicker(&common.DescendData{Value: 38990}))
	info := base.GetTickerInfo(&indexer.ASSET_PLAIN_SAT)
	require.Equal(t, "40000", info.TotalAscendAmt.String())
	require.Equal(t, "38990", info.TotalDescendAmt.String())
}

func TestAssetAscendDoesNotCreatePlainTicker(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	base := &BaseIndexer{
		db:          database,
		tickInfoMap: make(map[string]*common.TickerInfo),
	}
	assetName := *indexer.NewAssetNameFromString("brc20:f:test")
	ascend := &common.AscendData{Assets: wire.TxAssets{{
		Name:   assetName,
		Amount: *decimal(t, "100", 0),
	}}}

	require.NoError(t, base.applyAscendingTicker(ascend, []byte("brc20:f:test-1000000-0-0")))
	require.Contains(t, base.tickInfoMap, assetName.String())
	require.NotContains(t, base.tickInfoMap, indexer.ASSET_PLAIN_SAT.String())
}

func TestBindingAssetDoesNotCountAsPlainSats(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	base := &BaseIndexer{
		db:          database,
		tickInfoMap: make(map[string]*common.TickerInfo),
	}
	assetName := *indexer.NewAssetNameFromString("ordx:n:test")
	assets := wire.TxAssets{{
		Name:       assetName,
		Amount:     *decimal(t, "1000", 0),
		BindingSat: 1,
	}}

	require.NoError(t, base.applyAscendingTicker(
		&common.AscendData{Value: 1000, Assets: assets},
		[]byte("ordx:n:test-1000000-0-1"),
	))
	require.NotContains(t, base.tickInfoMap, indexer.ASSET_PLAIN_SAT.String())
	persistTicker(t, database, base.tickInfoMap[assetName.String()])
	base.tickInfoMap = make(map[string]*common.TickerInfo)

	require.NoError(t, base.applyDescendingTicker(&common.DescendData{
		Value:  1000,
		Assets: assets,
	}))
	require.NotContains(t, base.tickInfoMap, indexer.ASSET_PLAIN_SAT.String())
	require.Equal(t, "1000", base.GetTickerInfo(&assetName).TotalDescendAmt.String())
}

func TestAscendingTickerMustMatchAnchorAsset(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	base := &BaseIndexer{
		db:          database,
		tickInfoMap: make(map[string]*common.TickerInfo),
	}
	assetName := *indexer.NewAssetNameFromString("brc20:f:test")
	ascend := &common.AscendData{Assets: wire.TxAssets{{
		Name:   assetName,
		Amount: *decimal(t, "100", 0),
	}}}

	err := base.applyAscendingTicker(ascend, []byte(plainTickerPayload))
	require.ErrorContains(t, err, "does not match")
	require.Empty(t, base.tickInfoMap)
}

func TestRepeatedAscendAllowsUpdatedMaxSupply(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	base := &BaseIndexer{
		db:          database,
		tickInfoMap: make(map[string]*common.TickerInfo),
	}
	assetName := *indexer.NewAssetNameFromString("ordx:f:dogcoin")
	ascend := &common.AscendData{Assets: wire.TxAssets{{
		Name:       assetName,
		Amount:     *decimal(t, "1000", 0),
		BindingSat: 1,
	}}}

	require.NoError(t, base.applyAscendingTicker(ascend, []byte("ordx:f:dogcoin-1800000-0-1")))
	require.NoError(t, base.applyAscendingTicker(ascend, []byte("ordx:f:dogcoin-1800100-0-1")))
	require.Equal(t, "2000", base.GetTickerInfo(&assetName).TotalAscendAmt.String())
}

func TestInternalTickerInfoUsesCompilingView(t *testing.T) {
	base := &BaseIndexer{tickInfoMap: map[string]*common.TickerInfo{}}
	asset := indexer.AssetName{Protocol: "brc20", Type: "f", Ticker: "test"}
	base.tickInfoMap[asset.String()] = &common.TickerInfo{AssetName: asset, Divisibility: 7}

	info := base.GetInternalTickerInfo(&asset)
	require.NotNil(t, info)
	require.Equal(t, 7, info.Divisibility)
}

func persistTicker(t *testing.T, database indexer.KVDB, ticker *common.TickerInfo) {
	t.Helper()
	wb := database.NewWriteBatch()
	t.Cleanup(wb.Close)
	require.NoError(t, indexerdb.SetDB(stp.GetTickerInfoDBKey(ticker.AssetName.String()), ticker, wb))
	require.NoError(t, wb.Flush())
}

func decimal(t *testing.T, value string, precision int) *indexer.Decimal {
	t.Helper()
	result, err := indexer.NewDecimalFromString(value, precision)
	require.NoError(t, err)
	return result
}
