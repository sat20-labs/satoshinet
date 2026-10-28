package base

import (
	"encoding/hex"
	indexer "github.com/sat20-labs/indexer/common"
	"testing"

	dbcodec "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/stp"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBTCLuckyIndexedInvoiceBindsBlockIdentity(t *testing.T) {
	core, _ := btcec.PrivKeyFromBytes([]byte{71})
	winner, _ := btcec.PrivKeyFromBytes([]byte{72})
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{ChainParams: &chaincfg.TestNetParams, IndexerHost: "unused.invalid", IndexerProxy: "testnet"}))
	t.Cleanup(anchortx.Stop)
	witness, script, err := anchortx.GetP2WSHscript(core.PubKey().SerializeCompressed(), winner.PubKey().SerializeCompressed())
	require.NoError(t, err)
	const funding = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"
	unsigned, err := common.StandardAnchorScriptWithSig(funding, witness, 2000, nil, nil)
	require.NoError(t, err)
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Index: wire.AnchorTxOutIndex}, Sequence: wire.AnchorTxOutIndex, SignatureScript: unsigned})
	tx.AddTxOut(wire.NewTxOut(2000, nil, script))
	metadata := common.BTCLuckyMetadata{Version: 1, BlockHeight: 23, BlockHash: chainhash.Hash{1}}
	marker, err := metadata.Script()
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, marker))
	invoice, err := common.AnchorInvoice(tx, true)
	require.NoError(t, err)
	tx.TxIn[0].SignatureScript, err = common.StandardAnchorScriptWithSig(funding, witness, 2000, nil, ecdsa.Sign(core, chainhash.HashB(invoice)).Serialize())
	require.NoError(t, err)
	params := chaincfg.TestNetParams
	params.POSV2Height = 1
	b := &BaseIndexer{chaincfgParam: &params, coreNodeMap: map[string]*common.CoreNodeInfo{hex.EncodeToString(core.PubKey().SerializeCompressed()): {}}}
	ascend, err := b.genAscendFromAnchorTx(tx, 1)
	require.NoError(t, err)
	require.Equal(t, &metadata, ascend.BTCLucky)
	require.Equal(t, winner.PubKey().SerializeCompressed(), ascend.PubB)
	metadata.BlockHash[0]++
	tx.TxOut[1].PkScript, err = metadata.Script()
	require.NoError(t, err)
	_, err = b.genAscendFromAnchorTx(tx, 1)
	require.ErrorContains(t, err, "VerifyMessage failed")
}

func TestBTCLuckyListUsesAscendSnapshotAndDurableRecords(t *testing.T) {
	database := dbcodec.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	metadata := &common.BTCLuckyMetadata{Version: 1, BlockHeight: 23, BlockHash: chainhash.Hash{1}}
	makeRecord := func(key string, height int, value int64) *common.AscendData {
		return &common.AscendData{FundingUtxo: key, Height: height, AnchorTxId: key + "-anchor", Value: value, PubA: []byte{2}, PubB: []byte{3}, BTCLucky: metadata}
	}
	persisted := makeRecord("a:0", 4, 100)
	batch := database.NewWriteBatch()
	require.NoError(t, dbcodec.SetDB(stp.GetAscendDBKey(persisted.FundingUtxo), persisted, batch))
	require.NoError(t, batch.Flush())
	batch.Close()
	// Stable DB progress may lag the live RPC snapshot (including -1 before
	// its first flush). The public view must use the snapshot height.
	view := &RpcIndexer{BaseIndexer: BaseIndexer{db: database, lastHeight: 6, stats: &SyncStats{SyncBase: SyncBase{SyncHeight: -1}}, utxoIndex: common.NewUTXOIndex()}}
	view.utxoIndex.AscendMap["a:0"] = makeRecord("a:0", 5, 200)
	view.utxoIndex.AscendMap["b:0"] = makeRecord("b:0", 6, 300)
	view.utxoIndex.AscendMap["ordinary:0"] = &common.AscendData{Height: 1, Value: 999}
	view.utxoIndex.AscendMap["future:0"] = makeRecord("future:0", 7, 999)
	result, err := view.GetBTCLuckyBlocks("", 0, 1)
	require.NoError(t, err)
	require.Equal(t, 6, result.IndexedHeight)
	require.Equal(t, 2, result.Total)
	require.Equal(t, 1, result.MinedBlockCount)
	require.EqualValues(t, 500, result.TotalRewardSats)
	require.Len(t, result.Items, 1)
	require.Equal(t, "b:0", result.Items[0].FundingUtxo)
	result, err = view.GetBTCLuckyBlocks(hex.EncodeToString([]byte{3}), 9, 20)
	require.NoError(t, err)
	require.Equal(t, 2, result.Total)
	require.Empty(t, result.Items)
	result, err = view.GetBTCLuckyBlocks("different-miner", 0, 20)
	require.NoError(t, err)
	require.Zero(t, result.Total)
	require.Zero(t, result.MinedBlockCount)
	cloned := cloneAscendData(persisted)
	cloned.BTCLucky.BlockHeight++
	require.EqualValues(t, 23, persisted.BTCLucky.BlockHeight)
	// A rolled-back RPC snapshot must not expose records above its chain tip.
	view.lastHeight = 4
	result, err = view.GetBTCLuckyBlocks("", 0, 20)
	require.NoError(t, err)
	require.Zero(t, result.Total)
	// The durable record remains available once it leaves the live buffer.
	delete(view.utxoIndex.AscendMap, "a:0")
	result, err = view.GetBTCLuckyBlocks("", 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, result.Total)
	require.EqualValues(t, 100, result.TotalRewardSats)
	require.Equal(t, metadata, persisted.BTCLucky)
	require.EqualValues(t, 23, result.Items[0].BTCHeight)
}

func TestBTCLuckyCoreServiceIsMinerNotBootstrapSigner(t *testing.T) {
	database := dbcodec.NewKVDB(t.TempDir())
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	bootstrap, err := hex.DecodeString(indexer.GetBootstrapPubKey())
	require.NoError(t, err)
	miner := []byte{2, 3}
	view := &RpcIndexer{BaseIndexer: BaseIndexer{db: database, lastHeight: 2, utxoIndex: common.NewUTXOIndex()}}
	view.utxoIndex.AscendMap["core:0"] = &common.AscendData{FundingUtxo: "core:0", Height: 1, Value: 5000, PubA: bootstrap, PubB: miner, BTCLucky: &common.BTCLuckyMetadata{Version: 1, BlockHeight: 1, BlockHash: chainhash.Hash{1}}}
	result, err := view.GetBTCLuckyBlocks("", 0, 20)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, hex.EncodeToString(miner), result.Items[0].ServicePubKey)
}
