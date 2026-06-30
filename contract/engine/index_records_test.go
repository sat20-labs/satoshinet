package engine

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
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
		DeployNonce:    7,
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
		DeployNonce:    7,
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

func TestBuildContractIndexRecordsBindsResultBySpentFunding(t *testing.T) {
	deployTx, addr, err := tmplcontract.BuildDeployTx(tmplcontract.DeployTxBuildRequest{
		ContractPrefix: tmplcontract.TestnetContractPrefix,
		Contract:       tmplcontract.NewLimitOrderContract("ordx:f:test"),
		Deployer:       "template-indexer-test",
		DeployNonce:    7,
		GasLimit:       1000,
		Funding:        wire.TxOut{Value: 1},
	})
	require.NoError(t, err)
	fundingVout := requireContractFundingVout(t, deployTx, tmplcontract.TestnetContractPrefix)

	ctx := NewContractIndexContext()
	_, deployHistory, err := BuildContractIndexRecordsWithContext(deployTx, 42, tmplcontract.TestnetContractPrefix, &chaincfg.TestNetParams, ctx)
	require.NoError(t, err)
	ctx.AddFundingRefs(deployTx, deployHistory, tmplcontract.TestnetContractPrefix)

	resultTx := buildResultTxSpending(t, deployTx.TxHash(), fundingVout)
	_, resultHistory, err := BuildContractIndexRecordsWithContext(resultTx, 43, tmplcontract.TestnetContractPrefix, &chaincfg.TestNetParams, ctx)
	require.NoError(t, err)
	require.Len(t, resultHistory, 1)
	require.Equal(t, addr.EncodeAddress(), resultHistory[0].Contract)
	require.Equal(t, contractcommon.ContractTypeTemplate, resultHistory[0].ContractTypeID)
	require.Equal(t, tmplcontract.TemplateLimitOrder, resultHistory[0].Subtype)
	require.Equal(t, "result", resultHistory[0].Kind)
	require.Equal(t, "success", resultHistory[0].Status)
	require.Equal(t, deployTx.TxID(), resultHistory[0].Details["result_for_txid"])
	require.Equal(t, fundingVout, resultHistory[0].Details["result_for_vout"])
	require.Equal(t, "deploy", resultHistory[0].Details["result_for_kind"])
}

func requireContractFundingVout(t *testing.T, tx *wire.MsgTx, prefix string) uint32 {
	t.Helper()
	for i, txOut := range tx.TxOut {
		addr, ok, err := contractcommon.ParseContractPkScript(txOut.PkScript, prefix)
		require.NoError(t, err)
		if ok {
			require.NotEmpty(t, addr.EncodeAddress())
			return uint32(i)
		}
	}
	t.Fatalf("contract funding output not found")
	return 0
}

func buildResultTxSpending(t *testing.T, hash chainhash.Hash, vout uint32) *wire.MsgTx {
	t.Helper()
	resultScript, err := contractcommon.ResultNullDataScript(contractcommon.ResultPayload{
		Status:      contractcommon.ResultStatusSuccess,
		ResultCount: 1,
	})
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: hash, Index: vout}})
	tx.AddTxOut(wire.NewTxOut(0, nil, resultScript))
	return tx
}
