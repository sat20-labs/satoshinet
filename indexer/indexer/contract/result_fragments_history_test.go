package contractindex

import (
	"testing"

	db "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	sncommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestContractIndexerTracksResultCarryThroughFinalPayout(t *testing.T) {
	kvdb := db.NewKVDB(t.TempDir())
	require.NotNil(t, kvdb)
	t.Cleanup(func() { kvdb.Close() })
	idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
	deploy, addr := buildTestTemplateDeployTx(t)
	vout := requireTestContractFundingVout(t, deploy)
	deploy.TxOut[vout].Value = 1001
	point := framework.OutPoint{TxID: deploy.TxID(), Vout: vout}
	plan := framework.ResultPlan{Contract: addr.MustEncode(), Inputs: []framework.OutPoint{point},
		InputUTXOs: []framework.UTXO{framework.UTXOFromTxOutput(point, addr, 10, deploy.TxOut[vout])}}
	for n := 0; n < 1001; n++ {
		plan.Outputs = append(plan.Outputs, framework.ResultOutput{To: "recipient", Value: 1})
	}
	results, err := framework.BuildContractResultTxs(framework.ResultTxBuildRequest{
		Status: contract.ResultStatusSuccess, Plans: []framework.ResultPlan{plan},
		ResolveScript: func(output framework.ResultOutput) ([]byte, error) {
			if output.To == addr.MustEncode() {
				return contract.ContractPkScript(addr)
			}
			return []byte{txscript.OP_TRUE}, nil
		}}, framework.ResultTxBuildOptions{UseInputUTXOs: true})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Len(t, results[1].TxIn, 1)
	require.Equal(t, results[0].TxHash(), results[1].TxIn[0].PreviousOutPoint.Hash)
	for _, output := range results[1].TxOut {
		_, found, err := contract.ParseContractPkScript(output.PkScript, contract.TestnetContractPrefix)
		require.NoError(t, err)
		require.False(t, found, "final payout must have no contract output")
	}
	idx.ProcessBlock(testContractBlock(10, append([]*wire.MsgTx{deploy}, results...)...))
	idx.UpdateDB()
	idx = NewIndexer(kvdb, &chaincfg.TestNetParams)
	history, total := idx.GetContractHistory(addr.MustEncode(), 0, 0)
	require.Equal(t, 3, total, "deploy and both physical Results must survive persistence")
	byTx := make(map[string]contract.ContractHistoryRecord)
	for _, record := range history {
		byTx[record.TxID] = record
	}
	for _, result := range results {
		record, found := byTx[result.TxID()]
		require.True(t, found)
		require.Equal(t, "result", record.Kind)
		require.Equal(t, addr.MustEncode(), record.Contract)
		require.Equal(t, contract.ContractTypeTemplate, record.ContractTypeID)
		require.Equal(t, deploy.TxID(), record.Details["result_for_txid"])
	}
}

func TestContractIndexerTracksHistoricalResultInputs(t *testing.T) {
	for _, scenario := range []string{"without_carry", "invoke_then_historical", "historical_carry", "mixed_inputs_known_call"} {
		t.Run(scenario, func(t *testing.T) {
			kvdb := db.NewKVDB(t.TempDir())
			require.NotNil(t, kvdb)
			t.Cleanup(func() { kvdb.Close() })
			idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
			deploy, addr := buildTestTemplateDeployTx(t)
			vout := requireTestContractFundingVout(t, deploy)
			deploy.TxOut[vout].Value = 999
			funding := []*wire.MsgTx{deploy}
			vouts := []uint32{vout}
			var invoke *wire.MsgTx
			switch scenario {
			case "without_carry":
				vouts = append(vouts, uint32(len(deploy.TxOut)))
				deploy.AddTxOut(wire.NewTxOut(2, nil, deploy.TxOut[vout].PkScript))
				funding = append(funding, deploy)
			case "invoke_then_historical", "mixed_inputs_known_call":
				deploy.TxOut[vout].Value = 2
				invoke = buildTestTemplateInvokeTx(t, addr)
				invokeVout := requireTestContractFundingVout(t, invoke)
				invoke.TxOut[invokeVout].Value = 999
				funding = append(funding, invoke)
				vouts = append(vouts, invokeVout)
			case "historical_carry":
				deploy.TxOut[vout].Value = 1001
			}
			idx.ProcessBlock(testContractBlock(9, deploy))
			idx.UpdateDB()
			idx = NewIndexer(kvdb, &chaincfg.TestNetParams)
			plan := framework.ResultPlan{Contract: addr.MustEncode()}
			previous := make(map[wire.OutPoint]*wire.TxOut)
			for n, tx := range funding {
				point := framework.OutPoint{TxID: tx.TxID(), Vout: vouts[n]}
				plan.Inputs = append(plan.Inputs, point)
				height := int64(9)
				if tx == invoke {
					height = 10
				}
				plan.InputUTXOs = append(plan.InputUTXOs, framework.UTXOFromTxOutput(point, addr, height, tx.TxOut[vouts[n]]))
				previous[wire.OutPoint{Hash: tx.TxHash(), Index: vouts[n]}] = tx.TxOut[vouts[n]]
			}
			for n := 0; n < 1001; n++ {
				plan.Outputs = append(plan.Outputs, framework.ResultOutput{To: "recipient", Value: 1})
			}
			wantFragments := 2
			if scenario == "mixed_inputs_known_call" {
				plan.Outputs = []framework.ResultOutput{{To: "recipient", Value: 1001}}
				wantFragments = 1
			}
			resolve := func(output framework.ResultOutput) ([]byte, error) {
				if output.To == addr.MustEncode() {
					return contract.ContractPkScript(addr)
				}
				return []byte{txscript.OP_TRUE}, nil
			}
			results, err := framework.BuildContractResultTxs(framework.ResultTxBuildRequest{
				Status: contract.ResultStatusSuccess, Plans: []framework.ResultPlan{plan}, ResolveScript: resolve,
			}, framework.ResultTxBuildOptions{UseInputUTXOs: true})
			require.NoError(t, err)
			require.Len(t, results, wantFragments)
			require.NoError(t, framework.VerifyContractResultTxs(framework.ContractResultVerifyRequest{
				ResultTxs: results, CanonicalResultVerifyRequest: framework.CanonicalResultVerifyRequest{
					Status: contract.ResultStatusSuccess, Plans: []framework.ResultPlan{plan},
					ResolveScript: resolve, UseInputUTXO: true, CheckPayload: true,
				},
			}))
			if scenario == "invoke_then_historical" {
				require.Len(t, results[0].TxIn, 1)
				require.Equal(t, invoke.TxHash(), results[0].TxIn[0].PreviousOutPoint.Hash)
				require.Len(t, results[1].TxIn, 1)
				require.Equal(t, deploy.TxHash(), results[1].TxIn[0].PreviousOutPoint.Hash)
			}
			if scenario == "mixed_inputs_known_call" {
				require.Len(t, results[0].TxIn, 2)
				require.Equal(t, deploy.TxHash(), results[0].TxIn[0].PreviousOutPoint.Hash,
					"historical input must precede the known invoke to test association priority")
			}
			blockTxs := append([]*wire.MsgTx(nil), results...)
			wantHistory := 1 + len(results)
			if invoke != nil {
				blockTxs = append([]*wire.MsgTx{invoke}, blockTxs...)
				wantHistory++
			}
			block := testContractBlock(10, blockTxs...)
			for n, result := range results {
				tx := block.Transactions[len(block.Transactions)-len(results)+n]
				for _, input := range result.TxIn {
					output, found := previous[input.PreviousOutPoint]
					require.True(t, found)
					tx.Inputs = append(tx.Inputs, &sncommon.Input{
						Txid: input.PreviousOutPoint.Hash.String(), Vout: input.PreviousOutPoint.Index,
						Value: output.Value, Address: &sncommon.ScriptPubKey{PkScript: output.PkScript},
					})
				}
				for vout, output := range result.TxOut {
					previous[wire.OutPoint{Hash: result.TxHash(), Index: uint32(vout)}] = output
					_, found, err := contract.ParseContractPkScript(output.PkScript, contract.TestnetContractPrefix)
					require.NoError(t, err)
					if scenario != "historical_carry" || n == len(results)-1 {
						require.False(t, found, "no contract output can identify this Result")
					}
				}
			}
			if scenario == "historical_carry" {
				require.Equal(t, results[0].TxHash(), results[1].TxIn[0].PreviousOutPoint.Hash)
			}
			idx.ProcessBlock(block)
			idx.UpdateDB()
			idx = NewIndexer(kvdb, &chaincfg.TestNetParams)
			history, total := idx.GetContractHistory(addr.MustEncode(), 0, 0)
			require.Equal(t, wantHistory, total, "all physical Results must survive persistence")
			byTx := make(map[string]contract.ContractHistoryRecord)
			for _, record := range history {
				byTx[record.TxID] = record
			}
			for n, result := range results {
				record, found := byTx[result.TxID()]
				require.True(t, found)
				require.Equal(t, "result", record.Kind)
				require.Equal(t, addr.MustEncode(), record.Contract)
				require.Equal(t, contract.ContractTypeTemplate, record.ContractTypeID)
				if invoke != nil && n == 0 {
					require.Equal(t, invoke.TxID(), record.Details["result_for_txid"])
					require.Equal(t, "invoke", record.Details["result_for_kind"])
				} else {
					require.NotContains(t, record.Details, "result_for_txid", "input ownership must not invent a call association")
					require.NotContains(t, record.Details, "result_for_vout")
					require.NotContains(t, record.Details, "result_for_kind")
				}
			}
			require.True(t, idx.CheckSelf())
		})
	}
}

func TestContractIndexerIgnoresMismatchedResolvedInputs(t *testing.T) {
	for _, mismatch := range []string{"txid", "vout", "script", "unresolved"} {
		t.Run(mismatch, func(t *testing.T) {
			kvdb := db.NewKVDB(t.TempDir())
			require.NotNil(t, kvdb)
			t.Cleanup(func() { kvdb.Close() })
			idx := NewIndexer(kvdb, &chaincfg.TestNetParams)
			deploy, addr := buildTestTemplateDeployTx(t)
			vout := requireTestContractFundingVout(t, deploy)
			idx.ProcessBlock(testContractBlock(9, deploy))
			idx.UpdateDB()
			result := buildTestResultTxSpending(t, deploy.TxHash(), vout)
			block := testContractBlock(10, result)
			input := &sncommon.Input{Txid: deploy.TxID(), Vout: vout,
				Address: &sncommon.ScriptPubKey{PkScript: deploy.TxOut[vout].PkScript}}
			switch mismatch {
			case "txid":
				input.Txid = result.TxID()
			case "vout":
				input.Vout++
			case "script":
				input.Address.PkScript = []byte{txscript.OP_TRUE}
			case "unresolved":
				input.Address = nil
			}
			block.Transactions[0].Inputs = []*sncommon.Input{input}
			idx.ProcessBlock(block)
			idx.UpdateDB()
			idx = NewIndexer(kvdb, &chaincfg.TestNetParams)
			history, total := idx.GetContractHistory(addr.MustEncode(), 0, 0)
			require.Equal(t, 1, total, "unresolved input metadata must not invent a Result association")
			require.Equal(t, "deploy", history[0].Kind)
		})
	}
}
