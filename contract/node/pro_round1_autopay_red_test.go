package node

import (
	"encoding/binary"
	"fmt"
	"sort"
	"testing"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
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

// Local chain fixture: only initial actor coins are seeded. Work transactions
// are BIP86-signed, and Results spend the actual preceding transaction outputs.
// It does not substitute a permissive recipient resolver or edit runtime state.
type proRedChain struct {
	store     *template.RuntimeStore
	ledger    *framework.ContractUTXOOverlay
	coins     map[wire.OutPoint]*wire.TxOut
	gas       framework.GasConfig
	precision int
	seed      uint64
}

func proRedActor(t *testing.T, n uint64) (*btcec.PrivateKey, string, []byte) {
	t.Helper()
	var scalar [32]byte
	binary.BigEndian.PutUint64(scalar[24:], n+1)
	key, _ := btcec.PrivKeyFromBytes(scalar[:])
	address, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(txscript.ComputeTaprootKeyNoScript(key.PubKey())), &chaincfg.TestNetParams)
	require.NoError(t, err)
	script, err := txscript.PayToAddrScript(address)
	require.NoError(t, err)
	return key, address.EncodeAddress(), script
}

func proRedAssets(fee, gas int64, binding uint32) wire.TxAssets {
	var assets wire.TxAssets
	if fee > 0 {
		assets = append(assets, wire.AssetInfo{Name: *wire.NewAssetNameFromString("ordx:f:fee"), Amount: *scommon.NewDefaultDecimal(fee), BindingSat: binding})
	}
	if gas > 0 {
		assets = append(assets, wire.AssetInfo{Name: *wire.NewAssetNameFromString(framework.DefaultGasConfig().GasAssetName), Amount: *scommon.NewDefaultDecimal(gas)})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name.String() < assets[j].Name.String() })
	return assets
}

func newProRedChain(precision int) *proRedChain {
	gas := framework.DefaultGasConfig()
	return &proRedChain{store: template.NewRuntimeStore(), ledger: framework.NewContractUTXOOverlay(framework.ContractUTXOOverlayConfig{}), coins: make(map[wire.OutPoint]*wire.TxOut), gas: gas, precision: precision}
}

func (f *proRedChain) signAndCheck(t *testing.T, tx *wire.MsgTx, key *btcec.PrivateKey, height int64) {
	t.Helper()
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	view := blockchain.NewUtxoViewpoint()
	for _, input := range tx.TxIn {
		previous := f.coins[input.PreviousOutPoint]
		require.NotNil(t, previous, "fixture input must exist")
		fetcher.AddPrevOut(input.PreviousOutPoint, previous)
		view.Entries()[input.PreviousOutPoint] = blockchain.NewUtxoEntry(previous, int32(height-1), false)
	}
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	for n, input := range tx.TxIn {
		previous := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		var err error
		input.Witness, err = txscript.TaprootWitnessSignature(tx, hashes, n, previous.Value, previous.Assets, previous.PkScript, txscript.SigHashDefault, key)
		require.NoError(t, err)
		engine, err := txscript.NewEngine(previous.PkScript, tx, n, txscript.StandardVerifyFlags, nil, hashes, previous.Value, previous.Assets, fetcher)
		require.NoError(t, err)
		require.NoError(t, engine.Execute(), "fixture work signature")
	}
	require.NoError(t, blockchain.CheckTransactionSanity(btcutil.NewTx(tx)), "fixture work sanity")
	_, _, err := blockchain.CheckTransactionInputs(btcutil.NewTx(tx), true, int32(height), view, &chaincfg.TestNetParams)
	require.NoError(t, err, "fixture work quantities and BindingSat")
}

func (f *proRedChain) apply(t *testing.T, tx *wire.MsgTx, height int64) {
	t.Helper()
	for _, input := range tx.TxIn {
		delete(f.coins, input.PreviousOutPoint)
	}
	for n, output := range tx.TxOut {
		f.coins[wire.OutPoint{Hash: tx.TxHash(), Index: uint32(n)}] = output
	}
	require.NoError(t, f.ledger.ApplyTx(tx, height))
}

// Establish a genuine predecessor transaction. The initial seed is a single
// actor-owned UTXO with exactly these assets and their required carrier. Extra
// seed carrier becomes a fee here, never a sat added to later refund plans.
func (f *proRedChain) fund(t *testing.T, outputs []*wire.TxOut, height int64) *wire.MsgTx {
	t.Helper()
	key, _, script := proRedActor(t, 0)
	var assets wire.TxAssets
	var value int64
	for _, output := range outputs {
		value += output.Value
		require.NoError(t, assets.Merge(output.Assets))
	}
	carrier, err := contract.RequiredBindingSats(assets)
	require.NoError(t, err)
	if carrier > value {
		value = carrier
	}
	f.seed++
	var hash chainhash.Hash
	binary.BigEndian.PutUint64(hash[:8], f.seed)
	seed := wire.OutPoint{Hash: hash, Index: 100}
	f.coins[seed] = wire.NewTxOut(value, assets, script)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&seed, nil, nil))
	for _, output := range outputs {
		tx.AddTxOut(output)
	}
	f.signAndCheck(t, tx, key, height)
	f.apply(t, tx, height)
	t.Logf("predecessor height=%d txid=%s outputs=%d input_value=%d", height, tx.TxHash(), len(outputs), value)
	return tx
}

func (f *proRedChain) block(t *testing.T, height int64, work ...*wire.MsgTx) (template.BlockResultBuildResult, error) {
	t.Helper()
	built, err := template.BuildBlockResultTxs(template.BlockResultBuildRequest{Store: f.store, Txs: work, BlockHeight: height,
		GasConfig: f.gas, ContractUTXOs: f.ledger.Provider,
		AssetPrecision: func(name string) (int, bool) {
			if name == "ordx:f:fee" {
				return f.precision, true
			}
			return 8, true
		},
		ResolveInvoker: template.LastInputPreviousOutputInvokerResolver(&chaincfg.TestNetParams, func(out wire.OutPoint) ([]byte, bool) {
			coin, ok := f.coins[out]
			if !ok {
				return nil, false
			}
			return coin.PkScript, true
		}),
		ResolveScript: contractResultScriptResolver(&chaincfg.TestNetParams),
		ResolveOutput: contractResultOutputResolver(&chaincfg.TestNetParams, func(script []byte) (string, bool, error) {
			_, addresses, _, err := txscript.ExtractPkScriptAddrs(script, &chaincfg.TestNetParams)
			if err != nil || len(addresses) != 1 {
				return "", false, err
			}
			return addresses[0].EncodeAddress(), true, nil
		}),
	})
	if err != nil {
		return built, err
	}
	for _, tx := range work {
		f.apply(t, tx, height)
	}
	for _, tx := range built.ResultTxs {
		require.NoError(t, blockchain.CheckTransactionSanity(btcutil.NewTx(tx)))
		view := blockchain.NewUtxoViewpoint()
		for _, input := range tx.TxIn {
			coin := f.coins[input.PreviousOutPoint]
			require.NotNil(t, coin, "real Result input must exist and remain unspent")
			view.Entries()[input.PreviousOutPoint] = blockchain.NewUtxoEntry(coin, int32(height-1), false)
		}
		_, _, err := blockchain.CheckTransactionInputs(btcutil.NewTx(tx), true, int32(height), view, &chaincfg.TestNetParams)
		require.NoError(t, err, "real Result versus predecessor UTXOs")
		f.apply(t, tx, height)
		t.Logf("Result height=%d txid=%s inputs=%d outputs=%d", height, tx.TxHash(), len(tx.TxIn), len(tx.TxOut))
	}
	return built, nil
}

func (f *proRedChain) deploy(t *testing.T, recipient, rate string, principal int64, rejected ...bool) contract.ContractAddress {
	t.Helper()
	key, actor, script := proRedActor(t, 0)
	parent := f.fund(t, []*wire.TxOut{wire.NewTxOut(0, proRedAssets(principal, 15000, 0), script)}, 99)
	tx, address, err := template.BuildDeployTx(template.DeployTxBuildRequest{ContractPrefix: contract.TestnetContractPrefix,
		Contract: template.NewAutopayContract("dkvs", recipient, "ordx:f:fee", rate), Deployer: actor,
		DeployNonce: 1, GasLimit: f.gas.DeployBaseGas, Inputs: []wire.OutPoint{{Hash: parent.TxHash()}},
		Funding: wire.TxOut{Assets: proRedAssets(principal, 10000, 0)}})
	require.NoError(t, err)
	f.signAndCheck(t, tx, key, 100)
	built, err := f.block(t, 100, tx)
	require.NoError(t, err, "deployment fixture must reach production Result build")
	if len(rejected) > 0 && rejected[0] {
		require.False(t, f.store.Exists(address), "invalid deployment must not establish runtime")
		require.Len(t, built.Execution.Records, 1)
		require.NotEqual(t, template.ResultStatusSuccess, built.Execution.Records[0].Status, "deployment must fail as a business operation")
		refunded := scommon.NewDefaultDecimal(0)
		gasRefunded := scommon.NewDefaultDecimal(0)
		for _, result := range built.ResultTxs {
			for _, out := range result.TxOut {
				if string(out.PkScript) != string(script) {
					continue
				}
				for _, asset := range out.Assets {
					if asset.Name.String() == "ordx:f:fee" {
						refunded = refunded.AddAlignPrecision(&asset.Amount)
					}
					if asset.Name.String() == f.gas.GasAssetName {
						gasRefunded = gasRefunded.AddAlignPrecision(&asset.Amount)
					}
				}
			}
		}
		require.Equal(t, scommon.NewDefaultDecimal(principal).String(), refunded.String(), "rejected deployment must refund the entire principal")
		require.Equal(t, "9950", gasRefunded.String(), "only the existing Result fee may be consumed")
		t.Logf("rejected deploy no_runtime=true principal_refunded=%s gas_refunded=%s", refunded, gasRefunded)
	} else {
		require.True(t, f.store.Exists(address), "valid deployment accepted by public execution path")
	}
	t.Logf("deploy recipient=%q rate=%s precision=%d principal=%d runtime=%s records=%+v", recipient, rate, f.precision, principal, address.MustEncode(), built.Execution.Records)
	return address
}

func TestProRound1R1RecipientDeploy(t *testing.T) {
	_, valid, _ := proRedActor(t, 1)
	for _, recipient := range []string{valid, "", "not-a-recipient-address"} {
		t.Run(fmt.Sprintf("recipient_%s", recipient), func(t *testing.T) {
			f := newProRedChain(0)
			address := f.deploy(t, recipient, "1", 10, recipient == "not-a-recipient-address")
			built, err := f.block(t, 101)
			t.Logf("automatic payment runtime=%s next_height=101 Results=%d error=%v", address.MustEncode(), len(built.ResultTxs), err)
			require.NoError(t, err, "accepted deployment must not poison subsequent automatic Result construction")
		})
	}
}

func TestProRound1R2DefaultRatePrecision(t *testing.T) {
	_, recipient, _ := proRedActor(t, 1)
	for _, tc := range []struct {
		precision int
		rate      string
	}{{0, "1"}, {2, "0.25"}, {0, "0.5"}} {
		t.Run(fmt.Sprintf("precision_%d_rate_%s", tc.precision, tc.rate), func(t *testing.T) {
			f := newProRedChain(tc.precision)
			invalid := tc.precision == 0 && tc.rate == "0.5"
			address := f.deploy(t, recipient, tc.rate, 10, invalid)
			built, err := f.block(t, 101)
			require.NoError(t, err)
			if invalid {
				require.Empty(t, built.ResultTxs, "rejected default must leave no automatic payment")
				return
			}
			runtime, ok := f.store.Get(address)
			require.True(t, ok)
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			_, actor, _ := proRedActor(t, 0)
			delegate := state.AutopayData().AutopayDelegates[actor]
			actual := scommon.NewDefaultDecimal(0)
			resolve := contractResultScriptResolver(&chaincfg.TestNetParams)
			recipientScript, err := resolve(framework.ResultOutput{To: recipient})
			require.NoError(t, err)
			for _, tx := range built.ResultTxs {
				for _, out := range tx.TxOut {
					if string(out.PkScript) == string(recipientScript) {
						for _, asset := range out.Assets {
							if asset.Name.String() == "ordx:f:fee" {
								actual = actual.AddAlignPrecision(&asset.Amount)
							}
						}
					}
				}
			}
			t.Logf("precision=%d rate=%s principal=10 balance=%s total_paid=%s last_pay=%d paid_blocks=%d actual_Result_paid=%s", tc.precision, tc.rate, delegate.Balance, delegate.TotalPaid, delegate.LastPayHeight, state.AutopayData().PaidBlockCount, actual)
			require.Equal(t, delegate.TotalPaid.String(), actual.String(), "business paid accounting must equal actual Result asset payment")
		})
	}
}

func TestProRound1AutopayRejectedDeployIsolationAndValidation(t *testing.T) {
	for _, failure := range []string{"recipient", "precision"} {
		t.Run(failure, func(t *testing.T) {
			f := newProRedChain(0)
			key, actor, script := proRedActor(t, 0)
			_, recipient, _ := proRedActor(t, 1)
			parent := f.fund(t, []*wire.TxOut{
				wire.NewTxOut(0, proRedAssets(10, 15000, 0), script),
				wire.NewTxOut(0, proRedAssets(20, 15000, 0), script),
			}, 99)
			view := blockchain.NewUtxoViewpoint()
			for outpoint, output := range f.coins {
				view.Entries()[outpoint] = blockchain.NewUtxoEntry(output, 99, false)
			}
			var work []*wire.MsgTx
			var addresses []contract.ContractAddress
			for n := 0; n < 2; n++ {
				to, rate := recipient, "1"
				if n == 0 {
					if failure == "recipient" {
						to = "not-a-recipient-address"
					} else {
						rate = "0.5"
					}
				}
				tx, address, err := template.BuildDeployTx(template.DeployTxBuildRequest{ContractPrefix: contract.TestnetContractPrefix,
					Contract: template.NewAutopayContract("dkvs", to, "ordx:f:fee", rate), Deployer: actor,
					DeployNonce: uint64(n + 1), GasLimit: f.gas.DeployBaseGas, Inputs: []wire.OutPoint{{Hash: parent.TxHash(), Index: uint32(n)}},
					Funding: wire.TxOut{Assets: proRedAssets(int64(10*(n+1)), 10000, 0)}})
				require.NoError(t, err)
				f.signAndCheck(t, tx, key, 100)
				work = append(work, tx)
				addresses = append(addresses, address)
			}
			built, err := f.block(t, 100, work...)
			require.NoError(t, err)
			require.Len(t, built.ResultTxs, 2)
			require.False(t, f.store.Exists(addresses[0]))
			require.True(t, f.store.Exists(addresses[1]))
			require.NotEqual(t, template.ResultStatusSuccess, built.Execution.Records[0].Status)
			require.Equal(t, template.ResultStatusSuccess, built.Execution.Records[1].Status)
			resolve := contractResultScriptResolver(&chaincfg.TestNetParams)
			validator := NewTemplateBlockExecutionValidator(TemplateBlockExecutionConfig{ChainParams: &chaincfg.TestNetParams, GasConfig: f.gas,
				NewRuntime: func(*btcutil.Block, *blockchain.UtxoViewpoint) (*template.RuntimeStore, error) {
					return template.NewRuntimeStore(), nil
				},
				ContractUTXOs: func(contract.ContractAddress) ([]framework.UTXO, error) { return nil, nil },
				AssetPrecision: func(name string) (int, bool) {
					if name == "ordx:f:fee" {
						return 0, true
					}
					return 8, true
				},
				ResolveResultScript: resolve, ResolveOutput: contractResultOutputResolver(&chaincfg.TestNetParams, func(script []byte) (string, bool, error) {
					_, addresses, _, err := txscript.ExtractPkScriptAddrs(script, &chaincfg.TestNetParams)
					if err != nil || len(addresses) != 1 {
						return "", false, err
					}
					return addresses[0].EncodeAddress(), true, nil
				})})
			block := btcutil.NewBlock(&wire.MsgBlock{Transactions: append(append([]*wire.MsgTx{}, work...), built.ResultTxs...)})
			block.SetHeight(100)
			module, err := validator.ContractBlockModule(block, view)
			require.NoError(t, err)
			exec, err := module.ExecuteWorkBlock(framework.WorkExecutionRequest{Txs: work, Prefix: contract.TestnetContractPrefix})
			require.NoError(t, err)
			require.Equal(t, built.Execution.StateRoot, exec.StateRoot, "generation and validation must execute identical deployment decisions")
			require.NoError(t, module.VerifyResultTxs(framework.ResultVerifyRequest{ResultTxs: built.ResultTxs}, exec))
			next, err := f.block(t, 101)
			require.NoError(t, err)
			require.Len(t, next.ResultTxs, 1, "valid peer contract must keep paying after the rejected deployment")
		})
	}
}

func TestProRound1R3PartialBindingRefundCarry(t *testing.T) {
	for _, tc := range []struct {
		count   int
		binding uint32
	}{{1000, 2}, {1001, 0}, {1001, 1}, {1001, 2}} {
		t.Run(fmt.Sprintf("refunds_%d_binding_%d", tc.count, tc.binding), func(t *testing.T) {
			f := newProRedChain(0)
			_, recipient, _ := proRedActor(t, 1)
			address := f.deploy(t, recipient, "1", 0)
			key, _, actorScript := proRedActor(t, 0)
			parent := f.fund(t, []*wire.TxOut{wire.NewTxOut(0, proRedAssets(0, 150, 0), actorScript)}, 100)
			closeTx, err := template.BuildInvokeTx(template.InvokeTxBuildRequest{Contract: address, Action: template.InvokeAPIClose, GasLimit: f.gas.InvokeBaseGas, CallNonce: 1, Inputs: []wire.OutPoint{{Hash: parent.TxHash()}}, Funding: wire.TxOut{Assets: proRedAssets(0, 50, 0)}})
			require.NoError(t, err)
			f.signAndCheck(t, closeTx, key, 101)
			_, err = f.block(t, 101, closeTx)
			require.NoError(t, err)
			runtime, ok := f.store.Get(address)
			require.True(t, ok)
			state, err := runtime.RuntimeState()
			require.NoError(t, err)
			require.True(t, state.AutopayData().Closed)
			var previousOutputs []*wire.TxOut
			value := int64(0)
			if tc.binding == 1 {
				value = 1
			}
			for n := 0; n < tc.count; n++ {
				_, _, script := proRedActor(t, uint64(n+100))
				previousOutputs = append(previousOutputs, wire.NewTxOut(value, proRedAssets(1, 150, tc.binding), script))
			}
			previous := f.fund(t, previousOutputs, 102)
			var work []*wire.MsgTx
			for n := 0; n < tc.count; n++ {
				tx, err := template.BuildInvokeTx(template.InvokeTxBuildRequest{Contract: address, Action: template.InvokeAPIConfig, GasLimit: f.gas.InvokeBaseGas, CallNonce: uint64(n + 10), Inputs: []wire.OutPoint{{Hash: previous.TxHash(), Index: uint32(n)}}, Funding: wire.TxOut{Value: value, Assets: proRedAssets(1, 50, tc.binding)}})
				require.NoError(t, err)
				key, _, _ := proRedActor(t, uint64(n+100))
				f.signAndCheck(t, tx, key, 103)
				work = append(work, tx)
			}
			// Observe the genuine lifecycle-rejection refund plans independently
			// before asking the production physical constructor to encode them.
			candidate := f.store.Clone()
			exec, err := template.ExecuteBlock(template.BlockExecutionRequest{Store: candidate, Txs: work, BlockHeight: 103, GasConfig: f.gas, ContractUTXOs: f.ledger.Provider, AssetPrecision: func(string) (int, bool) { return 8, true }, ResolveInvoker: template.LastInputPreviousOutputInvokerResolver(&chaincfg.TestNetParams, func(out wire.OutPoint) ([]byte, bool) {
				coin, ok := f.coins[out]
				if !ok {
					return nil, false
				}
				return coin.PkScript, true
			})})
			require.NoError(t, err, "invalid invocation execution must produce refund plans")
			outputs := 0
			for _, plan := range exec.ResultPlans {
				outputs += len(plan.Outputs)
			}
			t.Logf("legal predecessor=%s height=102 calls=%d BindingSat=%d per_refund_value=%d actual_refund_outputs=%d", previous.TxHash(), tc.count, tc.binding, value, outputs)
			built, err := f.block(t, 103, work...)
			t.Logf("physical Result construction Results=%d error=%v", len(built.ResultTxs), err)
			require.NoError(t, err, "individually legal partial-binding refunds must remain constructible")
		})
	}
}
