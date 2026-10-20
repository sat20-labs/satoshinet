package contract_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/btcsuite/websocket"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/stretchr/testify/require"
)

// One deadline covers confirmation and block synchronization. On timeout the
// clients are shut down to release synchronous RPCs before joining the worker.
// The caller must fail the test after this error; these clients are not reusable.
func waitForPOSTxAndSync(ctx context.Context, node *rpctest.Harness, nodes []*rpctest.Harness, txHash *chainhash.Hash) error {
	phase := "transaction confirmation"
	done := make(chan error, 1)
	go func() {
		for {
			if err := ctx.Err(); err != nil {
				done <- err
				return
			}
			verbose, err := node.Client.GetRawTransactionVerbose(txHash)
			if err == nil && verbose.Confirmations >= 1 {
				phase = "block synchronization"
				done <- rpctest.JoinNodes(nodes, rpctest.Blocks)
				return
			}
			phase = fmt.Sprintf("transaction confirmation (last RPC error: %v)", err)
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				done <- ctx.Err()
				return
			case <-timer.C:
			}
		}
	}()

	shutdown := func() {
		node.Client.Shutdown()
		for _, n := range nodes {
			if n != nil && n.Client != nil {
				n.Client.Shutdown()
			}
		}
	}
	var err error
	select {
	case err = <-done:
		if ctx.Err() != nil {
			shutdown()
		}
	case <-ctx.Done():
		shutdown()
		err = <-done
	}
	// Do not accept a late confirmation or synchronization after the deadline.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", phase, ctxErr)
	}
	return err
}

func TestWaitForPOSTxAndSync(t *testing.T) {
	for _, test := range []struct {
		name          string
		height        int
		hash          int
		confirmations int
		noResponse    string
		wantTimeout   bool
	}{
		{name: "confirmed and same tips", height: 7, hash: 7, confirmations: 1},
		{name: "different heights", height: 6, hash: 7, confirmations: 1, wantTimeout: true},
		{name: "different hashes at same height", height: 7, hash: 8, confirmations: 1, wantTimeout: true},
		{name: "not confirmed", height: 7, hash: 7, wantTimeout: true},
		{name: "confirmation RPC never responds", height: 7, hash: 7, noResponse: "getrawtransaction", wantTimeout: true},
		{name: "sync RPC never responds", height: 7, hash: 7, confirmations: 1, noResponse: "getbestblock", wantTimeout: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			newNode := func(height, hash int) *rpctest.Harness {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ws, err := websocket.Upgrade(w, r, nil, 1024, 1024)
					if err != nil {
						return
					}
					defer ws.Close()
					for {
						_, data, err := ws.ReadMessage()
						if err != nil {
							return
						}
						var req struct {
							ID     json.RawMessage `json:"id"`
							Method string          `json:"method"`
						}
						if json.Unmarshal(data, &req) != nil {
							return
						}
						requests.Add(1)
						if req.Method == test.noResponse {
							continue
						}
						var result interface{}
						switch req.Method {
						case "getrawtransaction":
							result = map[string]interface{}{"confirmations": test.confirmations}
						case "getbestblock":
							result = map[string]interface{}{"height": height, "hash": fmt.Sprintf("%064x", hash)}
						default:
							return
						}
						reply, err := json.Marshal(map[string]interface{}{"id": req.ID, "error": nil, "result": result})
						if err != nil || ws.WriteMessage(websocket.TextMessage, reply) != nil {
							return
						}
					}
				}))
				t.Cleanup(server.Close)
				client, err := rpcclient.New(&rpcclient.ConnConfig{
					Host: strings.TrimPrefix(server.URL, "http://"), Endpoint: "ws",
					User: "test", Pass: "test", DisableTLS: true, DisableAutoReconnect: true,
				}, nil)
				require.NoError(t, err)
				t.Cleanup(func() { client.Shutdown(); client.WaitForShutdown() })
				return &rpctest.Harness{Client: client}
			}
			node := newNode(7, 7)
			nodes := []*rpctest.Harness{node, newNode(test.height, test.hash)}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			start := time.Now()
			err := waitForPOSTxAndSync(ctx, node, nodes, &chainhash.Hash{})
			if test.wantTimeout {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				// The helper must also release a blocked RPC and join its worker.
				require.Less(t, time.Since(start), 5*time.Second)
				_, _, rpcErr := node.Client.GetBestBlock()
				require.ErrorIs(t, rpcErr, rpcclient.ErrClientShutdown)
			} else {
				require.NoError(t, err)
				_, _, rpcErr := node.Client.GetBestBlock()
				require.NoError(t, rpcErr)
			}
			require.Positive(t, requests.Load(), "must exercise the real RPC client")
		})
	}
}
