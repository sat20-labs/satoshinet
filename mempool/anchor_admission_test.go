package mempool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	idx "github.com/sat20-labs/indexer/common"
	idxwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/btcutil/hdkeychain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

func TestMempoolAnchorAdmission(t *testing.T) {
	old := idx.ENABLE_TESTING
	idx.ENABLE_TESTING = true
	t.Cleanup(func() { idx.ENABLE_TESTING = old })
	master, err := hdkeychain.NewMaster(bip39.NewSeed("acquire pet news congress unveil erode paddle crumble blue fish match eye", ""), &chaincfg.TestNetParams)
	require.NoError(t, err)
	for _, child := range []uint32{hdkeychain.HardenedKeyStart + 86, hdkeychain.HardenedKeyStart, hdkeychain.HardenedKeyStart, 0, 0} {
		master, err = master.Derive(child)
		require.NoError(t, err)
	}
	core, err := master.ECPrivKey()
	require.NoError(t, err)
	peer, _ := btcec.PrivKeyFromBytes([]byte{2})
	witness, script, err := anchortx.GetP2WSHscript(core.PubKey().SerializeCompressed(), peer.PubKey().SerializeCompressed())
	require.NoError(t, err)
	funding := strings.Repeat("12", 32) + ":0"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := json.NewEncoder(w).Encode(idxwire.TxOutputRespV3{BaseResp: idxwire.BaseResp{Code: 0}, Data: &idx.AssetsInUtxo{OutPoint: funding, Value: 21000, PkScript: script}})
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{ChainParams: &chaincfg.TestNetParams, IndexerScheme: "http", IndexerHost: strings.TrimPrefix(server.URL, "http://"), IndexerProxy: "testnet"}))
	t.Cleanup(anchortx.Stop)
	invoice, err := anchortx.StandardAnchorScript(funding, witness, 21000, nil)
	require.NoError(t, err)
	assets, err := wire.SerializeTxAssets(&wire.TxAssets{})
	require.NoError(t, err)
	signed, err := txscript.NewScriptBuilder().AddData([]byte(funding)).AddData(witness).AddInt64(21000).AddData(assets).AddData(ecdsa.Sign(core, chainhash.HashB(invoice)).Serialize()).Script()
	require.NoError(t, err)
	original := wire.NewMsgTx(2)
	original.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.AnchorTxOutIndex}, signed, nil))
	original.AddTxOut(wire.NewTxOut(21000, nil, script))
	metadata, err := common.NullDataScript(common.CONTENT_TYPE_ASCENDING, []byte("::-2100000000000000-0-1"))
	require.NoError(t, err)
	original.AddTxOut(wire.NewTxOut(0, nil, metadata))
	recipient, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(peer.PubKey().SerializeCompressed()), &chaincfg.RegressionNetParams)
	require.NoError(t, err)
	recipientScript, err := txscript.PayToAddrScript(recipient)
	require.NoError(t, err)
	for _, mode := range []string{"valid", "deposit", "descending", "referrer", "precision", "binding-overflow", "activated-valid", "activated-deposit", "activated-tampered", "activated-legacy", "activated-locktime", "activated-future-locktime", "activated-trailing"} {
		t.Run(mode, func(t *testing.T) {
			params := chaincfg.RegressionNetParams
			if strings.HasPrefix(mode, "activated-") {
				params.POSV2Height = 1
			}
			harness, _, err := newPoolHarness(&params)
			require.NoError(t, err)
			harness.txPool.cfg.FetchAnchorTx = func(string) (*blockchain.AnchorTxInfo, error) { return nil, nil }
			tx := original.Copy()
			switch mode {
			case "deposit", "activated-deposit":
				tx.TxOut[0].PkScript = recipientScript
			case "descending", "referrer":
				operation := uint8(common.CONTENT_TYPE_DESCENDING)
				if mode == "referrer" {
					operation = common.CONTENT_TYPE_BINDREFERRER
				}
				script, err := common.NullDataScript(operation, []byte("payload"))
				require.NoError(t, err)
				tx.AddTxOut(wire.NewTxOut(0, nil, script))
			case "precision", "binding-overflow":
				payload := "::-2100000000000000-1-1"
				if mode == "binding-overflow" {
					payload = "::-2100000000000000-0-4294967297"
				}
				tx.TxOut[1].PkScript, err = common.NullDataScript(common.CONTENT_TYPE_ASCENDING, []byte(payload))
				require.NoError(t, err)
			}
			if strings.HasPrefix(mode, "activated-") && mode != "activated-legacy" {
				tx.Version = wire.TxVersion
				tx.TxIn[0].Sequence = wire.AnchorTxOutIndex
				invoice, err := common.AnchorInvoice(tx, true)
				require.NoError(t, err)
				tx.TxIn[0].SignatureScript, err = common.StandardAnchorScriptWithSig(funding, witness, 21000, nil, ecdsa.Sign(core, chainhash.HashB(invoice)).Serialize())
				require.NoError(t, err)
				if mode == "activated-tampered" {
					tx.TxOut[0].PkScript = recipientScript
				}
			}
			canonical := tx.Copy()
			switch mode {
			case "activated-locktime":
				tx.LockTime = 1
			case "activated-future-locktime":
				tx.LockTime = 500
			case "activated-trailing":
				tx.TxIn[0].SignatureScript = append(tx.TxIn[0].SignatureScript, txscript.OP_0)
			}
			_, err = harness.txPool.CheckMempoolAcceptance(btcutil.NewTx(tx))
			if mode == "valid" || mode == "deposit" || mode == "activated-valid" || mode == "activated-deposit" {
				require.NoError(t, err)
				if mode == "activated-valid" {
					accepted, err := harness.txPool.ProcessTransaction(btcutil.NewTx(tx), false, false, 0)
					require.NoError(t, err)
					require.Len(t, accepted, 1)
					require.Len(t, harness.txPool.anchorOutpoints, 1)
					harness.txPool.RemoveTransaction(btcutil.NewTx(tx), false)
					require.Empty(t, harness.txPool.anchorOutpoints)
				}
			} else {
				require.ErrorContains(t, err, "The anchor tx is invalid")
				if mode == "activated-locktime" || mode == "activated-future-locktime" || mode == "activated-trailing" {
					_, err = harness.txPool.ProcessTransaction(btcutil.NewTx(tx), false, false, 0)
					require.Error(t, err)
					require.Empty(t, harness.txPool.anchorOutpoints, "a rejected variant must not reserve funding")
					accepted, err := harness.txPool.ProcessTransaction(btcutil.NewTx(canonical), false, false, 0)
					require.NoError(t, err)
					require.Len(t, accepted, 1, "the original presigned outpoint remains usable")
				}

			}
		})
	}
}
