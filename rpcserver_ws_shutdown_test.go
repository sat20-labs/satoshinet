package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/websocket"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestWebsocketShutdownWaitsForInFlightRequest(t *testing.T) {
	previousCfg := cfg
	cfg = &config{RPCMaxConcurrentReqs: 1}
	t.Cleanup(func() { cfg = previousCfg })
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	previous := rpcHandlers["getblockcount"]
	rpcHandlers["getblockcount"] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) {
		close(entered)
		<-release
		return int32(0), nil
	}
	t.Cleanup(func() { rpcHandlers["getblockcount"] = previous })
	rpc := &rpcServer{}
	rpc.ntfnMgr = newWsNotificationManager(rpc)
	rpc.ntfnMgr.Start()
	t.Cleanup(func() { rpc.ntfnMgr.Shutdown(); rpc.ntfnMgr.WaitForShutdown() })
	clients := make(chan *wsClient, 1)
	exited := make(chan struct{})
	upgrader := websocket.Upgrader{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		client, err := newWebsocketClient(rpc, conn, r.RemoteAddr, true, true)
		if err != nil {
			conn.Close()
			return
		}
		client.Start()
		clients <- client
		client.WaitForShutdown()
		close(exited)
	}))
	t.Cleanup(httpServer.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.Close()
	client := <-clients
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"1.0","id":1,"method":"getblockcount","params":[]}`)))
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC did not start")
	}
	client.Disconnect()
	early := false
	select {
	case <-exited:
		early = true
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	if !early {
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			t.Fatal("RPC did not finish")
		}
	}
	require.False(t, early, "WebSocket shutdown returned while an async RPC still accessed node state")
}

func TestWebsocketHandshakeRegistrationDuringShutdown(t *testing.T) {
	previousCfg := cfg
	cfg = &config{RPCMaxConcurrentReqs: 1, RPCMaxWebsockets: 2}
	t.Cleanup(func() { cfg = previousCfg })
	rpc := &rpcServer{quit: make(chan int)}
	rpc.ntfnMgr = newWsNotificationManager(rpc)
	upgraded, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	upgrader := websocket.Upgrader{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(upgraded)
		<-release
		rpc.WebsocketHandler(conn, r.RemoteAddr, true, true)
		close(exited)
	}))
	defer httpServer.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.Close()
	<-upgraded
	rpc.ntfnMgr.Shutdown()
	close(rpc.quit)
	// This connection was already hijacked, but AddClient has not yet run.
	unblock()
	select {
	case <-exited:
	case <-time.After(time.Second):
		// Release the old unconditional send on failure, leaving no stuck fixture.
		select {
		case <-rpc.ntfnMgr.queueNotification:
		case <-time.After(time.Second):
		}
		select {
		case <-exited:
		case <-time.After(time.Second):
		}
		t.Fatal("accepted WebSocket blocked registering with a stopped manager")
	}
}

func TestWebsocketSubscriptionDuringShutdown(t *testing.T) {
	for _, method := range []string{"notifyblocks", "notifyreceived"} {
		t.Run(method, func(t *testing.T) {
			previousCfg := cfg
			cfg = &config{RPCMaxConcurrentReqs: 1, RPCMaxWebsockets: 2}
			t.Cleanup(func() { cfg = previousCfg })
			rpc := &rpcServer{quit: make(chan int), cfg: rpcserverConfig{ChainParams: &chaincfg.TestNetParams}}
			rpc.ntfnMgr = newWsNotificationManager(rpc)
			rpc.ntfnMgr.Start()
			entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			previous := wsHandlers[method]
			wsHandlers[method] = func(c *wsClient, cmd interface{}) (interface{}, error) {
				close(entered)
				<-release
				return previous(c, cmd)
			}
			t.Cleanup(func() { wsHandlers[method] = previous })
			upgrader := websocket.Upgrader{}
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rpc.wg.Add(1)
				defer rpc.wg.Done()
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				rpc.WebsocketHandler(conn, r.RemoteAddr, true, true)
				close(exited)
			}))
			defer httpServer.Close()
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpServer.URL, "http"), nil)
			require.NoError(t, err)
			defer conn.Close()
			params := `[]`
			if method == "notifyreceived" {
				address, err := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.TestNetParams)
				require.NoError(t, err)
				params = fmt.Sprintf(`[["%s"]]`, address.EncodeAddress())
			}
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"jsonrpc":"1.0","id":1,"method":"%s","params":%s}`, method, params))))
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("subscription did not start")
			}
			stopped := make(chan error, 1)
			go func() { stopped <- rpc.Stop() }()
			<-rpc.quit // notification manager has exited; request is still gated.
			unblock()
			select {
			case err := <-stopped:
				require.NoError(t, err)
			case <-time.After(time.Second):
				select {
				case <-rpc.ntfnMgr.queueNotification:
				case <-time.After(time.Second):
				}
				select {
				case <-stopped:
				case <-time.After(time.Second):
				}
				t.Fatal("RPC shutdown blocked on an in-flight subscription")
			}
			<-exited
		})
	}
}

func TestNotificationRegistrationsObserveShutdown(t *testing.T) {
	cases := []struct {
		name string
		call func(*wsNotificationManager)
	}{
		{"client", func(m *wsNotificationManager) { m.AddClient(nil) }},
		{"blocks", func(m *wsNotificationManager) { m.RegisterBlockUpdates(nil) }},
		{"stop-blocks", func(m *wsNotificationManager) { m.UnregisterBlockUpdates(nil) }},
		{"transactions", func(m *wsNotificationManager) { m.RegisterNewMempoolTxsUpdates(nil) }},
		{"stop-transactions", func(m *wsNotificationManager) { m.UnregisterNewMempoolTxsUpdates(nil) }},
		{"spent", func(m *wsNotificationManager) { m.RegisterSpentRequests(nil, nil) }},
		{"stop-spent", func(m *wsNotificationManager) { m.UnregisterSpentRequest(nil, &wire.OutPoint{}) }},
		{"received", func(m *wsNotificationManager) { m.RegisterTxOutAddressRequests(nil, []string{"test"}) }},
		{"stop-received", func(m *wsNotificationManager) { m.UnregisterTxOutAddressRequest(nil, "test") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newWsNotificationManager(&rpcServer{})
			entered, done := make(chan struct{}), make(chan struct{})
			go func() { close(entered); c.call(m); close(done) }()
			<-entered
			// No consumer: shutdown must release either an already queued sender or
			// a sender that reaches the queue after quit.
			m.Shutdown()
			select {
			case <-done:
			case <-time.After(100 * time.Millisecond):
				<-m.queueNotification // release the old send so the red test cannot leak
				<-done
				t.Fatal("queue send ignored notification-manager shutdown")
			}
		})
	}
}
