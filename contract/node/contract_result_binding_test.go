package node

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestContractResultBindingFragmentsConserveRealInputs(t *testing.T) {
	for _, tc := range []struct {
		count   int
		binding uint32
		mixed   bool
	}{
		{999, 2, false}, {1000, 2, false}, {1001, 2, false}, {2001, 2, false},
		{1001, 0, false}, {1001, 1, false}, {1001, 2, true},
	} {
		t.Run(fmt.Sprintf("outputs_%d_binding_%d_mixed_%v", tc.count, tc.binding, tc.mixed), func(t *testing.T) {
			addr := testNodeAutopayRuntime(t, "recipient", "ordx:f:fee", "1").Address()
			contractScript, err := contract.ContractPkScript(addr)
			require.NoError(t, err)
			resolve := func(out framework.ResultOutput) ([]byte, error) {
				if out.To == addr.MustEncode() {
					return contractScript, nil
				}
				return txscript.NewScriptBuilder().AddData([]byte(out.To)).AddOp(txscript.OP_DROP).AddOp(txscript.OP_TRUE).Script()
			}
			gasName, feeName := "ordx:f:gas", "ordx:f:fee"
			pool := wire.NewMsgTx(2)
			pool.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{91}, Index: 1}, nil, nil))
			plan := framework.ResultPlan{Contract: addr.MustEncode(), Height: 100, GasFee: scommon.NewDefaultDecimal(int64(tc.count * 50))}
			for n := 0; n < tc.count; n++ {
				amount := int64(1)
				if tc.mixed && n%2 == 0 {
					amount = 2
				}
				value := int64(0)
				if tc.binding != 0 {
					value = amount / int64(tc.binding)
				}
				fee := wire.AssetInfo{Name: *wire.NewAssetNameFromString(feeName), Amount: *scommon.NewDefaultDecimal(amount), BindingSat: tc.binding}
				gas := wire.AssetInfo{Name: *wire.NewAssetNameFromString(gasName), Amount: *scommon.NewDefaultDecimal(50)}
				pool.AddTxOut(wire.NewTxOut(value, wire.TxAssets{fee, gas}, contractScript))
				plan.Outputs = append(plan.Outputs, framework.ResultOutput{To: fmt.Sprintf("refund-%04d", n), Value: value, Assets: wire.TxAssets{fee}})
			}
			require.NoError(t, blockchain.CheckTransactionSanity(btcutil.NewTx(pool)), "every predecessor output obeys partial binding semantics")
			for n, out := range pool.TxOut {
				point := framework.OutPoint{TxID: pool.TxHash().String(), Vout: uint32(n)}
				plan.Inputs = append(plan.Inputs, point)
				plan.InputUTXOs = append(plan.InputUTXOs, framework.UTXOFromTxOutput(point, addr, 99, out))
			}
			txs, err := framework.BuildContractResultTxs(framework.ResultTxBuildRequest{Status: contract.ResultStatusSuccess, Plans: []framework.ResultPlan{plan}, ResolveScript: resolve}, framework.ResultTxBuildOptions{UseInputUTXOs: true})
			require.NoError(t, err)
			verify := func(actual []*wire.MsgTx) error {
				return framework.VerifyContractResultTxs(framework.ContractResultVerifyRequest{ResultTxs: actual, CanonicalResultVerifyRequest: framework.CanonicalResultVerifyRequest{
					Label: "template", Status: contract.ResultStatusSuccess, Plans: []framework.ResultPlan{plan}, GasAssetName: gasName, ResolveScript: resolve, UseInputUTXO: true, CheckPayload: true}})
			}
			require.NoError(t, verify(txs))
			ledger := framework.NewContractUTXOOverlay(framework.ContractUTXOOverlayConfig{})
			require.NoError(t, ledger.ApplyTx(pool, 99))
			seenInputs := make(map[wire.OutPoint]bool)
			refunds := make(map[string]int)
			actualGas := scommon.NewDefaultDecimal(0)
			for _, tx := range txs {
				require.LessOrEqual(t, len(tx.TxOut), 1000, "all outputs count, including metadata and carry")
				require.NoError(t, blockchain.CheckTransactionSanity(btcutil.NewTx(tx)))
				available, err := ledger.Provider(addr)
				require.NoError(t, err)
				view := blockchain.NewUtxoViewpoint()
				for _, coin := range available {
					point, err := framework.ResultWireOutPoint(coin.OutPoint)
					require.NoError(t, err)
					view.Entries()[*point] = blockchain.NewUtxoEntry(wire.NewTxOut(coin.PhysicalValue(), coin.TxAssets(), contractScript), 99, false)
				}
				for _, input := range tx.TxIn {
					require.False(t, seenInputs[input.PreviousOutPoint], "original/carry input may be consumed only once")
					seenInputs[input.PreviousOutPoint] = true
				}
				feeSats, fees, err := blockchain.CheckTransactionInputs(btcutil.NewTx(tx), true, 100, view, &chaincfg.TestNetParams)
				require.NoError(t, err)
				require.Zero(t, feeSats)
				for _, fee := range fees {
					require.Equal(t, gasName, fee.Name.String(), "refund principal must not become a fee")
					actualGas = actualGas.AddAlignPrecision(&fee.Amount)
				}
				for _, out := range tx.TxOut[:len(tx.TxOut)-1] {
					if string(out.PkScript) == string(contractScript) {
						continue
					}
					pushes, err := txscript.PushedData(out.PkScript)
					require.NoError(t, err)
					require.Len(t, pushes, 1)
					to := string(pushes[0])
					refunds[to]++
					var n int
					_, err = fmt.Sscanf(to, "refund-%04d", &n)
					require.NoError(t, err)
					want := plan.Outputs[n]
					require.Equal(t, want.Value, out.Value)
					require.True(t, (&out.Assets).Equal(want.Assets))
				}
				require.NoError(t, ledger.ApplyTx(tx, 100))
			}
			require.Equal(t, plan.GasFee.String(), actualGas.String(), "total physical charge equals one logical settlement")
			require.Len(t, refunds, tc.count)
			for _, count := range refunds {
				require.Equal(t, 1, count)
			}
			for n := range pool.TxOut {
				require.True(t, seenInputs[wire.OutPoint{Hash: pool.TxHash(), Index: uint32(n)}])
			}
			remaining, err := ledger.Provider(addr)
			require.NoError(t, err)
			require.Empty(t, remaining, "closed settlement leaves neither carry nor original escrow")
			if len(txs) > 1 {
				bad := append([]*wire.MsgTx(nil), txs...)
				bad[0] = txs[0].Copy()
				carry := bad[0].TxOut[len(bad[0].TxOut)-2]
				carry.Value++
				require.Error(t, verify(bad), "modified carry value must be rejected")
				bad[0] = txs[0].Copy()
				carry = bad[0].TxOut[len(bad[0].TxOut)-2]
				if len(carry.Assets) > 0 {
					carry.Assets[0].BindingSat++
					require.Error(t, verify(bad), "modified carry metadata must be rejected")
				}
				require.Error(t, verify(txs[:len(txs)-1]), "missing fragment")
				bad = append([]*wire.MsgTx(nil), txs...)
				bad[0], bad[1] = bad[1], bad[0]
				require.Error(t, verify(bad), "reordered fragments")
				bad = append([]*wire.MsgTx(nil), txs...)
				bad[1] = bad[0]
				require.Error(t, verify(bad), "duplicate fragment")
			}
		})
	}
}

func TestContractResultDefaultPartialBindingFragments(t *testing.T) {
	const count = 1001
	f := newProRedChain(0)
	key, _, actorScript := proRedActor(t, 0)
	_, recipient, _ := proRedActor(t, 1)
	address := f.deploy(t, recipient, "1", 0)
	parent := f.fund(t, []*wire.TxOut{wire.NewTxOut(0, proRedAssets(0, 150, 0), actorScript)}, 100)
	closeTx, err := template.BuildInvokeTx(template.InvokeTxBuildRequest{Contract: address, Action: template.InvokeAPIClose,
		GasLimit: f.gas.InvokeBaseGas, CallNonce: 1, Inputs: []wire.OutPoint{{Hash: parent.TxHash()}}, Funding: wire.TxOut{Assets: proRedAssets(0, 50, 0)}})
	require.NoError(t, err)
	f.signAndCheck(t, closeTx, key, 101)
	_, err = f.block(t, 101, closeTx)
	require.NoError(t, err)
	parent = f.fund(t, []*wire.TxOut{wire.NewTxOut(500, proRedAssets(500*count, 150*count, 1000), actorScript)}, 102)
	script, err := contract.ContractPkScript(address)
	require.NoError(t, err)
	work := wire.NewMsgTx(2)
	work.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: parent.TxHash()}, nil, nil))
	for n := 0; n < count; n++ {
		work.AddTxOut(wire.NewTxOut(0, proRedAssets(500, 50, 1000), script))
	}
	work.AddTxOut(wire.NewTxOut(500, nil, actorScript))
	f.signAndCheck(t, work, key, 103)
	built, err := f.block(t, 103, work)
	require.NoError(t, err)
	require.Len(t, built.ResultTxs, 2)
	require.Equal(t, built.Execution.StateRoot, f.store.StateRoot())
	require.Equal(t, "2e3b088c66407e1e1b5f067fa24a33818bc09ff855f1716f9f7bc2022d66de6c", fmt.Sprintf("%x", built.Execution.StateRoot))
	t.Logf("aggregation-equivalence state_root=%x", built.Execution.StateRoot)
	wantHashes := []string{
		"8a610a07a5191eb02e78f0fd8d3c6bc08ea5ac34aee5e75a3d504435cb02936f",
		"b9d34c28596ffce6eb6e522d8a80a529164cc0e1427ebd7dadb559947f8e7e07",
	}
	for i, tx := range built.ResultTxs {
		var raw bytes.Buffer
		require.NoError(t, tx.Serialize(&raw))
		require.Equal(t, wantHashes[i], fmt.Sprintf("%x", sha256.Sum256(raw.Bytes())), "serialized Result must match the pre-optimization fixture")
		t.Logf("aggregation-equivalence result=%d bytes=%d sha256=%x", i, raw.Len(), sha256.Sum256(raw.Bytes()))
	}
	refunds := 0
	for _, tx := range built.ResultTxs {
		require.LessOrEqual(t, len(tx.TxOut), 1000)
		for _, out := range tx.TxOut {
			if string(out.PkScript) != string(actorScript) {
				continue
			}
			require.Zero(t, out.Value)
			require.Len(t, out.Assets, 1)
			require.Equal(t, "500", out.Assets[0].Amount.String())
			require.EqualValues(t, 1000, out.Assets[0].BindingSat)
			refunds++
		}
	}
	require.Equal(t, count, refunds)
	remaining, err := f.ledger.Provider(address)
	require.NoError(t, err)
	require.Empty(t, remaining)
}
