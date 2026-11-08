//go:build rpctest

package contract_e2e

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/indexer/share/btclucky"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/rpcclient"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// These opt-in gates use real node processes and newly generated regtest data.
// The HTTP backend never supplies a Bitcoin job, so no BTC hash worker or
// submission can be started. They do not use an existing node or database.
type syncMiningGateFixture struct {
	executable      string
	bootstrap, core *btcec.PrivateKey
	witness, locked []byte
	utxos           map[string]*indexercommon.AssetsInUtxo
}

func newSyncMiningGateFixture(t *testing.T, blocks int) *syncMiningGateFixture {
	t.Helper()
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", strconv.Itoa(int(chaincfg.RegressionNetParams.POSV2Height)))
	previous := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() { indexercommon.ENABLE_TESTING = previous })
	f := &syncMiningGateFixture{bootstrap: keyFromMnemonic(t, bootstrapMnemonic, 0), core: keyFromMnemonic(t, coreMnemonic, 0)}
	var err error
	f.witness, f.locked, err = anchortx.GetP2WSHscript(f.bootstrap.PubKey().SerializeCompressed(), f.core.PubKey().SerializeCompressed())
	require.NoError(t, err)
	f.utxos = make(map[string]*indexercommon.AssetsInUtxo)
	for h := 1; h <= blocks; h++ {
		point := templateLockedOutPoint("sync-mining-gate", h)
		f.utxos[point] = &indexercommon.AssetsInUtxo{OutPoint: point, Value: 200000, PkScript: f.locked,
			Assets: []*indexercommon.DisplayAsset{testDisplayAsset("brc20:f:sgas", "100000")}}
	}
	base := contractE2EExecutablePath(t)
	dir := t.TempDir()
	f.executable = filepath.Join(dir, filepath.Base(base))
	require.NoError(t, os.Link(base, f.executable)) // Compiled artifacts only.
	require.NoError(t, os.Link(filepath.Join(filepath.Dir(base), "wallet.so"), filepath.Join(dir, "wallet.so")))
	writeContractE2EWalletConfig(t, dir)
	configPath := filepath.Join(dir, "conf.yaml")
	config, err := os.ReadFile(configPath)
	require.NoError(t, err)
	config = []byte(strings.Replace(string(config), "chain: testnet", "chain: regtest", 1) +
		"peers:\n  - b@" + hex.EncodeToString(f.bootstrap.PubKey().SerializeCompressed()) + "@http://127.0.0.1:1/stp/regtest\n")
	require.NoError(t, os.WriteFile(configPath, config, 0600))
	return f
}

type syncMiningGateL1 struct {
	server              *httptest.Server
	jobs, tips, submits atomic.Int64
	firstJobReady       atomic.Bool
	readyMu             sync.Mutex
	readyAtJob          func() bool
}

func (f *syncMiningGateFixture) l1(t *testing.T) *syncMiningGateL1 {
	t.Helper()
	l := &syncMiningGateL1{}
	pub := hex.EncodeToString(f.bootstrap.PubKey().SerializeCompressed())
	l.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(value any) {
			if err := json.NewEncoder(w).Encode(value); err != nil {
				t.Logf("fake L1 response canceled: %v", err)
			}
		}
		switch r.URL.Path {
		case "/testnet/v3/indexer/pubkey":
			write(indexerwire.IndexerPubKeyResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}, PubKey: pub})
		case "/testnet/kv/register":
			write(indexerwire.RegisterPubKeyResp{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}, PubKey: pub})
		case "/testnet/btc/lucky/job":
			jobNumber := l.jobs.Add(1)
			l.readyMu.Lock()
			ready := l.readyAtJob
			l.readyMu.Unlock()
			if jobNumber == 1 {
				if ready == nil || !ready() {
					t.Error("BTC requested a job before the real chain and index reached the advertised target")
				} else {
					l.firstJobReady.Store(true)
				}
			}
			var request btclucky.JobRequest
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&request) != nil || request.Network != "regtest" || request.Jobs != 1 || request.RewardAddress == "" {
				t.Errorf("invalid real BTC job request: method=%s request=%+v", r.Method, request)
			}
			write(map[string]any{"code": -1, "msg": "gate has no Bitcoin jobs", "data": nil})
		case "/testnet/v3/bitcoin/tip":
			l.tips.Add(1)
			write(map[string]any{"code": 0, "msg": "ok", "data": map[string]any{"height": 100, "block_hash": strings.Repeat("1", 64)}})
		case "/testnet/btc/lucky/submit":
			l.submits.Add(1)
			t.Error("gate must never submit a Bitcoin solution")
			write(map[string]any{"code": -1, "msg": "submission forbidden"})
		default:
			const prefix = "/testnet/v3/utxo/info/"
			point := strings.TrimPrefix(r.URL.Path, prefix)
			utxo, ok := f.utxos[point]
			if !strings.HasPrefix(r.URL.Path, prefix) || !ok {
				t.Errorf("unexpected fake L1 request: %s", r.URL.Path)
				write(map[string]any{"code": 404, "msg": "unknown fixture request"})
				return
			}
			write(indexerwire.TxOutputRespV3{BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"}, Data: utxo})
		}
	}))
	t.Cleanup(l.server.Close)
	return l
}

// Keep the real nodes' handshake and wire bytes intact, but hold historical
// blocks until the test has observed the follower's unsynchronized state.
func syncMiningGatePeer(t *testing.T, follower, source *rpctest.Harness) (<-chan struct{}, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	blocked, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var blockOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		cancel()
		listener.Close()
		<-done // Close both sockets and join forwarding before node cleanup.
	})
	go func() {
		defer close(done)
		client, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				t.Errorf("accept sync gate peer: %v", err)
			}
			return
		}
		defer client.Close()
		upstream, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", source.P2PAddress())
		if err != nil {
			if ctx.Err() == nil {
				t.Errorf("connect sync gate source: %v", err)
			}
			return
		}
		defer upstream.Close()
		forwarded := make(chan struct{}, 2)
		forward := func(copyMessages func() error) {
			defer func() { cancel(); forwarded <- struct{}{} }()
			if err := copyMessages(); err != nil && err != io.EOF && ctx.Err() == nil {
				t.Errorf("forward sync gate peer: %v", err)
			}
		}
		go forward(func() error {
			_, err := io.Copy(upstream, client)
			return err
		})
		go forward(func() error {
			for {
				var header [wire.MessageHeaderSize]byte
				if _, err := io.ReadFull(upstream, header[:]); err != nil {
					return err
				}
				length := binary.LittleEndian.Uint32(header[16:20])
				if length > wire.MaxProtocolMessageLength {
					return fmt.Errorf("sync gate message exceeds maximum payload: %d", length)
				}
				if strings.TrimRight(string(header[4:16]), "\x00") == wire.CmdBlock {
					blockOnce.Do(func() { close(blocked) })
					select {
					case <-release:
					case <-ctx.Done():
						return nil
					}
				}
				if _, err := client.Write(header[:]); err != nil {
					return err
				}
				if _, err := io.CopyN(client, upstream, int64(length)); err != nil {
					return err
				}
			}
		})
		<-ctx.Done()
		client.Close()
		upstream.Close()
		<-forwarded
		<-forwarded
	}()
	require.NoError(t, follower.Client.AddNode(listener.Addr().String(), rpcclient.ANOneTry))
	return blocked, unblock
}

func (f *syncMiningGateFixture) node(t *testing.T, l *syncMiningGateL1, producer bool, extra ...string) *rpctest.Harness {
	t.Helper()
	role, mnemonic, key := "core", coreMnemonic, f.core
	if producer {
		role, mnemonic, key = "bootstrap", bootstrapMnemonic, f.bootstrap
	}
	args := []string{"--notls", "--nocheckpoints", "--nodnsseed", "--indexerscheme=http",
		"--indexerhost=" + strings.TrimPrefix(l.server.URL, "http://"), "--indexerproxy=testnet",
		"--miningpubkey=" + hex.EncodeToString(key.PubKey().SerializeCompressed())}
	if producer {
		args = append(args, "--generate")
	} // Ordinary followers retain the actual generate=false default.
	generate := producer
	for _, arg := range extra {
		if arg == "--generate" {
			generate = true // The self-check gate reuses its zero node as Core.
		}
	}
	args = append(args, extra...)
	n := newContractE2EHarness(t, &chaincfg.RegressionNetParams, args, f.executable,
		[]string{"SATOSHINET_RPCTEST_NODE_ROLE=" + role, "SATOSHINET_RPCTEST_STP_MNEMONIC=" + mnemonic, "SATOSHINET_RPCTEST_STP_PASSWORD=rpctest"}, "127.0.0.1")
	n.MaxConnRetries, n.ConnectionRetryTimeout = 100, 100*time.Millisecond
	require.NoError(t, n.SetUp(false, 0))
	if generate {
		require.Eventually(t, func() bool { mining, err := n.Client.GetGenerate(); return err == nil && mining }, 30*time.Second, 100*time.Millisecond)
		require.NoError(t, n.Client.SetGenerate(false, 0))
	}
	mining, err := n.Client.GetGenerate()
	require.NoError(t, err)
	require.False(t, mining)
	t.Logf("gate node role=%s pid=%d rpc=%s p2p=%s raw_stdout=%s", role, n.NodePID(), n.RPCAddress(), n.P2PAddress(), n.LogFile())
	return n
}

func (f *syncMiningGateFixture) produce(t *testing.T, node, core *rpctest.Harness, first, last int) *chainhash.Hash {
	t.Helper()
	scheduled := false
	defer func() {
		if scheduled {
			for _, n := range []*rpctest.Harness{node, core} {
				if err := n.Client.SetGenerate(false, 0); err != nil {
					t.Errorf("stop scheduled producer %s: %v", n.RPCAddress(), err)
				}
			}
		}
	}()
	var hash *chainhash.Hash
	for h := first; h <= last; h++ {
		anchor := buildNetworkAnchorTx(t, templateLockedOutPoint("sync-mining-gate", h), 200000,
			testWireAsset("brc20:f:sgas", 100000), "brc20:f:sgas-100000-0-0", f.witness, f.bootstrap, f.locked, int32(h))
		sendTx(t, node, anchor)
		v2 := chaincfg.RegressionNetParams.POSV2Active(int32(h))
		if v2 {
			if !scheduled {
				require.NotNil(t, core, "POS v2 requires the connected Core producer")
				scheduled = true
				for _, n := range []*rpctest.Harness{node, core} {
					require.NoError(t, n.Client.SetGenerate(true, 1))
				}
			}
			// Submit one anchor at a time and use the real scheduled producer
			// and Bootstrap approval path, never discrete generate after H32.
			require.Eventually(t, func() bool {
				_, height, err := node.Client.GetBestBlock()
				return err == nil && height >= int32(h)
			}, 30*time.Second, 100*time.Millisecond, "scheduled POS height=%d", h)
			var height int32
			var err error
			hash, height, err = node.Client.GetBestBlock()
			require.NoError(t, err)
			require.Equal(t, int32(h), height, "only the submitted anchor may advance the chain")
		} else {
			hashes, err := node.Client.Generate(1)
			require.NoError(t, err, "height=%d", h)
			require.Len(t, hashes, 1)
			hash = hashes[0]
		}
		block, err := node.Client.GetBlock(hash)
		require.NoError(t, err)
		require.Len(t, block.Transactions, 2, "height=%d must contain the real anchor", h)
		require.Equal(t, anchor.TxHash(), block.Transactions[1].TxHash())
		if v2 {
			witness := block.Transactions[0].TxIn[0].Witness
			require.Len(t, witness, 2, "height=%d requires Bootstrap approval", h)
			sig, err := ecdsa.ParseDERSignature(witness[1])
			require.NoError(t, err)
			require.True(t, anchortx.VerifyMessage(f.bootstrap.PubKey(),
				scommon.POSApprovalMessage(chaincfg.RegressionNetParams.Net, int32(h), *hash), sig), "height=%d", h)
		}
	}
	return hash
}

func syncMiningGateLog(t *testing.T, node *rpctest.Harness) string {
	t.Helper()
	data, err := os.ReadFile(node.LogFile())
	require.NoError(t, err)
	return string(data)
}

// Observe the real endpoints at the first job request, not only afterwards.
// Timeouts keep a premature miner request from deadlocking a blocked node.
func syncMiningGateReadyAtJob(node *rpctest.Harness, height int32, hash *chainhash.Hash) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodPost, "http://"+node.RPCAddress(),
		strings.NewReader(`{"jsonrpc":"1.0","id":1,"method":"getbestblock","params":[]}`))
	if err != nil {
		return false
	}
	req.SetBasicAuth("user", "pass") // Existing rpctest's public fixture credentials.
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	var chain struct {
		Result struct {
			Height int32  `json:"height"`
			Hash   string `json:"hash"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	err = json.NewDecoder(response.Body).Decode(&chain)
	response.Body.Close()
	if err != nil || string(chain.Error) != "null" || chain.Result.Height != height || chain.Result.Hash != hash.String() {
		return false
	}
	url, err := node.IndexerURL("regtest")
	if err != nil {
		return false
	}
	response, err = client.Get(url + "/bestheight")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var index struct {
		Code int `json:"code"`
		Data struct {
			Height int32 `json:"height"`
		} `json:"data"`
	}
	return json.NewDecoder(response.Body).Decode(&index) == nil && index.Code == 0 && index.Data.Height == height
}

func syncMiningGateTip(t *testing.T, node *rpctest.Harness, height int32, hash *chainhash.Hash) {
	t.Helper()
	url, err := node.IndexerURL("regtest")
	require.NoError(t, err)
	client := &http.Client{Timeout: 2 * time.Second}
	require.Eventually(t, func() bool {
		got, h, err := node.Client.GetBestBlock()
		if err != nil || h != height || *got != *hash {
			return false
		}
		response, err := client.Get(url + "/bestheight")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		var state struct {
			Code int `json:"code"`
			Data struct {
				Height int32 `json:"height"`
			} `json:"data"`
		}
		return json.NewDecoder(response.Body).Decode(&state) == nil && state.Code == 0 && state.Data.Height == height
	}, 90*time.Second, 100*time.Millisecond, "chain hash/height and published index height must catch up")
	// The public index API exposes height/block statistics, not its private tip
	// hash. Compare the actual final block's index row as a separate witness.
	response, err := client.Get(fmt.Sprintf("%s/height/%d", url, height))
	require.NoError(t, err)
	defer response.Body.Close()
	var indexed struct {
		Code int `json:"code"`
		Info struct {
			Height   int32 `json:"height"`
			TxAmount int   `json:"txamount"`
		} `json:"info"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&indexed))
	require.Zero(t, indexed.Code)
	require.Equal(t, height, indexed.Info.Height)
	require.Equal(t, 2, indexed.Info.TxAmount)
	t.Logf("gate synchronized chain_height=%d chain_hash=%s index_height=%d index_transactions=%d", height, hash, indexed.Info.Height, indexed.Info.TxAmount)
}

func TestNetworkIndexerSelfCheckHeightOnce(t *testing.T) {
	f := newSyncMiningGateFixture(t, 401)
	for _, value := range []string{"-1", "2147483648"} {
		t.Run("reject-height-"+value, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// Invalid configuration exits before starting services. Preserve the
			// raw process output outside t.TempDir alongside the runner reports.
			log, err := os.CreateTemp(os.TempDir(), "sync-mining-invalid-height-*.log")
			require.NoError(t, err)
			cmd := exec.CommandContext(ctx, f.executable, "--regtest", "--notls", "--nolisten", "--norpc", "--nodnsseed",
				"--indexerscheme=http", "--indexerhost=127.0.0.1:1", "--indexerproxy=testnet",
				"--homedir="+dir, "--configfile="+filepath.Join(dir, "absent.conf"), "--datadir="+filepath.Join(dir, "data"),
				"--logdir="+filepath.Join(dir, "logs"), "--indexerselfcheckheight="+value)
			cmd.Dir, cmd.Stdout, cmd.Stderr = dir, log, log
			err = cmd.Run()
			require.NoError(t, log.Close())
			require.NoError(t, ctx.Err(), "invalid height must reject promptly, not time out")
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit)
			require.NotZero(t, exit.ExitCode())
			data, readErr := os.ReadFile(log.Name())
			require.NoError(t, readErr)
			require.Contains(t, strings.ToLower(string(data)), "indexerselfcheckheight")
			if value == "-1" {
				require.Contains(t, string(data), "must not be negative")
			} else {
				require.Contains(t, string(data), "out of range")
			}
			t.Logf("invalid height=%s rc=%d raw_log=%s", value, exit.ExitCode(), log.Name())
		})
	}
	source := f.node(t, f.l1(t), true)
	// Reuse the zero-check node as the wallet-backed Core required for
	// scheduled POS v2. The audit follower still replays the frozen history.
	zero := f.node(t, f.l1(t), false, "--generate", "--indexerselfcheckheight=0")
	require.NoError(t, rpctest.ConnectNode(zero, source))
	hash := f.produce(t, source, zero, 1, 199)
	// An ordinary audit follower must not replace the active Core's peer
	// entry by advertising the same validator identity.
	once := f.node(t, f.l1(t), false, "--indexerselfcheckheight=200", "--miningpubkey=")
	require.NoError(t, rpctest.ConnectNode(once, source))
	for _, n := range []*rpctest.Harness{zero, once} {
		syncMiningGateTip(t, n, 199, hash)
		require.NotContains(t, syncMiningGateLog(t, n), "IndexerMgr.checkSelf takes")
	}
	hash = f.produce(t, source, zero, 200, 200)
	syncMiningGateTip(t, once, 200, hash)
	require.Eventually(t, func() bool { return strings.Count(syncMiningGateLog(t, once), "IndexerMgr.checkSelf takes") == 1 }, 30*time.Second, 100*time.Millisecond)
	audit := syncMiningGateLog(t, once)
	match := regexp.MustCompile(`indexer self-check requested at 200: applied height (\d+), durable height (-?\d+)`).FindStringSubmatch(audit)
	require.Len(t, match, 3)
	require.Equal(t, "200", match[1])
	durable, err := strconv.Atoi(match[2])
	require.NoError(t, err)
	require.GreaterOrEqual(t, durable, 40, "the gate must exercise a real durable Base audit")
	require.LessOrEqual(t, durable, 200) // Never force a flush to the trigger height.
	require.Contains(t, audit, "DB checked successfully")
	hash = f.produce(t, source, zero, 201, 401)
	for _, n := range []*rpctest.Harness{source, zero, once} {
		syncMiningGateTip(t, n, 401, hash)
	}
	for _, n := range []*rpctest.Harness{source, zero} {
		log := syncMiningGateLog(t, n)
		require.NotContains(t, log, "BaseIndexer->checkSelf")
		require.NotContains(t, log, "IndexerMgr.checkSelf takes")
	}
	require.Equal(t, 1, strings.Count(syncMiningGateLog(t, once), "indexer self-check requested at 200:"))
	require.Equal(t, 1, strings.Count(syncMiningGateLog(t, once), "IndexerMgr.checkSelf takes"))
	before := len(syncMiningGateLog(t, once))
	require.NoError(t, once.Restart(false)) // Same private data, no copy or forced flush.
	syncMiningGateTip(t, once, 401, hash)
	restarted := syncMiningGateLog(t, once)[before:]
	require.Regexp(t, `indexer self-check target 200 already restored at height \d+; skipping`, restarted)
	require.NotContains(t, restarted, "indexer self-check requested")
	require.NotContains(t, restarted, "IndexerMgr.checkSelf takes")
	// This deliberately does not promise persistence of checkOnce: a crash
	// restoring the index below H may run the audit again on replay through H.
	t.Logf("self-check H=200 applied=200 durable=%d checks=1 omitted_and_zero_checks=0 final_height=401 restart_restored_target_skipped=true", durable)
}

func TestNetworkBTCLuckyMinerWaitsForSatoshiNetSync(t *testing.T) {
	f := newSyncMiningGateFixture(t, 3)
	source := f.node(t, f.l1(t), true)
	hash := f.produce(t, source, nil, 1, 3)
	args := []string{"--btcluckymining", "--btcluckyminingjobs=1", "--serverpubkey=" + hex.EncodeToString(f.bootstrap.PubKey().SerializeCompressed())}
	for _, scenario := range []string{"stop-while-waiting", "catch-up-then-stop"} {
		t.Run(scenario, func(t *testing.T) {
			l := f.l1(t)
			node := f.node(t, l, false, args...)
			l.readyMu.Lock()
			l.readyAtJob = func() bool { return syncMiningGateReadyAtJob(node, 3, hash) }
			l.readyMu.Unlock()
			require.Eventually(t, func() bool {
				log := syncMiningGateLog(t, node)
				return strings.Contains(log, "BTC lucky miner runtime: initialized") && strings.Contains(log, "waiting for SatoshiNet chain and indexer sync")
			}, 15*time.Second, 100*time.Millisecond)
			noStart := func() bool {
				return l.jobs.Load() == 0 && l.tips.Load() == 0 && !strings.Contains(syncMiningGateLog(t, node), "BTC lucky miner runtime: starting")
			}
			require.Never(t, func() bool { return !noStart() }, 7*time.Second, 100*time.Millisecond, "no peer must not imply synchronized")
			peers, err := node.Client.GetPeerInfo()
			require.NoError(t, err)
			require.Empty(t, peers)
			if scenario == "catch-up-then-stop" {
				blocked, unblock := syncMiningGatePeer(t, node, source)
				select {
				case <-blocked:
				case <-time.After(30 * time.Second):
					t.Fatal("historical block did not reach the cancelable P2P gate")
				}
				peers, err := node.Client.GetPeerInfo()
				require.NoError(t, err)
				require.Len(t, peers, 1, "the real source handshake must complete before holding blocks")
				require.Equal(t, int32(3), peers[0].StartingHeight)
				_, height, err := node.Client.GetBestBlock()
				require.NoError(t, err)
				require.Less(t, height, int32(3), "historical blocks must remain withheld")
				// The peer advertises the real target, but cannot deliver its
				// historical blocks yet. No new source blocks are produced.
				require.Never(t, func() bool { return !noStart() }, 7*time.Second, 100*time.Millisecond, "BTC must not start while historical validation is behind")
				unblock()
				syncMiningGateTip(t, node, 3, hash)
				require.Eventually(t, func() bool {
					return l.firstJobReady.Load() && strings.Contains(syncMiningGateLog(t, node), "start succeeded running=true")
				}, 20*time.Second, 100*time.Millisecond)
				log := syncMiningGateLog(t, node)
				require.Equal(t, 1, strings.Count(log, "BTC lucky miner runtime: starting"))
				require.Equal(t, 1, strings.Count(log, "start succeeded running=true"))
				require.Contains(t, log, "jobs=1 jobs_mode=1 low_priority=true")
				mining, err := node.Client.GetGenerate()
				require.NoError(t, err)
				require.False(t, mining, "BTC startup must not enable SatoshiNet PoS")
			}
			pid := node.NodePID()
			start := time.Now()
			require.NoError(t, node.StopNode(), "owned node shutdown must cancel waiting or active miner")
			require.Less(t, time.Since(start), 10*time.Second, "normal stop must not reach the harness kill fallback")
			jobs, tips := l.jobs.Load(), l.tips.Load()
			require.Never(t, func() bool { return l.jobs.Load() != jobs || l.tips.Load() != tips }, 1200*time.Millisecond, 100*time.Millisecond)
			require.Zero(t, l.submits.Load())
			if scenario == "stop-while-waiting" {
				require.True(t, noStart())
			}
			t.Logf("miner gate scenario=%s owned_pid=%d stopped=true jobs=%d tips=%d submissions=0 raw_stdout=%s", scenario, pid, jobs, tips, node.LogFile())
		})
	}
}
