package contract_e2e

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// The source already owns nonempty blocks before the only P2P connection is
// made. Neither node mines afterwards, so a fresh block announcement cannot
// compensate for failing to select the IPv6 peer for historical sync.
func TestNetworkRegtestIPv6HistoricalSync(t *testing.T) {
	probe, err := net.Listen("tcp6", "[::1]:0")
	require.NoError(t, err, "real IPv6 loopback is required for this acceptance test")
	require.NoError(t, probe.Close())
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", "")
	oldTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() { indexercommon.ENABLE_TESTING = oldTesting })

	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	bootstrapPub := hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed())
	witnessScript, lockedScript, err := anchortx.GetP2WSHscript(
		bootstrapKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	const targetHeight = 3
	const gas = "brc20:f:sgas"
	utxos := make(map[string]*indexercommon.AssetsInUtxo)
	for n := 0; n < targetHeight; n++ {
		point := templateLockedOutPoint("regtest-ipv6-history", n)
		utxos[point] = &indexercommon.AssetsInUtxo{OutPoint: point, Value: 200000,
			PkScript: lockedScript, Assets: []*indexercommon.DisplayAsset{testDisplayAsset(gas, "100000")}}
	}
	fakeL1 := startFakeL1Indexer(t, bootstrapPub, utxos)

	// Reuse the existing wallet-enabled build and harness, with a private
	// regtest config. All wallet service endpoints stay on loopback too.
	base := contractE2EExecutablePath(t)
	runtimeDir := t.TempDir()
	executable := filepath.Join(runtimeDir, filepath.Base(base))
	require.NoError(t, os.Link(base, executable))
	require.NoError(t, os.Link(filepath.Join(filepath.Dir(base), "wallet.so"), filepath.Join(runtimeDir, "wallet.so")))
	writeContractE2EWalletConfig(t, runtimeDir)
	configPath := filepath.Join(runtimeDir, "conf.yaml")
	config, err := os.ReadFile(configPath)
	require.NoError(t, err)
	config = []byte(strings.Replace(string(config), "chain: testnet", "chain: regtest", 1) +
		"peers:\n  - b@" + bootstrapPub + "@http://127.0.0.1:1/stp/regtest\n")
	require.NoError(t, os.WriteFile(configPath, config, 0600))

	start := func(role, mnemonic string) *rpctest.Harness {
		t.Helper()
		generator := rpctest.ListenAddressGenerator
		rpctest.ListenAddressGenerator = func() (string, string) {
			p2p, rpc := generator()
			_, port, err := net.SplitHostPort(p2p)
			require.NoError(t, err)
			return net.JoinHostPort("::1", port), rpc
		}
		pub := hex.EncodeToString(keyFromMnemonic(t, mnemonic, 0).PubKey().SerializeCompressed())
		node, err := rpctest.NewWithEnv(&chaincfg.RegressionNetParams, nil, []string{
			"--notls", "--nocheckpoints", "--nodnsseed", "--generate", "--indexerscheme=http",
			"--indexerhost=" + strings.TrimPrefix(fakeL1.URL, "http://"), "--indexerproxy=testnet",
			"--miningpubkey=" + pub, "--debuglevel=PEER=debug,SYNC=debug,RPCS=debug",
		}, executable, []string{"SATOSHINET_RPCTEST_NODE_ROLE=" + role,
			"SATOSHINET_RPCTEST_STP_MNEMONIC=" + mnemonic, "SATOSHINET_RPCTEST_STP_PASSWORD=rpctest"})
		rpctest.ListenAddressGenerator = generator
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, node.TearDown()) })
		node.MaxConnRetries, node.ConnectionRetryTimeout = 50, 100*time.Millisecond
		require.NoError(t, node.SetUp(false, 0))
		// Startup loads the wallet signer and starts the scheduled miner.
		// Wait for that one-time startup before stopping it, while no work
		// exists; discrete generation below is the only block producer.
		require.Eventually(t, func() bool {
			mining, err := node.Client.GetGenerate()
			return err == nil && mining
		}, 20*time.Second, 100*time.Millisecond)
		require.NoError(t, node.Client.SetGenerate(false, 0))
		mining, err := node.Client.GetGenerate()
		require.NoError(t, err)
		require.False(t, mining)
		genesis, err := node.Client.GetBlockHash(0)
		require.NoError(t, err)
		require.Equal(t, *chaincfg.RegressionNetParams.GenesisHash, *genesis)
		t.Logf("regtest %s pid=%d rpc=%s p2p=%s log=%s", role, node.NodePID(), node.RPCAddress(), node.P2PAddress(), node.LogFile())
		return node
	}

	source := start("bootstrap", bootstrapMnemonic)
	for n := 0; n < targetHeight; n++ {
		anchor := buildNetworkAnchorTx(t, templateLockedOutPoint("regtest-ipv6-history", n), 200000,
			testWireAsset(gas, 100000), gas+"-100000-0-0", witnessScript, bootstrapKey, lockedScript, int32(n+1))
		sendTx(t, source, anchor)
		hashes, err := source.Client.Generate(1)
		require.NoError(t, err)
		require.Len(t, hashes, 1)
		block, err := source.Client.GetBlock(hashes[0])
		require.NoError(t, err)
		require.Len(t, block.Transactions, 2, "history must contain a valid transaction, not an empty block")
		require.Equal(t, anchor.TxHash(), block.Transactions[1].TxHash())
		t.Logf("source history height=%d hash=%s work=%s", n+1, hashes[0], anchor.TxHash())
	}
	targetHash, target, err := source.Client.GetBestBlock()
	require.NoError(t, err)
	require.EqualValues(t, targetHeight, target)
	follower := start("core", coreMnemonic)
	_, before, err := follower.Client.GetBestBlock()
	require.NoError(t, err)
	require.Zero(t, before)
	connected, err := source.Client.GetPeerInfo()
	require.NoError(t, err)
	require.Empty(t, connected, "the historical source must be isolated while building its chain")

	require.NoError(t, rpctest.ConnectNode(follower, source))
	require.Eventually(t, func() bool {
		hash, height, err := follower.Client.GetBestBlock()
		return err == nil && height == target && *hash == *targetHash
	}, 30*time.Second, 100*time.Millisecond, "IPv6 follower did not actively catch up to the frozen source")
	peers, err := follower.Client.GetPeerInfo()
	require.NoError(t, err)
	require.Len(t, peers, 1)
	require.Equal(t, source.P2PAddress(), peers[0].Addr)
	require.False(t, peers[0].Inbound)
	require.EqualValues(t, target, peers[0].StartingHeight)
	// This node's getpeerinfo formats services as zero-padded decimal.
	services, err := strconv.ParseUint(peers[0].Services, 10, 64)
	require.NoError(t, err)
	require.True(t, wire.ServiceFlag(services).HasFlag(wire.SFNodeNetwork|wire.SFNodeWitness))
	for _, node := range []*rpctest.Harness{source, follower} {
		mining, err := node.Client.GetGenerate()
		require.NoError(t, err)
		require.False(t, mining, "neither node may mine after historical blocks are prepared")
		hash, height, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, target, height)
		require.Equal(t, *targetHash, *hash)
	}
	localLog, err := os.ReadFile(follower.LogFile())
	require.NoError(t, err)
	require.Contains(t, string(localLog), fmt.Sprintf("Syncing to block height %d from peer %s", target, source.P2PAddress()))
	require.Contains(t, string(localLog), "Sending getblocks")
	require.Contains(t, string(localLog), "Sending getdata")
	sourceLog, err := os.ReadFile(source.LogFile())
	require.NoError(t, err)
	require.Contains(t, string(sourceLog), "Received getblocks")
	t.Logf("historical_sync peer=%s version=%d services=%s advertised_height=%d before=%d after=%d hash=%s miners_stopped=true getblocks=true getdata=true",
		peers[0].Addr, peers[0].Version, peers[0].Services, peers[0].StartingHeight, before, target, targetHash)
}
