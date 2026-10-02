package rgb11names

import (
	"errors"
	"testing"
)

func TestNormalizedTickersAndAssetTypesShareOneOrdinalNamespace(t *testing.T) {
	s, _ := newIndex(t)
	a := address(t, 1)
	nft := register(2, a, "usd-t")
	nft.Register.AssetType = "n"
	apply(t, s, 0, own("alice", a, 1), bind("alice", a), register(1, a, "USD T"), nft, register(3, a, "USD--T"))
	if got := lookup(t, s, 1); got.AssetName != "rgb11:f:usd-t@alice" {
		t.Fatalf("first name=%s", got.AssetName)
	}
	if got := lookup(t, s, 2); got.AssetName != "rgb11:n:usd-t_2@alice" {
		t.Fatalf("asset type changed or counter split: %+v", got)
	}
	if got := lookup(t, s, 3); got.AssetName != "rgb11:f:usd-t_3@alice" {
		t.Fatalf("normalization collision not resolved: %+v", got)
	}
}

func TestBurnedDIDCannotAuthorizeNewNames(t *testing.T) {
	s, _ := newIndex(t)
	a := address(t, 1)
	apply(t, s, 0, own("alice", a, 1), bind("alice", a), register(1, a, "USD"))
	first := lookup(t, s, 1)
	apply(t, s, 1, own("alice", "", 2))
	if _, err := s.Lookup(Query{Kind: "primary", Value: a}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("burned DID has active bind: %v", err)
	}
	bk, events := block(2, s.tip.Hash, register(2, a, "USD"))
	if err := s.ApplyBlock(bk, events); !errors.Is(err, ErrNotFound) {
		t.Fatalf("burned DID authorized a new name: %v", err)
	}
	if lookup(t, s, 1) != first {
		t.Fatal("burn changed historical registration")
	}
	// An already registered asset still resolves without an active issuer DID.
	apply(t, s, 2, register(1, a, "USD"))
	counter, err := s.Lookup(Query{Kind: "counter", Provider: "alice", Ticker: "USD"})
	if err != nil || counter.Counter.MaxOrdinal != 1 {
		t.Fatalf("duplicate registration consumed ordinal: %+v %v", counter, err)
	}
}

func TestMissingOrUnownedBindCannotRegister(t *testing.T) {
	s, _ := newIndex(t)
	a, b := address(t, 1), address(t, 2)
	bk, events := block(0, "", own("alice", a, 1), bind("alice", b))
	if err := s.ApplyBlock(bk, events); !errors.Is(err, ErrOwner) {
		t.Fatalf("another address bound the DID: %v", err)
	}
	bk, events = block(0, "", own("alice", a, 1), register(1, a, "USD"))
	if err := s.ApplyBlock(bk, events); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbound address registered a name: %v", err)
	}
	if s.tip.Height != -1 || s.HasEffects() {
		t.Fatal("rejected authorization changed state")
	}
}

func TestEmptyEffectReplayAndDuplicateBindAreIdempotent(t *testing.T) {
	s, _ := newIndex(t)
	bk, _ := block(0, "")
	if err := s.ApplyBlock(bk, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyBlock(bk, []Event{}); err != nil {
		t.Fatalf("nil and empty effects replay differently: %v", err)
	}
	if s.HasEffects() {
		t.Fatal("empty block enabled naming ingestion")
	}
	a := address(t, 1)
	apply(t, s, 1, own("alice", a, 1), bind("alice", a))
	before, err := s.Lookup(Query{Kind: "primary", Value: a})
	if err != nil {
		t.Fatal(err)
	}
	apply(t, s, 2, bind("alice", a))
	after, err := s.Lookup(Query{Kind: "primary", Value: a})
	if err != nil || *after.Binding != *before.Binding {
		t.Fatalf("duplicate bind rewrote binding snapshot: %+v %v", after, err)
	}
}
