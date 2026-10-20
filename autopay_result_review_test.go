package main

import (
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

func resultReviewGas() framework.GasConfig {
	gas := framework.DefaultGasConfig()
	gas.GasAssetName, gas.BootstrapAddress = "ordx:f:gas", "bootstrap"
	gas.DeployBaseGas, gas.InvokeBaseGas, gas.TriggerBaseGas, gas.ResultBaseGas = 1, 1, 1, 1
	return gas
}

func resultReviewAssets(fee, gas int64) wire.TxAssets {
	var assets wire.TxAssets
	if fee > 0 {
		assets = append(assets, wire.AssetInfo{Name: *wire.NewAssetNameFromString("ordx:f:fee"), Amount: *scommon.NewDefaultDecimal(fee)})
	}
	if gas > 0 {
		assets = append(assets, wire.AssetInfo{Name: *wire.NewAssetNameFromString("ordx:f:gas"), Amount: *scommon.NewDefaultDecimal(gas)})
	}
	return assets
}

// Seed an existing contract through public invocation APIs. Only this initial
// fixture uses a pool; all later blocks spend the actual preceding Result.
func resultReviewAutopay(t *testing.T, store *template.RuntimeStore, ledger *framework.ContractUTXOOverlay, nonce uint64, delegates int) contract.ContractAddress {
	t.Helper()
	ap := template.NewAutopayContract("dkvs", fmt.Sprintf("recipient-%d", nonce), "ordx:f:fee", "1")
	content, err := ap.Encode()
	require.NoError(t, err)
	addr, _, err := template.DeriveContractAddress(contract.TestnetContractPrefix, content, "deployer", nonce)
	require.NoError(t, err)
	runtime, err := template.NewRuntimeWithDeployer(addr, template.DeployPayload{SubType: template.TemplateAutopay,
		Version: template.CurrentTemplateVersion, ContractContent: content, DeployNonce: nonce}, nil, "deployer")
	require.NoError(t, err)
	param, err := (&template.AutopayConfigInvokeParam{AmountPerBlock: "1"}).Encode()
	require.NoError(t, err)
	for n := 0; n < delegates; n++ {
		_, err = runtime.ApplyInvoke(template.ApplyInvokeRequest{Action: template.InvokeAPIConfig, Param: param,
			Invoker: fmt.Sprintf("payer-%d-%04d", nonce, n), CallID: fmt.Sprintf("config-%d", n), Height: 98,
			FundingOutput: framework.ContractOutputFromFunding(contract.FundingOutput{Contract: addr,
				OutPoint: contract.TxOutPoint{TxID: fmt.Sprintf("%064x", n+1)}, Assets: resultReviewAssets(1, 0)}),
		})
		require.NoError(t, err)
	}
	_, err = runtime.ApplyDefaultInvoke(template.ApplyInvokeRequest{GasAssetName: resultReviewGas().GasAssetName, Height: 98,
		Invoker: "deployer", CallID: "gas", FundingOutput: framework.ContractOutputFromFunding(contract.FundingOutput{
			Contract: addr, OutPoint: contract.TxOutPoint{TxID: "gas"}, Assets: resultReviewAssets(0, 100),
		})})
	require.NoError(t, err)
	store.Add(runtime)
	pool := wire.NewMsgTx(2)
	pool.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{byte(nonce)}, Index: 1}, nil, nil))
	script, err := contract.ContractPkScript(addr)
	require.NoError(t, err)
	pool.AddTxOut(wire.NewTxOut(0, resultReviewAssets(int64(delegates), 100), script))
	managed, ok := store.ManagedBalance(addr)
	require.True(t, ok)
	require.NoError(t, managed.Credit(0, pool.TxOut[0].Assets))
	require.NoError(t, ledger.ApplyTx(pool, 98))
	return addr
}

func resultReviewInvoke(t *testing.T, addr contract.ContractAddress, action, actor string, nonce uint64) *wire.MsgTx {
	t.Helper()
	gas := resultReviewGas()
	fee, err := gas.ResultFee(100)
	require.NoError(t, err)
	assets := resultReviewAssets(0, 1)
	assets[0].Amount = *fee
	script, err := template.InvokeNullDataScript(template.InvokePayload{Action: action, CallNonce: nonce, GasLimit: gas.InvokeBaseGas})
	require.NoError(t, err)
	contractScript, err := contract.ContractPkScript(addr)
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{byte(nonce), 99}, Index: 1}, []byte(actor), nil))
	if action == contract.ContractInvokeAPIDefault {
		assets = resultReviewAssets(0, 1)
	} else {
		tx.AddTxOut(wire.NewTxOut(0, nil, script))
	}
	tx.AddTxOut(wire.NewTxOut(0, assets, contractScript))
	return tx
}

func resultReviewBlock(t *testing.T, store *template.RuntimeStore, ledger *framework.ContractUTXOOverlay, height int64, txs ...*wire.MsgTx) (template.BlockResultBuildResult, error) {
	t.Helper()
	resolveScript := func(output framework.ResultOutput) ([]byte, error) {
		if addr, err := contract.DecodeContractAddress(output.To); err == nil {
			return contract.ContractPkScript(addr)
		}
		return txscript.NewScriptBuilder().AddData([]byte(output.To)).AddOp(txscript.OP_DROP).AddOp(txscript.OP_TRUE).Script()
	}
	built, err := template.BuildBlockResultTxs(template.BlockResultBuildRequest{Store: store, Txs: txs, BlockHeight: height,
		GasConfig: resultReviewGas(), ContractUTXOs: ledger.Provider, AssetPrecision: func(string) (int, bool) { return 8, true },
		ResolveInvoker: func(tx *wire.MsgTx, _ contract.Tx) (string, error) { return string(tx.TxIn[0].SignatureScript), nil },
		ResolveScript:  resolveScript,
		ResolveOutput: func(tx *wire.MsgTx) ([]framework.ResultOutput, error) {
			return framework.ResultOutputsFromTx(tx, contract.TestnetContractPrefix, contract.ParseContractPkScript,
				func(script []byte) (string, bool, error) {
					pushes, err := txscript.PushedData(script)
					if err != nil || len(pushes) != 1 {
						return "", false, err
					}
					return string(pushes[0]), true, nil
				})
		},
	})
	if err != nil {
		return built, err
	}
	for _, tx := range built.ResultTxs {
		if err := blockchain.CheckTransactionSanity(btcutil.NewTx(tx)); err != nil {
			return built, err
		}
		require.LessOrEqual(t, len(tx.TxOut), framework.MaxContractResultOutputs)
	}
	for _, tx := range txs {
		require.NoError(t, ledger.ApplyTx(tx, height))
	}
	actualGasFee := scommon.NewDefaultDecimal(0)
	for _, tx := range built.ResultTxs {
		view := blockchain.NewUtxoViewpoint()
		available := make(map[framework.OutPoint]string)
		for _, plan := range built.Execution.ResultPlans {
			addr, err := contract.DecodeContractAddress(plan.Contract)
			require.NoError(t, err)
			utxos, err := ledger.Provider(addr)
			require.NoError(t, err)
			for _, utxo := range utxos {
				available[utxo.OutPoint] = plan.Contract
				outpoint, err := framework.ResultWireOutPoint(utxo.OutPoint)
				require.NoError(t, err)
				script, err := contract.ContractPkScript(addr)
				require.NoError(t, err)
				view.Entries()[*outpoint] = blockchain.NewUtxoEntry(wire.NewTxOut(utxo.PhysicalValue(), utxo.TxAssets(), script), int32(utxo.Height), false)
			}
		}
		owner := ""
		for _, input := range tx.TxIn {
			key := framework.WireOutPointToFramework(input.PreviousOutPoint)
			got, ok := available[key]
			require.True(t, ok, "Result must spend an existing, unspent physical UTXO: %s", key)
			if owner == "" {
				owner = got
			}
			require.Equal(t, owner, got, "Result must not mix contracts")
			delete(available, key)
		}
		_, feeAssets, err := blockchain.CheckTransactionInputs(btcutil.NewTx(tx), true, int32(height), view, &chaincfg.TestNetParams)
		require.NoError(t, err, "validate real Result quantities against predecessor UTXOs")
		for _, asset := range feeAssets {
			require.Equal(t, resultReviewGas().GasAssetName, asset.Name.String(), "principal must never become a fee")
			actualGasFee = actualGasFee.AddAlignPrecision(&asset.Amount)
		}
		require.NoError(t, ledger.ApplyTx(tx, height))
	}
	expectedGasFee := scommon.NewDefaultDecimal(0)
	for _, plan := range built.Execution.ResultPlans {
		if plan.GasFee != nil {
			expectedGasFee = expectedGasFee.AddAlignPrecision(plan.GasFee)
		}
	}
	require.Equal(t, expectedGasFee.String(), actualGasFee.String(), "actual fees must equal execution's debit across all Results")
	return built, nil
}

func TestAutopayResultPerContractAcrossBlocks(t *testing.T) {
	store := template.NewRuntimeStore()
	ledger := framework.NewContractUTXOOverlay(framework.ContractUTXOOverlayConfig{})
	a := resultReviewAutopay(t, store, ledger, 1, 2000)
	b := resultReviewAutopay(t, store, ledger, 2, 1)
	// B's same-block GAS call creates its retained output before its first payment.
	first, err := resultReviewBlock(t, store, ledger, 100,
		resultReviewInvoke(t, a, template.InvokeAPIClose, "deployer", 1),
		resultReviewInvoke(t, b, contract.ContractInvokeAPIDefault, "deployer", 2))
	require.NoError(t, err)
	require.Len(t, first.ResultTxs, 2, "each contract must have its own Result")
	require.Equal(t, 1000, len(first.ResultTxs[0].TxOut)+len(first.ResultTxs[1].TxOut)-2)
	second, err := resultReviewBlock(t, store, ledger, 101)
	require.NoError(t, err, "independent contract Results must not share an output limit")
	require.Len(t, second.ResultTxs, 2)
	require.Equal(t, 1001, len(second.ResultTxs[0].TxOut)+len(second.ResultTxs[1].TxOut)-2)
	spendsPreviousResult := false
	for _, tx := range second.ResultTxs {
		require.LessOrEqual(t, len(tx.TxOut), framework.MaxContractResultOutputs)
		for _, input := range tx.TxIn {
			for _, parent := range first.ResultTxs {
				if input.PreviousOutPoint.Hash == parent.TxHash() {
					spendsPreviousResult = true
				}
			}
		}
	}
	require.True(t, spendsPreviousResult, "closing must continue from a real preceding Result; normal payment may select an older unspent pool")
	third, err := resultReviewBlock(t, store, ledger, 102)
	require.NoError(t, err)
	require.NotEmpty(t, third.ResultTxs, "remaining close refunds must complete across blocks")
	runtime, ok := store.Get(a)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AutopayData().Closed)
	require.Empty(t, state.Items)
	encoded, err := store.MarshalBinary()
	require.NoError(t, err)
	restored, err := template.DecodeRuntimeStore(encoded, nil)
	require.NoError(t, err)
	for height := int64(103); height <= 104; height++ {
		next, err := resultReviewBlock(t, restored, ledger, height)
		require.NoError(t, err)
		require.Empty(t, next.ResultTxs)
	}
}

func TestAutopayResultSplitsSingleContractRefunds(t *testing.T) {
	store := template.NewRuntimeStore()
	ledger := framework.NewContractUTXOOverlay(framework.ContractUTXOOverlayConfig{})
	addr := resultReviewAutopay(t, store, ledger, 4, 1001)
	var work []*wire.MsgTx
	for n := 0; n < 1001; n++ {
		work = append(work, resultReviewInvoke(t, addr, template.InvokeAPICancel, fmt.Sprintf("payer-4-%04d", n), uint64(n+1)))
	}
	work = append(work, resultReviewInvoke(t, addr, template.InvokeAPIClose, "deployer", 2000))
	built, err := resultReviewBlock(t, store, ledger, 100, work...)
	require.NoError(t, err)
	require.Len(t, built.ResultTxs, 2, "one contract's refunds must split instead of failing the block")
	refunded := scommon.NewDefaultDecimal(0)
	gasRefund := scommon.NewDefaultDecimal(0)
	for _, tx := range built.ResultTxs {
		for _, out := range tx.TxOut {
			if _, ok, _ := contract.ParseContractPkScript(out.PkScript, contract.TestnetContractPrefix); ok {
				continue
			}
			for _, asset := range out.Assets {
				if asset.Name.String() == "ordx:f:fee" {
					refunded = refunded.AddAlignPrecision(&asset.Amount)
				}
				if asset.Name.String() == resultReviewGas().GasAssetName {
					gasRefund = gasRefund.AddAlignPrecision(&asset.Amount)
				}
			}
		}
	}
	require.Equal(t, "1001", refunded.String(), "every principal refunded exactly once")
	require.Equal(t, "100", gasRefund.String(), "splitting must not charge gas again or retain a temporary carry")
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AutopayData().Closed)
	require.Empty(t, state.Items)
	data, err := store.MarshalBinary()
	require.NoError(t, err)
	restored, err := template.DecodeRuntimeStore(data, nil)
	require.NoError(t, err)
	for height := int64(101); height <= 102; height++ {
		next, err := resultReviewBlock(t, restored, ledger, height)
		require.NoError(t, err)
		require.Empty(t, next.ResultTxs, "closed cancel items must never be settled again")
	}
}

func TestAutopayCancelCloseLeavesNoResultOnFollowingBlocks(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("close_first_%v", closeFirst), func(t *testing.T) {
			store := template.NewRuntimeStore()
			ledger := framework.NewContractUTXOOverlay(framework.ContractUTXOOverlayConfig{})
			addr := resultReviewAutopay(t, store, ledger, 3, 1)
			cancel := resultReviewInvoke(t, addr, template.InvokeAPICancel, "payer-3-0000", 1)
			close := resultReviewInvoke(t, addr, template.InvokeAPIClose, "deployer", 2)
			txs := []*wire.MsgTx{cancel, close}
			if closeFirst {
				txs = []*wire.MsgTx{close, cancel}
			}
			first, err := resultReviewBlock(t, store, ledger, 100, txs...)
			require.NoError(t, err)
			require.Len(t, first.ResultTxs, 1)
			if closeFirst {
				require.Equal(t, contract.ResultStatusInvalid, first.Execution.Records[1].Status)
			} else {
				require.Equal(t, contract.ResultStatusSuccess, first.Execution.Records[0].Status)
				require.Equal(t, contract.ResultStatusSuccess, first.Execution.Records[1].Status)
			}
			fee, err := resultReviewGas().ResultFee(100)
			require.NoError(t, err)
			require.Equal(t, fee.AddAlignPrecision(fee).String(), first.Execution.ResultPlans[0].GasFee.String())
			returned := map[string]*contract.ManagedBalance{"payer-3-0000": {}, "deployer": {}}
			for _, output := range first.Execution.ResultPlans[0].Outputs {
				balance := returned[output.To]
				require.NotNil(t, balance, "refund destination must belong to the original payer or deployer")
				require.NoError(t, balance.Credit(output.Value, output.Assets))
			}
			principal, err := returned["payer-3-0000"].AssetAmount("ordx:f:fee")
			require.NoError(t, err)
			operating, err := returned["deployer"].AssetAmount(resultReviewGas().GasAssetName)
			require.NoError(t, err)
			require.Equal(t, "1", principal.String())
			require.Equal(t, "100", operating.String())
			current, _ := store.Get(addr)
			state, err := current.RuntimeState()
			require.NoError(t, err)
			require.True(t, state.AutopayData().Closed)
			require.Empty(t, state.Items, "closed terminal state must contain no pending normal controls")
			managed, _ := store.ManagedBalance(addr)
			require.True(t, managed.IsZero())
			root := store.StateRoot()
			encoded, err := store.MarshalBinary()
			require.NoError(t, err)
			store, err = template.DecodeRuntimeStore(encoded, nil)
			require.NoError(t, err)
			require.Equal(t, root, store.StateRoot())
			for height := int64(101); height <= 102; height++ {
				built, err := resultReviewBlock(t, store, ledger, height)
				require.NoError(t, err, "closed contract must never emit a zero-input Result")
				require.Empty(t, built.ResultTxs)
				require.Equal(t, root, store.StateRoot())
			}
			// A new invalid call after close still has to refund its own funds.
			failed := resultReviewInvoke(t, addr, template.InvokeAPICancel, "payer-3-0000", 3)
			assets := resultReviewAssets(7, 1)
			assets[1].Amount = *fee.AddAlignPrecision(scommon.NewDefaultDecimal(5))
			failed.TxOut[len(failed.TxOut)-1].Assets = assets
			rejected, err := resultReviewBlock(t, store, ledger, 103, failed)
			require.NoError(t, err)
			require.Len(t, rejected.ResultTxs, 1)
			require.Equal(t, contract.ResultStatusInvalid, rejected.Execution.Records[0].Status)
			require.Equal(t, fee.String(), rejected.Execution.ResultPlans[0].GasFee.String())
			refunded := contract.ManagedBalance{}
			for _, output := range rejected.Execution.ResultPlans[0].Outputs {
				require.Equal(t, "payer-3-0000", output.To)
				require.NoError(t, refunded.Credit(output.Value, output.Assets))
			}
			feeRefund, err := refunded.AssetAmount("ordx:f:fee")
			require.NoError(t, err)
			gasRefund, err := refunded.AssetAmount(resultReviewGas().GasAssetName)
			require.NoError(t, err)
			require.Equal(t, "7", feeRefund.String())
			require.Equal(t, "5", gasRefund.String())
			empty, err := resultReviewBlock(t, store, ledger, 104)
			require.NoError(t, err)
			require.Empty(t, empty.ResultTxs)
		})
	}
}

func TestAutopayFragmentFailureDoesNotCommitClose(t *testing.T) {
	store := template.NewRuntimeStore()
	ledger := framework.NewContractUTXOOverlay(framework.ContractUTXOOverlayConfig{})
	addr := resultReviewAutopay(t, store, ledger, 6, 1001)
	var work []*wire.MsgTx
	for n := 0; n < 1001; n++ {
		work = append(work, resultReviewInvoke(t, addr, template.InvokeAPICancel, fmt.Sprintf("payer-6-%04d", n), uint64(n+1)))
	}
	work = append(work, resultReviewInvoke(t, addr, template.InvokeAPIClose, "deployer", 2000))
	before := store.StateRoot()
	carryBuilt := false
	built, err := template.BuildBlockResultTxs(template.BlockResultBuildRequest{Store: store, Txs: work, BlockHeight: 100,
		GasConfig: resultReviewGas(), ContractUTXOs: ledger.Provider, AssetPrecision: func(string) (int, bool) { return 8, true },
		ResolveInvoker: func(tx *wire.MsgTx, _ contract.Tx) (string, error) { return string(tx.TxIn[0].SignatureScript), nil },
		ResolveScript: func(output framework.ResultOutput) ([]byte, error) {
			if output.To == "payer-6-1000" {
				return nil, fmt.Errorf("injected final fragment failure")
			}
			if address, err := contract.DecodeContractAddress(output.To); err == nil {
				carryBuilt = true
				return contract.ContractPkScript(address)
			}
			return txscript.NewScriptBuilder().AddData([]byte(output.To)).AddOp(txscript.OP_DROP).AddOp(txscript.OP_TRUE).Script()
		},
	})
	require.ErrorContains(t, err, "injected final fragment failure")
	require.True(t, carryBuilt, "failure must occur after constructing the first fragment")
	require.Empty(t, built.ResultTxs)
	require.Equal(t, before, store.StateRoot(), "no partial settlement or lifecycle commit")
	runtime, ok := store.Get(addr)
	require.True(t, ok)
	state, err := runtime.RuntimeState()
	require.NoError(t, err)
	require.False(t, state.AutopayData().Closed)
	// The identical work can still complete and close after the build error.
	_, err = resultReviewBlock(t, store, ledger, 100, work...)
	require.NoError(t, err)
	runtime, ok = store.Get(addr)
	require.True(t, ok)
	state, err = runtime.RuntimeState()
	require.NoError(t, err)
	require.True(t, state.AutopayData().Closed)
}
