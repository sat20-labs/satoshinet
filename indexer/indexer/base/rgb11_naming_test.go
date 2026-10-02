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
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
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

func TestRGB11NamingBaseBindTranscendAutoRegisterFlushAndRestart(t *testing.T) {
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
		Address: channelAddr.EncodeAddress(), PubA: channelA.PubKey().SerializeCompressed(), PubB: channelB.PubKey().SerializeCompressed(),
	}}

	providerKey, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	providerAddress, err := dkvsindexer.P2TRAddressFromPubKeyBytes(providerKey.PubKey().SerializeCompressed(), params)
	if err != nil {
		t.Fatal(err)
	}
	ownerUtxo := rgb11TestID(55) + ":0"
	resolver := dkvsindexer.StaticDIDResolver{Names: map[string]dkvsindexer.DIDIdentity{
		"alice": {
			CanonicalName: "alice", NameID: "alice", Active: true,
			OwnerAddresses: []string{providerAddress}, AddressParams: params,
			OwnerUtxo: ownerUtxo, OwnerSat: 42, InscriptionID: rgb11TestID(56) + "i0",
		},
	}}
	b.SetRGB11DIDResolver(resolver)

	genesis := params.GenesisBlock
	if err := b.SyncBlock(genesis, 0, 0, false); err != nil {
		t.Fatal(err)
	}

	signingPayload, err := rgb11names.PrimaryDIDBindSigningPayload("alice", params)
	if err != nil {
		t.Fatal(err)
	}
	bindPayload, err := rgb11names.EncodePrimaryDIDBindPayload(
		"alice", providerKey.PubKey().SerializeCompressed(), rgb11Sign(providerKey, signingPayload),
	)
	if err != nil {
		t.Fatal(err)
	}
	bindScript, err := common.NullDataScript(common.CONTENT_TYPE_PRIMARYDIDBIND, bindPayload)
	if err != nil {
		t.Fatal(err)
	}
	bindTx := wire.NewMsgTx(1)
	bindTx.AddTxOut(wire.NewTxOut(1, nil, channelScript))
	bindTx.AddTxOut(wire.NewTxOut(0, nil, bindScript))
	block1 := &wire.MsgBlock{
		Header:       wire.BlockHeader{PrevBlock: genesis.BlockHash(), Nonce: 101},
		Transactions: []*wire.MsgTx{rgb11TestCoinbase(channelScript), bindTx},
	}
	if err := b.SyncBlock(block1, 1, 2, false); err != nil {
		t.Fatal(err)
	}
	primary, err := b.GetRGB11Naming(rgb11names.Query{Kind: "primary", Value: providerAddress})
	if err != nil || primary.Binding == nil || primary.Binding.DID != "alice" || primary.Binding.OwnerUtxo != ownerUtxo {
		t.Fatalf("primary bind: %+v %v", primary, err)
	}

	contractID := rgb11TestID(70)
	genesisOutpoint := rgb11TestID(71) + ":0"
	baseContent, err := txscript.NewScriptBuilder().
		AddData([]byte(rgb11names.TranscendTemplateName)).
		AddData([]byte("rgb11:f:" + contractID)).
		AddInt64(0).AddInt64(0).Script()
	if err != nil {
		t.Fatal(err)
	}
	suffix, err := rgb11names.EncodeTranscendRegistrationSuffix("USDT", genesisOutpoint, providerAddress)
	if err != nil {
		t.Fatal(err)
	}
	contractContent := append(baseContent, suffix...)
	contractPath := "rgb11:f:" + contractID + "_" + rgb11names.TranscendTemplateName
	deployTime := int64(1234567)
	unsigned, err := txscript.NewScriptBuilder().
		AddData([]byte(contractPath)).AddData(contractContent).AddInt64(deployTime).Script()
	if err != nil {
		t.Fatal(err)
	}
	signed, err := txscript.NewScriptBuilder().
		AddData([]byte(contractPath)).AddData(contractContent).AddInt64(deployTime).
		AddData(rgb11Sign(channelA, unsigned)).AddData(rgb11Sign(channelB, unsigned)).Script()
	if err != nil {
		t.Fatal(err)
	}
	deployScript, err := common.NullDataScript(common.CONTENT_TYPE_DEPLOYCONTRACT, signed)
	if err != nil {
		t.Fatal(err)
	}
	deployTx := wire.NewMsgTx(1)
	deployTx.AddTxOut(wire.NewTxOut(10, nil, channelScript))
	deployTx.AddTxOut(wire.NewTxOut(0, nil, deployScript))
	block2 := &wire.MsgBlock{
		Header:       wire.BlockHeader{PrevBlock: block1.BlockHash(), Nonce: 102},
		Transactions: []*wire.MsgTx{rgb11TestCoinbase(channelScript), deployTx},
	}
	if err := b.SyncBlock(block2, 2, 2, false); err != nil {
		t.Fatal(err)
	}
	result, err := b.GetRGB11Naming(rgb11names.Query{Kind: "contract", Value: contractID})
	if err != nil || result.Registration == nil || result.Registration.AssetName != "rgb11:f:usdt@alice" ||
		result.Registration.GenesisAddress != providerAddress {
		t.Fatalf("auto registration: %+v %v", result, err)
	}

	backup := b.Clone(true)
	b.Subtract(backup)
	backup.UpdateDB()
	b.SetSyncBase(backup.GetSyncBase())
	restored := NewBaseIndexer(db, params, 0, 30)
	restored.Init()
	registration, err := restored.GetRGB11Naming(rgb11names.Query{Kind: "contract", Value: contractID})
	if err != nil || registration.Registration == nil || registration.Registration.AssetName != "rgb11:f:usdt@alice" {
		t.Fatalf("registry lost on restart: %+v %v", registration, err)
	}
	if height, hash := restored.GetInternalTip(); height != 2 || hash != block2.BlockHash().String() {
		t.Fatalf("base checkpoint %d/%s", height, hash)
	}
}

func TestRGB11PrimaryDIDBindRequiresResolverWithoutAdvancingBase(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	params := &chaincfg.TestNetParams
	b := NewBaseIndexer(db, params, 0, 30)
	b.Init()
	b.SetBlockCallback(func(*common.Block) {})
	addr, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{4}, 20), params)
	if err != nil {
		t.Fatal(err)
	}
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatal(err)
	}
	genesis := params.GenesisBlock
	if err := b.SyncBlock(genesis, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	key, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := rgb11names.PrimaryDIDBindSigningPayload("alice", params)
	encoded, err := rgb11names.EncodePrimaryDIDBindPayload("alice", key.PubKey().SerializeCompressed(), rgb11Sign(key, payload))
	if err != nil {
		t.Fatal(err)
	}
	opret, err := common.NullDataScript(common.CONTENT_TYPE_PRIMARYDIDBIND, encoded)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(1)
	tx.AddTxOut(wire.NewTxOut(0, nil, opret))
	block := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: genesis.BlockHash(), Nonce: 200},
		Transactions: []*wire.MsgTx{rgb11TestCoinbase(script), tx}}
	if err := b.SyncBlock(block, 1, 1, false); err == nil {
		t.Fatal("Primary DID bind unexpectedly succeeded without L1 resolver")
	}
	if height, _ := b.GetInternalTip(); height != 0 {
		t.Fatalf("base advanced on missing resolver: %d", height)
	}
}
