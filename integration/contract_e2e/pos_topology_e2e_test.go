//go:build rpctest

package contract_e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

// Seven real processes: one Bootstrap, two Cores and two Miners per Core.
// Nodes join through ordinary L1 anchor transactions; no sorter state is
// injected. Offline cases stop processes, rather than merely pausing mining.
func TestNetworkPOSV2MultiCoreMultiMinerOffline(t *testing.T) {
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", "2")
	t.Setenv("SATOSHINET_POS_MINER_INTERVAL", "5")
	t.Setenv("SATOSHINET_POS_PREWARNING_INTERVAL", "5")
	configureFastPOSTimers(t)
	previousTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() { indexercommon.ENABLE_TESTING = previousTesting })

	type member struct {
		name, mnemonic, pub string
		key                 *btcec.PrivateKey
		parent              *member
		children            []*member
		node                *rpctest.Harness
		reward              []byte
		online              bool
	}
	newMember := func(name, mnemonic string, parent *member) *member {
		if mnemonic == "" {
			entropy := sha256.Sum256([]byte("satoshinet-pos-topology-e2e:" + name))
			var err error
			mnemonic, err = bip39.NewMnemonic(entropy[:16])
			require.NoError(t, err)
		}
		key := keyFromMnemonic(t, mnemonic, 0)
		m := &member{name: name, mnemonic: mnemonic, key: key,
			pub: hex.EncodeToString(key.PubKey().SerializeCompressed()), parent: parent}
		if parent != nil {
			_, script, err := anchortx.GetP2WSHscript(parent.key.PubKey().SerializeCompressed(), key.PubKey().SerializeCompressed())
			require.NoError(t, err)
			m.reward = script
			parent.children = append(parent.children, m)
		} else {
			addr, err := scommon.PublicKeyToTaprootAddress(key.PubKey(), &chaincfg.TestNetParams)
			require.NoError(t, err)
			m.reward, err = txscript.PayToAddrScript(addr)
			require.NoError(t, err)
		}
		return m
	}
	boot := newMember("bootstrap", bootstrapMnemonic, nil)
	defaultCore := newMember("core-default", coreMnemonic, boot)
	newMember("core-second", "", boot)
	for _, core := range boot.children {
		newMember(core.name+"-miner-1", "", core)
		newMember(core.name+"-miner-2", "", core)
		sort.Slice(core.children, func(i, j int) bool { return core.children[i].pub < core.children[j].pub })
	}
	sort.Slice(boot.children, func(i, j int) bool { return boot.children[i].pub < boot.children[j].pub })
	sequence := []*member{boot}
	for _, core := range boot.children {
		sequence = append(sequence, core)
		sequence = append(sequence, core.children...)
	}

	actor := newTemplateNetworkActor(t, keyFromMnemonic(t, bootstrapMnemonic, 1))
	stake := indexercommon.GetStakeAssetNameWithHeightL2(1)
	amount := indexercommon.GetStakeAssetAmtWithHeightL2(1)
	const lockedValue = int64(200000)
	utxos := make(map[string]*indexercommon.AssetsInUtxo)
	anchors := make(map[*member]*wire.MsgTx)
	for _, m := range sequence[1:] {
		locked := templateLockedOutPoint("pos-topology:"+m.name, 0)
		witness, script, err := anchortx.GetP2WSHscript(m.parent.key.PubKey().SerializeCompressed(), m.key.PubKey().SerializeCompressed())
		require.NoError(t, err)
		utxos[locked] = &indexercommon.AssetsInUtxo{OutPoint: locked, Value: lockedValue, PkScript: script,
			Assets: []*indexercommon.DisplayAsset{testDisplayAsset(stake, fmt.Sprint(amount))}}
		// Core registration is mined below H; Miners register in the next block at H.
		targetHeight := int32(2)
		if m.parent == boot {
			targetHeight = 1
		}
		anchors[m] = buildNetworkAnchorTx(t, locked, lockedValue, testWireAsset(stake, amount),
			fmt.Sprintf("%s-%d-0-0", stake, amount), witness, m.parent.key, actor.pkScript, targetHeight)
	}
	// Ordinary Miners use their parent Core's indexer identity, which startup
	// checks against --serverpubkey. The two-node fixture used Bootstrap for all.
	parentByPub := make(map[string]string)
	for _, m := range sequence[1:] {
		parentByPub[m.pub] = m.parent.pub
	}
	fake := startFakeL1Indexer(t, boot.pub, utxos, parentByPub)
	boot.node, defaultCore.node = startSatoshiNetNetwork(t, fake)
	boot.online, defaultCore.online = true, true
	onlineNodes := func() []*rpctest.Harness {
		var nodes []*rpctest.Harness
		for _, m := range sequence {
			if m.online {
				nodes = append(nodes, m.node)
			}
		}
		return nodes
	}
	dumpFailure := func() {
		if t.Failed() {
			for _, m := range sequence {
				if m.node == nil {
					continue
				}
				data, _ := os.ReadFile(m.node.LogFile())
				if len(data) > 12000 {
					data = data[len(data)-12000:]
				}
				t.Logf("%s failure log:\n%s", m.name, data)
			}
		}
	}
	// Cores register first, then Miners, so each parent already exists when
	// the indexer processes its children. The default Core is in genesis state.
	for _, core := range boot.children {
		sendTx(t, boot.node, anchors[core])
	}
	for _, core := range boot.children {
		waitForPOSTx(t, boot.node, onlineNodes(), anchors[core])
	}
	for _, core := range boot.children {
		for _, miner := range core.children {
			sendTx(t, boot.node, anchors[miner])
		}
	}
	for _, core := range boot.children {
		for _, miner := range core.children {
			waitForPOSTx(t, boot.node, onlineNodes(), anchors[miner])
		}
	}
	_, height, err := boot.node.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, int32(2), height, "registration must complete in two blocks")
	for _, m := range sequence[1:] {
		if m.node == nil {
			m.node = startSatoshiNetNode(t, fake, m.name, m.mnemonic, "--serverpubkey="+m.parent.pub)
			m.online = true
			require.NoError(t, rpctest.ConnectNode(m.node, m.parent.node))
		}
	}
	require.NoError(t, rpctest.JoinNodes(onlineNodes(), rpctest.Blocks))
	for _, m := range sequence {
		require.Eventually(t, func() bool {
			data, _ := os.ReadFile(m.node.LogFile())
			return strings.Contains(string(data), "POS miner started")
		}, 25*time.Second, 100*time.Millisecond, "%s mining runtime did not start", m.name)
	}
	// Runs before node cleanup removes their logs, including newly joined nodes.
	t.Cleanup(dumpFailure)

	// H2 is Bootstrap's activated slot. Each following block advances exactly
	// one original slot, even when an ancestor produces on its behalf.
	next := 1
	funding := anchors[defaultCore]
	produce := func(t *testing.T, producer *member, reward []byte) {
		t.Helper()
		slot := sequence[next]
		prevHash, prevHeight, err := boot.node.Client.GetBestBlock()
		require.NoError(t, err)
		tx := buildTemplateSplitTx(t, actor.key, wire.OutPoint{Hash: funding.TxHash(), Index: 0},
			[]*wire.TxOut{wire.NewTxOut(funding.TxOut[0].Value-100, funding.TxOut[0].Assets, actor.pkScript)})
		signTemplateTaprootInputs(t, tx, actor.key, actor.redeemScript, actor.controlBlock)
		sendTx(t, boot.node, tx)
		waitForPOSTx(t, boot.node, onlineNodes(), tx)
		hash, gotHeight, err := boot.node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, prevHeight+1, gotHeight, "exactly one slot per submitted transaction")
		block, err := boot.node.Client.GetBlock(hash)
		require.NoError(t, err)
		require.Equal(t, *prevHash, block.Header.PrevBlock)
		coinbase := block.Transactions[0]
		require.NoError(t, scommon.VerifyStandardCoinbaseScript(coinbase.TxIn[0].SignatureScript,
			producer.key.PubKey().SerializeCompressed()), "height=%d slot=%s producer=%s", gotHeight, slot.name, producer.name)
		require.Equal(t, reward, coinbase.TxOut[0].PkScript, "height=%d slot=%s reward", gotHeight, slot.name)
		require.Len(t, coinbase.TxIn[0].Witness, 2)
		sig, err := ecdsa.ParseDERSignature(coinbase.TxIn[0].Witness[1])
		require.NoError(t, err)
		require.True(t, anchortx.VerifyMessage(boot.key.PubKey(), scommon.POSApprovalMessage(chaincfg.TestNetParams.Net, gotHeight, *hash), sig))
		require.NoError(t, blockchain.ValidatePOSWitnessCommitment(btcutil.NewBlock(block), false))
		for _, m := range sequence {
			if m.online {
				got, h, err := m.node.Client.GetBestBlock()
				require.NoError(t, err)
				require.Equal(t, gotHeight, h, m.name)
				require.Equal(t, hash, got, m.name)
			}
		}
		t.Logf("H%d slot=%s producer=%s online=%d", gotHeight, slot.name, producer.name, len(onlineNodes()))
		funding = tx
		next = (next + 1) % len(sequence)
	}
	advanceTo := func(t *testing.T, target *member) {
		for sequence[next] != target {
			slot := sequence[next]
			produce(t, slot, slot.reward)
		}
	}
	stop := func(t *testing.T, m *member) {
		t.Helper()
		require.NoError(t, m.node.StopNode())
		m.online = false
	}
	restart := func(t *testing.T, m *member) {
		t.Helper()
		require.NoError(t, m.node.Restart(false))
		m.online = true
		require.NoError(t, rpctest.ConnectNode(m.node, m.parent.node))
		require.NoError(t, rpctest.JoinNodes(onlineNodes(), rpctest.Blocks))
	}

	if !t.Run("all_online_full_round", func(t *testing.T) {
		for i := 0; i < len(sequence); i++ {
			slot := sequence[next]
			produce(t, slot, slot.reward)
		}
	}) {
		return
	}
	core := boot.children[0]
	miner := core.children[0]
	if !t.Run("miner_offline_core_substitutes", func(t *testing.T) {
		advanceTo(t, miner)
		stop(t, miner)
		produce(t, core, miner.reward) // Miner–Core reward, not Core–Bootstrap.
		produce(t, core.children[1], core.children[1].reward)
		restart(t, miner)
		advanceTo(t, miner)
		produce(t, miner, miner.reward) // Rejoined Miner produces its next slot.
	}) {
		return
	}
	if !t.Run("core_offline_miners_still_online", func(t *testing.T) {
		advanceTo(t, core)
		// Keep children on the network through another Core. That Core is a
		// gossip peer, not their parent or an eligible substitute for this group.
		for _, child := range core.children {
			require.NoError(t, rpctest.ConnectNode(child.node, boot.children[1].node))
		}
		stop(t, core)
		produce(t, boot, core.reward)
		for range core.children {
			produce(t, boot, core.reward) // Bootstrap pays Core–Bootstrap for child slots.
		}
		restart(t, core)
		advanceTo(t, core)
		produce(t, core, core.reward)
		for _, child := range core.children {
			produce(t, child, child.reward)
		}
	}) {
		return
	}
	if !t.Run("whole_group_offline_bootstrap_substitutes", func(t *testing.T) {
		advanceTo(t, core)
		for _, child := range core.children {
			stop(t, child)
		}
		stop(t, core)
		produce(t, boot, core.reward)
		for range core.children {
			produce(t, boot, core.reward)
		}
		other := boot.children[1]
		produce(t, other, other.reward)
		for _, child := range other.children {
			produce(t, child, child.reward)
		}
		restart(t, core)
		for _, child := range core.children {
			restart(t, child)
		}
		// Final full round checks catch-up/replay and restored production for
		// every member, after both kinds of ancestor substitution.
		for i := 0; i < len(sequence); i++ {
			slot := sequence[next]
			produce(t, slot, slot.reward)
		}
	}) {
		return
	}
}
