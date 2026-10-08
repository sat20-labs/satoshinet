package base

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"

	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"
	shareindexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type anchorCoreLookup struct {
	shareindexer.Indexer
	view *BaseIndexer
}

func (i *anchorCoreLookup) IsCoreNode(pub string) bool { return i.view.IsCoreNode(pub) }

// syncBlock holds this mutex while extracting AscendData. Querying the same
// global indexer recursively deadlocks; querying a different global view can
// instead accept/reject membership from the wrong historical branch.
func TestIndexAnchorUsesLockedCoreState(t *testing.T) {
	server, _ := btcec.PrivKeyFromBytes([]byte{71})
	client, _ := btcec.PrivKeyFromBytes([]byte{72})
	stranger, _ := btcec.PrivKeyFromBytes([]byte{73})
	pub := hex.EncodeToString(server.PubKey().SerializeCompressed())
	previous := shareindexer.ShareIndexer
	t.Cleanup(func() { shareindexer.ShareIndexer = previous })
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{
		IndexerHost: "unused.invalid", IndexerProxy: "testnet", ChainParams: &chaincfg.TestNetParams,
	}))
	t.Cleanup(anchortx.Stop)
	witness, _, err := anchortx.GetP2WSHscript(server.PubKey().SerializeCompressed(), client.PubKey().SerializeCompressed())
	require.NoError(t, err)
	const utxo = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"
	invoice, err := anchortx.StandardAnchorScript(utxo, witness, 2000, nil)
	require.NoError(t, err)
	makeScript := func(key *btcec.PrivateKey) []byte {
		assets, err := wire.SerializeTxAssets(new(wire.TxAssets))
		require.NoError(t, err)
		script, err := txscript.NewScriptBuilder().AddData([]byte(utxo)).AddData(witness).
			AddInt64(2000).AddData(assets).AddData(ecdsa.Sign(key, chainhash.HashB(invoice)).Serialize()).Script()
		require.NoError(t, err)
		return script
	}
	valid := makeScript(server)
	for _, tc := range []struct {
		name            string
		local, sameView bool
		script          []byte
		wantErr         string
	}{
		{name: "same_view_under_write_lock", local: true, sameView: true, script: valid},
		{name: "local_core_absent_from_global_view", local: true, script: valid},
		{name: "global_core_absent_from_local_view", script: valid, wantErr: "not signed by core node"},
		{name: "invalid_signature", local: true, script: makeScript(stranger), wantErr: "VerifyMessage failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &BaseIndexer{coreNodeMap: make(map[string]*common.CoreNodeInfo)}
			global := &BaseIndexer{coreNodeMap: make(map[string]*common.CoreNodeInfo)}
			if tc.local {
				b.coreNodeMap[pub] = &common.CoreNodeInfo{}
			} else {
				global.coreNodeMap[pub] = &common.CoreNodeInfo{}
			}
			if tc.sameView {
				global = b
			}
			shareindexer.ShareIndexer = &anchorCoreLookup{view: global}
			type result struct {
				data *common.AscendData
				err  error
			}
			done := make(chan result, 1)
			b.mutex.Lock()
			go func() {
				data, err := b.genAscendFromAnchorPkScript(tc.script, nil, false)
				done <- result{data, err}
			}()
			var got result
			select {
			case got = <-done:
				b.mutex.Unlock()
			case <-time.After(time.Second):
				// Release the lock so a failed regression leaves no stuck goroutine.
				b.mutex.Unlock()
				got = <-done
				t.Error("Anchor extraction reacquired the indexer's write-held mutex")
			}
			if tc.wantErr != "" {
				require.ErrorContains(t, got.err, tc.wantErr)
				return
			}
			require.NoError(t, got.err)
			require.Equal(t, server.PubKey().SerializeCompressed(), got.data.PubA)
			require.Equal(t, client.PubKey().SerializeCompressed(), got.data.PubB)
		})
	}
}

func TestIndexAnchorAndCoreUnstakeUseSameParentState(t *testing.T) {
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{IndexerHost: "unused.invalid", IndexerProxy: "testnet", ChainParams: &chaincfg.TestNetParams}))
	t.Cleanup(anchortx.Stop)
	bootstrap, _ := btcec.PrivKeyFromBytes([]byte{1})
	core, _ := btcec.PrivKeyFromBytes([]byte{71})
	client, _ := btcec.PrivKeyFromBytes([]byte{72})
	rootPub := hex.EncodeToString(bootstrap.PubKey().SerializeCompressed())
	corePub := hex.EncodeToString(core.PubKey().SerializeCompressed())
	var results []*BaseIndexer
	for _, order := range []string{"unstake-before-anchor", "anchor-before-unstake"} {
		t.Run(order, func(t *testing.T) {
			db := indexerdb.NewKVDB(t.TempDir())
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			params := chaincfg.TestNetParams
			params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: params.GenesisHash}}
			b := &BaseIndexer{db: db, stats: &SyncStats{}, chaincfgParam: &params,
				utxoIndex: common.NewUTXOIndex(), channelMap: make(map[string]*common.ChannelInfo),
				tickAddressMap: make(map[string]map[string]*indexer.Decimal), tickInfoMap: make(map[string]*common.TickerInfo),
				addressValueMap: make(map[string]*indexer.AddressValueV2), prevBlockHashMap: make(map[int]string), keepBlockHistory: 20,
				blockprocCB: func(*common.Block) {},
			}
			parent := common.NewCoreNodeInfo(nil)
			parent.ChildMiners[corePub] = &common.MinerAscendInfo{}
			child := common.NewCoreNodeInfo(nil)
			child.ServerNode = rootPub
			b.coreNodeMap = map[string]*common.CoreNodeInfo{rootPub: parent, corePub: child}
			b.seqMgr = common.NewMiningSequenceMgr(&params)
			require.NoError(t, b.seqMgr.Init(b.coreNodeMap, 0, ""))
			_, stakeScript, err := anchortx.GetP2WSHscript(bootstrap.PubKey().SerializeCompressed(), core.PubKey().SerializeCompressed())
			require.NoError(t, err)
			seed := wire.NewMsgTx(2)
			stakeName := indexer.NewAssetNameFromString(indexer.GetStakeAssetName(1))
			stakeAssets := wire.TxAssets{{Name: *stakeName, Amount: *indexer.NewDefaultDecimal(1)}}
			seed.AddTxOut(wire.NewTxOut(0, stakeAssets, stakeScript))
			prior := ConvertBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{seed}}, 0, &params)
			stakeOutput := prior.Transactions[0].Outputs[0]
			stakeAddress := stakeOutput.Address.Addresses[0]
			b.channelMap[stakeAddress] = &common.ChannelInfo{ChannelInfoInDB: common.ChannelInfoInDB{Address: stakeAddress, PubA: bootstrap.PubKey().SerializeCompressed(), PubB: core.PubKey().SerializeCompressed()}}
			b.addressValueMap[stakeAddress] = &indexer.AddressValueV2{Utxos: make(map[uint64]int64)}
			b.utxoIndex.Index[indexer.GetUtxo(1, seed.TxID(), 0)] = stakeOutput
			b.outputUtxo(stakeOutput)
			b.tickInfoMap[stakeName.String()] = &common.TickerInfo{AssetName: *stakeName, TotalAscendAmt: indexer.NewDefaultDecimal(10), TotalDescendAmt: indexer.NewDefaultDecimal(0)}
			unstake := wire.NewMsgTx(2)
			unstake.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: seed.TxHash()}, nil, nil))
			descendScript, err := common.NullDataScript(common.CONTENT_TYPE_DESCENDING, []byte(hex.EncodeToString(bytes.Repeat([]byte{8}, 32))))
			require.NoError(t, err)
			unstake.AddTxOut(wire.NewTxOut(0, stakeAssets, descendScript))
			invoice, err := common.CreateStakeInvoice(stakeName, indexer.NewDefaultDecimal(1))
			require.NoError(t, err)
			unstakeScript, err := common.NullDataScript(common.CONTENT_TYPE_UNSTAKE, invoice)
			require.NoError(t, err)
			unstake.AddTxOut(wire.NewTxOut(0, nil, unstakeScript))
			witness, channelScript, err := anchortx.GetP2WSHscript(core.PubKey().SerializeCompressed(), client.PubKey().SerializeCompressed())
			require.NoError(t, err)
			const funding = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"
			assetName := indexer.NewAssetNameFromString("brc20:f:order")
			assets := wire.TxAssets{{Name: *assetName, Amount: *indexer.NewDefaultDecimal(100)}}
			anchorInvoice, err := anchortx.StandardAnchorScript(funding, witness, 2000, assets)
			require.NoError(t, err)
			serialized, err := wire.SerializeTxAssets(&assets)
			require.NoError(t, err)
			sigScript, err := txscript.NewScriptBuilder().AddData([]byte(funding)).AddData(witness).AddInt64(2000).AddData(serialized).AddData(ecdsa.Sign(core, chainhash.HashB(anchorInvoice)).Serialize()).Script()
			require.NoError(t, err)
			anchor := wire.NewMsgTx(2)
			anchor.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.AnchorTxOutIndex}, sigScript, nil))
			anchor.AddTxOut(wire.NewTxOut(2000, assets, channelScript))
			ascending, err := common.NullDataScript(common.CONTENT_TYPE_ASCENDING, []byte("brc20:f:order-1000000-0-0"))
			require.NoError(t, err)
			anchor.AddTxOut(wire.NewTxOut(0, nil, ascending))
			msg := &wire.MsgBlock{Transactions: []*wire.MsgTx{wire.NewMsgTx(2), unstake, anchor}}
			if order == "anchor-before-unstake" {
				msg.Transactions[1], msg.Transactions[2] = anchor, unstake
			}
			block := ConvertBlock(msg, 1, &params)
			done := make(chan int, 1)
			go func() { done <- b.syncBlock(block, 1, false) }()
			select {
			case code := <-done:
				require.Zero(t, code)
			case <-time.After(3 * time.Second):
				t.Fatal("block indexing deadlocked")
			}
			require.Equal(t, 1, b.lastHeight)
			require.NotContains(t, b.coreNodeMap, corePub, "UNSTAKE must remove Core")
			require.Len(t, b.utxoIndex.AscendMap, 1)
			ascend := b.utxoIndex.AscendMap[funding]
			require.NotNil(t, ascend, "valid parent-state Anchor must be indexed even after UNSTAKE")
			require.Equal(t, anchor.TxID(), ascend.AnchorTxId)
			require.Contains(t, b.channelMap, ascend.Address)
			require.Equal(t, core.PubKey().SerializeCompressed(), b.channelMap[ascend.Address].PubA)
			require.Len(t, b.utxoIndex.ChannelLedgerMap, 2)
			require.Contains(t, b.utxoIndex.ChannelLedgerMap, string(stp.GetChannelLedgerDBKey(NewAscendingLedgerEntry(ascend))))
			require.Equal(t, "100", b.tickInfoMap[assetName.String()].TotalAscendAmt.String())
			require.Equal(t, "1", b.tickInfoMap[stakeName.String()].TotalDescendAmt.String())
			require.Contains(t, b.utxoIndex.Index, indexer.GetUtxo(1, anchor.TxID(), 0))
			results = append(results, b)
		})
	}
	require.Len(t, results, 2)
	require.Equal(t, results[0].utxoIndex.AscendMap, results[1].utxoIndex.AscendMap)
	require.Equal(t, results[0].utxoIndex.ChannelLedgerMap, results[1].utxoIndex.ChannelLedgerMap)
	require.Equal(t, results[0].tickInfoMap, results[1].tickInfoMap)
}

func TestValidAnchorWithMissingStakeAssetIndexesWithoutPanic(t *testing.T) {
	b, path, pubs := membershipIndexer(t)
	b.SetBlockCallback(func(*common.Block) {})
	oldChain := indexer.CHAIN
	indexer.CHAIN = "testnet"
	t.Cleanup(func() { indexer.CHAIN = oldChain })
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{IndexerHost: "unused.invalid", IndexerProxy: "testnet", ChainParams: b.chaincfgParam}))
	t.Cleanup(anchortx.Stop)
	server, _ := btcec.PrivKeyFromBytes([]byte{71})
	client, _ := btcec.PrivKeyFromBytes([]byte{73})
	previous := shareindexer.ShareIndexer
	shareindexer.ShareIndexer = &anchorCoreLookup{view: b}
	t.Cleanup(func() { shareindexer.ShareIndexer = previous })
	witness, channelScript, err := anchortx.GetP2WSHscript(server.PubKey().SerializeCompressed(), client.PubKey().SerializeCompressed())
	require.NoError(t, err)
	const funding = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"
	invoice, err := anchortx.StandardAnchorScript(funding, witness, 2000, nil)
	require.NoError(t, err)
	serialized, err := wire.SerializeTxAssets(new(wire.TxAssets))
	require.NoError(t, err)
	signed, err := txscript.NewScriptBuilder().AddData([]byte(funding)).AddData(witness).AddInt64(2000).AddData(serialized).AddData(ecdsa.Sign(server, chainhash.HashB(invoice)).Serialize()).Script()
	require.NoError(t, err)
	anchor := wire.NewMsgTx(2)
	anchor.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.AnchorTxOutIndex}, signed, nil))
	anchor.AddTxOut(wire.NewTxOut(2000, nil, channelScript))
	ascending, err := common.NullDataScript(common.CONTENT_TYPE_ASCENDING, []byte(indexer.ASSET_PLAIN_SAT.String()+"-2100000000000000-0-1"))
	require.NoError(t, err)
	anchor.AddTxOut(wire.NewTxOut(0, nil, ascending))
	missing := indexer.NewAssetNameFromString("ordx:f:missing")
	stake, err := common.CreateStakeInvoice(missing, indexer.NewDefaultDecimal(1))
	require.NoError(t, err)
	stakeScript, err := common.NullDataScript(common.CONTENT_TYPE_STAKE, stake)
	require.NoError(t, err)
	anchor.AddTxOut(wire.NewTxOut(0, nil, stakeScript))
	bindOutputs := b.chaincfgParam.POSV2Active(1)
	anchor.Version = wire.TxVersion
	anchor.TxIn[0].Sequence = wire.AnchorTxOutIndex
	invoice, err = common.AnchorInvoice(anchor, bindOutputs)
	require.NoError(t, err)
	anchor.TxIn[0].SignatureScript, err = common.StandardAnchorScriptWithSig(funding, witness, 2000, nil, ecdsa.Sign(server, chainhash.HashB(invoice)).Serialize())
	require.NoError(t, err)
	_, err = anchortx.CheckAnchorTxValid(anchor, false, bindOutputs)
	require.NoError(t, err, "Anchor signature and amounts remain valid")
	require.NotPanics(t, func() {
		require.NoError(t, b.SyncBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{wire.NewMsgTx(2), anchor}}, 1, 1, false))
	})
	require.Len(t, b.utxoIndex.AscendMap, 1)
	require.NotContains(t, b.coreNodeMap[pubs[0]].ChildMiners, pubs[2])
	require.NotContains(t, b.coreNodeMap[pubs[1]].ChildMiners, pubs[2])
	b.CommitBackup(b.Clone(true))
	require.Empty(t, b.utxoIndex.AscendMap)
	require.Empty(t, b.utxoIndex.ChannelLedgerMap)
	require.Equal(t, 1, b.stats.AscendCount)
	// A subsequent backup must not rewrite/count the historical anchor.
	b.CommitBackup(b.Clone(true))
	require.Equal(t, 1, b.stats.AscendCount)
	require.NoError(t, b.db.Close())
	b.db = nil
	store := indexerdb.NewKVDB(path)
	t.Cleanup(func() { store.Close() })
	reopened := NewBaseIndexer(store, b.chaincfgParam, 0, 100)
	reopened.Init()
	info, err := reopened.GetUtxoInfo(anchor.TxID() + ":0")
	require.NoError(t, err)
	require.NotNil(t, info)
	require.NotNil(t, NewRpcIndexer(reopened).GetAscendData(funding))
	require.Equal(t, 1, reopened.stats.AscendCount)
}
