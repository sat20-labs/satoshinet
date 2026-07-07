package base

import (
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

func TestPlainSatTickerInfo(t *testing.T) {
	info := newPlainSatTickerInfo()
	if info.AssetName != indexer.ASSET_PLAIN_SAT {
		t.Fatalf("plain ticker asset name = %s, want %s",
			info.AssetName.String(), indexer.ASSET_PLAIN_SAT.String())
	}
	if info.Divisibility != 0 || info.N != 1 {
		t.Fatalf("plain ticker divisibility/n = %d/%d, want 0/1",
			info.Divisibility, info.N)
	}
	if info.MaxSupply == nil || info.MaxSupply.String() != plainSatMaxSupply {
		t.Fatalf("plain ticker max supply = %v, want %s", info.MaxSupply, plainSatMaxSupply)
	}
	if info.TotalAscendAmt == nil || info.TotalDescendAmt == nil {
		t.Fatalf("plain ticker ascend/descend totals must be initialized")
	}
}

func TestRpcIndexerGetPlainTickerInfo(t *testing.T) {
	rpc := &RpcIndexer{
		BaseIndexer: BaseIndexer{
			tickInfoMap: make(map[string]*common.TickerInfo),
		},
	}

	tests := []struct {
		name   string
		ticker *indexer.AssetName
	}{
		{name: "nil", ticker: nil},
		{name: "all sat", ticker: &indexer.ASSET_ALL_SAT},
		{name: "plain sat", ticker: &indexer.ASSET_PLAIN_SAT},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := rpc.GetTickerInfo(test.ticker)
			if info == nil {
				t.Fatalf("GetTickerInfo returned nil")
			}
			if info.AssetName != indexer.ASSET_PLAIN_SAT {
				t.Fatalf("asset name = %s, want %s",
					info.AssetName.String(), indexer.ASSET_PLAIN_SAT.String())
			}
		})
	}
}
