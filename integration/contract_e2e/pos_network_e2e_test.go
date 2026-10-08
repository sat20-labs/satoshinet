//go:build rpctest

package contract_e2e

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// This uses real wallet-signed POS production, ordinary P2P propagation, a
// separate verifier's submitblock entry, and a killed/restarted Bootstrap.
func TestNetworkPOSV2ActivationApprovalAndRestart(t *testing.T) {
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", "2")
	t.Setenv("SATOSHINET_POS_MINER_INTERVAL", "5")
	t.Setenv("SATOSHINET_POS_PREWARNING_INTERVAL", "5")
	fixture := newTemplateNetworkFixture(t, nil)
	bootstrap := fixture.bootstrapNode
	core := fixture.coreNode
	t.Cleanup(func() {
		if t.Failed() {
			for _, node := range fixture.nodes {
				data, _ := os.ReadFile(node.LogFile())
				lines := strings.Split(string(data), "\n")
				if len(lines) > 120 {
					lines = lines[len(lines)-120:]
				}
				t.Logf("node %s failure log:\n%s", node.P2PAddress(), strings.Join(lines, "\n"))
			}
		}
	})
	key := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	firstHash, err := bootstrap.Client.GetBlockHash(1)
	require.NoError(t, err)
	first, err := bootstrap.Client.GetBlock(firstHash)
	require.NoError(t, err)
	require.Len(t, first.Transactions[0].TxIn[0].Witness, 1, "H-1 retains legacy format")

	gasAsset := fixture.gasAnchor.TxOut[0].Assets[0].Name.String()
	outs := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{50000000, 50000000}, []int64{1000, 1000}, fixture.traderA)
	require.Len(t, outs, 2)
	secondHash, err := bootstrap.Client.GetBlockHash(2)
	require.NoError(t, err)
	second, err := bootstrap.Client.GetBlock(secondHash)
	require.NoError(t, err)
	require.Len(t, second.Transactions[0].TxIn[0].Witness, 2, "H requires approval")
	sig, err := ecdsa.ParseDERSignature(second.Transactions[0].TxIn[0].Witness[1])
	require.NoError(t, err)
	require.True(t, anchortx.VerifyMessage(key.PubKey(), scommon.POSApprovalMessage(chaincfg.TestNetParams.Net, 2, *secondHash), sig))
	require.NoError(t, blockchain.ValidatePOSWitnessCommitment(btcutil.NewBlock(second), false))
	fixture.requireNodesSynced(t)

	// An isolated follower sees invalid variants before the valid one. The
	// bad approval is not header-committed and must not poison the block hash.
	oldTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() { indexercommon.ENABLE_TESTING = oldTesting })
	_, lockedScript, err := anchortx.GetP2WSHscript(key.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	locked := templateLockedOutPoint("gas", 0)
	fake := startFakeL1Indexer(t, hex.EncodeToString(key.PubKey().SerializeCompressed()), map[string]*indexercommon.AssetsInUtxo{
		locked: {OutPoint: locked, Value: 200000, PkScript: lockedScript, Assets: []*indexercommon.DisplayAsset{testDisplayAsset(gasAsset, "100000000")}},
	})
	observer := startSatoshiNetNode(t, fake, "observer", coreMnemonic)
	require.NoError(t, observer.Client.SetGenerate(false, 0))
	t.Cleanup(func() {
		if t.Failed() {
			data, _ := os.ReadFile(observer.LogFile())
			lines := strings.Split(string(data), "\n")
			if len(lines) > 100 {
				lines = lines[len(lines)-100:]
			}
			t.Logf("observer failure log:\n%s", strings.Join(lines, "\n"))
		}
	})
	require.NoError(t, observer.Client.SubmitBlock(btcutil.NewBlock(first), nil))
	for _, variant := range []string{"unsigned", "bad-signature", "header", "transaction-witness"} {
		bad := second.Copy()
		switch variant {
		case "unsigned":
			bad.Transactions[0].TxIn[0].Witness = bad.Transactions[0].TxIn[0].Witness[:1]
		case "bad-signature":
			bad.Transactions[0].TxIn[0].Witness[1] = []byte{1}
		case "header":
			bad.Header.Nonce++
		case "transaction-witness":
			// H2 spends the channel: element zero is the CHECKMULTISIG dummy.
			bad.Transactions[1].TxIn[0].Witness[1][0] ^= 1
		}
		require.Error(t, observer.Client.SubmitBlock(btcutil.NewBlock(bad), nil), variant)
		_, height, err := observer.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, int32(1), height, "rejected variant changed canonical tip")
	}
	// Proposal RPC bypasses the ordinary sync queue. Exercise it while an
	// approved block connects and advances the real asset index on this node.
	proposal := second.Copy()
	proposal.Transactions[0].TxIn[0].Witness = proposal.Transactions[0].TxIn[0].Witness[:1]
	proposalBytes, err := btcutil.NewBlock(proposal).Bytes()
	require.NoError(t, err)
	request, err := json.Marshal(map[string]string{"mode": "proposal", "data": hex.EncodeToString(proposalBytes)})
	require.NoError(t, err)
	proposalDone := make(chan error, 1)
	go func() {
		for i := 0; i < 20; i++ {
			result, err := observer.Client.RawRequest("getblocktemplate", []json.RawMessage{request})
			if err != nil {
				proposalDone <- err
				return
			}
			// Before connection this is a valid proposal. After connection,
			// its old parent is rejected either before or under the chain lock.
			if string(result) != "null" && string(result) != `"bad-prevblk"` && string(result) != `"inconclusive-not-best-prvblk"` {
				proposalDone <- fmt.Errorf("unexpected concurrent proposal result: %s", result)
				return
			}
		}
		proposalDone <- nil
	}()
	require.NoError(t, observer.Client.SubmitBlock(btcutil.NewBlock(second), nil), "valid same-hash variant after invalid witness")
	select {
	case err := <-proposalDone:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("proposal RPC blocked while the approved block connected")
	}
	observed, height, err := observer.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, int32(2), height)
	require.Equal(t, secondHash, observed)

	// The next slot is Core. Its unsigned proposal travels upward and only
	// the Bootstrap-approved bytes propagate back through ordinary P2P.
	split, err := bootstrap.Client.GetRawTransaction(&outs[0].Hash)
	require.NoError(t, err)
	fixture.splitAsset(t, split.MsgTx(), gasAsset, []int64{50000000}, []int64{900}, fixture.traderA)
	thirdHash, err := core.Client.GetBlockHash(3)
	require.NoError(t, err)
	third, err := core.Client.GetBlock(thirdHash)
	require.NoError(t, err)
	require.NoError(t, scommon.VerifyStandardCoinbaseScript(third.Transactions[0].TxIn[0].SignatureScript, coreKey.PubKey().SerializeCompressed()), "Core produced scheduled slot")
	require.Equal(t, lockedScript, third.Transactions[0].TxOut[0].PkScript, "Core–Bootstrap reward")
	require.Len(t, third.Transactions[0].TxIn[0].Witness, 2)

	// Kill rather than gracefully close: the acknowledged block must already
	// be durable, and cannot depend on ffldb's shutdown flush.
	require.NoError(t, bootstrap.Restart(true))
	restarted, restartedHeight, err := bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, int32(3), restartedHeight)
	require.Equal(t, thirdHash, restarted)
	require.NoError(t, rpctest.JoinNodes(fixture.nodes, rpctest.Blocks))
	fixture.splitAsset(t, third.Transactions[1], gasAsset, []int64{50000000}, []int64{800}, fixture.traderA)
	fixture.requireNodesSynced(t)
	fourthHash, err := bootstrap.Client.GetBlockHash(4)
	require.NoError(t, err)
	fourth, err := bootstrap.Client.GetBlock(fourthHash)
	require.NoError(t, err)
	require.Len(t, fourth.Transactions[0].TxIn[0].Witness, 2)

	// Core stops producing. Bootstrap may substitute after the existing
	// timeout, and pays the original Core–Bootstrap channel.
	require.NoError(t, core.Client.SetGenerate(false, 0))
	fixture.splitAsset(t, fourth.Transactions[1], gasAsset, []int64{50000000}, []int64{700}, fixture.traderA)
	fifthHash, err := bootstrap.Client.GetBlockHash(5)
	require.NoError(t, err)
	fifth, err := bootstrap.Client.GetBlock(fifthHash)
	require.NoError(t, err)
	require.NoError(t, scommon.VerifyStandardCoinbaseScript(fifth.Transactions[0].TxIn[0].SignatureScript, key.PubKey().SerializeCompressed()), "Bootstrap substituted for Core")
	require.Equal(t, lockedScript, fifth.Transactions[0].TxOut[0].PkScript)

	// Even a correctly Bootstrap-signed competitor cannot replace an
	// activated tip. Indexer reorg now stops the node for manual handling;
	// its before-close boundary is covered by the manager regression.
	require.NoError(t, observer.Client.SubmitBlock(btcutil.NewBlock(third), nil))
	require.NoError(t, observer.Client.SubmitBlock(btcutil.NewBlock(fourth), nil))
	altThird := third.Copy()
	altThird.Header.Nonce++
	msg := scommon.POSApprovalMessage(chaincfg.TestNetParams.Net, 3, altThird.BlockHash())
	altThird.Transactions[0].TxIn[0].Witness[1] = ecdsa.Sign(key, chainhash.HashB(msg)).Serialize()
	require.Error(t, observer.Client.SubmitBlock(btcutil.NewBlock(altThird), nil), "approved competitor must be rejected")
	stillCanonical, height, err := observer.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, int32(4), height)
	require.Equal(t, fourthHash, stillCanonical)
	require.Zero(t, fifth.Header.Bits, "current POS blocks add zero chainwork")

	// Resume Core production and extend the original approved chain.
	require.NoError(t, core.Client.SetGenerate(true, 1))
	fixture.requireNodesSynced(t)
	fixture.splitAsset(t, fifth.Transactions[1], gasAsset, []int64{50000000}, []int64{600}, fixture.traderA)
	sixthHash, err := bootstrap.Client.GetBlockHash(6)
	require.NoError(t, err)
	sixth, err := bootstrap.Client.GetBlock(sixthHash)
	require.NoError(t, err)
	require.Equal(t, *fifthHash, sixth.Header.PrevBlock)
	require.Len(t, sixth.Transactions[0].TxIn[0].Witness, 2)
	fixture.splitAsset(t, sixth.Transactions[1], gasAsset, []int64{50000000}, []int64{500}, fixture.traderA)
	seventhHash, err := bootstrap.Client.GetBlockHash(7)
	require.NoError(t, err)
	seventh, err := bootstrap.Client.GetBlock(seventhHash)
	require.NoError(t, err)
	require.Equal(t, *sixthHash, seventh.Header.PrevBlock)
	require.Len(t, seventh.Transactions[0].TxIn[0].Witness, 2)
	require.NoError(t, scommon.VerifyStandardCoinbaseScript(seventh.Transactions[0].TxIn[0].SignatureScript, coreKey.PubKey().SerializeCompressed()), "Core proposal approved on the canonical chain")
	fixture.requireNodesSynced(t)
}

// A producer must neither accept its unsigned proposal on ACK nor regenerate
// a different hash just because approval timed out.
func TestNetworkPOSV2UnavailableApproverRetainsCandidate(t *testing.T) {
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", "2")
	t.Setenv("SATOSHINET_POS_MINER_INTERVAL", "5")
	t.Setenv("SATOSHINET_POS_PREWARNING_INTERVAL", "5")
	fixture := newTemplateNetworkFixture(t, nil)
	gas := fixture.gasAnchor.TxOut[0].Assets[0].Name.String()
	fixture.splitAsset(t, fixture.gasAnchor, gas, []int64{100000000}, []int64{1000}, fixture.traderA)
	secondHash, err := fixture.bootstrapNode.Client.GetBlockHash(2)
	require.NoError(t, err)
	second, err := fixture.bootstrapNode.Client.GetBlock(secondHash)
	require.NoError(t, err)
	require.NoError(t, fixture.bootstrapNode.Client.SetGenerate(false, 0))
	tx := buildTemplateSplitTx(t, fixture.traderA, wire.OutPoint{Hash: second.Transactions[1].TxHash(), Index: 0}, []*wire.TxOut{wire.NewTxOut(900, testWireAsset(gas, 100000000), fixture.spendScript)})
	signTemplateTaprootInputs(t, tx, fixture.traderA, fixture.redeemScript, fixture.controlBlock)
	logBefore, err := os.ReadFile(fixture.coreNode.LogFile())
	require.NoError(t, err)
	sendTx(t, fixture.coreNode, tx)
	candidateHashes := func() []string {
		data, err := os.ReadFile(fixture.coreNode.LogFile())
		require.NoError(t, err)
		lines := strings.Split(string(data[len(logBefore):]), "\n")
		var hashes []string
		for _, line := range lines {
			if _, suffix, ok := strings.Cut(line, "OnTimeGenerateBlock generate block "); ok {
				hashes = append(hashes, strings.TrimSpace(suffix))
			}
		}
		return hashes
	}
	require.Eventually(t, func() bool { return len(candidateHashes()) > 0 }, 25*time.Second, 100*time.Millisecond)
	// Allow multiple four-second ACK timeouts and timer retries.
	time.Sleep(10 * time.Second)
	for _, node := range fixture.nodes {
		hash, height, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, int32(2), height, "unsigned candidate advanced tip")
		require.Equal(t, secondHash, hash)
	}
	hashes := candidateHashes()
	require.Len(t, hashes, 1, "timeout rebuilt a different candidate")
	require.NoError(t, fixture.bootstrapNode.Client.SetGenerate(true, 1))
	fixture.waitForTx(t, tx)
	thirdHash, err := fixture.bootstrapNode.Client.GetBlockHash(3)
	require.NoError(t, err)
	// The recovered Bootstrap may approve the retained candidate or win the
	// already elapsed substitute timeout. Either must carry a valid approval;
	// the producer must not rebuild another candidate at this parent.
	require.Len(t, candidateHashes(), 1, "producer rebuilt while retrying approval")
	third, err := fixture.bootstrapNode.Client.GetBlock(thirdHash)
	require.NoError(t, err)
	require.Len(t, third.Transactions[0].TxIn[0].Witness, 2)
}
