package satsnet_rpc

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/stretchr/testify/require"
)

func TestShutdownSatsNetClientBeforeInitialization(t *testing.T) {
	_client.mutex.Lock()
	client := _client.client
	_client.client = nil
	_client.mutex.Unlock()
	t.Cleanup(func() {
		_client.mutex.Lock()
		_client.client = client
		_client.mutex.Unlock()
	})
	require.NotPanics(t, ShutdownSatsNetClient)
	require.NotPanics(t, ShutdownSatsNetClient)
}

func TestShutdownSatsNetClientDuringInitialization(t *testing.T) {
	for _, stage := range []string{"handshake", "notifyblocks", "getblockcount"} {
		t.Run(stage, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			upgrader := websocket.Upgrader{}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if stage == "handshake" {
					close(entered)
					<-release
				}
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
					if request.Method == stage {
						close(entered)
						<-release
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
			t.Cleanup(func() { unblock(); ShutdownSatsNetClient(); backend.Close() })
			host, portText, err := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
			require.NoError(t, err)
			port, err := strconv.Atoi(portText)
			require.NoError(t, err)
			initialized := make(chan error, 1)
			go func() {
				_, err := InitSatsNetClient(host, port, "test", "test", "", false)
				initialized <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("initialization did not reach " + stage)
			}
			ShutdownSatsNetClient()
			ShutdownSatsNetClient()
			unblock()
			select {
			case err := <-initialized:
				require.ErrorIs(t, err, rpcclient.ErrClientShutdown)
			case <-time.After(time.Second):
				t.Fatal("shutdown left initialization blocked at " + stage)
			}
		})
	}
}
