package base

import "testing"

func TestGetInternalTipReturnsOneSnapshot(t *testing.T) {
	b := &BaseIndexer{lastHeight: 3447, lastHash: "tip-hash"}
	height, hash := b.GetInternalTip()
	if height != 3447 || hash != "tip-hash" {
		t.Fatalf("unexpected internal tip %d/%q", height, hash)
	}
}
