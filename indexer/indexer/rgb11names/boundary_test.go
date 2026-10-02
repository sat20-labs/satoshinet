package rgb11names

import (
	"errors"
	"testing"
)

func TestNormalizedTickersAndAssetTypesShareOneOrdinalNamespace(t *testing.T) {
	s, _ := newIndex(t)
	nft := register(2, "alice", "usd-t")
	nft.Register.AssetType = "n"
	apply(t, s, 0, register(1, "alice", "USD T"), nft, register(3, "alice", "USD--T"))
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

func TestOptionalInvalidRegistrationDoesNotRejectBlockOrConsumeOrdinal(t *testing.T) {
	s, _ := newIndex(t)
	bad := register(1, "alice", "USD_2")
	bad.Optional = true
	bk, events := block(0, "", bad)
	if err := s.ApplyBlock(bk, events); err != nil {
		t.Fatalf("optional naming failure rejected block: %v", err)
	}
	if s.tip.Height != 0 {
		t.Fatalf("cursor not advanced: %+v", s.tip)
	}
	if _, err := s.Lookup(Query{Kind: "contract", Value: id(1)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid optional registration became visible: %v", err)
	}
	apply(t, s, 1, register(2, "alice", "USD"))
	if got := lookup(t, s, 2); got.Ordinal != 1 {
		t.Fatalf("optional failure consumed ordinal: %+v", got)
	}
}

func TestEmptyBlockReplayIsIdempotent(t *testing.T) {
	s, _ := newIndex(t)
	bk, _ := block(0, "")
	if err := s.ApplyBlock(bk, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyBlock(bk, []Event{}); err != nil {
		t.Fatalf("nil and empty effects replay differently: %v", err)
	}
	if s.HasEffects() {
		t.Fatal("empty block unexpectedly created naming state")
	}
}
