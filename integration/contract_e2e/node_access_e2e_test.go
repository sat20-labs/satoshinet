package contract_e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	common "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcjson"
	"github.com/sat20-labs/satoshinet/chaincfg"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/peer"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

func nodeAccessStats(t *testing.T, node *rpctest.Harness) btcjson.GetNodeStatsResult {
	t.Helper()
	data, err := node.Client.RawRequest("getnodestats", nil)
	require.NoError(t, err)
	var result btcjson.GetNodeStatsResult
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

// A real signed UNSTAKE removes a still-connected Core from every indexer.
func TestAccessCoreExit(t *testing.T) {
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", "100000")
	configureFastPOSTimers(t)
	previous := common.ENABLE_TESTING
	common.ENABLE_TESTING = true
	t.Cleanup(func() { common.ENABLE_TESTING = previous })
	bootKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	bootPub := hex.EncodeToString(bootKey.PubKey().SerializeCompressed())
	firstPub := hex.EncodeToString(keyFromMnemonic(t, coreMnemonic, 0).PubKey().SerializeCompressed())
	entropy := sha256.Sum256([]byte("node-access-exiting-core"))
	words, err := bip39.NewMnemonic(entropy[:16])
	require.NoError(t, err)
	key := keyFromMnemonic(t, words, 0)
	pub := hex.EncodeToString(key.PubKey().SerializeCompressed())
	witness, script, err := anchortx.GetP2WSHscript(bootKey.PubKey().SerializeCompressed(), key.PubKey().SerializeCompressed())
	require.NoError(t, err)
	stake := common.GetStakeAssetNameWithHeightL2(1)
	amount := common.GetStakeAssetAmtWithHeightL2(1)
	point := templateLockedOutPoint("node-access-exit", 0)
	fake := startFakeL1Indexer(t, bootPub, map[string]*common.AssetsInUtxo{
		point: {OutPoint: point, Value: 200000, PkScript: script,
			Assets: []*common.DisplayAsset{testDisplayAsset(stake, fmt.Sprint(amount))}},
	})
	boot, first := startSatoshiNetNetwork(t, fake)
	ordinary := newContractE2EHarness(t, &chaincfg.TestNetParams, []string{
		"--notls", "--nocheckpoints", "--nodnsseed", "--indexerscheme=http",
		"--indexerhost=" + strings.TrimPrefix(fake.URL, "http://"), "--indexerproxy=testnet",
		"--addpeer=" + boot.P2PAddress(),
	}, contractE2EExecutablePath(t), nil, "127.0.0.1")
	ordinary.MaxConnRetries, ordinary.ConnectionRetryTimeout = 50, 100*time.Millisecond
	require.NoError(t, ordinary.SetUp(false, 0))
	anchor := buildNetworkAnchorTx(t, point, 200000, testWireAsset(stake, amount),
		fmt.Sprintf("%s-%d-0-0", stake, amount), witness, bootKey, script, 1)
	sendTx(t, boot, anchor)
	generateOrWaitBlockAtLeast(t, boot, []*rpctest.Harness{boot, first, ordinary}, 1)
	exiting := startSatoshiNetNode(t, fake, "access-exiting-core", words, "--serverpubkey="+bootPub)
	require.NoError(t, rpctest.ConnectNode(exiting, boot))
	require.NoError(t, rpctest.JoinNodes([]*rpctest.Harness{boot, first, ordinary, exiting}, rpctest.Blocks))
	// Let startup finish while membership still exists, then stop mining as
	// an operator would before withdrawing. Keep P2P and the indexer running.
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(exiting.LogFile())
		return err == nil && strings.Contains(string(data), "POS miner started")
	}, 25*time.Second, 100*time.Millisecond)
	require.NoError(t, exiting.Client.SetGenerate(false, 0))
	require.NoError(t, rpctest.ConnectNode(ordinary, exiting))
	waitAccessStats(t, ordinary, "registered core selected", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == pub
	}, 20*time.Second)
	require.NoError(t, rpctest.ConnectNode(ordinary, first))

	prev := anchor.TxOut[0]
	unstake := wire.NewMsgTx(wire.TxVersion)
	unstake.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: anchor.TxHash()}, nil, nil))
	payload, err := scommon.EncodeDescendPayloadV2(strings.Repeat("ab", 32), scommon.DESCEND_OP_CLOSE, nil)
	require.NoError(t, err)
	descend, err := scommon.NullDataScript(scommon.CONTENT_TYPE_DESCENDING, payload)
	require.NoError(t, err)
	unstake.AddTxOut(wire.NewTxOut(prev.Value-1000, prev.Assets, descend))
	invoice, err := scommon.CreateStakeInvoice(common.NewAssetNameFromString(stake), common.NewDefaultDecimal(amount))
	require.NoError(t, err)
	marker, err := scommon.NullDataScript(scommon.CONTENT_TYPE_UNSTAKE, invoice)
	require.NoError(t, err)
	unstake.AddTxOut(wire.NewTxOut(0, nil, marker))
	channel, err := anchortx.GetChannelAddress(bootKey.PubKey().SerializeCompressed(), key.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	require.NoError(t, err)
	marker, err = scommon.NullDataScript(scommon.CONTENT_TYPE_CHANNELID, []byte(channel+"-1"))
	require.NoError(t, err)
	unstake.AddTxOut(wire.NewTxOut(0, nil, marker))
	fetcher := txscript.NewCannedPrevOutputFetcher(prev.PkScript, prev.Value, prev.Assets)
	hashes := txscript.NewTxSigHashes(unstake, fetcher)
	keys := []*btcec.PrivateKey{bootKey, key}
	if bytes.Compare(keys[0].PubKey().SerializeCompressed(), keys[1].PubKey().SerializeCompressed()) > 0 {
		keys[0], keys[1] = keys[1], keys[0]
	}
	stack := wire.TxWitness{nil}
	for _, signer := range keys {
		sig, err := txscript.RawTxInWitnessSignature(unstake, hashes, 0, prev.Value, prev.Assets, witness, txscript.SigHashAll, signer)
		require.NoError(t, err)
		stack = append(stack, sig)
	}
	unstake.TxIn[0].Witness = append(stack, witness)
	sendTx(t, boot, unstake)
	generateOrWaitBlockAtLeast(t, boot, []*rpctest.Harness{boot, first, ordinary, exiting}, 2)
	data, err := os.ReadFile(boot.LogFile())
	require.NoError(t, err)
	require.Contains(t, string(data), "remove core node "+pub, "real UNSTAKE must remove indexed membership")
	waitAccessStats(t, ordinary, "withdrawn core replaced without maintenance delay", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == firstPub && s.PreferredCore == firstPub
	}, 20*time.Second)
	waitAccessStats(t, first, "replacement core counts the ordinary node", func(s btcjson.GetNodeStatsResult) bool {
		return s.ChildCount == 1
	}, 20*time.Second)
	peers, err := ordinary.Client.GetPeerInfo()
	require.NoError(t, err)
	connected := false
	for _, p := range peers {
		connected = connected || p.Addr == exiting.P2PAddress()
	}
	require.True(t, connected, "membership loss must not require transport loss")
	require.NoError(t, ordinary.Restart(false))
	require.NoError(t, rpctest.ConnectNode(ordinary, first))
	waitAccessStats(t, ordinary, "replacement survives restart after core exit", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == firstPub && s.PreferredCore == firstPub
	}, 20*time.Second)
}

func waitAccessStats(t *testing.T, node *rpctest.Harness, description string,
	check func(btcjson.GetNodeStatsResult) bool, timeout time.Duration) btcjson.GetNodeStatsResult {
	t.Helper()
	var result btcjson.GetNodeStatsResult
	require.Eventually(t, func() bool {
		data, err := node.Client.RawRequest("getnodestats", nil)
		return err == nil && json.Unmarshal(data, &result) == nil && check(result)
	}, timeout, 200*time.Millisecond, description)
	t.Logf("%s: %+v", description, result)
	return result
}

func waitAccessPeers(t *testing.T, node *rpctest.Harness, addresses ...string) {
	t.Helper()
	require.Eventually(t, func() bool {
		peers, err := node.Client.GetPeerInfo()
		if err != nil {
			return false
		}
		connected := make(map[string]bool)
		for _, p := range peers {
			if !p.Inbound {
				connected[p.Addr] = true
			}
		}
		for _, addr := range addresses {
			if !connected[addr] {
				return false
			}
		}
		return true
	}, 15*time.Second, 100*time.Millisecond, "all configured peers must stay available: %v", addresses)
}

// Real nodes own all discovery, selection, disk state and aggregation. Two
// protocol clients additionally exercise duplicate IDs and expiry over TCP.
func TestNetworkNodeAccessLifecycle(t *testing.T) {
	const selectionWait = 3 * time.Second
	const expiry = 8 * time.Second
	t.Setenv("SATOSHINET_RPCTEST_FAST_ACCESS", "1")
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", "100000")
	configureFastPOSTimers(t)
	previous := common.ENABLE_TESTING
	common.ENABLE_TESTING = true
	t.Cleanup(func() { common.ENABLE_TESTING = previous })

	bootKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	bootPub := hex.EncodeToString(bootKey.PubKey().SerializeCompressed())
	corePub := hex.EncodeToString(keyFromMnemonic(t, coreMnemonic, 0).PubKey().SerializeCompressed())
	entropy := sha256.Sum256([]byte("node-access-e2e-second-core"))
	secondWords, err := bip39.NewMnemonic(entropy[:16])
	require.NoError(t, err)
	secondKey := keyFromMnemonic(t, secondWords, 0)
	secondPub := hex.EncodeToString(secondKey.PubKey().SerializeCompressed())
	witness, script, err := anchortx.GetP2WSHscript(bootKey.PubKey().SerializeCompressed(), secondKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	stake := common.GetStakeAssetNameWithHeightL2(1)
	amount := common.GetStakeAssetAmtWithHeightL2(1)
	point := templateLockedOutPoint("node-access-core", 0)
	fake := startFakeL1Indexer(t, bootPub, map[string]*common.AssetsInUtxo{
		point: {OutPoint: point, Value: 200000, PkScript: script,
			Assets: []*common.DisplayAsset{testDisplayAsset(stake, fmt.Sprint(amount))}},
	})
	boot, first := startSatoshiNetNetwork(t, fake)
	help, err := boot.Client.RawRequest("help", []json.RawMessage{json.RawMessage(`"getnodestats"`)})
	require.NoError(t, err)
	require.Contains(t, string(help), "observednodes")
	// Start the ordinary harness at genesis so its wallet subscribes before
	// the registration block arrives over its configured bootstrap connection.
	ordinary := newContractE2EHarness(t, &chaincfg.TestNetParams, []string{
		"--notls", "--nocheckpoints", "--nodnsseed", "--indexerscheme=http",
		"--indexerhost=" + strings.TrimPrefix(fake.URL, "http://"), "--indexerproxy=testnet",
		"--addpeer=" + boot.P2PAddress(),
		"--addpeer=" + first.P2PAddress(),
	}, contractE2EExecutablePath(t), nil, "127.0.0.1")
	ordinary.MaxConnRetries, ordinary.ConnectionRetryTimeout = 50, 100*time.Millisecond
	require.NoError(t, ordinary.SetUp(false, 0))
	actor := newTemplateNetworkActor(t, keyFromMnemonic(t, bootstrapMnemonic, 1))
	// This explicit permanent connection must keep retrying even after the
	// preferred-Core window expires. It has no automatic address discovery.
	pinned := newContractE2EHarness(t, &chaincfg.TestNetParams, []string{
		"--notls", "--nocheckpoints", "--nodnsseed", "--indexerscheme=http",
		"--indexerhost=" + strings.TrimPrefix(fake.URL, "http://"), "--indexerproxy=testnet",
		"--connect=" + first.P2PAddress(),
		"--connect=" + boot.P2PAddress(),
	}, contractE2EExecutablePath(t), nil, "127.0.0.1")
	pinned.MaxConnRetries, pinned.ConnectionRetryTimeout = 50, 100*time.Millisecond
	require.NoError(t, pinned.SetUp(false, 0))
	waitAccessStats(t, pinned, "configured core selected", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == corePub
	}, 20*time.Second)
	require.NoError(t, pinned.Restart(false))
	waitAccessStats(t, pinned, "restart preserves the ping preference", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == corePub
	}, 20*time.Second)
	waitAccessPeers(t, pinned, first.P2PAddress(), boot.P2PAddress())
	t.Log("preferred core does not suppress other configured connections after restart")
	anchor := buildNetworkAnchorTx(t, point, 200000, testWireAsset(stake, amount),
		fmt.Sprintf("%s-%d-0-0", stake, amount), witness, bootKey, actor.pkScript, 1)
	sendTx(t, boot, anchor)
	generateOrWaitBlockAtLeast(t, boot, []*rpctest.Harness{boot, first}, 1)
	second := startSatoshiNetNode(t, fake, "access-core-two", secondWords, "--serverpubkey="+bootPub)
	require.NoError(t, rpctest.ConnectNode(second, boot))
	require.NoError(t, rpctest.JoinNodes([]*rpctest.Harness{boot, first, second}, rpctest.Blocks))

	selected := waitAccessStats(t, ordinary, "ordinary selects its connected core", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == corePub && s.PreferredAddr == first.P2PAddress()
	}, 20*time.Second)
	require.NoError(t, rpctest.ConnectNode(ordinary, second))
	require.NoError(t, rpctest.ConnectNode(first, ordinary))
	waitAccessStats(t, boot, "two cores and two ordinary nodes counted once", func(s btcjson.GetNodeStatsResult) bool {
		return s.CoreCount == 2 && s.ObservedNodes == 5
	}, 30*time.Second)
	require.EqualValues(t, 2, nodeAccessStats(t, first).ChildCount)
	require.Zero(t, nodeAccessStats(t, second).ChildCount)
	require.Equal(t, corePub, nodeAccessStats(t, ordinary).AccessCore, "selection must stay sticky")

	// Configured connections work independently of the saved ping preference.
	// No bootstrap is available, but the configured first Core remains reachable.
	require.NoError(t, boot.StopNode())
	require.NoError(t, ordinary.Restart(false))
	restarted := waitAccessStats(t, ordinary, "restart reuses preference on configured core", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == corePub && s.PreferredAddr == first.P2PAddress()
	}, 20*time.Second)
	require.NotEqual(t, selected.NodeID, restarted.NodeID, "instance ID belongs to this process only")
	peers, err := ordinary.Client.GetPeerInfo()
	require.NoError(t, err)
	for _, p := range peers {
		require.NotEqual(t, boot.P2PAddress(), p.Addr)
	}
	require.NoError(t, boot.Restart(false))
	waitAccessStats(t, boot, "bootstrap rebuilds reports after restart", func(s btcjson.GetNodeStatsResult) bool {
		return s.CoreCount == 2 && s.ObservedNodes == 5
	}, 30*time.Second)
	waitAccessPeers(t, ordinary, first.P2PAddress(), boot.P2PAddress())

	// An existing second Core connection must not replace a healthy primary,
	// but becomes eligible after the primary's selection wait.
	require.NoError(t, rpctest.ConnectNode(ordinary, second))
	waitAccessPeers(t, ordinary, second.P2PAddress(), boot.P2PAddress())
	require.Equal(t, corePub, nodeAccessStats(t, ordinary).AccessCore)
	lostAt := time.Now()
	require.NoError(t, first.StopNode())
	earliest := lostAt.Add(selectionWait)
	require.True(t, time.Now().Before(earliest), "must observe the protected window")
	for time.Now().Before(earliest) {
		stats := nodeAccessStats(t, ordinary)
		if !time.Now().Before(earliest) {
			break // An RPC response may straddle the window boundary.
		}
		require.Equal(t, corePub, stats.PreferredCore, "must preserve the failed primary during maintenance")
		require.NotEqual(t, secondPub, stats.AccessCore, "must not select the backup early")
		time.Sleep(100 * time.Millisecond)
	}
	waitAccessStats(t, ordinary, "failed preferred core is replaced", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == secondPub && s.PreferredAddr == second.P2PAddress()
	}, 20*time.Second)
	waitAccessStats(t, boot, "offline core is removed from total", func(s btcjson.GetNodeStatsResult) bool {
		return s.CoreCount == 1 && s.ObservedNodes == 3
	}, 30*time.Second)
	waitAccessPeers(t, ordinary, second.P2PAddress(), boot.P2PAddress())
	require.NoError(t, first.Restart(false))
	require.NoError(t, rpctest.ConnectNode(first, boot))
	waitAccessStats(t, pinned, "configured core reconnects after the maintenance window", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == corePub
	}, 30*time.Second)
	waitAccessStats(t, boot, "configured peer recovery restores count", func(s btcjson.GetNodeStatsResult) bool {
		return s.CoreCount == 2 && s.ObservedNodes == 5
	}, 30*time.Second)
	require.NoError(t, pinned.StopNode())
	require.NoError(t, ordinary.Restart(false))
	waitAccessPeers(t, ordinary, first.P2PAddress(), boot.P2PAddress())
	require.Equal(t, secondPub, nodeAccessStats(t, ordinary).PreferredCore)
	require.Empty(t, nodeAccessStats(t, ordinary).AccessCore, "saved preference must not create its own connection")
	require.NoError(t, rpctest.ConnectNode(ordinary, second))
	waitAccessStats(t, ordinary, "replacement core persists across restart", func(s btcjson.GetNodeStatsResult) bool {
		return s.AccessCore == secondPub && s.PreferredAddr == second.P2PAddress()
	}, 20*time.Second)

	// Same node ID over two TCP connections is one child. The clients answer
	// pings, but send their own access declaration only once, so it must expire
	// even though their TCP connections remain alive.
	probe := func(target *rpctest.Harness, id uint64, primary bool, count uint32, version uint32) *peer.Peer {
		t.Helper()
		p, err := peer.NewOutboundPeer(&peer.Config{
			ChainParams: &chaincfg.TestNetParams, ProtocolVersion: version,
			Services: wire.SFNodeNetwork | wire.SFNodeWitness,
			NewPing: func(nonce uint64) (*wire.MsgPing, time.Duration) {
				return &wire.MsgPing{Nonce: nonce, NodeID: id, UseAsCore: primary, ChildCount: count}, 5 * time.Minute
			},
			Listeners: peer.MessageListeners{
				OnPing: func(p *peer.Peer, msg *wire.MsgPing) { p.HandlePingMsg(msg) },
				OnPong: func(p *peer.Peer, msg *wire.MsgPong) { p.HandlePongMsg(msg) },
			},
		}, target.P2PAddress())
		require.NoError(t, err)
		conn, err := net.DialTimeout("tcp", target.P2PAddress(), 3*time.Second)
		require.NoError(t, err)
		p.AssociateConnection(conn)
		t.Cleanup(p.Disconnect)
		return p
	}
	probeOne := probe(second, 12345678, true, 0, wire.ProtocolVersion)
	probeTwo := probe(second, 12345678, true, 0, wire.ProtocolVersion)
	stats := waitAccessStats(t, second, "duplicate node IDs count once", func(s btcjson.GetNodeStatsResult) bool {
		return s.ChildCount == 2
	}, 10*time.Second)
	require.EqualValues(t, expiry/time.Second, stats.WindowSeconds)
	probeOne.Disconnect()
	require.Eventually(t, func() bool { return !probeOne.Connected() }, 5*time.Second, 100*time.Millisecond)
	require.EqualValues(t, 2, nodeAccessStats(t, second).ChildCount)
	probe(boot, 22222, false, 999, wire.ProtocolVersion)
	waitAccessStats(t, boot, "ordinary peers cannot supply core totals", func(s btcjson.GetNodeStatsResult) bool {
		return s.CoreCount == 2 && s.ObservedNodes == 5
	}, 30*time.Second)
	old := probe(second, 33333, true, 0, wire.NodeAccessVersion-1)
	require.Eventually(t, func() bool { return !old.Connected() }, 5*time.Second, 100*time.Millisecond,
		"old ping protocol must be rejected at handshake")
	waitAccessStats(t, second, "stale access declaration expires", func(s btcjson.GetNodeStatsResult) bool {
		return s.ChildCount == 1
	}, expiry+5*time.Second)
	require.True(t, probeTwo.Connected(), "expiry must not rely on TCP disconnection")
	waitAccessStats(t, boot, "expired child removed from aggregate", func(s btcjson.GetNodeStatsResult) bool {
		return s.ObservedNodes == 4
	}, 30*time.Second)
}
