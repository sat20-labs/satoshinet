package indexer

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/base"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the real IndexerMgr connectMutex, compiling index and WebSocket
// client. The RPC endpoint models CanServeBlock's chain read lock: a writer
// applying the supplied block must not wait on an index lock owned by RPC.
func TestIndexerRPCFetchDoesNotHoldConnectLock(t *testing.T) {
	for _, name := range []string{"concurrent-connect", "autopay-callback", "rpc-only", "rpc-retry", "replaced-baseline", "wrong-parent"} {
		t.Run(name, func(t *testing.T) {
			replace := name == "replaced-baseline"
			oldChain := indexercommon.CHAIN
			indexercommon.CHAIN = "testnet"
			t.Cleanup(func() { indexercommon.CHAIN = oldChain })
			params := chaincfg.TestNetParams
			db := indexerdb.NewKVDB(t.TempDir())
			t.Cleanup(func() { db.Close() })
			compiling := base.NewBaseIndexer(db, &params, 0, 30)
			compiling.Init()
			compiling.SetBlockCallback(func(*common.Block) {})
			require.NoError(t, compiling.SyncBlock(params.GenesisBlock, 0, 0, false))
			mgr := &IndexerMgr{baseDB: db, chaincfgParam: &params, compiling: compiling,
				rpcService: base.NewRpcIndexer(compiling)}
			var localStateReads atomic.Int32
			var initialStateReads int32
			if name == "autopay-callback" {
				priv, err := btcec.NewPrivateKey()
				require.NoError(t, err)
				payer, err := dkvs.P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &params)
				require.NoError(t, err)
				defaults := dkvs.NetworkDefaultsForParams(&params)
				state := &dkvs.AutopayContractState{TemplateName: "autopay.tc", CurrentBlock: 1,
					ServiceName: "dkvs", Recipient: defaults.AutopayRecipient,
					FeeAssetName: defaults.AutopayFeeAssetName, Status: "active",
					Delegates: map[string]dkvs.AutopayDelegateState{payer: {
						AmountPerBlock: defaults.FullRecordFeePerBlock, Balance: "100",
						LastPayHeight: 1, Status: "active", BlobKeyLimit: 1}}}
				mgr.cfg = &Config{DKVS: &DKVSIntegrationConfig{AutopayContract: "autopay", AutopayStateProvider: dkvs.RPCAutopayStateProvider{
					Call: func(string, []interface{}) (json.RawMessage, error) {
						localStateReads.Add(1)
						return json.Marshal(state)
					},
				}}}
				paidDB := indexerdb.NewKVDB(t.TempDir())
				t.Cleanup(func() { paidDB.Close() })
				mgr.dkvsIndexer = dkvs.New(paidDB, mgr.dkvsConfig())
				key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "rpc-lock")
				require.NoError(t, err)
				record, err := dkvs.NewAccountRecord(key, []byte("paid"), dkvs.RecordOptions{Seq: 1})
				require.NoError(t, err)
				proof, err := dkvs.NewAutopayFeeProof(key, "personal", uint32(dkvs.RecordSize(record)), 0, "autopay", "")
				require.NoError(t, err)
				record.FeeProof, err = dkvs.EncodeFeeProof(proof)
				require.NoError(t, err)
				signDKVSTestRecord(t, priv, record)
				_, err = mgr.dkvsIndexer.PutLocal(record)
				require.NoError(t, err)
				initialStateReads = localStateReads.Load()
				compiling.SetBlockCallback(mgr.processBlock)
			}

			coinbase := wire.NewMsgTx(1)
			coinbase.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: ^uint32(0)}, []byte{1, 1}, nil))
			coinbase.AddTxOut(wire.NewTxOut(1000, nil, []byte{txscript.OP_TRUE}))
			block := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: params.GenesisBlock.BlockHash()},
				Transactions: []*wire.MsgTx{coinbase}}
			if name == "wrong-parent" {
				block.Header.PrevBlock[0] ^= 1
			}
			target := block.BlockHash()
			var raw bytes.Buffer
			require.NoError(t, block.Serialize(&raw))

			var chainLock sync.RWMutex
			fetching, allowRead := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseRead := func() { releaseOnce.Do(func() { close(allowRead) }) }
			t.Cleanup(releaseRead)
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				readRequests := 0
				for {
					var request struct {
						ID     json.RawMessage `json:"id"`
						Method string          `json:"method"`
					}
					if conn.ReadJSON(&request) != nil {
						return
					}
					var result any
					var rpcError any
					switch request.Method {
					case "getblockcount":
						result = 0
					case "getblockhash":
						result = target.String()
					case "getblock":
						readRequests++
						if readRequests == 1 {
							close(fetching)
						}
						if name == "rpc-retry" && readRequests == 1 {
							rpcError = map[string]any{"code": -1, "message": "transient read failure"}
							break
						}
						<-allowRead
						chainLock.RLock()
						result = hex.EncodeToString(raw.Bytes())
						chainLock.RUnlock()
					}
					if conn.WriteJSON(map[string]any{"id": request.ID, "result": result, "error": rpcError}) != nil {
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
			chainLock.Lock()
			repaired := make(chan error, 1)
			go func() { repaired <- mgr.EnsureInternalTip(1, &target, 1) }()
			select {
			case <-fetching:
			case <-time.After(time.Second):
				chainLock.Unlock()
				releaseRead()
				t.Fatal("repair did not reach the real WebSocket getblock request")
			}
			lockFree := mgr.connectMutex.TryLock()
			if lockFree {
				if replace {
					// Reorg replaces the compiling object even when its restored
					// height/hash equal the snapshot before the RPC request.
					mgr.compiling = compiling.Clone(false)
				}
				mgr.connectMutex.Unlock()
			} else {
				t.Error("repair holds connectMutex while awaiting local getblock")
			}
			var connected chan struct{}
			if name == "concurrent-connect" || name == "autopay-callback" {
				connected = make(chan struct{})
				go func() { mgr.ConnectBlock(block, 1, 1); close(connected) }()
				select {
				case <-connected:
					if name == "autopay-callback" {
						assert.Greater(t, localStateReads.Load(), initialStateReads, "real callback must perform a cold-height AUTOPAY lookup")
					}
				case <-time.After(time.Second):
					t.Error("chain-lock holder cannot apply its supplied block while RPC is pending")
				}
			}
			// Release the reader even on the red version, so a failed assertion
			// does not strand the WebSocket handler or the repair goroutine.
			chainLock.Unlock()
			releaseRead()
			select {
			case err := <-repaired:
				if replace && lockFree {
					require.ErrorContains(t, err, "changed during RPC")
					require.Equal(t, 0, mgr.GetInternalSyncHeight())
				} else if name == "wrong-parent" {
					require.ErrorContains(t, err, "parent hash mismatch")
					require.Equal(t, 0, mgr.GetInternalSyncHeight())
				} else {
					require.NoError(t, err)
					require.True(t, mgr.InternalTipReady(1, &target))
				}
			case <-time.After(3 * time.Second):
				t.Fatal("repair did not finish after RPC could read the chain")
			}
			if connected != nil {
				select {
				case <-connected:
				case <-time.After(time.Second):
					t.Fatal("provided block connection did not finish")
				}
			}
		})
	}
}
