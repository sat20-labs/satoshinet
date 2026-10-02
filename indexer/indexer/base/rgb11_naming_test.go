package base

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

func rgb11TestID(n int) string { return fmt.Sprintf("%064x", n) }

func rgb11Sign(priv *secp256k1.PrivateKey, msg []byte) []byte {
	return ecdsa.Sign(priv, chainhash.HashB(msg)).Serialize()
}

func rgb11TestCoinbase(script []byte) *wire.MsgTx {
	tx := wire.NewMsgTx(1)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 0xffffffff}, []byte{1, 1}, nil))
	tx.AddTxOut(wire.NewTxOut(0, nil, script))
	return tx
}

func rgb11SignedTranscendDeploy(t *testing.T, channelScript []byte, a, b *secp256k1.PrivateKey,
	contractID, genesisOutpoint, provider string, validSignatures bool) *wire.MsgTx {

	t.Helper()
	baseContent, err := txscript.NewScriptBuilder().
		AddData([]byte(rgb11names.TranscendTemplateName)).
		AddData([]byte("rgb11:f:" + contractID)).
		AddInt64(0).AddInt64(0).Script()
	if err != nil {
		t.Fatal(err)
	}
	suffix, err := rgb11names.EncodeTranscendRegistrationSuffix("USDT", genesisOutpoint, provider)
	if err != nil {
		t.Fatal(err)
	}
	contractContent := append(baseContent, suffix...)
	contractPath := "rgb11:f:" + contractID + "_" + rgb11names.TranscendTemplateName
	deployTime := int64(1234567)
	unsigned, err := txscript.NewScriptBuilder().
		AddData([]byte(contractPath)).
		AddData(contractContent).
		AddInt64(deployTime).
		Script()
	if err != nil {
		t.Fatal(err)
	}
	sigA := rgb11Sign(a, unsigned)
	sigB := rgb11Sign(b, unsigned)
	if !validSignatures {
		sigB = rgb11Sign(a, []byte("wrong deploy invoice"))
	}
	signed, err := txscript.NewScriptBuilder().
		AddData([]byte(contractPath)).
		AddData(contractContent).
		AddInt64(deployTime).
		AddData(sigA).
		AddData(sigB).
		Script()
	if err != nil {
		t.Fatal(err)
	}
	deployScript, err := common.NullDataScript(common.CONTENT_TYPE_DEPLOYCONTRACT, signed)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxOut(wire.NewTxOut(10, nil, channelScript))
	tx.AddTxOut(wire.NewTxOut(0, nil, deployScript))
	return tx
}

func TestRGB11NamingBaseTranscendAutoRegisterFlushAndRestart(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()

	params := &chaincfg.TestNetParams
	b := NewBaseIndexer(db, params, 0, 30)
	b.Init()
	b.SetBlockCallback(func(*common.Block) {})

	channelAddr, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{1}, 20), params)
	if err != nil {
		t.Fatal(err)
	}
	channelScript, err := txscript.PayToAddrScript(channelAddr)
	if err != nil {
		t.Fatal(err)
	}
	channelA, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	channelB, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	b.channelMap[channelAddr.EncodeAddress()] = &common.ChannelInfo{ChannelInfoInDB: common.ChannelInfoInDB{
		Address: channelAddr.EncodeAddress(),
		PubA:    channelA.PubKey().SerializeCompressed(),
		PubB:    channelB.PubKey().SerializeCompressed(),
	}}

	genesis := params.GenesisBlock
	if err := b.SyncBlock(genesis, 0, 0, false); err != nil {
		t.Fatal(err)
	}

	contractID := rgb11TestID(70)
	genesisOutpoint := rgb11TestID(71) + ":0"
	deployTx := rgb11SignedTranscendDeploy(
		t, channelScript, channelA, channelB, contractID, genesisOutpoint, "alice", true,
	)
	block := &wire.MsgBlock{
		Header:       wire.BlockHeader{PrevBlock: genesis.BlockHash(), Nonce: 102},
		Transactions: []*wire.MsgTx{rgb11TestCoinbase(channelScript), deployTx},
	}
	if err := b.SyncBlock(block, 1, 1, false); err != nil {
		t.Fatal(err)
	}
	result, err := b.GetRGB11Naming(rgb11names.Query{Kind: "contract", Value: contractID})
	if err != nil || result.Registration == nil ||
		result.Registration.AssetName != "rgb11:f:usdt@alice" ||
		result.Registration.ProviderDID != "alice" ||
		result.Registration.GenesisOutpoint != genesisOutpoint {
		t.Fatalf("auto registration: %+v %v", result, err)
	}

	backup := b.Clone(true)
	b.Subtract(backup)
	backup.UpdateDB()
	b.SetSyncBase(backup.GetSyncBase())

	restored := NewBaseIndexer(db, params, 0, 30)
	restored.Init()
	registration, err := restored.GetRGB11Naming(rgb11names.Query{Kind: "contract", Value: contractID})
	if err != nil || registration.Registration == nil ||
		registration.Registration.AssetName != "rgb11:f:usdt@alice" {
		t.Fatalf("registry lost on restart: %+v %v", registration, err)
	}
	if height, hash := restored.GetInternalTip(); height != 1 || hash != block.BlockHash().String() {
		t.Fatalf("base checkpoint %d/%s", height, hash)
	}
}

func TestRGB11NamingIgnoresUnauthenticatedTranscendCandidate(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()

	params := &chaincfg.TestNetParams
	b := NewBaseIndexer(db, params, 0, 30)
	b.Init()
	b.SetBlockCallback(func(*common.Block) {})

	channelAddr, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{2}, 20), params)
	if err != nil {
		t.Fatal(err)
	}
	channelScript, err := txscript.PayToAddrScript(channelAddr)
	if err != nil {
		t.Fatal(err)
	}
	channelA, _ := secp256k1.GeneratePrivateKey()
	channelB, _ := secp256k1.GeneratePrivateKey()
	b.channelMap[channelAddr.EncodeAddress()] = &common.ChannelInfo{ChannelInfoInDB: common.ChannelInfoInDB{
		Address: channelAddr.EncodeAddress(),
		PubA:    channelA.PubKey().SerializeCompressed(),
		PubB:    channelB.PubKey().SerializeCompressed(),
	}}

	genesis := params.GenesisBlock
	if err := b.SyncBlock(genesis, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	contractID := rgb11TestID(80)
	deployTx := rgb11SignedTranscendDeploy(
		t, channelScript, channelA, channelB, contractID, rgb11TestID(81)+":0", "alice", false,
	)
	block := &wire.MsgBlock{
		Header:       wire.BlockHeader{PrevBlock: genesis.BlockHash(), Nonce: 103},
		Transactions: []*wire.MsgTx{rgb11TestCoinbase(channelScript), deployTx},
	}
	if err := b.SyncBlock(block, 1, 1, false); err != nil {
		t.Fatalf("invalid naming candidate stalled base index: %v", err)
	}
	if _, err := b.GetRGB11Naming(rgb11names.Query{Kind: "contract", Value: contractID}); err != rgb11names.ErrNotFound {
		t.Fatalf("unauthenticated candidate registered: %v", err)
	}
}
