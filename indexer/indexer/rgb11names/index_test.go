package rgb11names

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	idx "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

func address(t *testing.T, n byte) string {
	t.Helper()
	a, err := btcutil.NewAddressWitnessPubKeyHash(bytes.Repeat([]byte{n}, 20), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	return a.EncodeAddress()
}

func id(n int) string { return fmt.Sprintf("%064x", n) }

func newIndex(t *testing.T) (*Index, idx.KVDB) {
	t.Helper()
	db := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { db.Close() })
	s, err := Open(db, &chaincfg.TestNetParams, Cursor{Height: -1})
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func block(height int, parent string, effects ...Event) (*common.Block, []Event) {
	b := &common.Block{Height: height, Hash: id(1000 + height), PrevBlockHash: parent,
		Transactions: []*common.Transaction{{Txid: id(2000 + height)}, {Txid: id(3000 + height)}}}
	for i := range effects {
		effects[i].TxIndex, effects[i].EventIndex, effects[i].TxID = 1, uint32(i), b.Transactions[1].Txid
	}
	return b, effects
}

func apply(t *testing.T, s *Index, height int, effects ...Event) {
	t.Helper()
	b, events := block(height, s.tip.Hash, effects...)
	if err := s.ApplyBlock(b, events); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckSelf(); err != nil {
		t.Fatal(err)
	}
}

func own(did, owner string, revision uint64) Event {
	return Event{Ownership: &Ownership{
		DID: did, Address: owner, OwnerUtxo: id(4000+int(revision)) + ":0",
		OwnerSat: 42, InscriptionID: "inscription-" + did,
	}}
}

func bind(did, owner string) Event { return Event{Bind: &Bind{DID: did, Address: owner}} }

func register(n int, owner, ticker string) Event {
	return Event{Register: &Register{ContractID: id(n), BaseTicker: ticker, AssetType: "f", GenesisOutpoint: id(900+n) + ":0", GenesisAddress: owner}}
}

func lookup(t *testing.T, s *Index, n int) Registration {
	t.Helper()
	r, err := s.Lookup(Query{Kind: "contract", Value: id(n)})
	if err != nil {
		t.Fatal(err)
	}
	return *r.Registration
}

func TestRegistryLifecycle(t *testing.T) {
	s, _ := newIndex(t)
	a := address(t, 1)
	apply(t, s, 0, own("alice", a, 1), bind("alice", a), register(1, a, "USDT"), register(2, a, "usdt"))
	first := lookup(t, s, 1)
	if first.AssetName != "rgb11:f:usdt@alice" || first.Ordinal != 1 {
		t.Fatalf("first=%+v", first)
	}
	if second := lookup(t, s, 2); second.AssetName != "rgb11:f:usdt_2@alice" || second.Ordinal != 2 {
		t.Fatalf("second=%+v", second)
	}
	back, err := s.Lookup(Query{Kind: "name", Value: first.AssetName})
	if err != nil || *back.Registration != first {
		t.Fatalf("reverse lookup: %+v %v", back, err)
	}
	apply(t, s, 1, own("company", a, 1), bind("company", a), register(1, a, "USDT"), register(3, a, "USDT"))
	if got := lookup(t, s, 1); got != first {
		t.Fatalf("existing registration changed: %+v", got)
	}
	if got := lookup(t, s, 3); got.AssetName != "rgb11:f:usdt@company" {
		t.Fatalf("new provider: %+v", got)
	}
	counter, err := s.Lookup(Query{Kind: "counter", Provider: "alice", Ticker: "USDT"})
	if err != nil || counter.Counter.MaxOrdinal != 2 {
		t.Fatalf("duplicate consumed ordinal: %+v %v", counter, err)
	}
}

func TestOwnershipTransferRequiresExplicitRebindAndPreservesHistory(t *testing.T) {
	s, _ := newIndex(t)
	a, b := address(t, 1), address(t, 2)
	apply(t, s, 0, own("alice", a, 1), bind("alice", a), register(1, a, "USD"))
	first := lookup(t, s, 1)
	apply(t, s, 1, own("alice", b, 2))
	for _, owner := range []string{a, b} {
		if _, err := s.Lookup(Query{Kind: "primary", Value: owner}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unexpected active bind for %s: %v", owner, err)
		}
	}
	apply(t, s, 2, bind("alice", b), register(2, b, "USD"))
	if got := lookup(t, s, 2); got.Ordinal != 2 {
		t.Fatalf("DID namespace reset after transfer: %+v", got)
	}
	apply(t, s, 3, own("alice", a, 3))
	if _, err := s.Lookup(Query{Kind: "primary", Value: a}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old bind revived after transfer-back: %v", err)
	}
	apply(t, s, 4, bind("alice", a), register(3, a, "USD"))
	if lookup(t, s, 1) != first || lookup(t, s, 3).Ordinal != 3 {
		t.Fatal("history or counter changed")
	}
	// Replaying the same owner-UTXO snapshot must not revoke a bind.
	refresh := *own("alice", a, 3).Ownership
	apply(t, s, 5, Event{Ownership: &refresh})
	if _, err := s.Lookup(Query{Kind: "primary", Value: a}); err != nil {
		t.Fatal(err)
	}
}

func TestBlockFailureDoesNotConsumeNamesOrOrdinals(t *testing.T) {
	s, _ := newIndex(t)
	a, b := address(t, 1), address(t, 2)
	_ = b
	bad := register(2, a, "USD_2")
	bk, events := block(0, "", own("alice", a, 1), bind("alice", a), register(1, a, "USD"), bad)
	if err := s.ApplyBlock(bk, events); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid registration: %v", err)
	}
	if s.tip.Height != -1 || len(s.dirty) != 0 {
		t.Fatal("failed block advanced state")
	}
	if _, err := s.Lookup(Query{Kind: "contract", Value: id(1)}); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial registration survived")
	}
	apply(t, s, 0, own("alice", a, 1), bind("alice", a), register(1, a, "USD"))
	if lookup(t, s, 1).Ordinal != 1 {
		t.Fatal("ordinal was consumed by failure")
	}
}

func TestRegisteredContractCannotChangeGenesisOrTicker(t *testing.T) {
	for _, field := range []string{"ticker", "outpoint", "address", "type"} {
		t.Run(field, func(t *testing.T) {
			s, _ := newIndex(t)
			a := address(t, 1)
			apply(t, s, 0, own("alice", a, 1), bind("alice", a), register(1, a, "USD"))
			bad := register(1, a, "USD")
			switch field {
			case "ticker":
				bad.Register.BaseTicker = "EUR"
			case "outpoint":
				bad.Register.GenesisOutpoint = id(999) + ":1"
			case "address":
				bad.Register.GenesisAddress = address(t, 2)
			case "type":
				bad.Register.AssetType = "n"
			}
			bk, events := block(1, s.tip.Hash, bad)
			if err := s.ApplyBlock(bk, events); !errors.Is(err, ErrConflict) {
				t.Fatalf("accepted conflicting %s: %v", field, err)
			}
		})
	}
}

func TestEventOrderingAndReplay(t *testing.T) {
	s, _ := newIndex(t)
	a := address(t, 1)
	bk, events := block(0, "", own("alice", a, 1), bind("alice", a))
	if err := s.ApplyBlock(bk, events); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyBlock(bk, events); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	if err := s.ApplyBlock(bk, events[:1]); !errors.Is(err, ErrOrder) {
		t.Fatalf("changed replay: %v", err)
	}
	for _, mode := range []string{"order", "txid", "union", "coinbase", "parent", "height"} {
		t.Run(mode, func(t *testing.T) {
			next, list := block(1, s.tip.Hash, register(1, a, "USD"), register(2, a, "USD"))
			switch mode {
			case "order":
				list[1].EventIndex = list[0].EventIndex
			case "txid":
				list[0].TxID = id(555)
			case "union":
				list[0].Bind = &Bind{DID: "alice", Address: a}
			case "coinbase":
				list[0].TxIndex = 0
				list[0].TxID = next.Transactions[0].Txid
			case "parent":
				next.PrevBlockHash = id(999)
			case "height":
				next.Height++
			}
			if err := s.ApplyBlock(next, list); err == nil {
				t.Fatalf("accepted %s", mode)
			}
			if s.tip.Height != 0 {
				t.Fatal("invalid event advanced cursor")
			}
		})
	}
}

func TestOwnerFactsCannotChangeDIDLineageOrRegress(t *testing.T) {
	for _, mode := range []string{"sat", "inscription", "same-utxo-owner", "invalid-utxo"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newIndex(t)
			a := address(t, 1)
			apply(t, s, 0, own("alice", a, 2), bind("alice", a))
			fact := *own("alice", a, 2).Ownership
			switch mode {
			case "sat":
				fact.OwnerSat++
			case "inscription":
				fact.InscriptionID = "different-inscription"
			case "same-utxo-owner":
				fact.Address = address(t, 2)
			case "invalid-utxo":
				fact.OwnerUtxo = "not-an-outpoint"
			}
			bk, events := block(1, s.tip.Hash, Event{Ownership: &fact})
			err := s.ApplyBlock(bk, events)
			if mode == "invalid-utxo" {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted %s: %v", mode, err)
				}
			} else if !errors.Is(err, ErrOwner) {
				t.Fatalf("accepted %s: %v", mode, err)
			}
		})
	}
}

func TestBindTenCharacterBoundaryAndInvalidNames(t *testing.T) {
	for _, valid := range []string{"a", "abcdefghij", strings.Repeat("聪", 10)} {
		if err := ValidateDID(valid); err != nil {
			t.Fatalf("rejected %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "abcdefghijk", strings.Repeat("聪", 11), "Alice", "a@b", "a:b", "a/b", "a\\b", "a b", "a\u200bb", "a\n", string([]byte{0xff})} {
		if err := ValidateDID(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	for _, ticker := range []string{"", "USD_2", "USD_01", "USD_0", "USD@a", "USD:f"} {
		if _, err := BuildAssetName(ticker, "f", "alice", 1); err == nil {
			t.Fatalf("accepted ticker %q", ticker)
		}
	}
	if name, err := BuildAssetName(" USD T!! Coin ", "f", "alice", 2); err != nil || name != "rgb11:f:usd-t-coin_2@alice" {
		t.Fatalf("name=%s err=%v", name, err)
	}
	s, _ := newIndex(t)
	a := address(t, 1)
	apply(t, s, 0, own("abcdefghij", a, 1), bind("abcdefghij", a))
	bk, events := block(1, s.tip.Hash, bind("abcdefghijk", a))
	if err := s.ApplyBlock(bk, events); !errors.Is(err, ErrInvalid) {
		t.Fatalf("11-character bind: %v", err)
	}
}

func TestCollisionAndOrdinalOverflowFailClosed(t *testing.T) {
	for _, mode := range []string{"collision", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newIndex(t)
			a := address(t, 1)
			apply(t, s, 0, own("alice", a, 1), bind("alice", a))
			want := ErrConflict
			if mode == "collision" {
				s.state["name/rgb11:f:usd@alice"] = []byte(`"` + id(99) + `"`)
			} else {
				s.state["counter/alice/usd"] = []byte(fmt.Sprint(uint64(math.MaxUint64)))
				want = ErrOrdinalLimit
			}
			bk, events := block(1, s.tip.Hash, register(1, a, "USD"))
			if err := s.ApplyBlock(bk, events); !errors.Is(err, want) {
				t.Fatalf("%s: %v", mode, err)
			}
			if err := s.CheckSelf(); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("corruption missed: %v", err)
			}
		})
	}
}
