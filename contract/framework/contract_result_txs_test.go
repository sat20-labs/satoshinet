package framework

import (
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestContractResultTxsSplitAndVerify(t *testing.T) {
	for _, count := range []int{998, 999, 1000, 1001, 1997, 1998, 2001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			addr := testContractAddress(t, ModuleTemplate, 8)
			script, err := contract.ContractPkScript(addr)
			require.NoError(t, err)
			resolve := func(out ResultOutput) ([]byte, error) {
				if out.To == addr.MustEncode() {
					return script, nil
				}
				return txscript.NewScriptBuilder().AddData([]byte(out.To)).AddOp(txscript.OP_DROP).AddOp(txscript.OP_TRUE).Script()
			}
			// One physical UTXO: fragments must spend a real carry rather than reuse
			// the original input or manufacture independent inputs for every chunk.
			gas := wire.AssetInfo{Name: *wire.NewAssetNameFromString("ordx:f:gas"), Amount: *scommon.NewDefaultDecimal(10)}
			pool := wire.NewMsgTx(2)
			pool.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 1}, nil, nil))
			pool.AddTxOut(wire.NewTxOut(int64(count), wire.TxAssets{gas}, script))
			input := UTXOFromTxOutput(OutPoint{TxID: pool.TxHash().String()}, addr, 99, pool.TxOut[0])
			plan := ResultPlan{Contract: addr.MustEncode(), ResultCount: 7, Inputs: []OutPoint{input.OutPoint},
				InputUTXOs: []UTXO{input}, GasFee: scommon.NewDefaultDecimal(1)}
			for n := 0; n < count; n++ {
				plan.Outputs = append(plan.Outputs, ResultOutput{To: fmt.Sprintf("recipient-%04d", n), Value: 1})
			}
			plan.Outputs[len(plan.Outputs)-1].Assets = wire.TxAssets{gas}
			plan.Outputs[len(plan.Outputs)-1].Assets[0].Amount = *scommon.NewDefaultDecimal(9)
			plans := []ResultPlan{plan}
			req := ResultTxBuildRequest{Status: contract.ResultStatusSuccess, Plans: plans, ResolveScript: resolve}
			txs, err := BuildContractResultTxs(req, ResultTxBuildOptions{UseInputUTXOs: true})
			require.NoError(t, err)
			want := 1
			if count > 999 {
				want = 1 + (count-999+997)/998
			}
			require.Len(t, txs, want)
			verify := func(actual []*wire.MsgTx) error {
				return VerifyContractResultTxs(ContractResultVerifyRequest{ResultTxs: actual,
					CanonicalResultVerifyRequest: CanonicalResultVerifyRequest{Label: "template", Status: contract.ResultStatusSuccess,
						Plans: plans, GasAssetName: "ordx:f:gas", ResolveScript: resolve, UseInputUTXO: true, CheckPayload: true}})
			}
			require.NoError(t, verify(txs))
			parent := stubUTXOView{wire.OutPoint{Hash: pool.TxHash()}: addr}
			split, err := SplitBlockContractTxs(SplitRequest{Txs: txs, Prefix: contract.TestnetContractPrefix, Modules: testModules(), ParentView: parent})
			require.NoError(t, err, "classifier must recognize later fragments' carry inputs")
			require.Len(t, split.ResultTxs[ModuleTemplate], want)
			ledger := NewContractUTXOOverlay(ContractUTXOOverlayConfig{})
			require.NoError(t, ledger.ApplyTx(pool, 99))
			finalOutputs := 0
			var finalValue int64
			for n, tx := range txs {
				require.LessOrEqual(t, len(tx.TxOut), MaxContractResultOutputs)
				require.Len(t, tx.TxIn, 1)
				payload, err := ResultPayloadFromTx(tx, "template")
				require.NoError(t, err)
				require.EqualValues(t, 7, payload.ResultCount)
				available, err := ledger.Provider(addr)
				require.NoError(t, err)
				require.Len(t, available, 1)
				require.Equal(t, available[0].OutPoint, WireOutPointToFramework(tx.TxIn[0].PreviousOutPoint))
				for index, out := range tx.TxOut[:len(tx.TxOut)-1] {
					if n < len(txs)-1 && index == len(tx.TxOut)-2 {
						continue
					}
					finalOutputs++
					finalValue += out.Value
				}
				require.NoError(t, ledger.ApplyTx(tx, 100))
			}
			require.Equal(t, count, finalOutputs)
			require.EqualValues(t, count, finalValue)
			remaining, err := ledger.Provider(addr)
			require.NoError(t, err)
			require.Empty(t, remaining, "no temporary carry may survive the final Result")
			bad := make([]*wire.MsgTx, len(txs))
			copy(bad, txs)
			bad[0] = txs[0].Copy()
			bad[0].TxOut[0].Value++
			require.Error(t, verify(bad), "changed amount/carry txid must be rejected")
			bad[0] = txs[0].Copy()
			bad[0].TxIn[0].PreviousOutPoint.Index++
			require.Error(t, verify(bad), "foreign funding must be rejected")
			if len(txs) > 1 {
				require.Error(t, verify(txs[:1]), "missing fragments")
				bad[0] = txs[1]
				bad[1] = txs[0]
				require.Error(t, verify(bad), "reordered fragments")
				bad[0] = txs[0]
				bad[1] = txs[0]
				require.Error(t, verify(bad), "duplicate fragments")
			}
		})
	}
}

func TestContractResultFragmentsWithoutChange(t *testing.T) {
	for _, count := range []int{998, 999, 1000, 1998, 2997} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			addr := testContractAddress(t, ModuleTemplate, 60)
			script, err := contract.ContractPkScript(addr)
			require.NoError(t, err)
			resolve := func(out ResultOutput) ([]byte, error) {
				if out.To == addr.MustEncode() {
					return script, nil
				}
				return []byte{txscript.OP_TRUE}, nil
			}
			asset := wire.AssetInfo{Name: *wire.NewAssetNameFromString("ordx:f:partial"), Amount: *scommon.NewDefaultDecimal(500), BindingSat: 1000}
			plan := ResultPlan{Contract: addr.MustEncode()}
			for n := 0; n < count; n++ {
				input := UTXOFromTxOutput(OutPoint{TxID: fmt.Sprintf("%064x", n+1)}, addr, 99, wire.NewTxOut(0, wire.TxAssets{asset}, script))
				plan.Inputs = append(plan.Inputs, input.OutPoint)
				plan.InputUTXOs = append(plan.InputUTXOs, input)
				plan.Outputs = append(plan.Outputs, ResultOutput{To: "refund", Assets: wire.TxAssets{asset}})
			}
			before := CloneResultPlan(plan)
			txs, err := BuildContractResultTxs(ResultTxBuildRequest{Plans: []ResultPlan{plan}, ResolveScript: resolve}, ResultTxBuildOptions{UseInputUTXOs: true})
			require.NoError(t, err)
			require.Len(t, txs, (count+998)/999, "reserve only the outputs actually needed")
			seen := make(map[wire.OutPoint]bool)
			outputs := 0
			for _, tx := range txs {
				require.LessOrEqual(t, len(tx.TxOut), 1000)
				for _, input := range tx.TxIn {
					require.False(t, seen[input.PreviousOutPoint])
					seen[input.PreviousOutPoint] = true
				}
				for _, output := range tx.TxOut[:len(tx.TxOut)-1] {
					require.NotEqual(t, script, output.PkScript, "zero change needs no carry")
					require.Zero(t, output.Value)
					require.True(t, (&output.Assets).Equal(wire.TxAssets{asset}))
					outputs++
				}
			}
			require.Len(t, seen, count)
			require.Equal(t, count, outputs)
			require.Equal(t, before, CloneResultPlan(plan), "physical construction must not mutate the logical plan")
			require.NoError(t, VerifyContractResultTxs(ContractResultVerifyRequest{ResultTxs: txs, CanonicalResultVerifyRequest: CanonicalResultVerifyRequest{
				Plans: []ResultPlan{plan}, ResolveScript: resolve, UseInputUTXO: true, CheckPayload: true}}))
		})
	}
}

func TestContractResultFragmentsFailAtomically(t *testing.T) {
	addr := testContractAddress(t, ModuleTemplate, 61)
	script, err := contract.ContractPkScript(addr)
	require.NoError(t, err)
	input := UTXOFromTxOutput(OutPoint{TxID: fmt.Sprintf("%064x", 1)}, addr, 99, wire.NewTxOut(1000, nil, script))
	plan := ResultPlan{Contract: addr.MustEncode(), Inputs: []OutPoint{input.OutPoint}, InputUTXOs: []UTXO{input}}
	for n := 0; n < 1001; n++ {
		plan.Outputs = append(plan.Outputs, ResultOutput{To: "refund", Value: 1})
	}
	before := CloneResultPlan(plan)
	txs, err := BuildContractResultTxs(ResultTxBuildRequest{Plans: []ResultPlan{plan}, ResolveScript: func(ResultOutput) ([]byte, error) { return script, nil }}, ResultTxBuildOptions{UseInputUTXOs: true})
	require.Error(t, err, "a funded first fragment must not hide an unfunded final fragment")
	require.Nil(t, txs, "return no partially successful Result set")
	require.Equal(t, before, CloneResultPlan(plan))
}

func TestContractResultFragmentsAdjustForPartialBindingChange(t *testing.T) {
	addr := testContractAddress(t, ModuleTemplate, 62)
	script, err := contract.ContractPkScript(addr)
	require.NoError(t, err)
	resolve := func(out ResultOutput) ([]byte, error) {
		if out.To == addr.MustEncode() {
			return script, nil
		}
		return []byte{txscript.OP_TRUE}, nil
	}
	asset := wire.AssetInfo{Name: *wire.NewAssetNameFromString("ordx:f:business"), Amount: *scommon.NewDefaultDecimal(500)}
	partial := wire.AssetInfo{Name: *wire.NewAssetNameFromString("ordx:f:partial"), Amount: *scommon.NewDefaultDecimal(500), BindingSat: 1000}
	plan := ResultPlan{Contract: addr.MustEncode()}
	for n := 0; n < 2; n++ {
		input := UTXOFromTxOutput(OutPoint{TxID: fmt.Sprintf("%064x", n+1)}, addr, 99, wire.NewTxOut(0, wire.TxAssets{asset, partial}, script))
		plan.Inputs = append(plan.Inputs, input.OutPoint)
		plan.InputUTXOs = append(plan.InputUTXOs, input)
	}
	unit := asset
	unit.Amount = *scommon.NewDefaultDecimal(1)
	for n := 0; n < 1000; n++ {
		plan.Outputs = append(plan.Outputs, ResultOutput{To: "business", Assets: wire.TxAssets{unit}})
	}
	for n := 0; n < 2; n++ {
		plan.Outputs = append(plan.Outputs, ResultOutput{To: "refund", Assets: wire.TxAssets{partial}})
	}
	txs, err := BuildContractResultTxs(ResultTxBuildRequest{Plans: []ResultPlan{plan}, ResolveScript: resolve}, ResultTxBuildOptions{UseInputUTXOs: true})
	require.NoError(t, err, "a smaller first fragment avoids merging both partial bindings into an unfunded carry")
	require.Len(t, txs, 2)
	require.Len(t, txs[0].TxIn, 1, "leave the other original input for the last fragment")
	require.Len(t, txs[0].TxOut, 502, "500 business outputs, one legal carry, one metadata")
	require.Len(t, txs[1].TxIn, 2, "consume the real carry and the reserved original")
	require.NoError(t, VerifyContractResultTxs(ContractResultVerifyRequest{ResultTxs: txs, CanonicalResultVerifyRequest: CanonicalResultVerifyRequest{
		Plans: []ResultPlan{plan}, ResolveScript: resolve, UseInputUTXO: true, CheckPayload: true}}))
}
