//go:build rpctest
// +build rpctest

package contract_e2e

import (
	"encoding/hex"
	gethcommon "github.com/ethereum/go-ethereum/common"
	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"math/big"
	"testing"
)

func TestNetworkSolidityContractsDeployInvokeAndAssetSettlement(t *testing.T) {
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	counter := compileNetworkSolidityContract(t, solidityNetworkCounterSource, "Counter")
	erc20 := compileNetworkSolidityContract(t, solidityNetworkMiniERC20Source, "MiniERC20")
	vault := compileNetworkSolidityContract(t, solidityNetworkVaultSource, "SatoshiNetTimelockVault")

	const (
		gasLockedUtxo   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:0"
		assetLockedUtxo = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc:0"
		lockedValue     = int64(100000)
		vaultAsset      = "ordx:ft:usd"
	)
	gasAsset := evm.DefaultGasConfig().GasAssetName
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	callerKeys := []*btcec.PrivateKey{
		keyFromMnemonic(t, bootstrapMnemonic, 1),
		keyFromMnemonic(t, bootstrapMnemonic, 2),
		keyFromMnemonic(t, bootstrapMnemonic, 3),
	}
	witnessScript, lockedPkScript, err := anchortx.GetP2WSHscript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)

	fakeL1 := startFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()),
		map[string]*indexercommon.AssetsInUtxo{
			gasLockedUtxo: {
				OutPoint: gasLockedUtxo,
				Value:    lockedValue,
				PkScript: lockedPkScript,
				Assets: []*indexercommon.DisplayAsset{
					testDisplayAsset(gasAsset, "100000000"),
				},
			},
			assetLockedUtxo: {
				OutPoint: assetLockedUtxo,
				Value:    lockedValue,
				PkScript: lockedPkScript,
				Assets: []*indexercommon.DisplayAsset{
					testDisplayAssetWithPrecision(vaultAsset, "10.00", 2),
				},
			},
		})
	bootstrapNode, coreNode := startSatoshiNetNetwork(t, fakeL1)
	nodes := []*rpctest.Harness{bootstrapNode, coreNode}
	caller0Script, caller0Address, _, _ := testCallerTaprootScript(t, callerKeys[0])
	caller1Script, caller1Address, _, _ := testCallerTaprootScript(t, callerKeys[1])
	caller2Script, caller2Address, _, _ := testCallerTaprootScript(t, callerKeys[2])
	recipient := p2trAddressFromKey(t, callerKeys[2])

	gasAnchor := buildNetworkAnchorTx(t, gasLockedUtxo, lockedValue,
		testWireAsset(gasAsset, 100000000), gasAsset+"-100000000-0-0",
		witnessScript, bootstrapKey, caller0Script, 2)
	assetAnchor := buildNetworkAnchorTx(t, assetLockedUtxo, lockedValue,
		testWireDecimalAsset(t, vaultAsset, "10.00", 2), vaultAsset+"-10.00-2-0",
		witnessScript, bootstrapKey, caller0Script, 2)
	sendTx(t, bootstrapNode, gasAnchor)
	sendTx(t, bootstrapNode, assetAnchor)
	generateOrWaitBlockAtLeast(t, bootstrapNode, nodes, 1)

	counterDeploy, counterContract, counterChanges := buildSolidityDeployTx(t, callerKeys[0], 1,
		solidityDeployCode(t, counter, nil),
		[]wire.OutPoint{{Hash: gasAnchor.TxHash(), Index: 0}},
		wire.TxOut{Assets: wire.TxAssets{networkGasFunding(t, gasAsset, 3000000)}},
		[]*wire.TxOut{
			testSpendAssetOutput(gasAsset, 5000000, caller0Script),
			testSpendAssetOutput(gasAsset, 49900000, caller0Script),
			testSpendAssetOutput(gasAsset, 5000000, caller1Script),
			testSpendAssetOutput(gasAsset, 5000000, caller0Script),
			testSpendAssetOutput(gasAsset, 5000000, caller1Script),
			testSpendAssetOutput(gasAsset, 5000000, caller2Script),
			testSpendAssetOutput(gasAsset, 22000000, caller0Script),
		})
	sendAndMineTx(t, bootstrapNode, nodes, counterDeploy, 2)

	erc20DeployCode := solidityDeployCode(t, erc20,
		[]interface{}{"SatoshiNet Test Token", "SNT", uint8(6), new(big.Int).Mul(big.NewInt(1000), big.NewInt(1_000_000))})
	erc20Deploy, erc20Contract, _ := buildSolidityDeployTx(t, callerKeys[0], 2,
		erc20DeployCode,
		[]wire.OutPoint{counterChanges[0]},
		wire.TxOut{Assets: wire.TxAssets{networkGasFunding(t, gasAsset,
			5000000-networkGasFeeAmount(t, evm.DefaultGasConfig().DeployBaseGas))}},
		nil)
	sendAndMineTx(t, bootstrapNode, nodes, erc20Deploy, 3)

	_, bestHeight, err := bootstrapNode.Client.GetBestBlock()
	require.NoError(t, err)
	releaseHeight := uint64(bestHeight + 3)
	vaultDeployCode := solidityDeployCode(t, vault,
		[]interface{}{vaultAsset, recipient, "1.25", new(big.Int).SetUint64(releaseHeight)})
	vaultDeploy, vaultContract, _ := buildSolidityDeployTx(t, callerKeys[0], 3,
		vaultDeployCode,
		[]wire.OutPoint{counterChanges[1]},
		wire.TxOut{Assets: wire.TxAssets{
			networkGasFunding(t, gasAsset, 10000000),
		}},
		[]*wire.TxOut{testSpendAssetOutput(gasAsset, 39800000, caller0Script)})
	sendAndMineTx(t, bootstrapNode, nodes, vaultDeploy, 4)

	vaultDeposit := buildContractAssetDepositTx(t, callerKeys[0],
		wire.OutPoint{Hash: assetAnchor.TxHash(), Index: 0},
		vaultContract, testWireDecimalAsset(t, vaultAsset, "10.00", 2))
	sendAndMineTx(t, bootstrapNode, nodes, vaultDeposit, int32(releaseHeight)-1)
	requireAssetSummaryAmount(t, bootstrapNode, vaultContract.MustEncode(), vaultAsset, "10")
	requirePositiveAssetSummary(t, bootstrapNode, caller0Address, gasAsset)
	releaseTick := buildPassthroughAssetTx(t, callerKeys[0], counterChanges[6], gasAsset, 20000000, caller0Script)
	sendAndMineTx(t, bootstrapNode, nodes, releaseTick, int32(releaseHeight))
	vaultRelease := buildSolidityInvokeTx(t, callerKeys[0], vaultContract, 2,
		contractcommon.ContractInvokeAPICall, packNetworkSolidityMethod(t, vault.ABI, "release"),
		wire.OutPoint{Hash: releaseTick.TxHash(), Index: 0}, gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, vaultRelease, int32(releaseHeight)+1)
	requireAssetSummaryAmount(t, bootstrapNode, recipient, vaultAsset, "1.25")
	requireAssetSummaryAmount(t, bootstrapNode, vaultContract.MustEncode(), vaultAsset, "8.75")

	counterInvoke := buildSolidityInvokeTx(t, callerKeys[1], counterContract, 1,
		contractcommon.ContractInvokeAPICall, packNetworkSolidityMethod(t, counter.ABI, "inc"),
		counterChanges[2], gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, counterInvoke, int32(releaseHeight)+2)

	erc20Transfer := buildSolidityInvokeTx(t, callerKeys[0], erc20Contract, 1,
		contractcommon.ContractInvokeAPICall,
		packNetworkSolidityMethod(t, erc20.ABI, "transfer",
			gethcommon.HexToAddress(evmAddressFromAddressString(caller1Address).String()),
			big.NewInt(125_000_000)),
		counterChanges[3], gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, erc20Transfer, int32(releaseHeight)+3)

	erc20Approve := buildSolidityInvokeTx(t, callerKeys[1], erc20Contract, 2,
		contractcommon.ContractInvokeAPICall,
		packNetworkSolidityMethod(t, erc20.ABI, "approve",
			gethcommon.HexToAddress(evmAddressFromAddressString(caller2Address).String()),
			big.NewInt(20_000_000)),
		counterChanges[4], gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, erc20Approve, int32(releaseHeight)+4)

	erc20TransferFrom := buildSolidityInvokeTx(t, callerKeys[2], erc20Contract, 3,
		contractcommon.ContractInvokeAPICall,
		packNetworkSolidityMethod(t, erc20.ABI, "transferFrom",
			gethcommon.HexToAddress(evmAddressFromAddressString(caller1Address).String()),
			gethcommon.HexToAddress(evmAddressFromAddressString(caller2Address).String()),
			big.NewInt(12_500_000)),
		counterChanges[5], gasAsset, 5000000)
	sendAndMineTx(t, bootstrapNode, nodes, erc20TransferFrom, int32(releaseHeight)+5)

	requireAssetSummaryAmount(t, bootstrapNode, recipient, vaultAsset, "1.25")
	requireAssetSummaryAmount(t, bootstrapNode, vaultContract.MustEncode(), vaultAsset, "8.75")
	requirePositiveAssetSummary(t, bootstrapNode, caller0Address, gasAsset)
	waitForEVMContractQueries(t, bootstrapNode,
		[]string{counterContract.MustEncode(), erc20Contract.MustEncode(), vaultContract.MustEncode()},
		counterContract.MustEncode(),
	)

	bestHash, bestHeight, err := bootstrapNode.Client.GetBestBlock()
	require.NoError(t, err)
	coreBestHash, coreBestHeight, err := coreNode.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, bestHeight, coreBestHeight)
	require.Equal(t, bestHash, coreBestHash)
	requireContractStateAfterCoreRestart(t, coreNode, bootstrapNode, nodes, counterContract.MustEncode(), erc20Contract.MustEncode(), vaultContract.MustEncode())
}
