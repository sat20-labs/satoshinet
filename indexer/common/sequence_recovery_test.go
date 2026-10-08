package common

import (
	"encoding/hex"
	"sort"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/stretchr/testify/require"
)

func recoverySequence(t *testing.T) (*MiningSequenceMgr, map[string]*CoreNodeInfo, []string) {
	t.Helper()
	pubs := make([]string, 4)
	for i := range pubs {
		key, _ := btcec.PrivKeyFromBytes([]byte{byte(i + 1)})
		pubs[i] = hex.EncodeToString(key.PubKey().SerializeCompressed())
	}
	sort.Strings(pubs[1:3])
	params := chaincfg.TestNetParams
	params.POSV2Height = 1
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: params.GenesisHash}}
	cores := map[string]*CoreNodeInfo{pubs[0]: NewCoreNodeInfo(nil)}
	for _, pub := range pubs[1:3] {
		core := NewCoreNodeInfo(nil)
		core.ServerNode = pubs[0]
		cores[pub] = core
	}
	seq := NewMiningSequenceMgr(&params)
	require.NoError(t, seq.Init(cores, 0, ""))
	return seq, cores, pubs
}

func compareRecoveryRounds(t *testing.T, live, restored *MiningSequenceMgr, height int) {
	t.Helper()
	for h := height; h < height+12; h++ {
		a, b := live.GetCurrentMiningInfo(), restored.GetCurrentMiningInfo()
		require.Equal(t, a.PubKey, b.PubKey, "height %d", h)
		require.Equal(t, a.MiningAddress, b.MiningAddress)
		if a.Father != nil {
			require.NotNil(t, b.Father)
			require.Equal(t, a.Father.PubKey, b.Father.PubKey)
		}
		reward, err := live.POSReward(h, a.PubKey)
		require.NoError(t, err)
		restoredReward, err := restored.POSReward(h, b.PubKey)
		require.NoError(t, err)
		require.Equal(t, reward, restoredReward)
		require.NoError(t, live.MoveMiningAddr(h, a.MiningAddress))
		require.NoError(t, restored.MoveMiningAddr(h, b.MiningAddress))
	}
}

func TestSequenceRejectsConflictingMembership(t *testing.T) {
	seq, cores, pubs := recoverySequence(t)
	node, err := seq.AddNode(pubs[3], pubs[1], 1)
	require.NoError(t, err)
	duplicate, err := seq.AddNode(pubs[3], pubs[1], 2)
	require.NoError(t, err)
	require.Same(t, node, duplicate)
	require.Equal(t, 1, node.JoinHeight)
	_, err = seq.AddNode(pubs[3], pubs[2], 2)
	require.Error(t, err, "cross-parent membership must be refused")
	_, err = seq.AddNode(pubs[3], pubs[0], 2)
	require.Error(t, err, "Miner must exit before joining as Core")
	require.Equal(t, pubs[1], node.Father.PubKey)
	require.Equal(t, indexer.NODE_TYPE_MINER, node.NodeType)
	cores[pubs[1]].ChildMiners[pubs[3]] = &MinerAscendInfo{AscendHeight: 1}
	cores[pubs[2]].ChildMiners[pubs[3]] = &MinerAscendInfo{AscendHeight: 2}
	for i := 0; i < 20; i++ {
		require.Error(t, NewMiningSequenceMgr(seq.chainParam).Init(cores, 0, ""), "ambiguous snapshots must fail deterministically")
	}
}

func TestSequenceJoinSnapshotUsesOnlineEligibility(t *testing.T) {
	seq, cores, pubs := recoverySequence(t)
	delete(cores, pubs[1])
	seq = NewMiningSequenceMgr(seq.chainParam)
	require.NoError(t, seq.Init(cores, 0, ""))
	_, err := seq.AddNode(pubs[1], pubs[0], 1)
	require.NoError(t, err)
	newCore := NewCoreNodeInfo(nil)
	newCore.ServerNode, newCore.AscendHeight = pubs[0], 1
	cores[pubs[1]] = newCore
	cursor := seq.GetCurrentMiningAddr()
	require.NoError(t, seq.MoveMiningAddr(1, cursor))
	require.Equal(t, pubs[2], seq.GetCurrentMiningInfo().PubKey)
	restored := NewMiningSequenceMgr(seq.chainParam)
	require.NoError(t, restored.Init(cores, 1, cursor))
	compareRecoveryRounds(t, seq, restored, 2)
}

func TestSequenceCurrentExitSnapshotCanRestart(t *testing.T) {
	seq, cores, pubs := recoverySequence(t)
	require.NoError(t, seq.MoveMiningAddr(1, seq.GetCurrentMiningAddr()))
	require.Equal(t, pubs[1], seq.GetCurrentMiningInfo().PubKey)
	require.NoError(t, seq.RemoveNode(pubs[1]))
	delete(cores, pubs[1])
	cursor := seq.GetCurrentMiningAddr()
	require.NoError(t, seq.MoveMiningAddr(2, cursor))
	require.Equal(t, pubs[2], seq.GetCurrentMiningInfo().PubKey)
	restored := NewMiningSequenceMgr(seq.chainParam)
	require.NoError(t, restored.Init(cores, 2, cursor))
	compareRecoveryRounds(t, seq, restored, 3)
}

func TestSequenceInvalidParentDoesNotLeaveNode(t *testing.T) {
	seq, _, pubs := recoverySequence(t)
	parent, _ := btcec.PrivKeyFromBytes([]byte{5})
	_, err := seq.AddNode(pubs[3], hex.EncodeToString(parent.PubKey().SerializeCompressed()), 1)
	require.Error(t, err)
	require.Nil(t, seq.GetMiningInfo(pubs[3]))
}
