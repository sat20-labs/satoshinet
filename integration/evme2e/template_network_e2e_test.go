//go:build rpctest
// +build rpctest

package evme2e

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sort"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	"github.com/sat20-labs/satoshinet/contract/evm"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/integration/rpctest"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestNetworkTemplateLimitOrderContract(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:lot": 1000,
	})

	const limitAsset = "ordx:f:lot"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{10, 900},
		[]int64{10, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"limit-order-e2e",
		[]byte("limit-order-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "10", "10")
	sellTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 1, tmplcontract.InvokeAPISwap, sellParam,
		[]wire.OutPoint{assetOuts[0], gasOuts[1]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
		})
	fixture.sendAndWaitTx(t, sellTx)
	buyParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeBuy, "10", "10")
	buyTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 1, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[2]},
		wire.TxOut{
			Value:  110,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	sendTx(t, fixture.bootstrapNode, buyTx)
	fixture.waitForTx(t, buyTx)

	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "10")
	requirePositiveAssetSummary(t, fixture.bootstrapNode, contract.MustEncode(), gasAsset)
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateDefaultInvokeLimitOrderBuy(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:lotdef": 1000,
	})

	const limitAsset = "ordx:f:lotdef"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{10, 900},
		[]int64{10, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"limit-order-default-e2e",
		[]byte("limit-order-default-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "10", "10")
	sellTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 1, tmplcontract.InvokeAPISwap, sellParam,
		[]wire.OutPoint{assetOuts[0], gasOuts[1]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
		})
	fixture.sendAndWaitTx(t, sellTx)

	defaultBuyTx := buildTemplateDefaultInvokeTx(t, fixture, traderB, contract,
		[]wire.OutPoint{gasOuts[2]},
		wire.TxOut{
			Value:  100,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	require.False(t, txHasContractOpReturn(defaultBuyTx))
	fixture.sendAndWaitTx(t, defaultBuyTx)

	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "10")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateExchangeDefaultFundBuyAndClose(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:excha": 100,
		"ordx:f:exchb": 24,
	})

	const (
		assetA = "ordx:f:excha"
		assetB = "ordx:f:exchb"
	)
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	deployerAddr := fixture.spendAddress
	buyerAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset,
		[]int64{1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000}, traderA)
	assetAOuts := fixture.splitAsset(t, fixture.assetAnchors[assetA], assetA, []int64{100},
		[]int64{1000}, traderA)
	assetBOuts := fixture.splitAsset(t, fixture.assetAnchors[assetB], assetB, []int64{24},
		[]int64{1000}, traderB)

	exchange := tmplcontract.NewExchangeContract(assetA, assetB, tmplcontract.ExchangePriceModeHeight, []tmplcontract.ExchangePriceStep{{
		Threshold: "0",
		BPerA:     "2",
	}})
	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		exchange,
		deployerAddr,
		[]byte("exchange-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	fundTx := buildTemplateDefaultInvokeTx(t, fixture, traderA, contract,
		[]wire.OutPoint{assetAOuts[0], gasOuts[1]},
		wire.TxOut{
			Value: 0,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, assetA, 100),
			),
		})
	require.False(t, txHasContractOpReturn(fundTx))
	fixture.sendAndWaitTx(t, fundTx)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, contract.MustEncode(), assetA, "100")

	buyTx := buildTemplateDefaultInvokeTx(t, fixture, traderB, contract,
		[]wire.OutPoint{assetBOuts[0], gasOuts[2]},
		wire.TxOut{
			Value: 0,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, assetB, 24),
			),
		})
	require.False(t, txHasContractOpReturn(buyTx))
	fixture.sendAndWaitTx(t, buyTx)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, buyerAddr, assetA, "12")
	requireAssetSummaryAmount(t, fixture.bootstrapNode, contract.MustEncode(), assetA, "88")
	requireAssetSummaryAmount(t, fixture.bootstrapNode, deployerAddr, assetB, "24")

	closeTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 1, tmplcontract.InvokeAPIClose, nil,
		[]wire.OutPoint{gasOuts[3]},
		wire.TxOut{
			Value:  0,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, closeTx)
	requireAssetSummaryZero(t, fixture.bootstrapNode, contract.MustEncode(), assetA)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, deployerAddr, assetA, "100")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateLimitOrderLargeBuyFilledBySmallSells(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:lotbuy": 1000,
	})

	const limitAsset = "ordx:f:lotbuy"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{10, 10, 10, 900},
		[]int64{10, 10, 10, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"limit-order-big-buy-e2e",
		[]byte("limit-order-big-buy-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	buyParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeBuy, "40", "10")
	buyTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 1, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[1]},
		wire.TxOut{
			Value:  413,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, buyTx)

	for i := 0; i < 3; i++ {
		sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "10", "10")
		sellTx := buildTemplateInvokeTx(t, fixture, traderA, contract, uint64(i+1), tmplcontract.InvokeAPISwap, sellParam,
			[]wire.OutPoint{assetOuts[i], gasOuts[i+2]},
			wire.TxOut{
				Value:  tmplcontract.SwapInvokeFee,
				Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
			})
		fixture.sendAndWaitTx(t, sellTx)
	}

	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "30")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateLimitOrderLargeSellFilledBySmallBuys(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:lotsell": 1000,
	})

	const limitAsset = "ordx:f:lotsell"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{40, 900},
		[]int64{40, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"limit-order-big-sell-e2e",
		[]byte("limit-order-big-sell-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "40", "10")
	sellTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 1, tmplcontract.InvokeAPISwap, sellParam,
		[]wire.OutPoint{assetOuts[0], gasOuts[1]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 40)},
		})
	fixture.sendAndWaitTx(t, sellTx)

	buyValues := []int64{130, 120, 110}
	buyPrices := []string{"12", "11", "10"}
	for i := 0; i < 3; i++ {
		buyParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeBuy, "10", buyPrices[i])
		buyTx := buildTemplateInvokeTx(t, fixture, traderB, contract, uint64(i+1), tmplcontract.InvokeAPISwap, buyParam,
			[]wire.OutPoint{gasOuts[i+2]},
			wire.TxOut{
				Value:  buyValues[i],
				Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
			})
		fixture.sendAndWaitTx(t, buyTx)
	}

	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "30")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateLimitOrderBuyTakesLowerPricedSells(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:lotprice": 1000,
	})

	const limitAsset = "ordx:f:lotprice"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{10, 10, 10, 900},
		[]int64{10, 10, 10, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"limit-order-price-e2e",
		[]byte("limit-order-price-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	sellPrices := []string{"10", "9", "8"}
	for i := range sellPrices {
		sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "10", sellPrices[i])
		sellTx := buildTemplateInvokeTx(t, fixture, traderA, contract, uint64(i+1), tmplcontract.InvokeAPISwap, sellParam,
			[]wire.OutPoint{assetOuts[i], gasOuts[i+1]},
			wire.TxOut{
				Value:  tmplcontract.SwapInvokeFee,
				Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
			})
		fixture.sendAndWaitTx(t, sellTx)
	}

	buyParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeBuy, "40", "10")
	buyTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 1, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[4]},
		wire.TxOut{
			Value:  413,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, buyTx)

	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "30")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateLimitOrderRefundOpenOrders(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:lotrefund": 1000,
	})

	const limitAsset = "ordx:f:lotrefund"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderAAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{10, 10, 900},
		[]int64{10, 10, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"limit-order-refund-e2e",
		[]byte("limit-order-refund-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	for i := 0; i < 2; i++ {
		sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "10", "10")
		sellTx := buildTemplateInvokeTx(t, fixture, traderA, contract, uint64(i+1), tmplcontract.InvokeAPISwap, sellParam,
			[]wire.OutPoint{assetOuts[i], gasOuts[i+1]},
			wire.TxOut{
				Value:  tmplcontract.SwapInvokeFee,
				Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
			})
		fixture.sendAndWaitTx(t, sellTx)
	}

	refundTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 3, tmplcontract.InvokeAPIRefund, nil,
		nil,
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, refundTx)

	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderAAddr, limitAsset, "20")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateLimitOrderRefundCanTargetOneOrder(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:lottarget": 1000,
	})

	const limitAsset = "ordx:f:lottarget"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderAAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{10, 10, 900},
		[]int64{10, 10, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"limit-order-target-refund-e2e",
		[]byte("limit-order-target-refund-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	for i := 0; i < 2; i++ {
		sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "10", "10")
		sellTx := buildTemplateInvokeTx(t, fixture, traderA, contract, uint64(i+1), tmplcontract.InvokeAPISwap, sellParam,
			[]wire.OutPoint{assetOuts[i], gasOuts[i+1]},
			wire.TxOut{
				Value:  tmplcontract.SwapInvokeFee,
				Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
			})
		fixture.sendAndWaitTx(t, sellTx)
	}

	refundParam := templateRefundParam(t, []int64{0})
	refundTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 3, tmplcontract.InvokeAPIRefund, refundParam,
		nil,
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, refundTx)

	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderAAddr, limitAsset, "10")
	requireAssetSummaryAmount(t, fixture.bootstrapNode, contract.MustEncode(), limitAsset, "10")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateLimitOrderRefundPartiallyFilledSell(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:lotpartial": 1000,
	})

	const limitAsset = "ordx:f:lotpartial"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderAAddr := fixture.spendAddress
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{40, 900},
		[]int64{40, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"limit-order-partial-refund-e2e",
		[]byte("limit-order-partial-refund-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "40", "10")
	sellTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 1, tmplcontract.InvokeAPISwap, sellParam,
		[]wire.OutPoint{assetOuts[0], gasOuts[1]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 40)},
		})
	fixture.sendAndWaitTx(t, sellTx)

	for i := 0; i < 3; i++ {
		buyParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeBuy, "10", "10")
		buyTx := buildTemplateInvokeTx(t, fixture, traderB, contract, uint64(i+1), tmplcontract.InvokeAPISwap, buyParam,
			[]wire.OutPoint{gasOuts[i+2]},
			wire.TxOut{
				Value:  110,
				Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
			})
		fixture.sendAndWaitTx(t, buyTx)
	}

	refundParam := templateRefundParam(t, []int64{0})
	refundTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 5, tmplcontract.InvokeAPIRefund, refundParam,
		nil,
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, refundTx)

	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "30")
	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderAAddr, limitAsset, "10")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateAMMContract(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:amm": 100000,
	})

	const ammAsset = "ordx:f:amm"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000},
		[]int64{12000, 1010, 20}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[ammAsset], ammAsset, []int64{99000, 100, 900},
		[]int64{10000, 1000, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewAMMContract(ammAsset, "99000", 10000, "990000000"),
		"amm-e2e",
		[]byte("amm-random"),
		[]wire.OutPoint{assetOuts[0], gasOuts[0]},
		wire.TxOut{
			Value: 10000,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, ammAsset, 99000),
			),
		})
	fixture.sendAndWaitTx(t, deployTx)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, contract.MustEncode(), ammAsset, "99000")

	buyParam := templateLimitOrderParam(t, ammAsset, tmplcontract.OrderTypeBuy, "0", "1000")
	buyTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 1, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[1]},
		wire.TxOut{
			Value:  1010,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, buyTx)
	requirePositiveAssetSummary(t, fixture.bootstrapNode, traderBAddr, ammAsset)
	requirePositiveAssetSummary(t, fixture.bootstrapNode, contract.MustEncode(), gasAsset)

	sellParam := templateLimitOrderParam(t, ammAsset, tmplcontract.OrderTypeSell, "100", "1")
	sellTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 2, tmplcontract.InvokeAPISwap, sellParam,
		[]wire.OutPoint{assetOuts[1], gasOuts[2]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: networkTxAssets(networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, ammAsset, 100)),
		})
	fixture.sendAndWaitTx(t, sellTx)
	requirePositiveAssetSummary(t, fixture.bootstrapNode, traderBAddr, ammAsset)
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateAMMWaitsUntilAddLiquidityMeetsK(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:ammready": 1000,
	})

	const ammAsset = "ordx:f:ammready"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[ammAsset], ammAsset, []int64{90, 10, 900},
		[]int64{100, 100, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewAMMContract(ammAsset, "100", 20, "2000"),
		"amm-ready-e2e",
		[]byte("amm-ready-random"),
		[]wire.OutPoint{assetOuts[0], gasOuts[0]},
		wire.TxOut{
			Value: 20,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, ammAsset, 90),
			),
		})
	fixture.sendAndWaitTx(t, deployTx)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, traderBAddr, ammAsset, "910")

	beforeBuySummary := fetchAssetSummaryEventually(t, fixture.bootstrapNode, traderBAddr)
	buyParam := templateLimitOrderParam(t, ammAsset, tmplcontract.OrderTypeBuy, "1", "10")
	buyTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 1, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[1]},
		wire.TxOut{
			Value:  20,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, buyTx)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, traderBAddr, ammAsset, beforeBuySummary[ammAsset])

	addParam := templateAddLiquidityParam(t, ammAsset, "10", 1)
	addTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 2, tmplcontract.InvokeAPIAddLiquidity, addParam,
		[]wire.OutPoint{assetOuts[1], gasOuts[2]},
		wire.TxOut{
			Value: 1,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, ammAsset, 10),
			),
		})
	fixture.sendAndWaitTx(t, addTx)

	triggerParam := templateRefundParam(t, []int64{})
	triggerTx := buildTemplateInvokeTx(t, fixture, traderA, contract, 3, tmplcontract.InvokeAPIRefund, triggerParam,
		[]wire.OutPoint{gasOuts[3]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, triggerTx)

	requirePositiveAssetSummary(t, fixture.bootstrapNode, traderBAddr, ammAsset)
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateAMMRejectsBuySlippage(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:ammslip": 1000,
	})

	const ammAsset = "ordx:f:ammslip"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000},
		[]int64{1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[ammAsset], ammAsset, []int64{100, 900},
		[]int64{100, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewAMMContract(ammAsset, "100", 20, "2000"),
		"amm-slip-e2e",
		[]byte("amm-slip-random"),
		[]wire.OutPoint{assetOuts[0], gasOuts[0]},
		wire.TxOut{
			Value: 20,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, ammAsset, 100),
			),
		})
	fixture.sendAndWaitTx(t, deployTx)

	requireAssetSummaryAmount(t, fixture.bootstrapNode, traderBAddr, ammAsset, "900")
	beforeBuySummary := fetchAssetSummaryEventually(t, fixture.bootstrapNode, traderBAddr)
	buyParam := templateLimitOrderParam(t, ammAsset, tmplcontract.OrderTypeBuy, "90", "10")
	buyTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 1, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[1]},
		wire.TxOut{
			Value:  20,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, buyTx)

	requireAssetSummaryAmount(t, fixture.bootstrapNode, traderBAddr, ammAsset, beforeBuySummary[ammAsset])
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateAMMSellAddsAssetToPool(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:ammsell": 1000,
	})

	const ammAsset = "ordx:f:ammsell"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000},
		[]int64{1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[ammAsset], ammAsset, []int64{100, 100, 800},
		[]int64{100, 100, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewAMMContract(ammAsset, "100", 20, "2000"),
		"amm-sell-e2e",
		[]byte("amm-sell-random"),
		[]wire.OutPoint{assetOuts[0], gasOuts[0]},
		wire.TxOut{
			Value: 20,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, ammAsset, 100),
			),
		})
	fixture.sendAndWaitTx(t, deployTx)

	sellParam := templateLimitOrderParam(t, ammAsset, tmplcontract.OrderTypeSell, "1", "1")
	sellTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 1, tmplcontract.InvokeAPISwap, sellParam,
		[]wire.OutPoint{assetOuts[1], gasOuts[1]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: networkTxAssets(networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, ammAsset, 100)),
		})
	fixture.sendAndWaitTx(t, sellTx)

	requireAssetSummaryAmount(t, fixture.bootstrapNode, contract.MustEncode(), ammAsset, "200")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateAMMAddRemoveLiquidity(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:ammliq": 100000,
	})

	const ammAsset = "ordx:f:ammliq"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000},
		[]int64{200, 200, 20}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[ammAsset], ammAsset, []int64{100, 100, 900},
		[]int64{100, 100, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTx(t, fixture, traderA,
		tmplcontract.NewAMMContract(ammAsset, "100", 20, "2000"),
		"amm-liq-e2e",
		[]byte("amm-liq-random"),
		[]wire.OutPoint{assetOuts[0], gasOuts[0]},
		wire.TxOut{
			Value: 20,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, ammAsset, 100),
			),
		})
	fixture.sendAndWaitTx(t, deployTx)

	addParam := templateAddLiquidityParam(t, ammAsset, "100", 20)
	addTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 1, tmplcontract.InvokeAPIAddLiquidity, addParam,
		[]wire.OutPoint{assetOuts[1], gasOuts[1]},
		wire.TxOut{
			Value: 20,
			Assets: networkTxAssets(
				networkTemplateFunding(t, gasAsset, 100000),
				networkTemplateFunding(t, ammAsset, 100),
			),
		})
	fixture.sendAndWaitTx(t, addTx)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, contract.MustEncode(), ammAsset, "200")

	removeParam := templateRemoveLiquidityParam(t, ammAsset, "1")
	removeTx := buildTemplateInvokeTx(t, fixture, traderB, contract, 2, tmplcontract.InvokeAPIRemoveLiquidity, removeParam,
		[]wire.OutPoint{gasOuts[2]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, removeTx)
	requirePositiveAssetSummary(t, fixture.bootstrapNode, traderBAddr, ammAsset)
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
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset,
		[]int64{3000000, 3000000, 3000000, 5000000},
		[]int64{1000, 1000, 1000, 1000}, traderA)
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
	waitForTemplateAssetUtxo(t, fixture.bootstrapNode, templateContract.MustEncode(), gasAsset, 1)

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
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
		})
	buyParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeBuy, "10", "10")
	templateBuyTx := buildTemplateInvokeTxWithInputs(t, fixture, traderB, templateContract, 2, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[2]},
		wire.TxOut{
			Value:  110,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	evmInvokeTx := buildTemplateWitnessEVMInvokeTx(t, fixture, traderA, evmContract, 2,
		packNetworkSolidityMethod(t, counter.ABI, "incrementBy", big.NewInt(0)),
		[]wire.OutPoint{evmChanges[0]},
		wire.TxOut{Assets: wire.TxAssets{networkEVMGasFunding(t, gasAsset, 100000)}})

	sendTx(t, fixture.bootstrapNode, templateSellTx)
	sendTx(t, fixture.bootstrapNode, templateBuyTx)
	sendTx(t, fixture.bootstrapNode, evmInvokeTx)
	blockHash := fixture.waitForTxsInSameBlock(t, templateSellTx, templateBuyTx, evmInvokeTx)

	block, err := fixture.bootstrapNode.Client.GetBlock(blockHash)
	require.NoError(t, err)
	requireTemplateResultBeforeEVMResult(t, block)
	root, found, err := evm.FindCoinbaseStateRoot(block.Transactions[0])
	require.NoError(t, err)
	require.True(t, found)
	require.NotEqual(t, [32]byte{}, root.StateRoot)
	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "10")
	fixture.requireNodesSynced(t)
}

func TestNetworkTemplateContractStateRollbackOnInvalidate(t *testing.T) {
	fixture := newTemplateNetworkFixture(t, map[string]int64{
		"ordx:f:rollback": 1000,
	})

	const limitAsset = "ordx:f:rollback"
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	traderA := fixture.traderA
	traderB := fixture.traderB
	traderBAddr := fixture.spendAddress

	gasOuts := fixture.splitAsset(t, fixture.gasAnchor, gasAsset, []int64{1000000, 1000000, 1000000, 1000000},
		[]int64{1000, 1000, 1000, 1000}, traderA)
	assetOuts := fixture.splitAsset(t, fixture.assetAnchors[limitAsset], limitAsset, []int64{10, 900},
		[]int64{10, 1000}, traderA)

	deployTx, contract := buildTemplateDeployTxWithInputs(t, fixture, traderA,
		tmplcontract.NewLimitOrderContract(limitAsset),
		"rollback-template-e2e",
		[]byte("rollback-template-random"),
		[]wire.OutPoint{gasOuts[0]},
		wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, deployTx)

	sellParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeSell, "10", "10")
	sellTx := buildTemplateInvokeTxWithInputs(t, fixture, traderA, contract, 1, tmplcontract.InvokeAPISwap, sellParam,
		[]wire.OutPoint{assetOuts[0], gasOuts[1]},
		wire.TxOut{
			Value:  tmplcontract.SwapInvokeFee,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000), networkTemplateFunding(t, limitAsset, 10)},
		})
	fixture.sendAndWaitTx(t, sellTx)
	requireAssetSummaryAmount(t, fixture.bootstrapNode, contract.MustEncode(), limitAsset, "10")

	buyParam := templateLimitOrderParam(t, limitAsset, tmplcontract.OrderTypeBuy, "10", "10")
	buyTx := buildTemplateInvokeTxWithInputs(t, fixture, traderB, contract, 2, tmplcontract.InvokeAPISwap, buyParam,
		[]wire.OutPoint{gasOuts[2]},
		wire.TxOut{
			Value:  110,
			Assets: wire.TxAssets{networkTemplateFunding(t, gasAsset, 100000)},
		})
	fixture.sendAndWaitTx(t, buyTx)
	requireAssetSummaryAtLeast(t, fixture.bootstrapNode, traderBAddr, limitAsset, "10")

	buyHash := buyTx.TxHash()
	buyVerbose, err := fixture.bootstrapNode.Client.GetRawTransactionVerbose(&buyHash)
	require.NoError(t, err)
	require.NotEmpty(t, buyVerbose.BlockHash)
	buyBlockHash, err := chainhash.NewHashFromStr(buyVerbose.BlockHash)
	require.NoError(t, err)

	require.NoError(t, fixture.bootstrapNode.Client.InvalidateBlock(buyBlockHash))
	buyVerboseAfterInvalidate, err := fixture.bootstrapNode.Client.GetRawTransactionVerbose(&buyHash)
	if err != nil {
		require.ErrorContains(t, err, "No information available about transaction")
		return
	}
	require.Equal(t, uint64(0), buyVerboseAfterInvalidate.Confirmations)
}

type templateNetworkFixture struct {
	bootstrapNode *rpctest.Harness
	coreNode      *rpctest.Harness
	nodes         []*rpctest.Harness
	traderA       *btcec.PrivateKey
	traderB       *btcec.PrivateKey
	spendScript   []byte
	spendAddress  string
	redeemScript  []byte
	controlBlock  []byte
	gasAnchor     *wire.MsgTx
	assetAnchors  map[string]*wire.MsgTx
}

func newTemplateNetworkFixture(t *testing.T, assets map[string]int64) *templateNetworkFixture {
	t.Helper()
	configureFastPOSTimers(t)

	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	const lockedValue = int64(200000)
	gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	traderA := keyFromMnemonic(t, bootstrapMnemonic, 1)
	traderB := keyFromMnemonic(t, bootstrapMnemonic, 2)
	spendScript, spendAddress, redeemScript, controlBlock := testCallerTaprootScript(t, bootstrapKey)

	witnessScript, lockedPkScript, err := anchortx.GetP2WSHscript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)

	utxos := map[string]*indexercommon.AssetsInUtxo{}
	gasLockedUtxo := templateLockedOutPoint("gas", 0)
	utxos[gasLockedUtxo] = &indexercommon.AssetsInUtxo{
		OutPoint: gasLockedUtxo,
		Value:    lockedValue,
		PkScript: lockedPkScript,
		Assets: []*indexercommon.DisplayAsset{
			testDisplayAsset(gasAsset, "100000000"),
		},
	}
	for i, asset := range sortedTemplateAssets(assets) {
		lockedUtxo := templateLockedOutPoint(asset, i+1)
		utxos[lockedUtxo] = &indexercommon.AssetsInUtxo{
			OutPoint: lockedUtxo,
			Value:    lockedValue,
			PkScript: lockedPkScript,
			Assets: []*indexercommon.DisplayAsset{
				testDisplayAsset(asset, fmt.Sprintf("%d", assets[asset])),
			},
		}
	}

	fakeL1 := startFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()), utxos)
	bootstrapNode, coreNode := startSatoshiNetNetwork(t, fakeL1)
	nodes := []*rpctest.Harness{bootstrapNode, coreNode}

	gasAnchor := buildNetworkAnchorTx(t, gasLockedUtxo, lockedValue,
		testWireAsset(gasAsset, 100000000), gasAsset+"-100000000-0-1",
		witnessScript, bootstrapKey, spendScript)
	sendTx(t, bootstrapNode, gasAnchor)

	assetAnchors := make(map[string]*wire.MsgTx)
	for i, asset := range sortedTemplateAssets(assets) {
		lockedUtxo := templateLockedOutPoint(asset, i+1)
		amount := assets[asset]
		anchor := buildNetworkAnchorTx(t, lockedUtxo, lockedValue,
			testWireAsset(asset, amount), fmt.Sprintf("%s-%d-0-1", asset, amount),
			witnessScript, bootstrapKey, spendScript)
		sendTx(t, bootstrapNode, anchor)
		assetAnchors[asset] = anchor
	}
	waitForPOSTx(t, bootstrapNode, nodes, gasAnchor)

	return &templateNetworkFixture{
		bootstrapNode: bootstrapNode,
		coreNode:      coreNode,
		nodes:         nodes,
		traderA:       traderA,
		traderB:       traderB,
		spendScript:   spendScript,
		spendAddress:  spendAddress,
		redeemScript:  redeemScript,
		controlBlock:  controlBlock,
		gasAnchor:     gasAnchor,
		assetAnchors:  assetAnchors,
	}
}

func configureFastPOSTimers(t *testing.T) {
	t.Helper()
	if os.Getenv("SATOSHINET_POS_MINER_INTERVAL") == "" {
		t.Setenv("SATOSHINET_POS_MINER_INTERVAL", "1")
	}
	if os.Getenv("SATOSHINET_POS_PREWARNING_INTERVAL") == "" {
		t.Setenv("SATOSHINET_POS_PREWARNING_INTERVAL", "1")
	}
	if os.Getenv("SATOSHINET_POS_CHECKING_INTERVAL") == "" {
		t.Setenv("SATOSHINET_POS_CHECKING_INTERVAL", "1")
	}
}

func (f *templateNetworkFixture) splitAsset(t *testing.T, anchorTx *wire.MsgTx, asset string,
	amounts []int64, values []int64, signer *btcec.PrivateKey) []wire.OutPoint {

	t.Helper()
	require.Len(t, values, len(amounts))
	outputs := make([]*wire.TxOut, 0, len(amounts))
	for i := range amounts {
		outputs = append(outputs, wire.NewTxOut(values[i], testWireAsset(asset, amounts[i]), f.spendScript))
	}
	tx := buildTemplateSplitTx(t, signer, wire.OutPoint{Hash: anchorTx.TxHash(), Index: 0}, outputs)
	signTemplateTaprootInputs(t, tx, signer, f.redeemScript, f.controlBlock)
	f.sendAndWaitTx(t, tx)
	return collectSpendableOutPoints(t, tx, outputs)
}

func (f *templateNetworkFixture) sendAndWaitTx(t *testing.T, tx *wire.MsgTx) {
	t.Helper()
	sendTx(t, f.bootstrapNode, tx)
	f.waitForTx(t, tx)
}

func (f *templateNetworkFixture) waitForTx(t *testing.T, tx *wire.MsgTx) {
	t.Helper()
	waitForPOSTx(t, f.bootstrapNode, f.nodes, tx)
}

func (f *templateNetworkFixture) waitForTxsInSameBlock(t *testing.T, txs ...*wire.MsgTx) *chainhash.Hash {
	t.Helper()
	require.NotEmpty(t, txs)
	deadline := time.Now().Add(90 * time.Second)
	var lastBlock string
	lastStatus := make([]string, len(txs))
	for time.Now().Before(deadline) {
		confirmed := true
		blockHash := ""
		for i, tx := range txs {
			hash := tx.TxHash()
			verbose, err := f.bootstrapNode.Client.GetRawTransactionVerbose(&hash)
			if err != nil || verbose.Confirmations < 1 || verbose.BlockHash == "" {
				if err != nil {
					lastStatus[i] = fmt.Sprintf("%s err=%v", hash, err)
				} else {
					lastStatus[i] = fmt.Sprintf("%s confirmations=%d block=%s", hash, verbose.Confirmations, verbose.BlockHash)
				}
				confirmed = false
				continue
			}
			lastStatus[i] = fmt.Sprintf("%s confirmations=%d block=%s", hash, verbose.Confirmations, verbose.BlockHash)
			if blockHash == "" {
				blockHash = verbose.BlockHash
			} else if blockHash != verbose.BlockHash {
				t.Fatalf("transactions mined in different blocks: first=%s current=%s tx=%s", blockHash, verbose.BlockHash, hash)
			}
		}
		if confirmed && blockHash != "" {
			require.NoError(t, rpctest.JoinNodes(f.nodes, rpctest.Blocks))
			hash, err := chainhash.NewHashFromStr(blockHash)
			require.NoError(t, err)
			return hash
		}
		lastBlock = blockHash
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("transactions were not mined in the same block, last block=%s status=%v", lastBlock, lastStatus)
	return nil
}

func (f *templateNetworkFixture) requireNodesSynced(t *testing.T) {
	t.Helper()
	bestHash, bestHeight, err := f.bootstrapNode.Client.GetBestBlock()
	require.NoError(t, err)
	coreBestHash, coreBestHeight, err := f.coreNode.Client.GetBestBlock()
	require.NoError(t, err)
	require.Equal(t, bestHeight, coreBestHeight)
	require.Equal(t, bestHash, coreBestHash)
}

func requireAssetSummaryZero(t *testing.T, node *rpctest.Harness, address, assetName string) {
	t.Helper()
	var (
		lastSummary map[string]string
		lastErr     error
	)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		summary, err := fetchAssetSummary(node, address)
		lastSummary, lastErr = summary, err
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		value := summary[assetName]
		if value == "" || value == "0" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	require.True(t, lastSummary[assetName] == "" || lastSummary[assetName] == "0",
		"address=%s asset=%s summary=%v", address, assetName, lastSummary)
}

func requireAssetSummaryAtLeast(t *testing.T, node *rpctest.Harness, address, assetName, amount string) {
	t.Helper()
	want, err := indexercommon.NewDecimalFromString(amount, 0)
	require.NoError(t, err)
	var (
		lastSummary map[string]string
		lastErr     error
	)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		summary, err := fetchAssetSummary(node, address)
		lastSummary, lastErr = summary, err
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		gotText := summary[assetName]
		if gotText != "" {
			got, err := indexercommon.NewDecimalFromString(gotText, 0)
			if err == nil && got.Cmp(want) >= 0 {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	require.Failf(t, "asset summary too small", "address=%s asset=%s want_at_least=%s summary=%v",
		address, assetName, amount, lastSummary)
}

func fetchAssetSummaryEventually(t *testing.T, node *rpctest.Harness, address string) map[string]string {
	t.Helper()
	var (
		lastSummary map[string]string
		lastErr     error
	)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		summary, err := fetchAssetSummary(node, address)
		lastSummary, lastErr = summary, err
		if err == nil {
			return summary
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	return lastSummary
}

func (f *templateNetworkFixture) selectFundingOutPoints(t *testing.T, funding wire.TxOut) []wire.OutPoint {
	t.Helper()
	selected := make([]wire.OutPoint, 0, len(funding.Assets))
	seen := make(map[string]bool)
	totalValue := int64(0)
	for _, want := range funding.Assets {
		wantAssetName := want.Name.String()
		utxos := fetchTemplateAssetUtxos(t, f.bootstrapNode, f.spendAddress, wantAssetName)
		sort.SliceStable(utxos, func(i, j int) bool {
			if utxos[i].Value != utxos[j].Value {
				return utxos[i].Value < utxos[j].Value
			}
			return utxos[i].OutPoint < utxos[j].OutPoint
		})
		var picked *indexercommon.AssetsInUtxo
		for _, utxo := range utxos {
			if utxo == nil || seen[utxo.OutPoint] || !templateUtxoHasAsset(utxo, want) {
				continue
			}
			picked = utxo
			break
		}
		require.NotNil(t, picked, "missing funding utxo address=%s asset=%s amount=%s", f.spendAddress, wantAssetName, want.Amount.String())
		outpoint, err := tmplcontract.ParseOutPoint(picked.OutPoint)
		require.NoError(t, err)
		selected = append(selected, outpoint)
		seen[picked.OutPoint] = true
		totalValue += picked.Value
	}
	if totalValue < funding.Value {
		gasAsset := tmplcontract.DefaultGasConfig().GasAssetName
		utxos := fetchTemplateAssetUtxos(t, f.bootstrapNode, f.spendAddress, gasAsset)
		sort.SliceStable(utxos, func(i, j int) bool {
			if utxos[i].Value != utxos[j].Value {
				return utxos[i].Value < utxos[j].Value
			}
			return utxos[i].OutPoint < utxos[j].OutPoint
		})
		for _, utxo := range utxos {
			if utxo == nil || seen[utxo.OutPoint] {
				continue
			}
			outpoint, err := tmplcontract.ParseOutPoint(utxo.OutPoint)
			require.NoError(t, err)
			selected = append(selected, outpoint)
			seen[utxo.OutPoint] = true
			totalValue += utxo.Value
			if totalValue >= funding.Value {
				break
			}
		}
	}
	require.GreaterOrEqual(t, totalValue, funding.Value, "missing funding value address=%s", f.spendAddress)
	require.NotEmpty(t, selected)
	return selected
}

func fetchTemplateAssetUtxos(t *testing.T, node *rpctest.Harness, address, assetName string) []*indexercommon.AssetsInUtxo {
	t.Helper()
	baseURL, err := node.IndexerURL("testnet")
	require.NoError(t, err)
	endpoint := fmt.Sprintf("%s/v3/address/asset/%s/%s", baseURL, url.PathEscape(address), url.PathEscape(assetName))
	var lastErr error
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(endpoint)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var out indexerwire.UtxosWithAssetRespV3
		err = json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if out.Code != 0 {
			lastErr = fmt.Errorf("indexer response code %d: %s", out.Code, out.Msg)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		return out.Data
	}
	require.NoError(t, lastErr)
	return nil
}

func waitForTemplateAssetUtxo(t *testing.T, node *rpctest.Harness, address, assetName string, amount int64) {
	t.Helper()
	want := networkTemplateFunding(t, assetName, amount)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, utxo := range fetchTemplateAssetUtxos(t, node, address, assetName) {
			if templateUtxoHasAsset(utxo, want) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.Failf(t, "missing asset utxo", "address=%s asset=%s amount=%d", address, assetName, amount)
}

func templateUtxoHasAsset(utxo *indexercommon.AssetsInUtxo, want wire.AssetInfo) bool {
	for _, asset := range utxo.Assets {
		if asset == nil || asset.AssetName.String() != want.Name.String() || asset.Invalid {
			continue
		}
		amount, err := indexercommon.NewDecimalFromString(asset.Amount, asset.Precision)
		if err != nil {
			continue
		}
		return amount.Cmp(&want.Amount) >= 0
	}
	return false
}

func waitForPOSTx(t *testing.T, node *rpctest.Harness, nodes []*rpctest.Harness, tx *wire.MsgTx) {
	t.Helper()
	txHash := tx.TxHash()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		verbose, err := node.Client.GetRawTransactionVerbose(&txHash)
		if err == nil && verbose.Confirmations >= 1 {
			require.NoError(t, rpctest.JoinNodes(nodes, rpctest.Blocks))
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	for i, n := range nodes {
		if n == nil || n.Client == nil {
			continue
		}
		_, height, heightErr := n.Client.GetBestBlock()
		mempool, mempoolErr := n.Client.GetRawMempool()
		verbose, verboseErr := n.Client.GetRawTransactionVerbose(&txHash)
		confirmations := uint64(0)
		if verboseErr == nil {
			confirmations = verbose.Confirmations
		}
		t.Logf("waitForPOSTx timeout node=%d rpc=%s height=%d height_err=%v mempool_len=%d mempool_err=%v tx_confirmations=%d tx_verbose_err=%v",
			i, n.RPCAddress(), height, heightErr, len(mempool), mempoolErr, confirmations, verboseErr)
		if logPath := n.LogFile(); logPath != "" {
			if data, err := os.ReadFile(logPath); err == nil {
				const maxLogTail = 8192
				if len(data) > maxLogTail {
					data = data[len(data)-maxLogTail:]
				}
				t.Logf("waitForPOSTx timeout node=%d log_tail:\n%s", i, string(data))
			}
		}
	}
	verbose, err := node.Client.GetRawTransactionVerbose(&txHash)
	require.NoError(t, err)
	require.GreaterOrEqual(t, verbose.Confirmations, uint64(1))
}

func templateLockedOutPoint(asset string, index int) string {
	sum := chainhash.HashH([]byte(fmt.Sprintf("template-e2e:%s:%d", asset, index)))
	return sum.String() + ":0"
}

func sortedTemplateAssets(assets map[string]int64) []string {
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

func buildTemplateDeployTx(t *testing.T, fixture *templateNetworkFixture, signer *btcec.PrivateKey, contract tmplcontract.Contract,
	deployer string, random []byte, inputs []wire.OutPoint, funding wire.TxOut) (*wire.MsgTx, tmplcontract.ContractAddress) {

	t.Helper()
	if fixture != nil && len(inputs) == 0 {
		inputs = fixture.selectFundingOutPoints(t, funding)
	}
	return buildTemplateDeployTxWithInputs(t, fixture, signer, contract, deployer, random, inputs, funding)
}

func buildTemplateDeployTxWithInputs(t *testing.T, fixture *templateNetworkFixture, signer *btcec.PrivateKey, contract tmplcontract.Contract,
	deployer string, random []byte, inputs []wire.OutPoint, funding wire.TxOut) (*wire.MsgTx, tmplcontract.ContractAddress) {

	t.Helper()
	tx, address, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: tmplcontract.TestnetContractPrefix,
		Contract:       contract,
		Deployer:       deployer,
		Random:         random,
		GasLimit:       networkTemplateDeployGasLimit(),
		Funding:        funding,
		Inputs:         inputs,
	})
	require.NoError(t, err)
	signTemplateTaprootInputs(t, tx, signer, fixture.redeemScript, fixture.controlBlock)
	return tx, address
}

func buildTemplateDefaultInvokeTx(t *testing.T, fixture *templateNetworkFixture, signer *btcec.PrivateKey, contract tmplcontract.ContractAddress,
	inputs []wire.OutPoint, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	if fixture != nil && len(inputs) == 0 {
		inputs = fixture.selectFundingOutPoints(t, funding)
	}
	pkScript, err := contractcommon.ContractPkScript(contract)
	require.NoError(t, err)
	funding.PkScript = pkScript
	tx := wire.NewMsgTx(wire.TxVersion)
	for _, input := range inputs {
		tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
	}
	tx.AddTxOut(&funding)
	signTemplateTaprootInputs(t, tx, signer, fixture.redeemScript, fixture.controlBlock)
	return tx
}

func txHasContractOpReturn(tx *wire.MsgTx) bool {
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		if _, _, err := contractcommon.ReadNullDataScript(txOut.PkScript); err == nil {
			return true
		}
	}
	return false
}

func buildTemplateInvokeTx(t *testing.T, fixture *templateNetworkFixture, signer *btcec.PrivateKey, contract tmplcontract.ContractAddress,
	nonce uint64, action string, param []byte, inputs []wire.OutPoint, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	if fixture != nil && len(inputs) == 0 {
		inputs = fixture.selectFundingOutPoints(t, funding)
	}
	return buildTemplateInvokeTxWithInputs(t, fixture, signer, contract, nonce, action, param, inputs, funding)
}

func buildTemplateInvokeTxWithInputs(t *testing.T, fixture *templateNetworkFixture, signer *btcec.PrivateKey, contract tmplcontract.ContractAddress,
	nonce uint64, action string, param []byte, inputs []wire.OutPoint, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	tx, err := tmplcontract.BuildInvokeTx(tmplcontract.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  networkTemplateInvokeGasLimit(),
		CallNonce: nonce,
		Action:    action,
		Param:     param,
		Funding:   funding,
		Inputs:    inputs,
	})
	require.NoError(t, err)
	signTemplateTaprootInputs(t, tx, signer, fixture.redeemScript, fixture.controlBlock)
	return tx
}

func buildTemplateWitnessEVMDeployTx(t *testing.T, fixture *templateNetworkFixture, signer *btcec.PrivateKey, nonce uint64,
	initCode []byte, inputs []wire.OutPoint, funding wire.TxOut, changeOutputs []*wire.TxOut) (*wire.MsgTx, evm.ContractAddress, []wire.OutPoint) {

	t.Helper()
	tx, contract, err := evm.BuildDeployTx(evm.DeployTxBuildRequest{
		ContractPrefix: evm.TestnetContractPrefix,
		Caller:         evmAddressFromAddressString(fixture.spendAddress),
		GasLimit:       networkEVMDeployGasLimit(),
		DeployNonce:    nonce,
		InitCode:       initCode,
		Funding:        funding,
		Inputs:         inputs,
		ChangeOutputs:  changeOutputs,
	})
	require.NoError(t, err)
	signTemplateTaprootInputs(t, tx, signer, fixture.redeemScript, fixture.controlBlock)
	change := collectSpendableOutPoints(t, tx, changeOutputs)
	return tx, contract, change
}

func buildTemplateWitnessEVMInvokeTx(t *testing.T, fixture *templateNetworkFixture, signer *btcec.PrivateKey, contract evm.ContractAddress,
	nonce uint64, calldata []byte, inputs []wire.OutPoint, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	tx, err := evm.BuildInvokeTx(evm.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  networkEVMInvokeGasLimit(),
		CallNonce: nonce,
		Calldata:  calldata,
		Funding:   funding,
		Inputs:    inputs,
	})
	require.NoError(t, err)
	signTemplateTaprootInputs(t, tx, signer, fixture.redeemScript, fixture.controlBlock)
	return tx
}

func buildTemplateSplitTx(t *testing.T, signer *btcec.PrivateKey, input wire.OutPoint, outputs []*wire.TxOut) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: input,
	})
	for _, output := range outputs {
		tx.AddTxOut(output)
	}
	return tx
}

func signTemplateTaprootInputs(t *testing.T, tx *wire.MsgTx, signer *btcec.PrivateKey, redeemScript, controlBlock []byte) {
	t.Helper()
	for _, txIn := range tx.TxIn {
		txIn.SignatureScript = nil
		txIn.Witness = wire.TxWitness{
			signer.PubKey().SerializeCompressed(),
			redeemScript,
			controlBlock,
		}
	}
}

func templateLimitOrderParam(t *testing.T, assetName string, orderType int, amt, unitPrice string) []byte {
	t.Helper()
	param := tmplcontract.LimitOrderInvokeParam{
		OrderType: orderType,
		AssetName: assetName,
		Amt:       amt,
		UnitPrice: unitPrice,
	}
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func templateRefundParam(t *testing.T, itemIDs []int64) []byte {
	t.Helper()
	param := tmplcontract.RefundInvokeParam{ItemIDs: itemIDs}
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func templateAddLiquidityParam(t *testing.T, assetName, amt string, value int64) []byte {
	t.Helper()
	param := tmplcontract.AddLiquidityInvokeParam{
		OrderType: tmplcontract.OrderTypeAddLiquidity,
		AssetName: assetName,
		Amt:       amt,
		Value:     value,
	}
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func templateRemoveLiquidityParam(t *testing.T, assetName, lptAmt string) []byte {
	t.Helper()
	param := tmplcontract.RemoveLiquidityInvokeParam{
		OrderType: tmplcontract.OrderTypeRemoveLiquidity,
		AssetName: assetName,
		LptAmt:    lptAmt,
	}
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func networkTemplateFunding(t *testing.T, assetName string, amount int64) wire.AssetInfo {
	t.Helper()
	return wire.AssetInfo{
		Name:   *wire.NewAssetNameFromString(assetName),
		Amount: *indexercommon.NewDefaultDecimal(amount),
	}
}

func networkTxAssets(assets ...wire.AssetInfo) wire.TxAssets {
	var out wire.TxAssets
	for _, asset := range assets {
		_ = out.Add(&asset)
	}
	return out
}

func networkEVMGasFunding(t *testing.T, assetName string, amount int64) wire.AssetInfo {
	t.Helper()
	return wire.AssetInfo{
		Name:   *wire.NewAssetNameFromString(assetName),
		Amount: *indexercommon.NewDefaultDecimal(amount),
	}
}

func networkTemplateDeployGasLimit() uint64 {
	return tmplcontract.DefaultGasConfig().DeployBaseGas
}

func networkTemplateInvokeGasLimit() uint64 {
	return tmplcontract.DefaultGasConfig().InvokeBaseGas
}

func networkEVMDeployGasLimit() uint64 {
	return maxNetworkGasLimit(evm.DefaultGasConfig().DeployBaseGas, 8_000_000)
}

func networkEVMInvokeGasLimit() uint64 {
	return maxNetworkGasLimit(evm.DefaultGasConfig().InvokeBaseGas, 5_000_000)
}

func maxNetworkGasLimit(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func networkGasFeeAmount(t *testing.T, gas uint64) int64 {
	t.Helper()
	amount, err := contractcommon.GasFeeAtHeight(gas, 0)
	require.NoError(t, err)
	require.LessOrEqual(t, amount, uint64(1<<63-1))
	return int64(amount)
}

func requireTemplateResultBeforeEVMResult(t *testing.T, block *wire.MsgBlock) {
	t.Helper()
	templateResultIndex := -1
	evmResultIndex := -1
	types := make([]string, 0, len(block.Transactions))
	for i, tx := range block.Transactions {
		label := fmt.Sprintf("%d:%s", i, tx.TxHash())
		templateInfo, templateErr := tmplcontract.ClassifyTxForBlockOrder(tx, tmplcontract.TestnetContractPrefix)
		if templateErr == nil && templateInfo.IsTemplate {
			label += fmt.Sprintf(":template:%d", templateInfo.Type)
		}
		if templateErr == nil && templateInfo.IsTemplate && templateInfo.Type == tmplcontract.TxTypeResult && templateResultIndex < 0 {
			templateResultIndex = i
			types = append(types, label)
			continue
		}
		evmInfo, evmErr := evm.ClassifyTxForBlockOrder(tx, evm.TestnetContractPrefix)
		if evmErr == nil && evmInfo.IsEVM {
			label += fmt.Sprintf(":evm:%d", evmInfo.Type)
		}
		if evmErr == nil && evmInfo.IsEVM && evmInfo.Type == evm.TxTypeResult && evmResultIndex < 0 {
			evmResultIndex = i
		}
		types = append(types, label)
	}
	require.GreaterOrEqual(t, templateResultIndex, 0, "missing template result transaction: %v", types)
	require.GreaterOrEqual(t, evmResultIndex, 0, "missing EVM result transaction: %v", types)
	require.Less(t, templateResultIndex, evmResultIndex, "template result must be before EVM result")
}

func requireEVMResultStatusForTx(t *testing.T, node *rpctest.Harness, tx *wire.MsgTx, contract evm.ContractAddress, status evm.ResultStatus) {
	t.Helper()
	outputs, err := evm.FindContractOutputsForContract(tx, evm.StandardContractScriptResolver(evm.TestnetContractPrefix), contract)
	require.NoError(t, err)
	require.NotEmpty(t, outputs)
	hash := tx.TxHash()
	verbose, err := node.Client.GetRawTransactionVerbose(&hash)
	require.NoError(t, err)
	require.NotEmpty(t, verbose.BlockHash)
	blockHash, err := chainhash.NewHashFromStr(verbose.BlockHash)
	require.NoError(t, err)
	block, err := node.Client.GetBlock(blockHash)
	require.NoError(t, err)

	target := wire.OutPoint{Hash: hash, Index: outputs[0].Vout}
	for _, blockTx := range block.Transactions {
		parsed, err := evm.ParseTx(blockTx, nil)
		if err != nil || parsed.Type != evm.TxTypeResult || parsed.Result == nil {
			continue
		}
		for _, txIn := range blockTx.TxIn {
			if txIn.PreviousOutPoint == target {
				require.Equal(t, status, parsed.Result.Status)
				return
			}
		}
	}
	t.Fatalf("missing EVM result for tx %s in block %s", hash, verbose.BlockHash)
}

func testTaprootAddress(t *testing.T, key *btcec.PrivateKey) string {
	t.Helper()
	tapKey := txscript.ComputeTaprootKeyNoScript(key.PubKey())
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(tapKey), &chaincfg.TestNetParams)
	require.NoError(t, err)
	return addr.EncodeAddress()
}

func testCallerTaprootScript(t *testing.T, internalKey *btcec.PrivateKey) ([]byte, string, []byte, []byte) {
	t.Helper()
	redeemScript := testCallerSpendScript(t)
	leaf := txscript.NewBaseTapLeaf(redeemScript)
	tree := txscript.AssembleTaprootScriptTree(leaf)
	rootHash := tree.RootNode.TapHash()
	outputKey := txscript.ComputeTaprootOutputKey(internalKey.PubKey(), rootHash[:])
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(outputKey), &chaincfg.TestNetParams)
	require.NoError(t, err)
	pkScript, err := txscript.PayToAddrScript(addr)
	require.NoError(t, err)
	control := tree.LeafMerkleProofs[0].ToControlBlock(internalKey.PubKey())
	controlBytes, err := control.ToBytes()
	require.NoError(t, err)
	return pkScript, addr.EncodeAddress(), redeemScript, controlBytes
}
