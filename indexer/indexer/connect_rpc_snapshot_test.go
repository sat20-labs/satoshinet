package indexer

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/base"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both RPC-only sync and concurrent supplied blocks must retain the snapshot
// owning AddressValueV2.Op until it is committed. Reopen the database to avoid
// accidentally verifying only the live address cache.
func TestIndexerRPCPreservesPendingAddressSnapshot(t *testing.T) {
	for _, concurrent := range []bool{true, false} {
		t.Run(fmt.Sprintf("concurrent=%v", concurrent), func(t *testing.T) {
			oldChain := indexercommon.CHAIN
			indexercommon.CHAIN = "testnet"
			t.Cleanup(func() { indexercommon.CHAIN = oldChain })
			params := chaincfg.TestNetParams
			path := t.TempDir()
			db := indexerdb.NewKVDB(path)
			t.Cleanup(func() { db.Close() })
			compiling := base.NewBaseIndexer(db, &params, 0, 30)
			compiling.Init()
			compiling.SetBlockCallback(func(*common.Block) {})
			compiling.SetUpdateDBCallback(func() {})
			blocks := make([]*wire.MsgBlock, 102)
			blocks[0] = params.GenesisBlock
			address, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{7}, 20), &params)
			require.NoError(t, err)
			script, err := txscript.PayToAddrScript(address)
			require.NoError(t, err)
			for h := 1; h < len(blocks); h++ {
				tx := wire.NewMsgTx(1)
				tx.LockTime = uint32(h)
				tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: ^uint32(0)}, []byte{1, byte(h)}, nil))
				pkScript := []byte{txscript.OP_TRUE}
				if h == 70 {
					pkScript = script
				}
				tx.AddTxOut(wire.NewTxOut(1000, nil, pkScript))
				blocks[h] = &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: blocks[h-1].BlockHash(), Nonce: uint32(h)}, Transactions: []*wire.MsgTx{tx}}
			}
			for h := 0; h <= 61; h++ {
				require.NoError(t, compiling.SyncBlock(blocks[h], h, 100, false))
			}
			compiling.UpdateDB()
			require.Equal(t, 61, compiling.GetSyncHeight())
			mgr := &IndexerMgr{baseDB: db, chaincfgParam: &params, compiling: compiling, rpcService: base.NewRpcIndexer(compiling)}
			fetching, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				for {
					var req struct {
						ID     json.RawMessage   `json:"id"`
						Method string            `json:"method"`
						Params []json.RawMessage `json:"params"`
					}
					if conn.ReadJSON(&req) != nil {
						return
					}
					var result any
					switch req.Method {
					case "getblockcount":
						result = 61
					case "getblockhash":
						var h int
						_ = json.Unmarshal(req.Params[0], &h)
						result = blocks[h].BlockHash().String()
					case "getblock":
						var hash string
						_ = json.Unmarshal(req.Params[0], &hash)
						for h := 62; h <= 100; h++ {
							if blocks[h].BlockHash().String() == hash {
								if concurrent && h == 63 {
									close(fetching)
									<-release
								}
								var raw bytes.Buffer
								_ = blocks[h].Serialize(&raw)
								result = hex.EncodeToString(raw.Bytes())
								break
							}
						}
					}
					if conn.WriteJSON(map[string]any{"id": req.ID, "result": result, "error": nil}) != nil {
						return
					}
				}
			}))
			t.Cleanup(server.Close)
			host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
			require.NoError(t, err)
			port, err := strconv.Atoi(portText)
			require.NoError(t, err)
			_, err = satsnet_rpc.InitSatsNetClient(host, port, "test", "test", "", false)
			require.NoError(t, err)
			t.Cleanup(satsnet_rpc.ShutdownSatsNetClient)
			finished := make(chan error, 1)
			go func() { finished <- mgr.catchUpWithRPC(100, 100) }()
			if concurrent {
				select {
				case <-fetching:
				case <-time.After(3 * time.Second):
					t.Fatal("RPC did not pause at block 63")
				}
				for h := 63; h <= 100; h++ {
					mgr.connectMutex.Lock()
					err := mgr.connectBlockLocked(blocks[h], h, 100)
					mgr.connectMutex.Unlock()
					require.NoError(t, err)
				}
				require.Equal(t, 81, mgr.compilingBackupDB.GetHeight())
				unblock()
			}
			select {
			case err := <-finished:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("RPC catch-up did not finish")
			}
			// The RPC-only path used to force-write height 80, bypassing this backup.
			assert.Equal(t, 61, compiling.GetSyncHeight())
			assert.Equal(t, 81, mgr.compilingBackupDB.GetHeight())
			require.NoError(t, mgr.connectBlockLocked(blocks[101], 101, 101))
			assert.Equal(t, 81, compiling.GetSyncHeight())
			require.NoError(t, db.Close())
			db = indexerdb.NewKVDB(path)
			id, err := indexerdb.GetAddressIdFromDB(db, address.EncodeAddress())
			require.NoError(t, err)
			restoredAddress, err := indexerdb.GetAddressByIDFromDB(db, id)
			require.NoError(t, err)
			require.Equal(t, address.EncodeAddress(), restoredAddress)
			restored := base.NewBaseIndexer(db, &params, 0, 30)
			restored.Init()
			utxo := blocks[70].Transactions[0].TxID() + ":0"
			output, err := restored.GetUtxoInfo(utxo)
			require.NoError(t, err)
			require.Equal(t, int64(1000), output.Value)
			require.Equal(t, script, output.PkScript)
		})
	}
}
