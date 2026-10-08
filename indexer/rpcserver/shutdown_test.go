package rpcserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	idxdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/indexer/indexer"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
	"github.com/stretchr/testify/require"
)

func TestIndexerCloseWaitsForActiveHTTPReader(t *testing.T) {
	dataPath := t.TempDir()
	mgr := indexer.NewIndexerMgr(&indexer.Config{DataPath: dataPath}, true, nil)
	mgr.Init()
	rpc := NewRpc(mgr)
	require.NoError(t, rpc.Start("127.0.0.1:0", "testnet", ""))
	mgr.SetRPCShutdown(rpc.Stop)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	t.Cleanup(func() { unblock(); require.NoError(t, mgr.Close()) })
	rpc.server.Handler.(*gin.Engine).GET("/shutdown-reader", func(c *gin.Context) {
		close(entered)
		<-release
		if err := mgr.GetBaseDB().Write([]byte("reader-finished"), []byte("persisted")); err != nil {
			c.Status(500)
			return
		}
		c.Status(200)
	})
	read := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + rpc.server.Addr + "/shutdown-reader")
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				err = io.ErrUnexpectedEOF
			}
		}
		read <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- mgr.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("closed under an HTTP reader: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblock()
	require.Error(t, <-read, "Stop forcibly closes the client's connection")
	require.NoError(t, <-closed)
	reopened := idxdb.NewKVDB(dataPath + "/db/indexer/testnet/base")
	require.NotNil(t, reopened)
	defer reopened.Close()
	data, err := reopened.Read([]byte("reader-finished"))
	require.NoError(t, err)
	require.Equal(t, "persisted", string(data))
	listener, err := net.Listen("tcp", rpc.server.Addr)
	require.NoError(t, err)
	listener.Close()
}

func TestIndexerRPCStopInterruptsIncompleteBody(t *testing.T) {
	rpc := NewRpc(&indexer.IndexerMgr{})
	require.NoError(t, rpc.Start("127.0.0.1:0", "testnet", ""))
	entered, finished := make(chan struct{}), make(chan struct{})
	rpc.server.Handler.(*gin.Engine).POST("/shutdown-body", func(c *gin.Context) {
		defer close(finished)
		// Consume the first byte so shutdown overlaps a body read, rather
		// than merely an accepted connection that has not reached a handler.
		var first [1]byte
		_, err := io.ReadFull(c.Request.Body, first[:])
		if err != nil {
			return
		}
		close(entered)
		_, _ = io.Copy(io.Discard, c.Request.Body)
	})
	conn, err := net.Dial("tcp", rpc.server.Addr)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(); rpc.server.Close(); rpc.Stop() })
	_, err = fmt.Fprintf(conn, "POST /shutdown-body HTTP/1.1\r\nHost: localhost\r\nContent-Length: 100\r\n\r\n{")
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request body read did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- rpc.Stop() }()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Stop waited for the client to finish its request body")
	}
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before the body reader exited")
	}
}

func TestIndexerCloseCancelsDisconnectedInternalRPC(t *testing.T) {
	requested := make(chan struct{})
	upgrader := websocket.Upgrader{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if conn.ReadJSON(&request) != nil {
				return
			}
			if request.Method == "getblockhash" {
				close(requested)
				return // Lose the pending response as when the local node RPC stops.
			}
			var result interface{}
			if request.Method == "getblockcount" {
				result = 1
			}
			if conn.WriteJSON(map[string]interface{}{"id": request.ID, "error": nil, "result": result}) != nil {
				return
			}
		}
	}))
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	_, err = satsnet_rpc.InitSatsNetClient(host, port, "test", "test", "", false)
	require.NoError(t, err)
	mgr := &indexer.IndexerMgr{}
	rpc := NewRpc(mgr)
	require.NoError(t, rpc.Start("127.0.0.1:0", "testnet", ""))
	mgr.SetRPCShutdown(rpc.Stop)
	t.Cleanup(func() {
		satsnet_rpc.ShutdownSatsNetClient()
		backend.Close()
		rpc.server.Close()
		mgr.Close()
	})
	requestFinished := make(chan struct{})
	go func() {
		defer close(requestFinished)
		resp, err := http.Get("http://" + rpc.server.Addr + "/testnet/btc/block/blockhash/1")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("indexer HTTP handler did not call the internal RPC")
	}
	backend.Close()
	closed := make(chan error, 1)
	go func() { closed <- mgr.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("indexer Close waited for the stopped local RPC to reconnect")
	}
	select {
	case <-requestFinished:
	case <-time.After(time.Second):
		t.Fatal("HTTP client did not observe shutdown")
	}
}

func TestIndexerRPCStopCancelsWatchRequest(t *testing.T) {
	rpc := NewRpc(&indexer.IndexerMgr{})
	require.NoError(t, rpc.Stop())
	require.NoError(t, rpc.Start("127.0.0.1:0", "testnet", ""))
	t.Cleanup(func() { require.NoError(t, rpc.Stop()) })
	entered, finished := make(chan struct{}), make(chan struct{})
	rpc.server.Handler.(*gin.Engine).GET("/shutdown-watch", func(c *gin.Context) { close(entered); <-c.Request.Context().Done(); close(finished); c.Status(204) })
	read := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + rpc.server.Addr + "/shutdown-watch")
		if err == nil {
			resp.Body.Close()
		}
		read <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not start")
	}
	require.NoError(t, rpc.Stop())
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before the watch exited")
	}
	<-read // Force-close may win over the watch handler's final response.
	require.NoError(t, rpc.Stop())
}

func TestIndexerRPCStartReportsOccupiedListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	rpc := NewRpc(&indexer.IndexerMgr{})
	require.Error(t, rpc.Start(listener.Addr().String(), "testnet", ""))
	require.NoError(t, rpc.Stop())
}
