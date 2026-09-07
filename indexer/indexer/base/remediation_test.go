package base

import (
	"bytes"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestCloneDeepCopiesMutableStateWithoutChangingLiveOps(t *testing.T) {
	assetName := *indexer.NewAssetNameFromString("brc20:f:test")
	amount := indexer.NewDefaultDecimal(10)
	assets := wire.TxAssets{{Name: assetName, Amount: *amount, BindingSat: 1}}
	original := &BaseIndexer{
		stats:         &SyncStats{},
		chaincfgParam: &chaincfg.TestNetParams,
		utxoIndex:     common.NewUTXOIndex(),
		delUTXOs: []*UtxoValue{{Address: &common.ScriptPubKey{
			Addresses: []string{"deleted"}, PkScript: []byte{1},
		}}},
		tickAddressMap: map[string]map[string]*indexer.Decimal{
			assetName.String(): {"address": amount.Clone()},
		},
		tickInfoMap: map[string]*common.TickerInfo{
			assetName.String(): {AssetName: assetName, MaxSupply: amount.Clone(),
				TotalAscendAmt: amount.Clone(), TotalDescendAmt: amount.Clone()},
		},
		addressValueMap: map[string]*indexer.AddressValueV2{
			"address": {Op: 7, Utxos: map[uint64]int64{1: 2}},
		},
		coreNodeMap: map[string]*common.CoreNodeInfo{
			"core": {ChildMiners: map[string]*common.MinerAscendInfo{"miner": {AscendHeight: 1}}},
		},
		channelMap: map[string]*common.ChannelInfo{
			"channel": {ChannelInfoInDB: common.ChannelInfoInDB{PubA: []byte{1}, PubB: []byte{2}}},
		},
		blockVector: []*common.BlockValueInDB{{Height: 1}},
	}
	original.utxoIndex.Index["utxo"] = &common.Output{Address: &common.ScriptPubKey{
		Addresses: []string{"address"}, PkScript: []byte{2},
	}, Assets: assets.Clone()}
	original.utxoIndex.AscendMap["ascend"] = &common.AscendData{
		Assets: assets.Clone(), Sig: []byte{1}, PubA: []byte{2}, PubB: []byte{3},
	}
	original.utxoIndex.DescendMap["descend"] = &common.DescendData{
		Assets: assets.Clone(), ReturnedChannelOutputs: []string{"out"},
	}
	original.utxoIndex.ChannelLedgerMap["ledger"] = &common.ChannelLedgerEntry{
		Assets: assets.Clone(), L1Outpoints: []string{"l1"}, ReturnedChannelOutputs: []string{"returned"},
	}
	original.utxoIndex.ChannelStateEventMap["event"] = &common.ChannelStateEvent{PunishTxIds: []string{"punish"}}
	original.utxoIndex.ReferrerMap["referrer"] = &common.ReferrerInfo{Name: "name"}

	cloned := original.Clone(false)
	require.Equal(t, 7, original.addressValueMap["address"].Op)
	require.Equal(t, 7, cloned.addressValueMap["address"].Op)

	cloned.utxoIndex.Index["utxo"].Address.Addresses[0] = "changed"
	cloned.utxoIndex.AscendMap["ascend"].PubA[0] = 9
	cloned.utxoIndex.DescendMap["descend"].ReturnedChannelOutputs[0] = "changed"
	cloned.utxoIndex.ChannelLedgerMap["ledger"].L1Outpoints[0] = "changed"
	cloned.utxoIndex.ChannelStateEventMap["event"].PunishTxIds[0] = "changed"
	cloned.tickInfoMap[assetName.String()].MaxSupply = indexer.NewDefaultDecimal(99)
	cloned.coreNodeMap["core"].ChildMiners["miner"].AscendHeight = 9
	cloned.channelMap["channel"].PubA[0] = 9
	cloned.blockVector[0].Height = 9
	cloned.delUTXOs[0].Address.PkScript[0] = 9

	require.Equal(t, "address", original.utxoIndex.Index["utxo"].Address.Addresses[0])
	require.Equal(t, byte(2), original.utxoIndex.AscendMap["ascend"].PubA[0])
	require.Equal(t, "out", original.utxoIndex.DescendMap["descend"].ReturnedChannelOutputs[0])
	require.Equal(t, "l1", original.utxoIndex.ChannelLedgerMap["ledger"].L1Outpoints[0])
	require.Equal(t, "punish", original.utxoIndex.ChannelStateEventMap["event"].PunishTxIds[0])
	require.Equal(t, "10", original.tickInfoMap[assetName.String()].MaxSupply.String())
	require.Equal(t, 1, original.coreNodeMap["core"].ChildMiners["miner"].AscendHeight)
	require.Equal(t, byte(1), original.channelMap["channel"].PubA[0])
	require.Equal(t, 1, original.blockVector[0].Height)
	require.Equal(t, byte(1), original.delUTXOs[0].Address.PkScript[0])
}

type bindingCountingDB struct {
	indexer.KVDB
	bindingWrites int
}

type bindingCountingBatch struct {
	indexer.WriteBatch
	db *bindingCountingDB
}

func (db *bindingCountingDB) NewWriteBatch() indexer.WriteBatch {
	return &bindingCountingBatch{WriteBatch: db.KVDB.NewWriteBatch(), db: db}
}

func (batch *bindingCountingBatch) Put(key, value []byte) error {
	if bytes.HasPrefix(key, []byte(indexer.DB_KEY_ADDRESS)) ||
		bytes.HasPrefix(key, []byte(indexer.DB_KEY_ADDRESSID)) {
		batch.db.bindingWrites++
	}
	return batch.WriteBatch.Put(key, value)
}

func TestDBSnapshotPersistsOnlyDirtyAddressBindings(t *testing.T) {
	database := &bindingCountingDB{KVDB: indexerdb.NewKVDB(t.TempDir())}
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	const (
		address = "tb1qtestaddress"
		txid    = "638c2b31e464cdf847da46b65a0ea3e745069217ae83023eabda50db30296ee0"
		utxo    = txid + ":47"
	)
	base := &BaseIndexer{
		db: database, stats: &SyncStats{}, chaincfgParam: &chaincfg.TestNetParams,
		utxoIndex: common.NewUTXOIndex(), delUTXOs: make([]*UtxoValue, 0),
		tickAddressMap: make(map[string]map[string]*indexer.Decimal),
		tickInfoMap:    make(map[string]*common.TickerInfo),
		addressValueMap: map[string]*indexer.AddressValueV2{
			address: {AddressType: int(txscript.WitnessV0PubKeyHashTy), AddressId: 4,
				Op: 1, Utxos: map[uint64]int64{}},
			"existing": {AddressId: 5, Op: 0, Utxos: map[uint64]int64{}},
		},
		coreNodeMap: make(map[string]*common.CoreNodeInfo), channelMap: make(map[string]*common.ChannelInfo),
		blockVector: make([]*common.BlockValueInDB, 0),
	}
	seed := database.KVDB.NewWriteBatch()
	require.NoError(t, indexerdb.BindAddressDBKeyToId("existing", 5, seed))
	require.NoError(t, seed.Flush())
	seed.Close()
	base.utxoIndex.Index[utxo] = &common.Output{
		Height: 1, TxId: 1, N: 47, Value: 1000,
		Address: &common.ScriptPubKey{Type: int(txscript.WitnessV0PubKeyHashTy), Addresses: []string{address}},
	}

	snapshot := base.Clone(true)
	require.Zero(t, base.addressValueMap[address].Op)
	require.Equal(t, 1, snapshot.addressValueMap[address].Op)
	base.Subtract(snapshot)
	require.Empty(t, base.addressValueMap)
	snapshot.UpdateDB()
	require.Equal(t, 2, database.bindingWrites, "only the dirty address needs forward/reverse bindings")

	reloaded := &BaseIndexer{
		db: database, utxoIndex: common.NewUTXOIndex(),
		addressValueMap: make(map[string]*indexer.AddressValueV2),
	}
	require.NoError(t, reloaded.loadUtxoFromDB(utxo))
	require.Contains(t, reloaded.utxoIndex.Index, utxo)

	// A later snapshot containing a clean, DB-loaded address must not rewrite bindings.
	base.addressValueMap[address] = reloaded.addressValueMap[address]
	require.Zero(t, base.addressValueMap[address].Op)
	database.bindingWrites = 0
	next := base.Clone(true)
	base.Subtract(next)
	next.UpdateDB()
	require.Zero(t, database.bindingWrites)
}

func TestDBSnapshotSubtractRetainsPostSnapshotAddressChanges(t *testing.T) {
	base := &BaseIndexer{
		stats: &SyncStats{}, utxoIndex: common.NewUTXOIndex(),
		addressValueMap: map[string]*indexer.AddressValueV2{
			"changed":   {Op: 1, Utxos: map[uint64]int64{}},
			"unchanged": {Op: 1, Utxos: map[uint64]int64{}},
		},
	}
	snapshot := base.Clone(true)
	output := &common.Output{
		Height: 1, TxId: 1, N: 1,
		Address: &common.ScriptPubKey{Addresses: []string{"changed"}},
	}
	base.outputUtxo(output)
	require.Equal(t, 1, base.addressValueMap["changed"].Op)
	require.Empty(t, snapshot.addressValueMap["changed"].Utxos)
	base.Subtract(snapshot)
	require.NotContains(t, base.addressValueMap, "unchanged")
	require.Contains(t, base.addressValueMap["changed"].Utxos, common.GetUtxoId(output))
}

func TestDBSnapshotCloneSerializesReadClones(t *testing.T) {
	base := &BaseIndexer{
		stats: &SyncStats{}, utxoIndex: common.NewUTXOIndex(),
		addressValueMap: map[string]*indexer.AddressValueV2{"address": {Op: 1}},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = base.Clone(false)
		}
	}()
	for i := 0; i < 100; i++ {
		_ = base.Clone(true)
	}
	<-done
}

func TestStakeAndUnstakeMalformedAssetsDoNotPanic(t *testing.T) {
	assetName := indexer.NewAssetNameFromString("brc20:f:missing")
	invoice, err := common.CreateStakeInvoice(assetName, indexer.NewDefaultDecimal(1))
	require.NoError(t, err)
	base := &BaseIndexer{channelMap: make(map[string]*common.ChannelInfo)}
	tx := &common.Transaction{Txid: "stake", Outputs: []*common.Output{{
		Address: &common.ScriptPubKey{Type: int(txscript.WitnessV0ScriptHashTy), Addresses: []string{"channel"}},
	}}}
	require.NotPanics(t, func() { base.handleStakeAssetV2(1, tx, invoice) })
	require.NotPanics(t, func() {
		base.removeMinerNode(&common.DescendData{Height: 1, NullDataUtxo: "unstake"}, invoice)
	})
}

func TestRecordChannelStateEventSerializesCloneAccess(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	base := &BaseIndexer{
		db: database, stats: &SyncStats{SyncBase: SyncBase{SyncHeight: 7}},
		utxoIndex: common.NewUTXOIndex(), chaincfgParam: &chaincfg.TestNetParams,
		tickAddressMap: make(map[string]map[string]*indexer.Decimal),
		tickInfoMap:    make(map[string]*common.TickerInfo), addressValueMap: make(map[string]*indexer.AddressValueV2),
		coreNodeMap: make(map[string]*common.CoreNodeInfo), channelMap: make(map[string]*common.ChannelInfo),
	}
	errs := make(chan error, 20)
	go func() {
		defer close(errs)
		for i := 0; i < 20; i++ {
			errs <- base.RecordChannelStateEvent(&common.ChannelStateEvent{
				ChannelId: "channel", EventType: common.CHANNEL_EVENT_L2_DRAINED,
				ObservedL1TxId: "tx", Message: string(rune('a' + i)),
			})
		}
	}()
	for i := 0; i < 20; i++ {
		_ = base.Clone(false)
	}
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestRecordChannelStateEventStoresOwnedCopy(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	base := &BaseIndexer{
		db: database, stats: &SyncStats{SyncBase: SyncBase{SyncHeight: 7}},
		utxoIndex: common.NewUTXOIndex(),
	}
	event := &common.ChannelStateEvent{
		ChannelId: "channel", EventType: common.CHANNEL_EVENT_L2_DRAINED,
		ObservedL1TxId: "tx", PunishTxIds: []string{"punish"},
	}
	require.NoError(t, base.RecordChannelStateEvent(event))
	require.Empty(t, event.Status, "recording must not mutate caller-owned event")
	require.Zero(t, event.L2Height, "recording must not mutate caller-owned event")
	require.Len(t, base.utxoIndex.ChannelStateEventMap, 1)
	for _, stored := range base.utxoIndex.ChannelStateEventMap {
		require.NotSame(t, event, stored)
		require.Equal(t, common.CHANNEL_EVENT_STATUS_OBSERVED, stored.Status)
		require.Equal(t, 7, stored.L2Height)
		event.PunishTxIds[0] = "changed"
		require.Equal(t, "punish", stored.PunishTxIds[0])
	}
}

func TestBaseIndexerIsMainnetUsesNetworkIdentityAndHandlesNil(t *testing.T) {
	var nilBase *BaseIndexer
	require.False(t, nilBase.IsMainnet())
	require.False(t, (&BaseIndexer{}).IsMainnet())
	params := chaincfg.MainNetParams
	params.Name = "renamed-mainnet"
	require.True(t, (&BaseIndexer{chaincfgParam: &params}).IsMainnet())
	require.False(t, (&BaseIndexer{chaincfgParam: &chaincfg.TestNetParams}).IsMainnet())
}
