package contract_e2e

import (
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/stretchr/testify/require"
)

func TestNetworkTemplateAutopaySatoshiResult(t *testing.T) {
	f := newSignedTemplateFixture(t, nil)
	gas := template.DefaultGasConfig().GasAssetName
	address := scenarioDeploy(t, f, template.NewAutopayContract("satoshi-review", f.traderBActor.address, contract.SatoshiAssetName, "1"),
		"satoshi-review", 0, scenarioFunding(t, 10, map[string]int64{gas: 1000}))
	before := scenarioAutopayView(t, f.bootstrapNode, address)
	require.Equal(t, "10", before.Delegates[f.traderAActor.address].Balance)
	require.Zero(t, before.PaidBlocks)
	scenarioMineBlocks(t, f, 1)
	after := scenarioAutopayView(t, f.bootstrapNode, address)
	delegate := after.Delegates[f.traderAActor.address]
	require.Equal(t, "9", delegate.Balance)
	require.Equal(t, "1", delegate.TotalPaid)
	require.EqualValues(t, 1, after.PaidBlocks)
	hash, err := f.bootstrapNode.Client.GetBlockHash(after.LastPayHeight)
	require.NoError(t, err)
	block, err := f.bootstrapNode.Client.GetBlock(hash)
	require.NoError(t, err)
	var paid int64
	for _, tx := range block.Transactions {
		if _, err := framework.ResultPayloadFromTx(tx, "template"); err != nil {
			continue
		}
		outputs, err := framework.ResultOutputsFromTx(tx, template.TestnetContractPrefix,
			contract.ParseContractPkScript, testnetScriptRecipient)
		require.NoError(t, err)
		paid += templateResultValueTo(outputs, f.traderBActor.address)
	}
	require.EqualValues(t, 1, paid, "the mined physical Result must pay exactly one satoshi")
	coreBlock, err := f.coreNode.Client.GetBlock(hash)
	require.NoError(t, err)
	require.Equal(t, block, coreBlock, "both nodes retain the same actual Result bytes and root commitment")
	root, found, err := contract.FindCoinbaseStateRoot(block.Transactions[0])
	require.NoError(t, err)
	require.True(t, found)
	outputs := scenarioInvoke(t, f, f.traderA, address, template.InvokeAPIClose, nil, scenarioFunding(t, 0, map[string]int64{gas: 50}))
	require.EqualValues(t, 9, templateResultValueTo(outputs, f.traderAActor.address))
	require.True(t, scenarioAutopayView(t, f.bootstrapNode, address).Closed)
	require.True(t, scenarioAutopayView(t, f.coreNode, address).Closed)
	f.requireNodesSynced(t)
	t.Logf("real_network_satoshi_payment=1 principal=10->9 close_refund=9 block=%s state_root=%x", hash, root.StateRoot)
}
