//go:build rpctest
// +build rpctest

package contract_e2e

import (
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/contract/evm"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNetworkEVMCloseProfit(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:evmprofit": 100,
	})

	const profitAsset = "ordx:f:evmprofit"
	gasAsset := evm.DefaultGasConfig().GasAssetName
	deployer := fixture.traderA
	deployerAddr := fixture.traderAActor.address
	bootstrapAddr := bootstrapP2TRAddress(t)

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset,
		[]int64{1000000, 1000000},
		[]int64{1000, 1000}, deployer)
	profitOuts := fixture.splitAsset(t, fixture.assetAnchors[profitAsset], profitAsset,
		[]int64{100},
		[]int64{1000}, deployer)

	deployTx, contract, _ := buildTemplateWitnessEVMDeployTx(t, fixture, deployer, 91,
		testEVMCloseInitCode(),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  0,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		},
		nil)
	fixture.sendAndWaitTx(t, deployTx)

	profitDeposit := buildContractAssetDepositTx(t, deployer, profitOuts[0], contract, testWireAsset(profitAsset, 100))
	fixture.sendAndWaitTx(t, profitDeposit)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, contract.MustEncode(), profitAsset, "100")

	closeTx := buildTemplateWitnessEVMCloseTx(t, fixture, deployer, contract, 1,
		[]wire.OutPoint{gasOuts[1]},
		wire.TxOut{
			Value:  0,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, closeTx)

	requireAssetSummaryZero(t, fixture.bootstrapNode, contract.MustEncode(), profitAsset)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, deployerAddr, profitAsset, "70")
	requireAssetSummaryAmount(t, fixture.bootstrapNode, bootstrapAddr, profitAsset, "30")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateAndEVMSameBlockPriorityAndCombinedStateRoot(t *testing.T) {
	t.Setenv("SATOSHINET_POS_MINER_INTERVAL", "5")
	t.Setenv("SATOSHINET_POS_PREWARNING_INTERVAL", "5")
	t.Setenv("SATOSHINET_POS_CHECKING_INTERVAL", "1")

	counter := compileNetworkSolidityContract(t, solidityNetworkCounterSource, "Counter")
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:mix": 1000,
	})

	const limitAsset = "ordx:f:mix"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderAAddr := fixture.traderAActor.address
	traderBAddr := fixture.traderBActor.address

	gasOuts := fixture.splitAssetTo(t, fixture.gasAnchor, gasAsset,
		[]int64{3000000, 3000000, 3000000, 5000000},
		[]int64{1000, 1000, 1000, 1000}, traderA,
		[]*templateNetworkActor{fixture.traderAActor, fixture.traderAActor, fixture.traderBActor, fixture.traderAActor})
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{10, 900},
		[]int64{10, 1000}, traderA)

	templateDeployTx, templateContract := buildTemplateDeployTxWithInputs(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"mixed-template-e2e",
		[]byte("mixed-template-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, templateDeployTx)

	evmDeployTx, evmContract, evmChanges := buildTemplateWitnessEVMDeployTx(t, fixture, traderA, 1,
		solidityDeployCode(t, counter, nil),
		[]wire.OutPoint{gasOuts[3]},
		wire.TxOut{Assets: wire.TxAssets{networkEVMGasFunding(t, gasAsset, 3000000)}},
		[]*wire.TxOut{testSpendAssetOutput(gasAsset, 1900000, fixture.spendScript)})
	fixture.sendAndWaitTx(t, evmDeployTx)
	require.NotEmpty(t, evmChanges)
	requireEVMResultStatusForTx(t, fixture.bootstrapNode, evmDeployTx, evmContract, evm.ResultStatusSuccess)
	waitForTemplateAssetUtxo(t, fixture.bootstrapNode, fixture.spendAddress, gasAsset, 1)

	sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "10", "10")
	templateSellTx := buildTemplateInvokeTxWithInputs(t, fixture, traderA, templateContract, 1, tmplcontract.InvokeAPISwap, sellParam,
		[]wire.OutPoint{assetOuts[0], gasOuts[1]},
		wire.TxOut{
			Value:  0,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
		})
	buyParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeBuy, "10", "10")
	templateBuyTx := buildTemplateInvokeTxWithInputs(t, fixture, traderB, templateContract, 2, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[2]},
		wire.TxOut{
			Value:  100,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	evmInvokeTx := buildTemplateWitnessEVMInvokeTx(t, fixture, traderA, evmContract, 2,
		contractcommon.ContractInvokeAPICall, networkSoliditySelector("inc()"),
		[]wire.OutPoint{evmChanges[0]},
		wire.TxOut{Assets: wire.TxAssets{networkEVMGasFunding(t, gasAsset, 100000)}})

	sendTx(t, fixture.bootstrapNode, templateSellTx)
	sendTx(t, fixture.bootstrapNode, templateBuyTx)
	sendTx(t, fixture.bootstrapNode, evmInvokeTx)
	blockHash := fixture.waitForTxsInSameBlock(t, templateSellTx, templateBuyTx, evmInvokeTx)

	block, err := fixture.bootstrapNode.Client.GetBlock(blockHash)
	require.NoError(t, err)
	requireTemplateResultBeforeEVMResult(t, block)
	templateBuyOutputs := templateResultOutputsForTx(t, fixture.bootstrapNode, templateBuyTx, templateContract)
	requireTemplateResultAssetAmount(t, templateBuyOutputs, traderBAddr, limitAsset, "10")
	requireTemplateResultValue(t, templateBuyOutputs, traderAAddr, 100)
	root, found, err := evm.FindCoinbaseStateRoot(block.Transactions[0])
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, [32]byte{}, root.StateRoot)
	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "10")
	fixture.requireNodesSynced(t)
}
