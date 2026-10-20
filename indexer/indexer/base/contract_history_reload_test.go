package base

import (
	"bytes"
	"fmt"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	framework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/indexer/common"
	contractindex "github.com/sat20-labs/satoshinet/indexer/indexer/contract"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestLoadStoredContractScript(t *testing.T) {
	for _, params := range []*chaincfg.Params{&chaincfg.MainNetParams, &chaincfg.TestNetParams, &chaincfg.RegressionNetParams} {
		for _, typ := range []byte{contract.ContractTypeTemplate, contract.ContractTypeEVM, contract.ContractTypeAgent} {
			t.Run(fmt.Sprintf("%s/type=%d", params.Name, typ), func(t *testing.T) {
				store := indexerdb.NewKVDB(t.TempDir())
				require.NotNil(t, store)
				t.Cleanup(func() { store.Close() })
				prefix, hashSize := contract.TestnetContractPrefix, 32
				if params.Net == wire.MainNet {
					prefix = contract.MainnetContractPrefix
				}
				if typ == contract.ContractTypeEVM {
					hashSize = 20
				}
				addr, err := contract.NewContractAddressFromHash(prefix, contract.AddressVersionV1, typ, bytes.Repeat([]byte{7}, hashSize))
				require.NoError(t, err)
				want, err := contract.ContractPkScript(addr)
				require.NoError(t, err)
				key := fmt.Sprintf("%064x:0", typ)
				value := &common.UtxoValueInDB{UtxoId: indexercommon.ToUtxoId(1, 1, 0), Value: 7,
					AddressType: uint16(txscript.ContractTy), AddressIds: []uint64{1}}
				batch := store.NewWriteBatch()
				require.NoError(t, indexerdb.BindAddressDBKeyToId(addr.MustEncode(), 1, batch))
				require.NoError(t, indexerdb.SetDB(indexerdb.GetUTXODBKey(key), value, batch))
				require.NoError(t, batch.Flush())
				batch.Close()
				b := &BaseIndexer{db: store, chaincfgParam: params, utxoIndex: common.NewUTXOIndex(),
					addressValueMap: make(map[string]*indexercommon.AddressValueV2)}
				require.NoError(t, b.loadUtxoFromDB(key))
				loaded := b.utxoIndex.Index[key]
				require.Equal(t, want, loaded.Address.PkScript)
				require.Equal(t, int64(7), loaded.Value)
				require.Equal(t, []string{addr.MustEncode()}, loaded.Address.Addresses)
			})
		}
	}
}

func TestLoadStoredContractRejectsInvalidAddressMetadata(t *testing.T) {
	for _, invalid := range []string{"missing", "multiple", "noncontract"} {
		t.Run(invalid, func(t *testing.T) {
			store := indexerdb.NewKVDB(t.TempDir())
			require.NotNil(t, store)
			t.Cleanup(func() { store.Close() })
			addr, err := btcutil.NewAddressTaproot(bytes.Repeat([]byte{7}, 32), &chaincfg.TestNetParams)
			require.NoError(t, err)
			value := &common.UtxoValueInDB{AddressType: uint16(txscript.ContractTy), AddressIds: []uint64{1}}
			if invalid == "missing" {
				value.AddressIds = nil
			} else if invalid == "multiple" {
				value.AddressIds = []uint64{1, 1}
			}
			key := fmt.Sprintf("%064x:0", 1)
			batch := store.NewWriteBatch()
			require.NoError(t, indexerdb.BindAddressDBKeyToId(addr.EncodeAddress(), 1, batch))
			require.NoError(t, indexerdb.SetDB(indexerdb.GetUTXODBKey(key), value, batch))
			require.NoError(t, batch.Flush())
			batch.Close()
			b := &BaseIndexer{db: store, chaincfgParam: &chaincfg.TestNetParams, utxoIndex: common.NewUTXOIndex(),
				addressValueMap: make(map[string]*indexercommon.AddressValueV2)}
			require.Error(t, b.loadUtxoFromDB(key))
			require.NotContains(t, b.utxoIndex.Index, key, "bad metadata must not publish a usable contract UTXO")
		})
	}
}

func TestContractResultHistoryAfterUTXOReload(t *testing.T) {
	for _, restart := range []bool{false, true} {
		for _, carry := range []bool{false, true} {
			t.Run(fmt.Sprintf("restart=%v/carry=%v", restart, carry), func(t *testing.T) {
				b, path, _ := membershipIndexer(t)
				idx := contractindex.NewIndexer(b.db, b.chaincfgParam)
				b.SetBlockCallback(idx.ProcessBlock)
				deploy, addr, err := contract.BuildDeployTx(contract.DeployTxBuildRequest{
					Type: contract.ContractTypeTemplate, SubType: contract.TemplateLimitOrder,
					Deployer: "reload-test", DeployNonce: 1, GasLimit: contract.DeployBaseGas,
					ContractContent: []byte(`{"assetName":"ordx:f:test"}`), Funding: wire.TxOut{Value: 1001},
				})
				require.NoError(t, err)
				script, err := contract.ContractPkScript(addr)
				require.NoError(t, err)
				if !carry {
					for _, output := range deploy.TxOut {
						if txscript.GetScriptClass(output.PkScript) == txscript.ContractTy {
							output.Value = 999
						}
					}
					deploy.AddTxOut(wire.NewTxOut(2, nil, script))
				}
				plan := framework.ResultPlan{Contract: addr.MustEncode()}
				for vout, output := range deploy.TxOut {
					if txscript.GetScriptClass(output.PkScript) != txscript.ContractTy {
						continue
					}
					point := framework.OutPoint{TxID: deploy.TxID(), Vout: uint32(vout)}
					plan.Inputs = append(plan.Inputs, point)
					plan.InputUTXOs = append(plan.InputUTXOs, framework.UTXOFromTxOutput(point, addr, 1, output))
				}
				first := &wire.MsgBlock{Transactions: []*wire.MsgTx{wire.NewMsgTx(2), deploy}}
				require.NoError(t, b.SyncBlock(first, 1, 1, false))
				idx.UpdateDB()
				// This is the production snapshot commit: persist the old
				// UTXOs and remove their scripts from the compiling cache.
				b.CommitBackup(b.Clone(true))
				require.Empty(t, b.utxoIndex.Index)
				if restart {
					params := b.chaincfgParam
					require.NoError(t, b.db.Close())
					b.db = nil
					store := indexerdb.NewKVDB(path)
					require.NotNil(t, store)
					t.Cleanup(func() { store.Close() })
					b = NewBaseIndexer(store, params, 0, 100)
					b.Init()
				}
				idx = contractindex.NewIndexer(b.db, b.chaincfgParam)
				var processed *common.Block
				b.SetBlockCallback(func(block *common.Block) {
					processed = block
					idx.ProcessBlock(block)
				})
				for n := 0; n < 1001; n++ {
					plan.Outputs = append(plan.Outputs, framework.ResultOutput{To: "recipient", Value: 1})
				}
				resolve := func(output framework.ResultOutput) ([]byte, error) {
					if output.To == addr.MustEncode() {
						return script, nil
					}
					return []byte{txscript.OP_TRUE}, nil
				}
				results, err := framework.BuildContractResultTxs(framework.ResultTxBuildRequest{
					Status: contract.ResultStatusSuccess, Plans: []framework.ResultPlan{plan}, ResolveScript: resolve,
				}, framework.ResultTxBuildOptions{UseInputUTXOs: true})
				require.NoError(t, err)
				require.Len(t, results, 2)
				require.NoError(t, framework.VerifyContractResultTxs(framework.ContractResultVerifyRequest{
					ResultTxs: results, CanonicalResultVerifyRequest: framework.CanonicalResultVerifyRequest{
						Status: contract.ResultStatusSuccess, Plans: []framework.ResultPlan{plan},
						ResolveScript: resolve, UseInputUTXO: true, CheckPayload: true,
					},
				}))
				for n, result := range results {
					require.Len(t, result.TxIn, 1)
					wantParent := deploy.TxHash()
					if carry && n == 1 {
						wantParent = results[0].TxHash()
					}
					require.Equal(t, wantParent, result.TxIn[0].PreviousOutPoint.Hash)
					for _, output := range result.TxOut {
						if !carry || n == 1 {
							require.NotEqual(t, txscript.ContractTy, txscript.GetScriptClass(output.PkScript))
						}
					}
				}
				second := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: first.BlockHash()},
					Transactions: append([]*wire.MsgTx{wire.NewMsgTx(2)}, results...)}
				require.NoError(t, b.SyncBlock(second, 2, 2, false))
				idx.UpdateDB()
				idx = contractindex.NewIndexer(b.db, b.chaincfgParam)
				history, total := idx.GetContractHistory(addr.MustEncode(), 0, 0)
				require.Equal(t, 3, total, "cold-loaded inputs must preserve both physical Results")
				byTx := make(map[string]contract.ContractHistoryRecord)
				for _, record := range history {
					byTx[record.TxID] = record
				}
				for _, result := range results {
					record, found := byTx[result.TxID()]
					require.True(t, found)
					require.Equal(t, "result", record.Kind)
					require.Equal(t, contract.ContractTypeTemplate, record.ContractTypeID)
					require.NotContains(t, record.Details, "result_for_txid")
				}
				require.NotNil(t, processed)
				for _, tx := range processed.Transactions[1:] {
					for _, input := range tx.Inputs {
						require.Equal(t, script, input.Address.PkScript, "SyncBlock must resolve scripts without test injection")
					}
				}
				b.CommitBackup(b.Clone(true))
				for _, point := range plan.Inputs {
					_, err := b.GetUtxoInfo(point.String())
					require.Error(t, err, "old funding must actually be spent and deleted from DB")
				}
				require.Equal(t, second.BlockHash().String(), b.lastHash)
				require.Equal(t, 2, b.GetHeight())
				require.True(t, idx.CheckSelf())
			})
		}
	}
}
