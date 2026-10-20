package contract_e2e

import (
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// One signed default transaction contains all calls, so they must settle in
// the same mined block. L1 anchors use the existing isolated indexer fixture;
// both SatoshiNet nodes execute and verify the actual dependent Results.
func TestNetworkTemplatePartialBindingResultFragments(t *testing.T) {
	const asset, count = "ordx:f:r3partial", 1001
	f := newTemplateNetworkFixture(t, map[string]int64{asset: 500 * count}, map[string]uint32{asset: 1000})
	f.traderAActor = signedTemplateActor(t, f.traderA)
	gas := template.DefaultGasConfig().GasAssetName
	f.splitAssetTo(t, f.gasAnchor, gas, []int64{40000000}, []int64{50000}, f.traderA, []*templateNetworkActor{f.traderAActor})
	address := scenarioDeploy(t, f, template.NewAutopayContract("r3-fragments", f.traderAActor.address, asset, "1"),
		"r3-fragments", 0, scenarioFunding(t, 0, map[string]int64{gas: 50}))
	scenarioInvoke(t, f, f.traderA, address, template.InvokeAPIClose, nil, scenarioFunding(t, 0, map[string]int64{gas: 50}))
	require.True(t, scenarioAutopayView(t, f.bootstrapNode, address).Closed)
	anchor := f.assetAnchors[asset]
	assetOutput := wire.NewTxOut(50000, anchor.TxOut[0].Assets.Clone(), f.traderAActor.pkScript)
	funding := buildTemplateSplitTx(t, f.traderA, wire.OutPoint{Hash: anchor.TxHash()}, []*wire.TxOut{assetOutput})
	f.signFundingInput(t, funding, anchor, f.traderA)
	f.sendAndWaitTx(t, funding)
	contractScript, err := contract.ContractPkScript(address)
	require.NoError(t, err)
	work := wire.NewMsgTx(2)
	work.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: funding.TxHash()}, nil, nil))
	// Select real GAS UTXOs through the existing scenario funding selector.
	for _, point := range scenarioSelectFunding(t, f, f.traderAActor, scenarioFunding(t, 0, map[string]int64{gas: count * 50}), contract.InvokeBaseGas*count) {
		work.AddTxIn(wire.NewTxIn(&point, nil, nil))
	}
	for n := 0; n < count; n++ {
		assets := testWireAsset(asset, 500)
		assets[0].BindingSat = 1000
		require.NoError(t, assets.Merge(testWireAsset(gas, 50)))
		work.AddTxOut(wire.NewTxOut(0, assets, contractScript))
	}
	signScenarioInputsWithChange(t, f, work, f.traderA, contract.InvokeBaseGas*count, 0)
	f.sendAndWaitTx(t, work)
	workHash := work.TxHash()
	confirmed, err := f.bootstrapNode.Client.GetRawTransactionVerbose(&workHash)
	require.NoError(t, err)
	blockHash, err := chainhash.NewHashFromStr(confirmed.BlockHash)
	require.NoError(t, err)
	block, err := f.bootstrapNode.Client.GetBlock(blockHash)
	require.NoError(t, err)
	header, err := f.bootstrapNode.Client.GetBlockHeaderVerbose(blockHash)
	require.NoError(t, err)
	available := make(map[wire.OutPoint]*wire.TxOut)
	for n := 0; n < count; n++ {
		available[wire.OutPoint{Hash: workHash, Index: uint32(n)}] = work.TxOut[n]
	}
	var fragments, refunds int
	var gasFees int64
	for _, tx := range block.Transactions {
		if _, err := framework.ResultPayloadFromTx(tx, "template"); err != nil {
			continue
		}
		ours := false
		for _, input := range tx.TxIn {
			if available[input.PreviousOutPoint] != nil {
				ours = true
			}
		}
		if !ours {
			continue
		}
		fragments++
		require.LessOrEqual(t, len(tx.TxOut), 1000)
		view := blockchain.NewUtxoViewpoint()
		for _, input := range tx.TxIn {
			coin := available[input.PreviousOutPoint]
			require.NotNil(t, coin, "Result must consume a reserved original or an earlier real carry exactly once")
			view.Entries()[input.PreviousOutPoint] = blockchain.NewUtxoEntry(coin, header.Height, false)
			delete(available, input.PreviousOutPoint)
		}
		feeSats, fees, err := blockchain.CheckTransactionInputs(btcutil.NewTx(tx), true, header.Height, view, &chaincfg.TestNetParams)
		require.NoError(t, err)
		require.Zero(t, feeSats)
		for _, fee := range fees {
			require.Equal(t, gas, fee.Name.String())
			gasFees += fee.Amount.NewPrecision(0).Value.Int64()
		}
		for n, out := range tx.TxOut[:len(tx.TxOut)-1] {
			if string(out.PkScript) == string(contractScript) {
				available[wire.OutPoint{Hash: tx.TxHash(), Index: uint32(n)}] = out
				continue
			}
			require.Equal(t, f.traderAActor.pkScript, out.PkScript)
			require.Zero(t, out.Value)
			require.Len(t, out.Assets, 1)
			require.Equal(t, asset, out.Assets[0].Name.String())
			require.EqualValues(t, 1000, out.Assets[0].BindingSat)
			require.Equal(t, "500", out.Assets[0].Amount.String())
			refunds++
		}
	}
	require.Equal(t, 2, fragments, "both physical Results must be mined in the work block")
	require.Equal(t, count, refunds)
	require.EqualValues(t, count*50, gasFees)
	require.Empty(t, available, "all call funding and carries must be consumed before completing the block")
	require.True(t, scenarioAutopayView(t, f.bootstrapNode, address).Closed)
	f.requireNodesSynced(t)
}
