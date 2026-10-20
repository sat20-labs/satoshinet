package node

import (
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Reuse the signed-chain fixture and independently replay each candidate using
// the node's real module validator and the unconsumed parent UTXOs.
func (f *proRedChain) satoshiReviewBlock(t *testing.T, address contract.ContractAddress, height int64, work ...*wire.MsgTx) template.BlockResultBuildResult {
	t.Helper()
	parent := f.store.Clone()
	physical, err := f.ledger.Provider(address)
	require.NoError(t, err)
	view := blockchain.NewUtxoViewpoint()
	for point, output := range f.coins {
		view.Entries()[point] = blockchain.NewUtxoEntry(output, int32(height-1), false)
	}
	built, err := f.block(t, height, work...)
	require.NoError(t, err, "real satoshi Result generation")
	validator := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{
		ChainParams: &chaincfg.TestNetParams, GasConfig: f.gas,
		NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*template.RuntimeStore, error) {
			return parent.Clone(), nil
		},
		ContractUTXOs: func(requested contract.ContractAddress) ([]framework.UTXO, error) {
			require.True(t, requested.Equal(address))
			return physical, nil
		},
		AssetPrecision:      func(string) (int, bool) { return 8, true },
		ResolveResultScript: contractResultScriptResolver(&chaincfg.TestNetParams),
		ResolveOutput: contractResultOutputResolver(&chaincfg.TestNetParams, func(script []byte) (string, bool, error) {
			_, addresses, _, err := txscript.ExtractPkScriptAddrs(script, &chaincfg.TestNetParams)
			if err != nil || len(addresses) != 1 {
				return "", false, err
			}
			return addresses[0].EncodeAddress(), true, nil
		}),
	})
	block := btcutil.NewBlock(&wire.MsgBlock{Transactions: append(append([]*wire.MsgTx{}, work...), built.ResultTxs...)})
	block.SetHeight(int32(height))
	_, err = validator.HasContractBlockActivity(block, view)
	require.NoError(t, err, "node activity probe must not stall on integer satoshi amounts")
	module, err := validator.ContractBlockModule(block, view)
	require.NoError(t, err)
	exec, err := module.ExecuteWorkBlock(framework.WorkExecutionRequest{Txs: work, Prefix: contract.TestnetContractPrefix})
	require.NoError(t, err)
	require.Equal(t, built.Execution.StateRoot, exec.StateRoot)
	require.NoError(t, module.VerifyResultTxs(framework.ResultVerifyRequest{ResultTxs: built.ResultTxs}, exec))
	return built
}

func TestReviewAutopaySatoshiResultLifecycle(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, minerFee := range []bool{false, true} {
			for _, terminal := range []string{template.InvokeAPICancel, template.InvokeAPIClose} {
				t.Run(fmt.Sprintf("config=%v/miner_fee=%v/%s", explicit, minerFee, terminal), func(t *testing.T) {
					f := newProRedChain(8)
					key, actor, actorScript := proRedActor(t, 0)
					_, recipient, recipientScript := proRedActor(t, 1)
					if minerFee {
						recipient = ""
					}
					parent := f.fund(t, []*wire.TxOut{
						wire.NewTxOut(10, proRedAssets(0, 15000, 0), actorScript),
						wire.NewTxOut(0, proRedAssets(0, 150, 0), actorScript),
					}, 99)
					deploy, address, err := template.BuildDeployTx(template.DeployTxBuildRequest{
						ContractPrefix: contract.TestnetContractPrefix, Deployer: actor, DeployNonce: 1,
						Contract: template.NewAutopayContract("sats-review", recipient, contract.SatoshiAssetName, "1"),
						GasLimit: f.gas.DeployBaseGas, Inputs: []wire.OutPoint{{Hash: parent.TxHash()}},
						Funding: wire.TxOut{Value: 10, Assets: proRedAssets(0, 10000, 0)},
					})
					require.NoError(t, err)
					f.signAndCheck(t, deploy, key, 100)
					work := []*wire.MsgTx{deploy}
					if explicit {
						param, err := (&template.AutopayConfigInvokeParam{AmountPerBlock: "1"}).Encode()
						require.NoError(t, err)
						config, err := template.BuildInvokeTx(template.InvokeTxBuildRequest{
							Contract: address, Action: template.InvokeAPIConfig, Param: param, GasLimit: f.gas.InvokeBaseGas, CallNonce: 1,
							Inputs: []wire.OutPoint{{Hash: parent.TxHash(), Index: 1}}, Funding: wire.TxOut{Assets: proRedAssets(0, 50, 0)},
						})
						require.NoError(t, err)
						f.signAndCheck(t, config, key, 100)
						work = append(work, config)
					}
					deployed := f.satoshiReviewBlock(t, address, 100, work...)
					require.NotEmpty(t, deployed.ResultTxs)
					for _, record := range deployed.Execution.Records {
						require.Equal(t, template.ResultStatusSuccess, record.Status)
					}
					encoded, err := f.store.MarshalBinary()
					require.NoError(t, err)
					f.store, err = template.DecodeRuntimeStore(encoded, nil)
					require.NoError(t, err)
					before, _ := f.store.Get(address)
					beforeState, err := before.RuntimeState()
					require.NoError(t, err)
					if !explicit {
						require.Equal(t, 10, beforeState.AutopayData().AutopayDelegates[actor].AmountPerBlock.Precision)
					}
					paid := f.satoshiReviewBlock(t, address, 101)
					require.Len(t, paid.ResultTxs, 1)
					current, _ := f.store.Get(address)
					state, err := current.RuntimeState()
					require.NoError(t, err)
					delegate := state.AutopayData().AutopayDelegates[actor]
					require.Equal(t, "9", delegate.Balance.String())
					require.Equal(t, "1", delegate.TotalPaid.String())
					require.EqualValues(t, 1, delegate.PaidBlockCount)
					require.EqualValues(t, 101, delegate.LastPayHeight)
					require.EqualValues(t, 1, state.AutopayData().PaidBlockCount)
					var total, paidToRecipient int64
					for _, output := range paid.ResultTxs[0].TxOut {
						total += output.Value
						if string(output.PkScript) == string(recipientScript) {
							paidToRecipient += output.Value
						}
					}
					if minerFee {
						require.EqualValues(t, 9, total, "one actual satoshi is collected as miner fee")
						require.Zero(t, paidToRecipient)
					} else {
						require.EqualValues(t, 10, total)
						require.EqualValues(t, 1, paidToRecipient)
					}
					invoke := func(height int64, action string) template.BlockResultBuildResult {
						t.Helper()
						parent := f.fund(t, []*wire.TxOut{wire.NewTxOut(0, proRedAssets(0, 150, 0), actorScript)}, height)
						tx, err := template.BuildInvokeTx(template.InvokeTxBuildRequest{Contract: address, Action: action,
							GasLimit: f.gas.InvokeBaseGas, CallNonce: uint64(height), Inputs: []wire.OutPoint{{Hash: parent.TxHash()}},
							Funding: wire.TxOut{Assets: proRedAssets(0, 50, 0)}})
						require.NoError(t, err)
						f.signAndCheck(t, tx, key, height)
						return f.satoshiReviewBlock(t, address, height, tx)
					}
					refund := invoke(102, terminal)
					var refunded int64
					for _, tx := range refund.ResultTxs {
						for _, out := range tx.TxOut {
							if string(out.PkScript) == string(actorScript) {
								refunded += out.Value
							}
						}
					}
					require.EqualValues(t, 9, refunded)
					if terminal == template.InvokeAPICancel {
						repeated := invoke(103, template.InvokeAPICancel)
						for _, tx := range repeated.ResultTxs {
							for _, out := range tx.TxOut {
								require.Zero(t, out.Value, "repeat cancel cannot refund principal twice")
							}
						}
						invoke(104, template.InvokeAPIClose)
					}
					closed, _ := f.store.Get(address)
					closedState, err := closed.RuntimeState()
					require.NoError(t, err)
					require.True(t, closedState.AutopayData().Closed)
					require.Empty(t, f.satoshiReviewBlock(t, address, 105).ResultTxs)
					t.Log("actual_payment=1 principal=10->9 refunded=9 generator_validator_roots_equal=true")
				})
			}
		}
	}
}
