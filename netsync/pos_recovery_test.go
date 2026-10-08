package netsync

import (
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	_ "github.com/sat20-labs/satoshinet/database/ffldb"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/mempool"
	"github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type recoveryIndex struct{ attempts int }

func (*recoveryIndex) Init(*blockchain.BlockChain, <-chan struct{}) error { return nil }
func (*recoveryIndex) DisconnectBlock(database.Tx, *btcutil.Block, []blockchain.SpentTxOut) error {
	return nil
}
func (i *recoveryIndex) ConnectBlock(database.Tx, *btcutil.Block, []blockchain.SpentTxOut) error {
	i.attempts++
	if i.attempts == 1 {
		return fmt.Errorf("one-time receiving-node canonical transaction failure")
	}
	return nil
}

type recoveryReady struct {
	height int
	hash   chainhash.Hash
	seq    *scommon.MiningSequenceMgr
}

func (r *recoveryReady) GetSeqMgr() *scommon.MiningSequenceMgr       { return r.seq }
func (r *recoveryReady) GetInternalTip() (int, chainhash.Hash, bool) { return r.height, r.hash, true }
func (r *recoveryReady) InternalTipReady(h int, hash *chainhash.Hash) bool {
	return h == r.height && hash != nil && *hash == r.hash
}
func (r *recoveryReady) EnsureInternalTip(h int, hash *chainhash.Hash, _ int) error {
	if r.InternalTipReady(h, hash) {
		return nil
	}
	return fmt.Errorf("test parent not ready")
}
func (r *recoveryReady) WaitForInternalTip(h int, hash *chainhash.Hash, _ <-chan struct{}) error {
	return r.EnsureInternalTip(h, hash, h)
}

type recoveryNotifier struct{}

func (recoveryNotifier) AnnounceNewTransactions([]*mempool.TxDesc)            {}
func (recoveryNotifier) UpdatePeerHeights(*chainhash.Hash, int32, *peer.Peer) {}
func (recoveryNotifier) RelayInventory(*wire.InvVect, interface{})            {}
func (recoveryNotifier) TransactionConfirmed(*btcutil.Tx)                     {}

func TestSyncReceiverRetriesStoredPOSCommitAndZeroWorkDescendants(t *testing.T) {
	DisableLog()
	peer.DisableLog()
	params := chaincfg.TestNetParams
	genesis := params.GenesisBlock.BlockHash()
	params.GenesisHash, params.POSV2Height = &genesis, 1
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: &genesis}}
	key, _ := btcec.PrivKeyFromBytes([]byte{1})
	pub := hex.EncodeToString(key.PubKey().SerializeCompressed())
	seq := scommon.NewMiningSequenceMgr(&params)
	require.NoError(t, seq.Init(map[string]*scommon.CoreNodeInfo{pub: scommon.NewCoreNodeInfo(nil)}, 0, ""))
	ready := &recoveryReady{hash: genesis, seq: seq}
	index := &recoveryIndex{}
	path := t.TempDir()
	db, err := database.Create("ffldb", path, params.Net)
	require.NoError(t, err)
	chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: &params, TimeSource: blockchain.NewMedianTime(), IndexManager: index, AssetIndexReadiness: ready})
	require.NoError(t, err)
	makeBlock := func(height int) *btcutil.Block {
		best := chain.BestSnapshot()
		producerSig := ecdsa.Sign(key, chainhash.HashB(scommon.GetScriptSignData(height, 0))).Serialize()
		script, err := txscript.NewScriptBuilder().AddInt64(int64(height)).AddInt64(0).AddData(producerSig).Script()
		require.NoError(t, err)
		addr, err := btcutil.DecodeAddress(seq.GetCurrentMiningAddr(), &params)
		require.NoError(t, err)
		reward, err := txscript.PayToAddrScript(addr)
		require.NoError(t, err)
		coinbase := wire.NewMsgTx(2)
		coinbase.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.MaxPrevOutIndex}, script, wire.TxWitness{make([]byte, 32)}))
		coinbase.AddTxOut(wire.NewTxOut(0, nil, reward))
		root := blockchain.CalcMerkleRoot([]*btcutil.Tx{btcutil.NewTx(coinbase)}, true)
		var preimage [64]byte
		copy(preimage[:32], root[:])
		coinbase.AddTxOut(wire.NewTxOut(0, nil, append(append([]byte(nil), blockchain.WitnessMagicBytes...), chainhash.DoubleHashB(preimage[:])...)))
		msg := &wire.MsgBlock{Header: wire.BlockHeader{Version: 4, PrevBlock: best.Hash, Timestamp: best.MedianTime.Add(time.Second), Bits: 0}, Transactions: []*wire.MsgTx{coinbase}}
		msg.Header.MerkleRoot = blockchain.CalcMerkleRoot([]*btcutil.Tx{btcutil.NewTx(coinbase)}, false)
		approval := ecdsa.Sign(key, chainhash.HashB(scommon.POSApprovalMessage(params.Net, int32(height), msg.BlockHash()))).Serialize()
		coinbase.TxIn[0].Witness = append(coinbase.TxIn[0].Witness, approval)
		return btcutil.NewBlock(msg)
	}
	newSync := func() (*SyncManager, *peer.Peer) {
		sm, err := New(&Config{Chain: chain, ChainParams: &params, MaxPeers: 1, DisableCheckpoints: true, PeerNotifier: recoveryNotifier{}})
		require.NoError(t, err)
		p, err := peer.NewOutboundPeer(&peer.Config{ChainParams: &params}, "127.0.0.1:19526")
		require.NoError(t, err)
		sm.peerStates[p] = &peerSyncState{requestedBlocks: make(map[chainhash.Hash]struct{}), requestedTxns: make(map[chainhash.Hash]struct{})}
		sm.syncPeer.Store(p)
		sm.Start()
		return sm, p
	}
	receive := func(sm *SyncManager, p *peer.Peer, block *btcutil.Block) {
		inv := wire.NewMsgInv()
		require.NoError(t, inv.AddInvVect(wire.NewInvVect(wire.InvTypeWitnessBlock, block.Hash())))
		sm.QueueInv(inv, p)
		done := make(chan struct{}, 1)
		sm.QueueBlock(block, p, done)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("normal sync receiver stalled")
		}
	}
	candidate := makeBlock(1)
	sm, p := newSync()
	receive(sm, p, candidate)
	require.Equal(t, 1, index.attempts)
	require.Equal(t, int32(0), chain.BestSnapshot().Height)
	require.NoError(t, sm.Stop())
	require.NoError(t, db.Close())
	db, err = database.Open("ffldb", path, params.Net)
	require.NoError(t, err)
	defer db.Close()
	chain, err = blockchain.New(&blockchain.Config{DB: db, ChainParams: &params, TimeSource: blockchain.NewMedianTime(), IndexManager: index, AssetIndexReadiness: ready})
	require.NoError(t, err)
	sm, p = newSync()
	defer sm.Stop()
	known, err := sm.haveInventory(wire.NewInvVect(wire.InvTypeWitnessBlock, candidate.Hash()))
	require.NoError(t, err)
	require.False(t, known, "normal sync must request a stored incomplete POS commit")
	var accepted, connected int
	chain.Subscribe(func(n *blockchain.Notification) {
		if n.Type == blockchain.NTBlockAccepted {
			accepted++
		}
		if n.Type == blockchain.NTBlockConnected {
			connected++
		}
	})
	for height := 1; height <= 3; height++ {
		if height > 1 {
			require.NoError(t, seq.MoveMiningAddr(height-1, seq.GetCurrentMiningAddr()))
			ready.height, ready.hash = height-1, chain.BestSnapshot().Hash
			candidate = makeBlock(height)
		}
		receive(sm, p, candidate)
		require.Equal(t, int32(height), chain.BestSnapshot().Height)
		require.Equal(t, *candidate.Hash(), chain.BestSnapshot().Hash)
		require.True(t, chain.CanServeBlock(candidate.Hash()))
		entry, err := chain.FetchUtxoEntry(wire.OutPoint{Hash: candidate.MsgBlock().Transactions[0].TxHash(), Index: 0})
		require.NoError(t, err)
		require.NotNil(t, entry)
	}
	require.Equal(t, 3, accepted)
	require.Equal(t, 3, connected)
	require.Equal(t, 4, index.attempts)
}

func TestPOSNotificationConcurrentSyncPeerChange(t *testing.T) {
	DisableLog()
	params := chaincfg.RegressionNetParams
	params.Checkpoints = nil
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	defer db.Close()
	chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: &params, TimeSource: blockchain.NewMedianTime()})
	require.NoError(t, err)
	sm, err := New(&Config{Chain: chain, ChainParams: &params, MaxPeers: 1, DisableCheckpoints: true, PeerNotifier: recoveryNotifier{}})
	require.NoError(t, err)
	p, err := peer.NewOutboundPeer(&peer.Config{ChainParams: &params}, "127.0.0.1:19527")
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			sm.syncPeer.Store(p)
			sm.syncPeer.Store(nil)
		}
	}()
	require.NotPanics(t, func() {
		for i := 0; i < 10000; i++ {
			sm.handleBlockchainNotification(&blockchain.Notification{Type: blockchain.NTBlockAccepted, Data: btcutil.NewBlock(params.GenesisBlock)})
		}
	})
	<-done
}
