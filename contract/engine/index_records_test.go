package engine

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestBuildContractIndexRecordsIncludesTemplateDeploy(t *testing.T) {
	tx, addr, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: tmplcontract.TestnetContractPrefix,
		Contract:       tmplcontract.NewLimitOrderContract("ordx:f:test"),
		Deployer:       "template-indexer-test",
		Random:         []byte("template-indexer-random"),
		GasLimit:       1000,
		Funding:        wire.TxOut{Value: 1},
	})
	require.NoError(t, err)

	summaries, history, err := BuildContractIndexRecords(tx, 42, tmplcontract.TestnetContractPrefix, &chaincfg.TestNetParams)
	require.NoError(t, err)

	require.NotEmpty(t, summaries)
	require.NotEmpty(t, history)
	require.Equal(t, addr.EncodeAddress(), history[0].Contract)
	require.Equal(t, contractcommon.ContractTypeTemplate, history[0].ContractTypeID)
	require.Equal(t, "deploy", history[0].Kind)
	require.Equal(t, int64(42), history[0].Height)

	var foundSummary bool
	for _, summary := range summaries {
		if summary.Address != addr.EncodeAddress() {
			continue
		}
		if summary.Subtype == "" {
			continue
		}
		foundSummary = true
		require.Equal(t, contractcommon.ContractTypeTemplate, summary.ContractTypeID)
		require.Equal(t, tmplcontract.TemplateLimitOrder, summary.Subtype)
		require.Equal(t, int64(42), summary.CreatedHeight)
	}
	require.True(t, foundSummary)
}

func TestBuildContractIndexRecordsIncludesTemplateInvokeFunding(t *testing.T) {
	_, addr, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: tmplcontract.TestnetContractPrefix,
		Contract:       tmplcontract.NewLimitOrderContract("ordx:f:test"),
		Deployer:       "template-indexer-test",
		Random:         []byte("template-indexer-random"),
		GasLimit:       1000,
		Funding:        wire.TxOut{Value: 1},
	})
	require.NoError(t, err)
	invokeTx, err := tmplcontract.BuildInvokeTx(tmplcontract.InvokeTxBuildRequest{
		Contract:  addr,
		GasLimit:  2000,
		CallNonce: 7,
		Action:    tmplcontract.InvokeAPISwap,
		Funding:   wire.TxOut{Value: 123},
	})
	require.NoError(t, err)

	_, history, err := BuildContractIndexRecords(invokeTx, 43, tmplcontract.TestnetContractPrefix, &chaincfg.TestNetParams)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, addr.EncodeAddress(), history[0].Contract)
	require.Equal(t, contractcommon.ContractTypeTemplate, history[0].ContractTypeID)
	require.Equal(t, "invoke", history[0].Kind)
	require.Equal(t, tmplcontract.InvokeAPISwap, history[0].Action)
	require.Equal(t, int64(2000), history[0].GasLimit)
	require.Equal(t, uint64(7), history[0].Nonce)
	require.NotEmpty(t, history[0].Details["funding_outputs"])
}
