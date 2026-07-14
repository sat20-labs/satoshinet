package anchortx

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil/hdkeychain"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

const testBootstrapMnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"

func TestGetLockedUtxoInfoUsesConfiguredL1Indexer(t *testing.T) {
	const utxo = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff:0"
	pkScript := []byte{0x51}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/testnet/v3/utxo/info/"+utxo, r.URL.Path)

		err := json.NewEncoder(w).Encode(indexerwire.TxOutputRespV3{
			BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
			Data: &indexercommon.AssetsInUtxo{
				OutPoint: utxo,
				Value:    12345,
				PkScript: pkScript,
			},
		})
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	host := strings.TrimPrefix(server.URL, "http://")
	require.True(t, StartAnchorManager(&AnchorConfig{
		IndexerScheme: "http",
		IndexerHost:   host,
		IndexerProxy:  "testnet",
		ChainParams:   &chaincfg.TestNetParams,
	}))
	t.Cleanup(Stop)

	info, err := GetLockedUtxoInfo(utxo)
	require.NoError(t, err)
	require.Equal(t, utxo, info.Utxo)
	require.Equal(t, int64(12345), info.Value)
	require.Equal(t, pkScript, info.pkScript)
	require.Empty(t, info.AssetInfo)
}

func TestCheckAnchorTxValidChecksFakeL1IndexerLockedUTXO(t *testing.T) {
	const utxo = "111122223333444455556666777788889999aaaabbbbccccddddeeeeffff0000:1"

	coreKey, peerKey := testAnchorKeys(t)

	corePub := coreKey.PubKey().SerializeCompressed()
	peerPub := peerKey.PubKey().SerializeCompressed()
	witnessScript, lockedPkScript, err := GetP2WSHscript(corePub, peerPub)
	require.NoError(t, err)

	const value = int64(21000)
	server := fakeL1IndexerServer(t, map[string]*indexercommon.AssetsInUtxo{
		utxo: {
			OutPoint: utxo,
			Value:    value,
			PkScript: lockedPkScript,
		},
	})
	t.Cleanup(server.Close)
	startTestAnchorManager(t, server)

	anchorScript := signedAnchorScript(t, utxo, witnessScript, value, nil, coreKey)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: 0},
		SignatureScript:  anchorScript,
	})
	tx.AddTxOut(wire.NewTxOut(value, nil, []byte{txscript.OP_TRUE}))
	addAscendingTicker(t, tx, nil)

	ascend, err := CheckAnchorTxValid(tx, true)
	require.NoError(t, err)
	require.Equal(t, utxo, ascend.Utxo)
	require.Equal(t, value, ascend.Value)
	require.Equal(t, lockedPkScript, mustWitnessScriptHash(t, ascend.WitnessScript))
	require.Equal(t, corePub, ascend.PubKeyA)
	require.Equal(t, peerPub, ascend.PubKeyB)
}

func TestCheckAnchorTxValidRejectsFakeL1IndexerMismatch(t *testing.T) {
	const utxo = "22223333444455556666777788889999aaaabbbbccccddddeeeeffff00001111:0"

	coreKey, peerKey := testAnchorKeys(t)

	corePub := coreKey.PubKey().SerializeCompressed()
	peerPub := peerKey.PubKey().SerializeCompressed()
	witnessScript, lockedPkScript, err := GetP2WSHscript(corePub, peerPub)
	require.NoError(t, err)

	const value = int64(21000)
	server := fakeL1IndexerServer(t, map[string]*indexercommon.AssetsInUtxo{
		utxo: {
			OutPoint: utxo,
			Value:    value - 1,
			PkScript: lockedPkScript,
		},
	})
	t.Cleanup(server.Close)
	startTestAnchorManager(t, server)

	anchorScript := signedAnchorScript(t, utxo, witnessScript, value, nil, coreKey)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: 0},
		SignatureScript:  anchorScript,
	})
	tx.AddTxOut(wire.NewTxOut(value, nil, []byte{txscript.OP_TRUE}))

	_, err = CheckAnchorTxValid(tx, true)
	require.ErrorContains(t, err, "invalid value")
}

func TestSameFundingUTXOCanProduceDifferentAnchorTxIDs(t *testing.T) {
	const utxo = "3333444455556666777788889999aaaabbbbccccddddeeeeffff000011112222:0"

	coreKey, peerKey := testAnchorKeys(t)

	corePub := coreKey.PubKey().SerializeCompressed()
	peerPub := peerKey.PubKey().SerializeCompressed()
	witnessScript, lockedPkScript, err := GetP2WSHscript(corePub, peerPub)
	require.NoError(t, err)

	const value = int64(21000)
	server := fakeL1IndexerServer(t, map[string]*indexercommon.AssetsInUtxo{
		utxo: {
			OutPoint: utxo,
			Value:    value,
			PkScript: lockedPkScript,
		},
	})
	t.Cleanup(server.Close)
	startTestAnchorManager(t, server)

	anchorScript := signedAnchorScript(t, utxo, witnessScript, value, nil, coreKey)
	tx1 := wire.NewMsgTx(2)
	tx1.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.AnchorTxOutIndex},
		SignatureScript:  anchorScript,
	})
	tx1.AddTxOut(wire.NewTxOut(value, nil, []byte{txscript.OP_TRUE}))
	addAscendingTicker(t, tx1, nil)

	tx2 := wire.NewMsgTx(2)
	tx2.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Hash: chainhash.Hash{}, Index: wire.AnchorTxOutIndex},
		SignatureScript:  anchorScript,
	})
	tx2.AddTxOut(wire.NewTxOut(value, nil, []byte{txscript.OP_2}))
	addAscendingTicker(t, tx2, nil)

	require.NotEqual(t, tx1.TxID(), tx2.TxID())
	ascend1, err := CheckAnchorTxValid(tx1, true)
	require.NoError(t, err)
	ascend2, err := CheckAnchorTxValid(tx2, true)
	require.NoError(t, err)
	require.Equal(t, ascend1.Utxo, ascend2.Utxo)
}

func TestAscendingTickerMatchesAnchor(t *testing.T) {
	assetName := *indexercommon.NewAssetNameFromString("brc20:f:test")
	assets := wire.TxAssets{{
		Name:       assetName,
		Amount:     *indexercommon.NewDecimal(100, 0),
		BindingSat: 0,
	}}

	t.Run("matching asset", func(t *testing.T) {
		tx := wire.NewMsgTx(2)
		addAscendingTicker(t, tx, assets)
		require.NoError(t, checkAscendingTickerInfo(tx, &AscendInfo{
			AnchorInfo: AnchorInfo{TxAssets: assets},
		}))
	})

	t.Run("mismatched asset", func(t *testing.T) {
		tx := wire.NewMsgTx(2)
		addAscendingTicker(t, tx, nil)
		err := checkAscendingTickerInfo(tx, &AscendInfo{
			AnchorInfo: AnchorInfo{TxAssets: assets},
		})
		require.ErrorContains(t, err, "does not match")
	})

	t.Run("missing marker", func(t *testing.T) {
		err := checkAscendingTickerInfo(wire.NewMsgTx(2), &AscendInfo{})
		require.ErrorContains(t, err, "missing ascending ticker")
	})

	t.Run("duplicate marker", func(t *testing.T) {
		tx := wire.NewMsgTx(2)
		addAscendingTicker(t, tx, nil)
		addAscendingTicker(t, tx, nil)
		err := checkAscendingTickerInfo(tx, &AscendInfo{})
		require.ErrorContains(t, err, "duplicate ascending markers")
	})
}

func addAscendingTicker(t *testing.T, tx *wire.MsgTx, assets wire.TxAssets) {
	t.Helper()
	name := indexercommon.ASSET_PLAIN_SAT
	divisibility := 0
	n := uint32(1)
	if len(assets) == 1 {
		name = assets[0].Name
		divisibility = assets[0].Amount.Precision
		n = assets[0].BindingSat
	}
	payload := name.String() + "-21000000000000000-" +
		strconv.Itoa(divisibility) + "-" + strconv.Itoa(int(n))
	script, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_ASCENDING, []byte(payload))
	require.NoError(t, err)
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
}

func fakeL1IndexerServer(t *testing.T, utxos map[string]*indexercommon.AssetsInUtxo) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/testnet/v3/utxo/info/"
		require.True(t, strings.HasPrefix(r.URL.Path, prefix), r.URL.Path)
		utxo := strings.TrimPrefix(r.URL.Path, prefix)
		data, ok := utxos[utxo]
		if !ok {
			err := json.NewEncoder(w).Encode(indexerwire.TxOutputRespV3{
				BaseResp: indexerwire.BaseResp{Code: 404, Msg: "missing utxo"},
			})
			require.NoError(t, err)
			return
		}

		err := json.NewEncoder(w).Encode(indexerwire.TxOutputRespV3{
			BaseResp: indexerwire.BaseResp{Code: 0, Msg: "ok"},
			Data:     data,
		})
		require.NoError(t, err)
	}))
}

func startTestAnchorManager(t *testing.T, server *httptest.Server) {
	t.Helper()
	host := strings.TrimPrefix(server.URL, "http://")
	require.True(t, StartAnchorManager(&AnchorConfig{
		IndexerScheme: "http",
		IndexerHost:   host,
		IndexerProxy:  "testnet",
		ChainParams:   &chaincfg.TestNetParams,
	}))
	t.Cleanup(Stop)
}

func signedAnchorScript(t *testing.T, utxo string, witnessScript []byte, value int64,
	assets wire.TxAssets, key *btcec.PrivateKey) []byte {

	t.Helper()
	invoice, err := StandardAnchorScript(utxo, witnessScript, value, assets)
	require.NoError(t, err)
	digest := chainhash.HashB(invoice)
	sig := ecdsa.Sign(key, digest)

	assetsBuf, err := wire.SerializeTxAssets(&assets)
	require.NoError(t, err)
	script, err := txscript.NewScriptBuilder().
		AddData([]byte(utxo)).
		AddData(witnessScript).
		AddInt64(value).
		AddData(assetsBuf).
		AddData(sig.Serialize()).
		Script()
	require.NoError(t, err)
	return script
}

func mustWitnessScriptHash(t *testing.T, witnessScript []byte) []byte {
	t.Helper()
	pkScript, err := WitnessScriptHash(witnessScript)
	require.NoError(t, err)
	return pkScript
}

func testAnchorKeys(t *testing.T) (*btcec.PrivateKey, *btcec.PrivateKey) {
	t.Helper()
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})
	coreKey := testAnchorKeyFromMnemonic(t, testBootstrapMnemonic, 0)
	require.Equal(t, indexercommon.GetBootstrapPubKey(),
		hex.EncodeToString(coreKey.PubKey().SerializeCompressed()))
	return coreKey, testAnchorKeyFromMnemonic(t, testBootstrapMnemonic, 1)
}

func testAnchorKeyFromMnemonic(t *testing.T, mnemonic string, index uint32) *btcec.PrivateKey {
	t.Helper()
	require.True(t, bip39.IsMnemonicValid(mnemonic))
	seed := bip39.NewSeed(mnemonic, "")
	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.TestNetParams)
	require.NoError(t, err)
	key, err := masterKey.Derive(hdkeychain.HardenedKeyStart + 86)
	require.NoError(t, err)
	key, err = key.Derive(hdkeychain.HardenedKeyStart)
	require.NoError(t, err)
	key, err = key.Derive(hdkeychain.HardenedKeyStart)
	require.NoError(t, err)
	key, err = key.Derive(0)
	require.NoError(t, err)
	key, err = key.Derive(index)
	require.NoError(t, err)
	privKey, err := key.ECPrivKey()
	require.NoError(t, err)
	require.Len(t, privKey.Serialize(), btcec.PrivKeyBytesLen)
	return privKey
}
