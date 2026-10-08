package main

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	idxcommon "github.com/sat20-labs/indexer/common"
	idxdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	assetindexer "github.com/sat20-labs/satoshinet/indexer/indexer"
	dkvsp2p "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs/p2p"
	"github.com/sat20-labs/satoshinet/mempool"
	"github.com/sat20-labs/satoshinet/mining/posminer"
	"github.com/stretchr/testify/require"
)

func TestServerShutdownDrainsIndexCallbacksAndClosesDB(t *testing.T) {
	previousChain, previousCfg := idxcommon.CHAIN, cfg
	cfg = &config{}
	t.Cleanup(func() { idxcommon.CHAIN = previousChain; cfg = previousCfg })
	path := t.TempDir()
	interrupt := make(chan struct{})
	mgr := assetindexer.NewIndexerMgr(&assetindexer.Config{DataPath: path}, true, interrupt)
	mgr.Init()
	chainDB, err := database.Create("ffldb", t.TempDir(), chaincfg.TestNetParams.Net)
	require.NoError(t, err)
	t.Cleanup(func() { chainDB.Close() })
	rpc := &rpcServer{quit: make(chan int)}
	rpc.ntfnMgr = newWsNotificationManager(rpc)
	// Hold the existing RPC shutdown wait to model an in-flight notification
	// handler while a second Stop caller enters the node's final shutdown.
	rpc.ntfnMgr.wg.Add(1)
	s := &server{assetIndexer: mgr, quit: make(chan struct{}), rpcServer: rpc, db: chainDB, feeEstimator: mempool.NewFeeEstimator(1, 1), posMiner: &posminer.POSMiner{}}
	sp := &serverPeer{server: s}
	entered, release, callbackDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, workerOnce sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	unblockWorker := func() { workerOnce.Do(rpc.ntfnMgr.wg.Done) }
	t.Cleanup(func() { unblockWorker(); unblock(); <-callbackDone; mgr.Close() })
	writeResult := make(chan error, 1)
	go func() {
		defer close(callbackDone)
		sp.withDKVSHandler(func(dkvsp2p.Handler) {
			close(entered)
			<-release
			writeResult <- mgr.GetBaseDB().Write([]byte("last-callback"), []byte("persisted"))
		})
	}()
	<-entered
	firstStopped := make(chan error, 1)
	go func() { firstStopped <- s.Stop() }()
	select {
	case <-rpc.ntfnMgr.quit:
	case <-time.After(time.Second):
		t.Fatal("first Stop did not reach RPC shutdown")
	}
	require.NoError(t, s.Stop()) // Already shutting down, while the first caller waits.
	close(interrupt)
	stopped := make(chan struct{})
	go func() { s.WaitForShutdown(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("database closed before background tasks stopped")
	case <-time.After(30 * time.Millisecond):
	}
	unblockWorker()
	select {
	case <-stopped:
		t.Fatal("database closed under an active DKVS callback")
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("node shutdown did not finish")
	}
	require.NoError(t, <-firstStopped)
	require.NoError(t, <-writeResult)
	// Late callbacks must not acquire a store after closure.
	sp.withDKVSHandler(func(dkvsp2p.Handler) { t.Error("callback entered after shutdown") })
	s.WaitForShutdown()
	reopened := idxdb.NewKVDB(filepath.Join(path, "db/indexer", chaincfg.TestNetParams.Name, "base"))
	require.NotNil(t, reopened)
	defer reopened.Close()
	value, err := reopened.Read([]byte("last-callback"))
	require.NoError(t, err)
	require.Equal(t, "persisted", string(value))
}

func TestServerQueriesReturnDuringShutdown(t *testing.T) {
	cases := []struct {
		name string
		call func(*testing.T, *server)
	}{
		{"count", func(t *testing.T, s *server) { require.Zero(t, s.ConnectedCount()) }},
		{"peer-id", func(t *testing.T, s *server) { require.Nil(t, s.GetPeerById(1)) }},
		{"validator", func(t *testing.T, s *server) { require.Nil(t, s.GetPeerByValidatorId("test")) }},
		{"core", func(t *testing.T, s *server) { require.Nil(t, s.GetRandomCorePeer()) }},
		{"group", func(t *testing.T, s *server) { require.Zero(t, s.OutboundGroupCount("test")) }},
		{"rpc-connect", func(t *testing.T, s *server) {
			require.Error(t, (&rpcConnManager{server: s}).Connect("127.0.0.1:1", false))
		}},
		{"rpc-remove-id", func(t *testing.T, s *server) { require.Error(t, (&rpcConnManager{server: s}).RemoveByID(1)) }},
		{"rpc-remove-address", func(t *testing.T, s *server) { require.Error(t, (&rpcConnManager{server: s}).RemoveByAddr("test")) }},
		{"rpc-disconnect-id", func(t *testing.T, s *server) { require.Error(t, (&rpcConnManager{server: s}).DisconnectByID(1)) }},
		{"rpc-disconnect-address", func(t *testing.T, s *server) { require.Error(t, (&rpcConnManager{server: s}).DisconnectByAddr("test")) }},
		{"rpc-peers", func(t *testing.T, s *server) { require.Empty(t, (&rpcConnManager{server: s}).ConnectedPeers()) }},
		{"rpc-persistent", func(t *testing.T, s *server) { require.Empty(t, (&rpcConnManager{server: s}).PersistentPeers()) }},
	}
	for _, c := range cases {
		for _, pendingReply := range []bool{false, true} {
			t.Run(c.name+map[bool]string{false: "/send", true: "/reply"}[pendingReply], func(t *testing.T) {
				s := &server{query: make(chan interface{}), quit: make(chan struct{})}
				done := make(chan struct{})
				go func() { defer close(done); c.call(t, s) }()
				var message interface{}
				if pendingReply {
					select {
					case message = <-s.query:
					case <-time.After(time.Second):
						t.Fatal("query not sent")
					}
				}
				close(s.quit)
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("query waited for exited peer handler")
				}
				if pendingReply {
					// A buffered reply still permits peerHandler to finish if it handled
					// the query after the caller observed quit.
					replied := make(chan struct{})
					go func() {
						defer close(replied)
						switch msg := message.(type) {
						case getConnCountMsg:
							msg.reply <- 0
						case getPeerMsg:
							msg.reply <- nil
						case getPeerByValidatorIdMsg:
							msg.reply <- nil
						case getOutboundGroup:
							msg.reply <- 0
						case connectNodeMsg:
							msg.reply <- nil
						case removeNodeMsg:
							msg.reply <- nil
						case disconnectNodeMsg:
							msg.reply <- nil
						case getPeersMsg:
							msg.reply <- nil
						case getAddedNodesMsg:
							msg.reply <- nil
						}
					}()
					select {
					case <-replied:
					case <-time.After(time.Second):
						t.Fatal("peer handler blocked on abandoned reply")
					}
				}
			})
		}
	}
}
