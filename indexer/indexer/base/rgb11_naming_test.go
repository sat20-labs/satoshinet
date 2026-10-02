package base

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/indexer/rgb11names"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type namingSourceFunc func(*common.Block) ([]rgb11names.Event, error)
func (f namingSourceFunc) RGB11NamingEvents(b *common.Block) ([]rgb11names.Event, error) { return f(b) }

func TestRGB11NamingBaseBlockFlushAndRestart(t *testing.T) {
	db := indexerdb.NewKVDB(t.TempDir())
	defer db.Close()
	params := &chaincfg.TestNetParams
	b := NewBaseIndexer(db, params, 0, 30)
	b.Init()
	b.SetBlockCallback(func(*common.Block) {})
	a, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{1}, 20), params)
	if err != nil { t.Fatal(err) }
	script, err := txscript.PayToAddrScript(a)
	if err != nil { t.Fatal(err) }
	id := func(n int) string { return fmt.Sprintf("%064x", n) }
	unavailable := true
	source := namingSourceFunc(func(block *common.Block) ([]rgb11names.Event, error) {
		if block.Height != 1 { return nil, nil }
		if unavailable { return nil, errors.New("validated L1 effects temporarily unavailable") }
		txid := block.Transactions[1].Txid
		return []rgb11names.Event{
			{TxIndex: 1, EventIndex: 0, TxID: txid, Ownership: &rgb11names.Ownership{DID: "alice", Sat: 42, Address: a.EncodeAddress(), Revision: 1, L1Height: 900000, L1Hash: id(77)}},
			{TxIndex: 1, EventIndex: 1, TxID: txid, Bind: &rgb11names.Bind{DID: "alice", Address: a.EncodeAddress()}},
			{TxIndex: 1, EventIndex: 2, TxID: txid, Register: &rgb11names.Register{ContractID: id(1), BaseTicker: "USD", AssetType: "f", GenesisOutpoint: id(99)+":0", GenesisAddress: a.EncodeAddress(), AuthorizedBy: a.EncodeAddress()}},
		}, nil
	})
	if err := b.ConfigureRGB11NamingSource(source); err != nil { t.Fatal(err) }
	genesis := params.GenesisBlock
	if err := b.SyncBlock(genesis, 0, 0, false); err != nil { t.Fatal(err) }
	coinbase := wire.NewMsgTx(1)
	coinbase.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 0xffffffff}, []byte{1, 1}, nil))
	coinbase.AddTxOut(wire.NewTxOut(0, nil, script))
	spend := wire.NewMsgTx(1)
	spend.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: genesis.Transactions[0].TxHash(), Index: 0}, nil, nil))
	previous := genesis.Transactions[0].TxOut[0]
	spend.AddTxOut(wire.NewTxOut(previous.Value, previous.Assets.Clone(), script))
	block := &wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: genesis.BlockHash(), Nonce: 1}, Transactions: []*wire.MsgTx{coinbase, spend}}
	// This exercises the indexer (which consumes validated blocks), not chain
	// signature validation or an STP deposit; the naming source is a fixture.
	if err := b.SyncBlock(block, 1, 1, false); err == nil { t.Fatal("source failure was swallowed") }
	if height, _ := b.GetInternalTip(); height != 0 { t.Fatalf("base advanced on naming failure: %d", height) }
	if result, err := b.GetRGB11Naming(rgb11names.Query{Kind: "status"}); err != nil || result.Cursor.Height != 0 { t.Fatalf("naming cursor advanced: %+v %v", result, err) }
	unavailable = false
	if err := b.SyncBlock(block, 1, 1, false); err != nil { t.Fatal(err) }
	view := NewRpcIndexer(b)
	result, err := view.GetRGB11Naming(rgb11names.Query{Kind: "contract", Value: id(1)})
	if err != nil || result.Registration.AssetName != "rgb11:f:usd@alice" || result.Cursor.Hash != block.BlockHash().String() { t.Fatalf("RPC snapshot: %+v %v", result, err) }
	backup := b.Clone(true)
	b.Subtract(backup)
	backup.UpdateDB()
	b.SetSyncBase(backup.GetSyncBase())
	restored := NewBaseIndexer(db, params, 0, 30)
	restored.Init()
	registration, err := restored.GetRGB11Naming(rgb11names.Query{Kind: "contract", Value: id(1)})
	if err != nil || *registration.Registration != *result.Registration { t.Fatalf("registry lost on base restart: %+v %v", registration, err) }
	if height, hash := restored.GetInternalTip(); height != 1 || hash != block.BlockHash().String() { t.Fatalf("base checkpoint %d/%s", height, hash) }
	if err := b.ConfigureRGB11NamingSource(nil); err == nil { t.Fatal("live source replacement allowed") }
	// A restart without the validating adapter can query historical mappings,
	// but cannot silently advance them using stale ownership observations.
	if err := restored.applyRGB11NamingBlockLocked(&common.Block{}); !errors.Is(err, rgb11names.ErrUnavailable) { t.Fatalf("missing source accepted: %v", err) }
}
