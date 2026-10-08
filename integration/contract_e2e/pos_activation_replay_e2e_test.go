//go:build rpctest

package contract_e2e

import (
	"encoding/hex"
	"os"
	"strconv"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Produce several legacy-format blocks before activation, restart at H-1,
// then produce approved blocks. An empty, isolated node replays the original
// bytes through submitblock, including a restart on the same activation edge.
func TestNetworkPOSV2ActivationBoundaryAndLegacyReplay(t *testing.T) {
	const activationHeight int32 = 6
	t.Setenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT", strconv.Itoa(int(activationHeight)))
	t.Setenv("SATOSHINET_POS_MINER_INTERVAL", "5")
	t.Setenv("SATOSHINET_POS_PREWARNING_INTERVAL", "5")
	fixture := newTemplateNetworkFixture(t, nil)
	bootKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	var observer *rpctest.Harness
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		nodes := append([]*rpctest.Harness{}, fixture.nodes...)
		if observer != nil {
			nodes = append(nodes, observer)
		}
		for _, node := range nodes {
			data, _ := os.ReadFile(node.LogFile())
			if len(data) > 12000 {
				data = data[len(data)-12000:]
			}
			t.Logf("node %s failure log:\n%s", node.P2PAddress(), data)
		}
	})
	_, coreReward, err := anchortx.GetP2WSHscript(bootKey.PubKey().SerializeCompressed(), coreKey.PubKey().SerializeCompressed())
	require.NoError(t, err)
	bootAddress, err := scommon.PublicKeyToTaprootAddress(bootKey.PubKey(), &chaincfg.TestNetParams)
	require.NoError(t, err)
	bootReward := bootAddress.ScriptAddress()
	blocks := make([]*wire.MsgBlock, 0, 9)
	checkBlock := func(height int32, block *wire.MsgBlock) {
		t.Helper()
		coinbase := block.Transactions[0]
		// Below the existing testnet checkpoint the legacy sorter always
		// selects Bootstrap. Activation starts ordinary Bootstrap/Core rotation.
		producer := bootKey
		if height >= activationHeight && (height-activationHeight)%2 == 1 {
			producer = coreKey
		}
		require.NoError(t, scommon.VerifyStandardCoinbaseScript(coinbase.TxIn[0].SignatureScript, producer.PubKey().SerializeCompressed()), "height=%d", height)
		if height < activationHeight {
			require.Len(t, coinbase.TxIn[0].Witness, 1, "legacy block must not require approval: height=%d", height)
		} else {
			require.Len(t, coinbase.TxIn[0].Witness, 2, "activated block requires approval: height=%d", height)
			sig, err := ecdsa.ParseDERSignature(coinbase.TxIn[0].Witness[1])
			require.NoError(t, err)
			require.True(t, anchortx.VerifyMessage(bootKey.PubKey(), scommon.POSApprovalMessage(chaincfg.TestNetParams.Net, height, block.BlockHash()), sig))
			require.NoError(t, blockchain.ValidatePOSWitnessCommitment(btcutil.NewBlock(block), false))
			if producer == coreKey {
				require.Equal(t, coreReward, coinbase.TxOut[0].PkScript)
			}
		}
		if producer == bootKey {
			// Compare the witness program with the original key-only reward.
			require.Equal(t, bootReward, coinbase.TxOut[0].PkScript[2:])
		}
		if height > 1 {
			require.Equal(t, blocks[height-2].BlockHash(), block.Header.PrevBlock)
		}
	}
	readBlock := func(height int32) *wire.MsgBlock {
		t.Helper()
		hash, err := fixture.bootstrapNode.Client.GetBlockHash(int64(height))
		require.NoError(t, err)
		block, err := fixture.bootstrapNode.Client.GetBlock(hash)
		require.NoError(t, err)
		checkBlock(height, block)
		return block
	}
	blocks = append(blocks, readBlock(1))
	funding := fixture.gasAnchor
	produce := func(height int32) {
		t.Helper()
		tx := buildTemplateSplitTx(t, fixture.traderA, wire.OutPoint{Hash: funding.TxHash(), Index: 0},
			[]*wire.TxOut{wire.NewTxOut(funding.TxOut[0].Value-100, funding.TxOut[0].Assets, fixture.spendScript)})
		fixture.signFundingInput(t, tx, funding, fixture.traderA)
		fixture.sendAndWaitTx(t, tx)
		_, gotHeight, err := fixture.bootstrapNode.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, height, gotHeight)
		blocks = append(blocks, readBlock(height))
		funding = tx
		t.Logf("produced H%d approval=%t", height, height >= activationHeight)
	}
	for height := int32(2); height < activationHeight; height++ {
		produce(height)
	}
	// Loading a legacy H-1 sorter must still select the right first v2 slot.
	require.NoError(t, fixture.bootstrapNode.Restart(false))
	require.NoError(t, rpctest.JoinNodes(fixture.nodes, rpctest.Blocks))
	beforeHash, beforeHeight, err := fixture.bootstrapNode.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, activationHeight-1, beforeHeight)
	require.Equal(t, blocks[activationHeight-2].BlockHash(), *beforeHash)
	for height := activationHeight; height < activationHeight+4; height++ {
		produce(height)
	}

	// The replay node starts empty after the source has already crossed H.
	// Its isolated RPC submissions exercise full per-height validation without
	// copying a database, bypassing validation, or adding approval to old blocks.
	locked := templateLockedOutPoint("gas", 0)
	gas := fixture.gasAnchor.TxOut[0].Assets[0].Name.String()
	fake := startFakeL1Indexer(t, hex.EncodeToString(bootKey.PubKey().SerializeCompressed()), map[string]*indexercommon.AssetsInUtxo{
		locked: {OutPoint: locked, Value: 200000, PkScript: coreReward, Assets: []*indexercommon.DisplayAsset{testDisplayAsset(gas, "100000000")}},
	})
	observer = startSatoshiNetNode(t, fake, "legacy-replay", coreMnemonic)
	require.NoError(t, observer.Client.SetGenerate(false, 0))
	_, emptyHeight, err := observer.Client.GetBestBlock()
	require.NoError(t, err)
	require.Zero(t, emptyHeight)
	for i, block := range blocks {
		height := int32(i + 1)
		if height == activationHeight {
			unsigned := block.Copy()
			unsigned.Transactions[0].TxIn[0].Witness = unsigned.Transactions[0].TxIn[0].Witness[:1]
			require.Error(t, observer.Client.SubmitBlock(btcutil.NewBlock(unsigned), nil), "legacy witness format is invalid at H")
			hash, h, err := observer.Client.GetBestBlock()
			require.NoError(t, err)
			require.Equal(t, activationHeight-1, h)
			require.Equal(t, blocks[i-1].BlockHash(), *hash)
		}
		require.NoError(t, observer.Client.SubmitBlock(btcutil.NewBlock(block), nil), "replay H%d", height)
		hash, h, err := observer.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, height, h)
		require.Equal(t, block.BlockHash(), *hash)
		stored, err := observer.Client.GetBlock(hash)
		require.NoError(t, err)
		require.Equal(t, block, stored, "replay must retain original legacy/approved bytes")
		if height == activationHeight-1 {
			require.NoError(t, observer.Restart(false))
			require.NoError(t, observer.Client.SetGenerate(false, 0))
			recovered, recoveredHeight, err := observer.Client.GetBestBlock()
			require.NoError(t, err)
			require.Equal(t, height, recoveredHeight)
			require.Equal(t, *hash, *recovered)
		}
		t.Logf("replayed H%d approval=%t", height, height >= activationHeight)
	}
	requireAssetSummaryAmount(t, observer, fixture.spendAddress, gas, "100000000")
	_, finalHeight, err := observer.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, activationHeight+3, finalHeight)
	// Ensure reopening the mixed legacy/v2 chain also restores its tip.
	require.NoError(t, observer.Restart(false))
	recovered, recoveredHeight, err := observer.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, finalHeight, recoveredHeight)
	require.Equal(t, blocks[len(blocks)-1].BlockHash(), *recovered)
	require.Eventually(t, func() bool {
		summary, err := fetchAssetSummary(observer, fixture.spendAddress)
		return err == nil && summary[gas] == "100000000"
	}, 15*time.Second, 100*time.Millisecond, "reopened chain lost replayed asset state")
}
