package base

import (
	"fmt"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	shareindexer "github.com/sat20-labs/satoshinet/indexer/share/indexer"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestAssetAnchorCreditsUnboundSatsAcrossReopen(t *testing.T) {
	for _, existingBTC := range []bool{false, true} {
		for _, binding := range []uint32{0, 1} {
			t.Run(fmt.Sprintf("existing-btc=%v/binding=%d", existingBTC, binding), func(t *testing.T) {
				b, path, _ := membershipIndexer(t)
				b.SetBlockCallback(func(*common.Block) {})
				oldChain := indexer.CHAIN
				indexer.CHAIN = "testnet"
				t.Cleanup(func() { indexer.CHAIN = oldChain })
				require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{IndexerHost: "unused.invalid", IndexerProxy: "testnet", ChainParams: b.chaincfgParam}))
				t.Cleanup(anchortx.Stop)
				previous := shareindexer.ShareIndexer
				shareindexer.ShareIndexer = &anchorCoreLookup{view: b}
				t.Cleanup(func() { shareindexer.ShareIndexer = previous })
				server, _ := btcec.PrivKeyFromBytes([]byte{71})
				client, _ := btcec.PrivKeyFromBytes([]byte{73})
				witness, channelScript, err := anchortx.GetP2WSHscript(server.PubKey().SerializeCompressed(), client.PubKey().SerializeCompressed())
				require.NoError(t, err)
				height := 0
				var prev chainhash.Hash
				sync := func(tx *wire.MsgTx) {
					height++
					require.NoError(t, b.SyncBlock(&wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: prev, Nonce: uint32(height)}, Transactions: []*wire.MsgTx{wire.NewMsgTx(2), tx}}, height, height, false))
					hash, err := chainhash.NewHashFromStr(b.lastHash)
					require.NoError(t, err)
					prev = *hash
				}
				anchor := func(funding string, value int64, assets wire.TxAssets, metadata string) *wire.MsgTx {
					invoice, err := anchortx.StandardAnchorScript(funding, witness, value, assets)
					require.NoError(t, err)
					serialized, err := wire.SerializeTxAssets(&assets)
					require.NoError(t, err)
					signed, err := txscript.NewScriptBuilder().AddData([]byte(funding)).AddData(witness).AddInt64(value).AddData(serialized).AddData(ecdsa.Sign(server, chainhash.HashB(invoice)).Serialize()).Script()
					require.NoError(t, err)
					tx := wire.NewMsgTx(2)
					tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.AnchorTxOutIndex}, signed, nil))
					tx.AddTxOut(wire.NewTxOut(value, assets, channelScript))
					marker, err := common.NullDataScript(common.CONTENT_TYPE_ASCENDING, []byte(metadata))
					require.NoError(t, err)
					tx.AddTxOut(wire.NewTxOut(0, nil, marker))
					bindOutputs := b.chaincfgParam.POSV2Active(int32(height + 1))
					tx.Version = wire.TxVersion
					tx.TxIn[0].Sequence = wire.AnchorTxOutIndex
					invoice, err = common.AnchorInvoice(tx, bindOutputs)
					require.NoError(t, err)
					tx.TxIn[0].SignatureScript, err = common.StandardAnchorScriptWithSig(funding, witness, value, assets, ecdsa.Sign(server, chainhash.HashB(invoice)).Serialize())
					require.NoError(t, err)
					_, err = anchortx.CheckAnchorTxValid(tx, false, bindOutputs)
					require.NoError(t, err)
					return tx
				}
				btcTotal := int64(2000)
				if binding == 1 {
					btcTotal -= 100
				}
				if existingBTC {
					sync(anchor(strings.Repeat("bb", 32)+":0", 100, nil, "::-2100000000000000-0-1"))
					btcTotal += 100
				}
				assets := wire.TxAssets{{Name: *indexer.NewAssetNameFromString("ordx:f:carrier"), Amount: *indexer.NewDefaultDecimal(100), BindingSat: binding}}
				deposit := anchor(strings.Repeat("aa", 32)+":0", 2000, assets, fmt.Sprintf("ordx:f:carrier-1000000-0-%d", binding))
				sync(deposit)
				// A normal double-signed withdrawal retains every asset in channel change.
				withdraw := wire.NewMsgTx(2)
				withdraw.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: deposit.TxHash(), Index: 0}, nil, nil))
				withdraw.AddTxOut(wire.NewTxOut(1000, assets.Clone(), channelScript))
				descending, err := common.NullDataScript(common.CONTENT_TYPE_DESCENDING, []byte(strings.Repeat("cc", 32)))
				require.NoError(t, err)
				withdraw.AddTxOut(wire.NewTxOut(1000, nil, descending))
				channel, err := common.GetChannelAddress(server.PubKey().SerializeCompressed(), client.PubKey().SerializeCompressed(), b.chaincfgParam)
				require.NoError(t, err)
				channelID, err := common.NullDataScript(common.CONTENT_TYPE_CHANNELID, []byte(channel+"-1"))
				require.NoError(t, err)
				withdraw.AddTxOut(wire.NewTxOut(0, nil, channelID))
				hashes := txscript.NewTxSigHashes(withdraw, txscript.NewCannedPrevOutputFetcher(channelScript, 2000, assets))
				stack := wire.TxWitness{nil}
				for _, key := range []*btcec.PrivateKey{server, client} {
					sig, err := txscript.RawTxInWitnessSignature(withdraw, hashes, 0, 2000, assets, witness, txscript.SigHashAll, key)
					require.NoError(t, err)
					stack = append(stack, sig)
				}
				withdraw.TxIn[0].Witness = append(stack, witness)
				engine, err := txscript.NewEngine(channelScript, withdraw, 0, txscript.StandardVerifyFlags, nil, hashes, 2000, assets, txscript.NewCannedPrevOutputFetcher(channelScript, 2000, assets))
				require.NoError(t, err)
				require.NoError(t, engine.Execute(), "withdrawal must carry valid channel signatures")
				require.NotPanics(t, func() { sync(withdraw) })
				require.Equal(t, fmt.Sprint(btcTotal), b.GetTickerInfo(&indexer.ASSET_PLAIN_SAT).TotalAscendAmt.String())
				require.Equal(t, "1000", b.GetTickerInfo(&indexer.ASSET_PLAIN_SAT).TotalDescendAmt.String())
				b.CommitBackup(b.Clone(true))
				require.NoError(t, b.db.Close())
				b.db = nil
				db := indexerdb.NewKVDB(path)
				t.Cleanup(func() { db.Close() })
				reopened := NewBaseIndexer(db, b.chaincfgParam, 0, 100)
				reopened.Init()
				ticker := reopened.GetTickerInfo(&indexer.ASSET_PLAIN_SAT)
				require.Equal(t, fmt.Sprint(btcTotal), ticker.TotalAscendAmt.String())
				require.Equal(t, "1000", ticker.TotalDescendAmt.String())
				require.Equal(t, "100", reopened.GetTickerInfo(&assets[0].Name).TotalAscendAmt.String())
				info, err := reopened.GetUtxoInfo(withdraw.TxID() + ":0")
				require.NoError(t, err)
				require.Equal(t, int64(1000), info.Value)
			})
		}
	}
}
