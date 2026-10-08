package base

import (
	"encoding/hex"
	"fmt"
	"sort"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func membershipIndexer(t *testing.T) (*BaseIndexer, string, []string) {
	t.Helper()
	path := t.TempDir()
	db := indexerdb.NewKVDB(path)
	params := chaincfg.TestNetParams
	params.POSV2Height = 1
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: params.GenesisHash}}
	b := NewBaseIndexer(db, &params, 0, 100)
	t.Cleanup(func() {
		if b.db != nil {
			b.db.Close()
		}
	})
	b.reset()
	b.lastHeight = 0
	pubs := make([]string, 3)
	for i := range pubs {
		key, _ := btcec.PrivKeyFromBytes([]byte{byte(i + 71)})
		pubs[i] = hex.EncodeToString(key.PubKey().SerializeCompressed())
	}
	sort.Strings(pubs[:2])
	root := indexer.GetBootstrapPubKey()
	b.coreNodeMap[root] = common.NewCoreNodeInfo(nil)
	for _, pub := range pubs[:2] {
		core := common.NewCoreNodeInfo(nil)
		core.ServerNode = root
		b.coreNodeMap[pub] = core
		b.coreNodeMap[root].ChildMiners[pub] = &common.MinerAscendInfo{}
	}
	b.coreNodeMapUpdated = true
	b.seqMgr = common.NewMiningSequenceMgr(&params)
	require.NoError(t, b.seqMgr.Init(b.coreNodeMap, 0, ""))
	return b, path, pubs
}

func membershipStake(t *testing.T, b *BaseIndexer, parent, child string, height int, nonce uint32) string {
	t.Helper()
	a, err := hex.DecodeString(parent)
	require.NoError(t, err)
	c, err := hex.DecodeString(child)
	require.NoError(t, err)
	_, script, err := anchortx.GetP2WSHscript(a, c)
	require.NoError(t, err)
	name := indexer.NewAssetNameFromString(indexer.GetStakeAssetNameWithHeightL2(height))
	amount := indexer.NewDefaultDecimal(indexer.GetStakeAssetAmtWithHeightL2(height))
	assets := wire.TxAssets{{Name: *name, Amount: *amount}}
	tx := wire.NewMsgTx(2)
	tx.LockTime = nonce
	tx.AddTxOut(wire.NewTxOut(0, assets, script))
	converted := ConvertBlock(&wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}, height, b.chaincfgParam).Transactions[0]
	addr := converted.Outputs[0].Address.Addresses[0]
	b.channelMap[addr] = &common.ChannelInfo{ChannelInfoInDB: common.ChannelInfoInDB{Address: addr, PubA: a, PubB: c}, IsNew: true}
	invoice, err := common.CreateStakeInvoice(name, amount)
	require.NoError(t, err)
	b.handleStakeAssetV2(height, converted, invoice)
	return tx.TxID() + ":0"
}

func restartMembership(t *testing.T, b *BaseIndexer, path string, height int) *BaseIndexer {
	t.Helper()
	b.miningAddress = b.seqMgr.GetCurrentMiningAddr()
	require.NoError(t, b.seqMgr.MoveMiningAddr(height, b.miningAddress))
	b.lastHeight, b.lastHash = height, "membership-snapshot"
	b.UpdateDB()
	require.NoError(t, b.db.Close())
	b.db = nil
	db := indexerdb.NewKVDB(path)
	t.Cleanup(func() { db.Close() })
	restored := NewBaseIndexer(db, b.chaincfgParam, 0, 100)
	restored.Init()
	return restored
}

func compareMembershipRounds(t *testing.T, a, b *common.MiningSequenceMgr, height int) {
	t.Helper()
	for h := height; h < height+12; h++ {
		na, nb := a.GetCurrentMiningInfo(), b.GetCurrentMiningInfo()
		require.Equal(t, na.PubKey, nb.PubKey, "height %d", h)
		require.Equal(t, na.MiningAddress, nb.MiningAddress)
		if na.Father != nil {
			require.NotNil(t, nb.Father)
			require.Equal(t, na.Father.PubKey, nb.Father.PubKey)
		}
		ra, err := a.POSReward(h, na.PubKey)
		require.NoError(t, err)
		rb, err := b.POSReward(h, nb.PubKey)
		require.NoError(t, err)
		require.Equal(t, ra, rb)
		require.NoError(t, a.MoveMiningAddr(h, na.MiningAddress))
		require.NoError(t, b.MoveMiningAddr(h, nb.MiningAddress))
	}
}

func TestStakeMembershipPersistsOneParentAndRole(t *testing.T) {
	for _, kind := range []string{"same-parent", "another-core", "miner-to-core"} {
		t.Run(kind, func(t *testing.T) {
			b, path, pubs := membershipIndexer(t)
			first := membershipStake(t, b, pubs[0], pubs[2], 1, 1)
			parent := pubs[0]
			if kind == "another-core" {
				parent = pubs[1]
			}
			if kind == "miner-to-core" {
				parent = indexer.GetBootstrapPubKey()
			}
			membershipStake(t, b, parent, pubs[2], 1, 2)
			require.NotContains(t, b.coreNodeMap[pubs[1]].ChildMiners, pubs[2])
			require.NotContains(t, b.coreNodeMap, pubs[2], "registered Miner cannot simultaneously be a Core")
			require.Equal(t, first, b.coreNodeMap[pubs[0]].ChildMiners[pubs[2]].AscendUtxo)
			require.Equal(t, pubs[0], b.seqMgr.GetMiningInfo(pubs[2]).Father.PubKey)
			// Parent-state reconstruction uses the same membership snapshot as restart.
			parentView := b.Clone(false)
			restored := restartMembership(t, b, path, 1)
			parentView.seqMgr = common.NewMiningSequenceMgr(b.chaincfgParam)
			require.NoError(t, parentView.seqMgr.Init(parentView.coreNodeMap, 1, b.miningAddress))
			compareMembershipRounds(t, parentView.seqMgr, restored.seqMgr, 2)
			restored.seqMgr = common.NewMiningSequenceMgr(b.chaincfgParam)
			require.NoError(t, restored.seqMgr.Init(restored.coreNodeMap, 1, b.miningAddress))
			compareMembershipRounds(t, b.seqMgr, restored.seqMgr, 2)
		})
	}
}

func TestMembershipCurrentExitPersistsRecoverableCursor(t *testing.T) {
	b, path, pubs := membershipIndexer(t)
	require.NoError(t, b.seqMgr.MoveMiningAddr(1, b.seqMgr.GetCurrentMiningAddr()))
	require.Equal(t, pubs[0], b.seqMgr.GetCurrentMiningInfo().PubKey)
	a, err := hex.DecodeString(indexer.GetBootstrapPubKey())
	require.NoError(t, err)
	c, err := hex.DecodeString(pubs[0])
	require.NoError(t, err)
	addr, err := common.GetChannelAddress(a, c, b.chaincfgParam)
	require.NoError(t, err)
	b.channelMap[addr] = &common.ChannelInfo{ChannelInfoInDB: common.ChannelInfoInDB{Address: addr, PubA: a, PubB: c}, IsNew: true}
	name := indexer.NewAssetNameFromString(indexer.GetStakeAssetName(2))
	amount := indexer.NewDefaultDecimal(indexer.GetStakeAssetAmtWithHeightL2(2))
	invoice, err := common.CreateStakeInvoice(name, amount)
	require.NoError(t, err)
	b.removeMinerNode(&common.DescendData{Height: 2, Address: addr, Assets: wire.TxAssets{{Name: *name, Amount: *amount}}}, invoice)
	require.NotContains(t, b.coreNodeMap, pubs[0])
	restored := restartMembership(t, b, path, 2)
	compareMembershipRounds(t, b.seqMgr, restored.seqMgr, 3)
}

func TestMembershipJoinAfterActivationRestoresSameNextMember(t *testing.T) {
	b, path, pubs := membershipIndexer(t)
	delete(b.coreNodeMap, pubs[0])
	delete(b.coreNodeMap[indexer.GetBootstrapPubKey()].ChildMiners, pubs[0])
	b.seqMgr = common.NewMiningSequenceMgr(b.chaincfgParam)
	require.NoError(t, b.seqMgr.Init(b.coreNodeMap, 0, ""))
	// The new Core sorts immediately after Bootstrap and before the old Core.
	membershipStake(t, b, indexer.GetBootstrapPubKey(), pubs[0], 1, 1)
	require.Contains(t, b.coreNodeMap, pubs[0])
	restored := restartMembership(t, b, path, 1)
	require.Equal(t, pubs[1], b.seqMgr.GetCurrentMiningInfo().PubKey)
	require.Equal(t, pubs[1], restored.seqMgr.GetCurrentMiningInfo().PubKey)
	compareMembershipRounds(t, b.seqMgr, restored.seqMgr, 2)
}

func TestAnchorStakeMissingAssetDoesNotPanic(t *testing.T) {
	b, _, _ := membershipIndexer(t)
	invoice, err := common.CreateStakeInvoice(indexer.NewAssetNameFromString("ordx:f:missing"), indexer.NewDefaultDecimal(1))
	require.NoError(t, err)
	before := len(b.coreNodeMap)
	require.NotPanics(t, func() { b.handleStakeAsset(&common.AscendData{Height: 1, Value: 1}, invoice) })
	require.Equal(t, before, len(b.coreNodeMap), "invalid STAKE must not register a node")
}

func TestUnstakeUsesOriginalRegistrationAsset(t *testing.T) {
	oldChain, oldTesting := indexer.CHAIN, indexer.ENABLE_TESTING
	indexer.CHAIN, indexer.ENABLE_TESTING = "testnet", false
	t.Cleanup(func() { indexer.CHAIN, indexer.ENABLE_TESTING = oldChain, oldTesting })
	for _, role := range []string{"core", "miner"} {
		for _, joined := range []int{3399, 3400} {
			t.Run(fmt.Sprintf("%s/%d", role, joined), func(t *testing.T) {
				b, path, pubs := membershipIndexer(t)
				require.NoError(t, b.seqMgr.Init(b.coreNodeMap, joined-1, b.seqMgr.GetCurrentMiningAddr()))
				parent := pubs[0]
				if role == "core" {
					parent = indexer.GetBootstrapPubKey()
				}
				membershipStake(t, b, parent, pubs[2], joined, 1)
				require.NotNil(t, b.seqMgr.GetMiningInfo(pubs[2]))
				a, err := hex.DecodeString(parent)
				require.NoError(t, err)
				c, err := hex.DecodeString(pubs[2])
				require.NoError(t, err)
				addr, err := common.GetChannelAddress(a, c, b.chaincfgParam)
				require.NoError(t, err)
				remove := func(name string) {
					asset := indexer.NewAssetNameFromString(name)
					amount := indexer.NewDefaultDecimal(indexer.GetStakeAssetAmtWithHeightL2(joined))
					invoice, err := common.CreateStakeInvoice(asset, amount)
					require.NoError(t, err)
					b.removeMinerNode(&common.DescendData{Height: 3401, Address: addr, Assets: wire.TxAssets{{Name: *asset, Amount: *amount}}}, invoice)
				}
				otherHeight := 3400
				if joined == 3400 {
					otherHeight = 3399
				}
				remove(indexer.GetStakeAssetNameWithHeightL2(otherHeight))
				require.NotNil(t, b.seqMgr.GetMiningInfo(pubs[2]), "another stake asset must not remove the member")
				remove(indexer.GetStakeAssetNameWithHeightL2(joined))
				require.Nil(t, b.seqMgr.GetMiningInfo(pubs[2]))
				require.NotContains(t, b.coreNodeMap, pubs[2])
				require.NotContains(t, b.coreNodeMap[parent].ChildMiners, pubs[2])
				for h := joined; h < 3401; h++ {
					require.NoError(t, b.seqMgr.MoveMiningAddr(h, b.seqMgr.GetCurrentMiningAddr()))
				}
				restored := restartMembership(t, b, path, 3401)
				require.Nil(t, restored.seqMgr.GetMiningInfo(pubs[2]))
				require.NotContains(t, restored.coreNodeMap[parent].ChildMiners, pubs[2])
				compareMembershipRounds(t, b.seqMgr, restored.seqMgr, 3402)
			})
		}
	}
}
