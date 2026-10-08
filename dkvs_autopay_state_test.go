package main

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

	"github.com/btcsuite/websocket"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/node"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/base"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/indexer/share/satsnet_rpc"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Use the real WebSocket serviceRequestSem (one slot), real DKVS paid record,
// and cold height cache. Only the getblock chain-lock wait is controlled by
// the fixture; the AUTOPAY lookup must read committed state without that queue.
func TestDKVSAutopayCallbackDoesNotWaitForRPCSlot(t *testing.T) {
	oldCfg, oldChain := cfg, indexercommon.CHAIN
	cfg = &config{RPCMaxConcurrentReqs: 1, RPCMaxWebsockets: 4}
	indexercommon.CHAIN = "testnet"
	t.Cleanup(func() { cfg = oldCfg; indexercommon.CHAIN = oldChain })
	params := chaincfg.TestNetParams
	db, err := database.Create("ffldb", t.TempDir(), params.Net)
	require.NoError(t, err)
	defer db.Close()
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	payer, err := dkvs.P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &params)
	require.NoError(t, err)
	defaults := dkvs.NetworkDefaultsForParams(&params)
	ap := template.NewAutopayContract("dkvs", defaults.AutopayRecipient, defaults.AutopayFeeAssetName, defaults.FullRecordFeePerBlock)
	addr, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{9}, 32))
	require.NoError(t, err)
	content, err := ap.Encode()
	require.NoError(t, err)
	runtime, err := template.NewRuntime(addr, contractcommon.DeployPayload{Type: contractcommon.ContractTypeTemplate, SubType: ap.TemplateName(), Version: ap.Version(), ContractContent: content}, nil)
	require.NoError(t, err)
	amount, err := indexercommon.NewDecimalFromString(defaults.FullRecordFeePerBlock, 8)
	require.NoError(t, err)
	running, err := runtime.RuntimeState()
	require.NoError(t, err)
	running.AutopayData().AutopayStatus = "active"
	running.AutopayData().AutopayDelegates = map[string]template.AutopayDelegate{payer: {AmountPerBlock: amount, Balance: amount, LastPayHeight: 2, Status: "active", BlobKeyLimit: 1}}
	raw, err := json.Marshal(running)
	require.NoError(t, err)
	runtime.SetState("template-runtime-state", raw)
	runtime.SetCurrentBlock(2)
	store := template.NewRuntimeStore()
	store.Add(runtime)
	require.NoError(t, node.NewTemplateStateStore(db).StoreBlockState(&chainhash.Hash{2}, store))
	stateRaw := json.RawMessage(nil)
	view, err := runtime.StateView(framework.StateViewContext{ContractAddress: addr.String()})
	require.NoError(t, err)
	stateRaw, err = json.Marshal(view)
	require.NoError(t, err)

	var chainLock sync.RWMutex
	waiting, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var stateRPCs atomic.Int32
	oldGetBlock, oldGetCount, oldGetState := rpcHandlers["getblock"], rpcHandlers["getblockcount"], rpcHandlers["getcontractstate"]
	t.Cleanup(func() {
		rpcHandlers["getblock"] = oldGetBlock
		rpcHandlers["getblockcount"] = oldGetCount
		rpcHandlers["getcontractstate"] = oldGetState
	})
	block := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: params.GenesisBlock.BlockHash(), Nonce: 1}}
	var serialized bytes.Buffer
	require.NoError(t, block.Serialize(&serialized))
	rpcHandlers["getblockcount"] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) { return int32(0), nil }
	rpcHandlers["getblock"] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) {
		close(waiting)
		<-release
		chainLock.RLock()
		defer chainLock.RUnlock()
		return hex.EncodeToString(serialized.Bytes()), nil
	}
	rpcHandlers["getcontractstate"] = func(*rpcServer, interface{}, <-chan struct{}) (interface{}, error) {
		stateRPCs.Add(1)
		return stateRaw, nil
	}
	rpc := &rpcServer{}
	rpc.ntfnMgr = newWsNotificationManager(rpc)
	rpc.ntfnMgr.Start()
	defer func() { rpc.ntfnMgr.Shutdown(); rpc.ntfnMgr.WaitForShutdown() }()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			rpc.WebsocketHandler(conn, r.RemoteAddr, true, true)
		}
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	_, err = satsnet_rpc.InitSatsNetClient(host, port, "test", "test", "", false)
	require.NoError(t, err)
	defer satsnet_rpc.ShutdownSatsNetClient()

	baseDB := indexerdb.NewKVDB(t.TempDir())
	defer baseDB.Close()
	idxDB := indexerdb.NewKVDB(t.TempDir())
	defer idxDB.Close()
	compiling := base.NewBaseIndexer(baseDB, &params, 0, 30)
	compiling.Init()
	compiling.SetBlockCallback(func(*common.Block) {})
	require.NoError(t, compiling.SyncBlock(params.GenesisBlock, 0, 2, false))
	require.NoError(t, compiling.SyncBlock(block, 1, 2, false))
	// Use exactly the node's default provider injection and the same height cache
	// and paid-retention callback used by IndexerMgr.processBlock.
	nodeConfig := nodeDKVSConfig(db, "http://127.0.0.1:18084")
	require.Equal(t, "http://127.0.0.1:18084", nodeConfig.ResolverL1NSBaseURL)
	provider := nodeConfig.AutopayStateProvider
	cached := &dkvs.HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return uint64(compiling.GetHeight()) }}
	verifier := dkvs.LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: dkvs.AutopayFeeVerifier{StateProvider: cached, Contract: addr.String(), ServiceName: "dkvs", Recipient: defaults.AutopayRecipient, FeeAssetName: defaults.AutopayFeeAssetName, FullRecordFeePerBlock: defaults.FullRecordFeePerBlock, AddressParams: &params}}
	idx := dkvs.New(idxDB, dkvs.Config{CurrentHeight: func() uint64 { return uint64(compiling.GetHeight()) }, FeeVerifier: verifier})
	key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "lock-test")
	require.NoError(t, err)
	record, err := dkvs.NewAccountRecord(key, []byte("paid"), dkvs.RecordOptions{Seq: 1})
	require.NoError(t, err)
	proof, err := dkvs.NewAutopayFeeProof(key, "personal", uint32(dkvs.RecordSize(record)), 0, addr.String(), "")
	require.NoError(t, err)
	record.FeeProof, err = dkvs.EncodeFeeProof(proof)
	require.NoError(t, err)
	hash := dkvs.SigningHash(record)
	sig, err := schnorr.Sign(priv, hash[:])
	require.NoError(t, err)
	record.Signature = sig.Serialize()
	_, err = idx.PutLocal(record)
	require.NoError(t, err)
	before := stateRPCs.Load()
	compiling.SetBlockCallback(func(b *common.Block) { idx.RefreshPaidRetentionAt(uint64(b.Height)) })
	block2 := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: block.BlockHash(), Nonce: 2}}
	chainLock.Lock()
	fetched := make(chan error, 1)
	go func() { hash := block.BlockHash(); _, err := satsnet_rpc.GetRawBlock(&hash); fetched <- err }()
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		chainLock.Unlock()
		unblock()
		t.Fatal("getblock did not acquire RPC slot")
	}
	connected := make(chan error, 1)
	go func() { connected <- compiling.SyncBlock(block2, 2, 2, false) }()
	blocked := false
	var connectErr error
	select {
	case connectErr = <-connected:
	case <-time.After(time.Second):
		blocked = true
		t.Error("real AUTOPAY callback waits for the sole RPC slot while holding the chain lock")
	}
	chainLock.Unlock()
	unblock()
	require.NoError(t, <-fetched)
	if blocked {
		connectErr = <-connected
	}
	require.NoError(t, connectErr)
	require.Equal(t, before, stateRPCs.Load(), "cold callback must use committed local state")
	_, err = idx.Get(record.Key)
	require.NoError(t, err, "valid AUTOPAY record must remain active")
	require.Zero(t, stateRPCs.Load(), "node default provider must never use the local RPC queue")
	state, err := provider.GetAutopayState(" " + addr.String() + " ")
	require.NoError(t, err)
	require.Equal(t, addr.String(), state.Contract)
	require.Equal(t, int64(2), state.CurrentBlock)
	require.Equal(t, int64(2), state.Delegates[payer].LastPayHeight)
	_, err = provider.GetAutopayState("invalid-address")
	require.Error(t, err)
	missing, err := contractcommon.NewContractAddressFromHash(contractcommon.TestnetContractPrefix, 1, contractcommon.ContractTypeTemplate, bytes.Repeat([]byte{10}, 32))
	require.NoError(t, err)
	_, err = provider.GetAutopayState(missing.String())
	require.ErrorIs(t, err, dkvs.ErrInvalidFeeProof)
	_, err = (localAutopayStateProvider{}).GetAutopayState(addr.String())
	require.Error(t, err, "unavailable committed state must fail without RPC fallback")
	require.Zero(t, stateRPCs.Load())
}
