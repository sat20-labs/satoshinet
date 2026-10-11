package contract_e2e

import (
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Every delegate is created by a signed, mined funding transaction. This crosses
// the real Result output limit without seeding private runtime state or storage.
func TestNetworkTemplateAutopayCloseAcrossOutputLimit(t *testing.T) {
	f := newTemplateNetworkFixture(t, nil)
	f.traderAActor = signedTemplateActor(t, f.traderA)
	gas := tmplcontract.DefaultGasConfig().GasAssetName
	count := tmplcontract.AutopayMaxCloseDelegateOutputs + 3
	actors := make([]*templateNetworkActor, count)
	amounts, values := []int64{40000000}, []int64{50000}
	recipients := []*templateNetworkActor{f.traderAActor}
	for i := range actors {
		key := keyFromMnemonic(t, bootstrapMnemonic, uint32(i+10))
		actors[i] = signedTemplateActor(t, key)
		amounts = append(amounts, 10000)
		values = append(values, 100)
		recipients = append(recipients, actors[i])
	}
	fundingInputs := f.splitAssetTo(t, f.gasAnchor, gas, amounts, values, f.traderA, recipients)
	address := scenarioDeploy(t, f, tmplcontract.NewAutopayContract("batch-close", f.traderAActor.address, gas, "2000"),
		"autopay-batch", 0, scenarioFunding(t, 0, map[string]int64{gas: 50}))
	works := make([]*wire.MsgTx, len(actors))
	pkScript, err := contractcommon.ContractPkScript(address)
	require.NoError(t, err)
	for i, actor := range actors {
		// The existing fixture resolves two active signers. Reuse its second slot
		// while creating each independent delegate; all previous UTXOs persist.
		f.traderB, f.traderBActor = actor.key, actor
		tx := wire.NewMsgTx(wire.TxVersion)
		tx.AddTxIn(wire.NewTxIn(&fundingInputs[i+1], nil, nil))
		tx.AddTxOut(wire.NewTxOut(0, wire.TxAssets{networkTemplateFunding(t, gas, 1050)}, pkScript))
		signScenarioInputsWithChange(t, f, tx, actor.key, contractcommon.InvokeBaseGas, 0)
		sendTx(t, f.bootstrapNode, tx)
		works[i] = tx
		if (i+1)%20 == 0 || i == len(actors)-1 {
			// Delegate creation is setup for the output-boundary test. Keep the
			// mempool bounded so POS acknowledgment deadlines are not a load test.
			f.waitForTx(t, tx)
			t.Logf("confirmed delegate funding %d/%d", i+1, len(actors))
		}
	}
	for _, work := range works {
		requireScenarioResult(t, f, work, address)
	}
	view := scenarioAutopayView(t, f.bootstrapNode, address)
	require.Len(t, view.Delegates, count)
	require.Zero(t, view.PaidBlocks, "individual balances below the minimum cannot pay a fee")
	for _, actor := range actors {
		require.Equal(t, "1000", view.Delegates[actor.address].Balance)
	}
	param, err := (&tmplcontract.AutopayConfigInvokeParam{GasFundingAmount: "500"}).Encode()
	require.NoError(t, err)
	scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIConfig, param, scenarioFunding(t, 0, map[string]int64{gas: 550}))
	start, err := f.bootstrapNode.Client.GetBlockCount()
	require.NoError(t, err)
	scenarioInvoke(t, f, f.traderA, address, tmplcontract.InvokeAPIClose, nil, scenarioFunding(t, 0, map[string]int64{gas: 50}))
	for i := 0; i < 3 && !scenarioAutopayView(t, f.bootstrapNode, address).Closed; i++ {
		scenarioMineBlocks(t, f, 1)
	}
	require.True(t, scenarioAutopayView(t, f.bootstrapNode, address).Closed, "close must finish in subsequent mined blocks")
	end, err := f.bootstrapNode.Client.GetBlockCount()
	require.NoError(t, err)
	refunded := make(map[string]int64, count)
	for _, actor := range actors {
		refunded[actor.address] = 0
	}
	var refundBlocks int
	for height := start + 1; height <= end; height++ {
		hash, err := f.bootstrapNode.Client.GetBlockHash(height)
		require.NoError(t, err)
		block, err := f.bootstrapNode.Client.GetBlock(hash)
		require.NoError(t, err)
		paid := false
		for _, tx := range block.Transactions {
			if _, err := contractframework.ResultPayloadFromTx(tx, "template"); err != nil {
				continue
			}
			require.LessOrEqual(t, len(tx.TxOut), contractframework.MaxContractResultOutputs, "each batch must respect the consensus output limit")
			outputs, err := contractframework.ResultOutputsFromTx(tx, tmplcontract.TestnetContractPrefix,
				contractcommon.ParseContractPkScript, testnetScriptRecipient)
			require.NoError(t, err)
			for recipient := range refunded {
				amount := templateResultAssetAmountTo(t, outputs, recipient, gas)
				if amount == "" {
					continue
				}
				require.Equal(t, "1000", amount, "delegate refund must contain the complete principal exactly once")
				refunded[recipient] += 1000
				paid = true
			}
		}
		if paid {
			refundBlocks++
		}
	}
	require.GreaterOrEqual(t, refundBlocks, 2, "more delegates than one Result can refund require multiple real blocks")
	for recipient, amount := range refunded {
		require.EqualValues(t, 1000, amount, "delegate %s", recipient)
	}
	view = scenarioAutopayView(t, f.bootstrapNode, address)
	require.True(t, view.Closed)
	require.Contains(t, []string{"", "0"}, view.FeeBalance)
	for _, delegate := range view.Delegates {
		require.Equal(t, tmplcontract.AutopayStatusClosed, delegate.Status)
	}
	f.requireNodesSynced(t)
}
