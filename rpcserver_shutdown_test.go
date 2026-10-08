package main

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/websocket"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/mining"
	"github.com/stretchr/testify/require"
)

// Use the production listener and handlers: both JSON-RPC and WebSocket hijack
// HTTP connections, so http.Server.Shutdown alone cannot drain these requests.
func TestRPCShutdownWaitsForRequests(t *testing.T) {
	for _, transport := range []string{"HTTP", "WebSocket"} {
		t.Run(transport, func(t *testing.T) {
			previousCfg := cfg
			cfg = &config{RPCMaxClients: 10, RPCMaxWebsockets: 10, RPCMaxConcurrentReqs: 1}
			t.Cleanup(func() { cfg = previousCfg })
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			previous := rpcHandlers["getblockcount"]
			rpcHandlers["getblockcount"] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) {
				close(entered)
				<-release
				defer close(finished)
				return int32(0), nil
			}
			t.Cleanup(func() { rpcHandlers["getblockcount"] = previous })
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte("shutdown-test:local-fixture"))
			rpc := &rpcServer{cfg: rpcserverConfig{Listeners: []net.Listener{listener}}, quit: make(chan int), authsha: sha256.Sum256([]byte(authorization)), statusLines: make(map[int]string)}
			rpc.ntfnMgr = newWsNotificationManager(rpc)
			rpc.Start()
			stopped := make(chan error, 1)
			t.Cleanup(func() { unblock(); rpc.Stop() })
			request := `{"jsonrpc":"1.0","id":1,"method":"getblockcount","params":[]}`
			clientDone := make(chan struct{})
			if transport == "WebSocket" {
				conn, _, err := websocket.DefaultDialer.Dial("ws://"+listener.Addr().String()+"/ws", http.Header{"Authorization": []string{authorization}})
				require.NoError(t, err)
				t.Cleanup(func() { conn.Close() })
				require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(request)))
				close(clientDone)
			} else {
				go func() {
					defer close(clientDone)
					req, err := http.NewRequest("POST", "http://"+listener.Addr().String(), strings.NewReader(request))
					if err != nil {
						return
					}
					req.Header.Set("Authorization", authorization)
					response, err := http.DefaultClient.Do(req)
					if err == nil {
						io.Copy(io.Discard, response.Body)
						response.Body.Close()
					}
				}()
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("RPC did not start")
			}
			go func() { stopped <- rpc.Stop() }()
			early := false
			select {
			case <-stopped:
				early = true
			case <-time.After(50 * time.Millisecond):
			}
			unblock()
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not finish")
			}
			if !early {
				select {
				case err := <-stopped:
					require.NoError(t, err)
				case <-time.After(3 * time.Second):
					t.Fatal("RPC shutdown did not finish")
				}
			}
			<-clientDone
			require.False(t, early, "RPC Stop returned while a handler was still accessing node state")
			reopened, err := net.Listen("tcp", listener.Addr().String())
			require.NoError(t, err)
			require.NoError(t, reopened.Close())
		})
	}
}

// Embed the existing source interface; the retained template only reads its
// last-update timestamp, and never selects transactions in this test.
type shutdownTxSource struct {
	mining.TxSource
	updated time.Time
}

func (s shutdownTxSource) LastUpdated() time.Time { return s.updated }

func TestRPCShutdownCancelsTemplateLongPoll(t *testing.T) {
	params := chaincfg.RegressionNetParams
	genesis := params.GenesisBlock.BlockHash()
	params.GenesisHash = &genesis
	params.Checkpoints = []chaincfg.Checkpoint{{Height: 0, Hash: &genesis}}
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	defer db.Close()
	timeSource := blockchain.NewMedianTime()
	chain, err := blockchain.New(&blockchain.Config{DB: db, ChainParams: &params, TimeSource: timeSource})
	require.NoError(t, err)
	now := time.Now()
	source := shutdownTxSource{updated: now}
	generator := mining.NewBlkTmplGenerator(&mining.Policy{}, &params, source, chain, timeSource, nil, nil)
	state := newGbtWorkState(timeSource)
	block := *params.GenesisBlock
	state.template = &mining.BlockTemplate{Block: &block}
	best := chain.BestSnapshot()
	state.prevHash, state.lastGenerated, state.lastTxUpdate = &best.Hash, now, now
	rpc := &rpcServer{cfg: rpcserverConfig{Chain: chain, Generator: generator}, gbtWorkState: state, quit: make(chan int)}
	result := make(chan error, 1)
	go func() {
		_, err := handleGetBlockTemplateLongPoll(rpc, encodeTemplateID(&block.Header.PrevBlock, now), true, make(chan struct{}))
		result <- err
	}()
	// Ensure the production handler reached its long-poll wait before quitting.
	deadline := time.After(time.Second)
	for {
		state.Lock()
		waiting := len(state.notifyMap) > 0
		state.Unlock()
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("long poll returned early: %v", err)
		case <-deadline:
			t.Fatal("long poll did not start")
		case <-time.After(time.Millisecond):
		}
	}
	close(rpc.quit)
	select {
	case err := <-result:
		require.ErrorIs(t, err, ErrClientQuit)
	case <-time.After(time.Second):
		t.Fatal("long poll did not observe RPC shutdown")
	}
}
