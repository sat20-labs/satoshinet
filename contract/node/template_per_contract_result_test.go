package node

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestTemplateValidatorRequiresResultsPerContract(t *testing.T) {
	var plans []template.ResultPlan
	var txs []*wire.MsgTx
	resolve := func(framework.ResultOutput) ([]byte, error) { return []byte{0x51}, nil }
	for n := 0; n < 2; n++ {
		addr := testNodeAutopayRuntime(t, fmt.Sprintf("recipient-%d", n), "ordx:f:fee", "1").Address()
		output := testNodeContractOutput(fmt.Sprintf("%064x", n+1), 0, addr, 1, nil)
		plan := template.ResultPlan{Contract: addr.MustEncode(), Inputs: []framework.OutPoint{output.OutPoint},
			InputUTXOs: []framework.UTXO{{OutPoint: output.OutPoint, Contract: addr, TxOutput: output.IndexerTxOutput()}},
			Outputs:    []framework.ResultOutput{{To: "recipient", Value: 1}}}
		plans = append(plans, plan)
		tx, err := framework.BuildResultTx(framework.ResultTxBuildRequest{Status: contract.ResultStatusSuccess,
			Plans: []framework.ResultPlan{plan}, ResolveScript: resolve}, framework.ResultTxBuildOptions{UseInputUTXOs: true})
		require.NoError(t, err)
		txs = append(txs, tx)
	}
	v := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{ResolveResultScript: resolve,
		ResolveOutput: func(tx *wire.MsgTx) ([]framework.ResultOutput, error) {
			return []framework.ResultOutput{{To: "recipient", Value: tx.TxOut[0].Value}}, nil
		}})
	block := btcutil.NewBlock(&wire.MsgBlock{})
	block.SetHeight(100)
	module, err := v.ContractBlockModule(block, blockchain.NewUtxoViewpoint())
	require.NoError(t, err)
	exec := framework.ExecutionResult{ModuleType: framework.ModuleTemplate, ResultPlans: plans}
	built, err := module.BuildResultTxs(framework.ResultBuildRequest{}, exec)
	require.NoError(t, err)
	require.Len(t, built.ResultTxs, 2, "production assembly must separate contracts")
	for n := range txs {
		require.Equal(t, txs[n].TxHash(), built.ResultTxs[n].TxHash())
	}
	verify := func(txs []*wire.MsgTx) error {
		return module.VerifyResultTxs(framework.ResultVerifyRequest{ResultTxs: txs}, exec)
	}
	require.NoError(t, verify(txs), "production settlement module must accept one Result per contract")
	require.NoError(t, v.verifyResults(txs, plans), "trusted embedding verifier must use the same contract grouping")
	merged, err := framework.BuildResultTx(framework.ResultTxBuildRequest{Status: contract.ResultStatusSuccess,
		Plans: plans, ResolveScript: resolve}, framework.ResultTxBuildOptions{UseInputUTXOs: true})
	require.NoError(t, err)
	for name, bad := range map[string][]*wire.MsgTx{
		"missing": txs[:1], "duplicate": {txs[0], txs[0]}, "reordered": {txs[1], txs[0]}, "mixed contracts": {merged},
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, verify(bad)) })
	}
}
