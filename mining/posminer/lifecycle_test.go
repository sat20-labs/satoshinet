package posminer

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	_ "github.com/sat20-labs/satoshinet/database/ffldb"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	shareindexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/mining"
	peerpkg "github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func lifecycleMiner(t *testing.T) *POSMiner {
	t.Helper()
	params := chaincfg.RegressionNetParams
	genesis := params.GenesisBlock.BlockHash()
	params.GenesisHash = &genesis
	params.POSV2Height = 1
	params.Bech32HRPSegwit = chaincfg.TestNetParams.Bech32HRPSegwit
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: &genesis}}
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: &params, TimeSource: blockchain.NewMedianTime()})
	require.NoError(t, err)
	key, _ := btcec.PrivKeyFromBytes([]byte{1})
	pub := hex.EncodeToString(key.PubKey().SerializeCompressed())
	seq := scommon.NewMiningSequenceMgr(&params)
	require.NoError(t, seq.Init(map[string]*scommon.CoreNodeInfo{pub: scommon.NewCoreNodeInfo(nil)}, 0, ""))
	previous := shareindexer.ShareIndexer
	shareindexer.ShareIndexer = &replaceableMiningIndexer{seq: seq}
	t.Cleanup(func() { shareindexer.ShareIndexer = previous })
	addr, err := btcutil.DecodeAddress(seq.GetCurrentMiningAddr(), &params)
	require.NoError(t, err)
	g := mining.NewBlkTmplGenerator(&mining.Policy{}, &params, nil, chain, blockchain.NewMedianTime(), nil, nil)
	m := New(&Config{ChainParams: &params, Chain: chain, BlockTemplateGenerator: g, MiningAddr: addr, MiningPubKey: pub})
	t.Cleanup(func() { m.Stop(); m.WaitForShutdown() })
	return m
}

func TestPOSMinerStatusRepeatedStartStopAndRestart(t *testing.T) {
	m := lifecycleMiner(t)
	require.NoError(t, m.Start())
	done := make(chan struct{})
	go func() {
		defer close(done)
		require.Zero(t, m.HashesPerSecond())
		require.True(t, m.IsMining())
		m.SetNumWorkers(1) // setgenerate true repeats this before Start.
		require.NoError(t, m.Start())
		require.Equal(t, int32(1), m.NumWorkers())
		m.Stop()
		require.False(t, m.IsMining())
		require.NoError(t, m.Start())
		m.SetNumWorkers(0)
		require.False(t, m.IsMining())
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("POS status / repeated start permanently blocked on obsolete worker channels")
	}
}

func TestPOSMinerQueryConcurrentWithStop(t *testing.T) {
	m := lifecycleMiner(t)
	require.NoError(t, m.Start())
	done := make(chan struct{}, 2)
	go func() {
		for i := 0; i < 100; i++ {
			m.HashesPerSecond()
			m.IsMining()
			m.NumWorkers()
		}
		done <- struct{}{}
	}()
	go func() { m.Stop(); done <- struct{}{} }()
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("concurrent POS status and stop blocked")
		}
	}
	require.False(t, m.IsMining())
}

func TestGenerateNBlocksRejectsPOSV2WithoutApproval(t *testing.T) {
	m := lifecycleMiner(t)
	before := m.cfg.Chain.BestSnapshot()
	hashes, err := m.GenerateNBlocks(1)
	require.ErrorContains(t, err, "Bootstrap approval")
	require.Empty(t, hashes)
	require.Equal(t, before.Hash, m.cfg.Chain.BestSnapshot().Hash)
	require.False(t, m.IsMining())
}

func TestPOSMinerCandidateDuringDiscreteGeneration(t *testing.T) {
	for _, priorStart := range []bool{false, true} {
		name := "first-generate"
		if priorStart {
			name = "generate-after-stop"
		}
		t.Run(name, func(t *testing.T) {
			m := lifecycleMiner(t)
			if priorStart {
				require.NoError(t, m.Start())
				m.Stop()
				// A non-nil manager must not receive discrete-mode messages.
				// Replace only the retained pointer with a poison manager;
				// leave the stopped manager's background cleanup untouched.
				m.Lock()
				m.validatorMgr = &ValidatorManager{}
				m.Unlock()
			} else {
				require.Nil(t, m.validatorMgr)
			}
			p := peerpkg.NewInboundPeer(&peerpkg.Config{ChainParams: m.cfg.ChainParams})
			msg := &wire.MsgMineBlock{Nonce: 1, SubCmd: wire.CmdBlock}
			// Pause the real generate call after it publishes its running
			// flags, before the activation check or template construction.
			m.submitBlockLock.Lock()
			generated := make(chan error, 1)
			go func() {
				_, err := m.GenerateNBlocks(1)
				generated <- err
			}()
			defer func() {
				m.submitBlockLock.Unlock()
				select {
				case err := <-generated:
					require.ErrorContains(t, err, "Bootstrap approval")
				case <-time.After(3 * time.Second):
					t.Error("discrete generation did not finish after releasing submission lock")
				}
				require.False(t, m.IsMining())
			}()
			require.Eventually(t, m.IsMining, time.Second, time.Millisecond)
			m.Lock()
			discrete := m.discreteMining
			m.Unlock()
			require.True(t, discrete)
			require.NotPanics(t, func() { m.OnBlockGenerated(p, msg) })
		})
	}
}

func TestPOSMinerCandidateConcurrentWithStopAndRestart(t *testing.T) {
	m := lifecycleMiner(t)
	p := peerpkg.NewInboundPeer(&peerpkg.Config{ChainParams: m.cfg.ChainParams})
	msg := &wire.MsgMineBlock{Nonce: 1, SubCmd: wire.CmdBlock}
	// Invalid payload takes the normal manager's rejection path without
	// changing the chain, including when a callback overlaps Stop.
	require.NotPanics(t, func() { m.OnBlockGenerated(p, msg) })
	require.NoError(t, m.Start())
	t.Cleanup(m.Stop)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			m.OnBlockGenerated(p, msg)
		}
	}()
	for i := 0; i < 25; i++ {
		m.Stop()
		m.OnBlockGenerated(p, msg)
		require.NoError(t, m.Start())
		m.OnBlockGenerated(p, msg)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("candidate callback blocked while stopping and restarting")
	}
	m.Stop()
	require.False(t, m.IsMining())
}
