//go:build rpctest
// +build rpctest

package contract_e2e

import (
	"encoding/hex"
	"errors"
	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/contract/evm"
	sindexercommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNetworkAscendFromFakeL1Indexer(t *testing.T) {
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	const (
		lockedUtxo  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:0"
		lockedValue = int64(50000)
	)
	gasAsset := evm.DefaultGasConfig().GasAssetName
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	callerKeys := []*btcec.PrivateKey{
		keyFromMnemonic(t, bootstrapMnemonic, 1),
		keyFromMnemonic(t, bootstrapMnemonic, 2),
		keyFromMnemonic(t, bootstrapMnemonic, 3),
	}
	require.Equal(t, indexercommon.GetBootstrapPubKey(),
		hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()))

	witnessScript, lockedPkScript, err := anchortx.GetP2WSHscript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)

	// The fake L1 indexer must be listening before the SatoshiNet node starts,
	// because the node reads the L1 indexer endpoint from its startup config and
	// uses it while validating newly received ascend transactions.
	fakeL1 := startFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()),
		map[string]*indexercommon.AssetsInUtxo{
			lockedUtxo: {
				OutPoint: lockedUtxo,
				Value:    lockedValue,
				PkScript: lockedPkScript,
				Assets: []*indexercommon.DisplayAsset{
					testDisplayAsset(gasAsset, "1000000"),
				},
			},
		})
	bootstrapNode, coreNode := startSatoshiNetNetwork(t, fakeL1)
	nodes := []*rpctest.Harness{bootstrapNode, coreNode}
	spendScript, spendAddress, redeemScript, controlBlock := testCallerTaprootScript(t, callerKeys[0])

	anchorTx := wire.NewMsgTx(2)
	anchorTx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.AnchorTxOutIndex},
		SignatureScript: signedAnchorScript(t, lockedUtxo, witnessScript,
			lockedValue, testWireAsset(gasAsset, 1000000), bootstrapKey),
	})
	anchorTx.AddTxOut(wire.NewTxOut(lockedValue, testWireAsset(gasAsset, 1000000), spendScript))
	ascendingScript, err := sindexercommon.NullDataScript(
		sindexercommon.CONTENT_TYPE_ASCENDING,
		[]byte(gasAsset+"-1000000-0-0"),
	)
	require.NoError(t, err)
	anchorTx.AddTxOut(wire.NewTxOut(0, nil, ascendingScript))

	txHash, err := bootstrapNode.Client.SendRawTransaction(anchorTx, true)
	require.NoError(t, err)
	require.Equal(t, anchorTx.TxHash(), *txHash)

	verbose, err := bootstrapNode.Client.GetRawTransactionVerbose(txHash)
	require.NoError(t, err)
	require.Equal(t, uint64(0), verbose.Confirmations)

	blockHashes := generateOrWaitBlockAtLeast(t, bootstrapNode, nodes, 1)

	verbose, err = bootstrapNode.Client.GetRawTransactionVerbose(txHash)
	require.NoError(t, err)
	require.Equal(t, uint64(1), verbose.Confirmations)

	deployTx, invokeTxs, contract := buildCounterContractTxs(t, anchorTx, callerKeys,
		spendScript, spendAddress, redeemScript, controlBlock)
	deployHash, err := bootstrapNode.Client.SendRawTransaction(deployTx, true)
	require.NoError(t, err)
	require.Equal(t, deployTx.TxHash(), *deployHash)
	blockHashes = generateOrWaitBlockAtLeast(t, bootstrapNode, nodes, 2)
	deployVerboseAfterMine, err := bootstrapNode.Client.GetRawTransactionVerbose(deployHash)
	require.NoError(t, err)
	require.Equal(t, uint64(1), deployVerboseAfterMine.Confirmations)

	for _, invokeTx := range invokeTxs {
		invokeHash, err := bootstrapNode.Client.SendRawTransaction(invokeTx, true)
		require.NoError(t, err)
		require.Equal(t, invokeTx.TxHash(), *invokeHash)
	}
	mempoolHashes, err := bootstrapNode.Client.GetRawMempool()
	require.NoError(t, err)
	require.NotEmpty(t, mempoolHashes)
	blockHashes = generateOrWaitBlockAtLeast(t, bootstrapNode, nodes, 3)

	bestHash, bestHeight, err := bootstrapNode.Client.GetBestBlock()
	require.NoError(t, err)
	require.GreaterOrEqual(t, bestHeight, int32(3))
	require.Equal(t, blockHashes[0], bestHash)
	coreBestHash, coreBestHeight, err := coreNode.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, bestHeight, coreBestHeight)
	require.Equal(t, bestHash, coreBestHash)

	deployTxHash := deployTx.TxHash()
	deployVerbose, err := bootstrapNode.Client.GetRawTransactionVerbose(&deployTxHash)
	require.NoError(t, err)
	require.GreaterOrEqual(t, deployVerbose.Confirmations, uint64(1))
	for _, invokeTx := range invokeTxs {
		invokeHash := invokeTx.TxHash()
		invokeVerbose, err := bootstrapNode.Client.GetRawTransactionVerbose(&invokeHash)
		require.NoError(t, err)
		require.GreaterOrEqual(t, invokeVerbose.Confirmations, uint64(1))
	}

	block, err := bootstrapNode.Client.GetBlock(blockHashes[0])
	require.NoError(t, err)
	coinbase := block.Transactions[0]
	root, found, err := evm.FindCoinbaseStateRoot(coinbase)
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, [32]byte{}, root.StateRoot)
	require.Equal(t, evm.TestnetContractPrefix, contract.Prefix())
}

func TestWaitForPOSBlockError(t *testing.T) {
	require.True(t, waitForPOSBlockError(errors.New("no any new tx")))
	require.True(t, waitForPOSBlockError(errors.New(
		"Server is already in POS mining. Please call setgenerate 0 before calling discrete generate commands.",
	)))
	require.False(t, waitForPOSBlockError(errors.New("unexpected RPC failure")))
	require.False(t, waitForPOSBlockError(nil))
}
