package rgb11names

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	idx "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

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
	b := &common.Block{
		Height: height, Hash: id(1000 + height), PrevBlockHash: parent,
		Transactions: []*common.Transaction{{Txid: id(2000 + height)}, {Txid: id(3000 + height)}},
	}
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

func register(n int, provider, ticker string) Event {
	return Event{Register: &Register{
		ContractID: id(n), BaseTicker: ticker, AssetType: "f",
		GenesisOutpoint: id(900+n) + ":0", ProviderDID: provider,
	}}
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
	apply(t, s, 0, register(1, "alice", "USDT"), register(2, "alice", "usdt"))
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

	// A different provider owns a separate ticker namespace.
	apply(t, s, 1, register(1, "alice", "USDT"), register(3, "company", "USDT"))
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

func TestBlockFailureDoesNotConsumeNamesOrOrdinals(t *testing.T) {
	s, _ := newIndex(t)
	bad := register(2, "alice", "USD_2")
	bk, events := block(0, "", register(1, "alice", "USD"), bad)
	if err := s.ApplyBlock(bk, events); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid registration: %v", err)
	}
	if s.tip.Height != -1 || len(s.dirty) != 0 {
		t.Fatal("failed block advanced state")
	}
	apply(t, s, 0, register(1, "alice", "USD"))
	if lookup(t, s, 1).Ordinal != 1 {
		t.Fatal("ordinal was consumed by failure")
	}
}

func TestRegisteredContractCannotChangeImmutableFacts(t *testing.T) {
	for _, field := range []string{"ticker", "outpoint", "provider", "type"} {
		t.Run(field, func(t *testing.T) {
			s, _ := newIndex(t)
			apply(t, s, 0, register(1, "alice", "USD"))
			bad := register(1, "alice", "USD")
			switch field {
			case "ticker":
				bad.Register.BaseTicker = "EUR"
			case "outpoint":
				bad.Register.GenesisOutpoint = id(999) + ":1"
			case "provider":
				bad.Register.ProviderDID = "company"
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
	bk, events := block(0, "", register(1, "alice", "USD"))
	if err := s.ApplyBlock(bk, events); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyBlock(bk, events); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	if err := s.ApplyBlock(bk, nil); !errors.Is(err, ErrOrder) {
		t.Fatalf("changed replay: %v", err)
	}
	for _, mode := range []string{"order", "txid", "coinbase", "parent", "height", "missing"} {
		t.Run(mode, func(t *testing.T) {
			next, list := block(1, s.tip.Hash, register(2, "alice", "USD"), register(3, "alice", "USD"))
			switch mode {
			case "order":
				list[1].EventIndex = list[0].EventIndex
			case "txid":
				list[0].TxID = id(555)
			case "coinbase":
				list[0].TxIndex = 0
				list[0].TxID = next.Transactions[0].Txid
			case "parent":
				next.PrevBlockHash = id(999)
			case "height":
				next.Height++
			case "missing":
				list[0].Register = nil
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

func TestDIDAndTickerBoundaries(t *testing.T) {
	for _, valid := range []string{"a", "abcdefghij", strings.Repeat("聪", 10)} {
		if err := ValidateDID(valid); err != nil {
			t.Fatalf("rejected %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "abcdefghijk", strings.Repeat("聪", 11), "Alice", "a@b", "a:b", "a/b", "a\\b", "a b"} {
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
}

func TestCollisionAndOrdinalOverflowFailClosed(t *testing.T) {
	for _, mode := range []string{"collision", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newIndex(t)
			want := ErrConflict
			if mode == "collision" {
				s.state["name/rgb11:f:usd@alice"] = []byte(`"` + id(99) + `"`)
			} else {
				s.state["counter/alice/usd"] = []byte(fmt.Sprint(uint64(math.MaxUint64)))
				want = ErrOrdinalLimit
			}
			bk, events := block(0, "", register(1, "alice", "USD"))
			if err := s.ApplyBlock(bk, events); !errors.Is(err, want) {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}
