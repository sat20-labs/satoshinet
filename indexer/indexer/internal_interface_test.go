package indexer

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/indexer/base"
)

func TestWaitForInternalTipCancels(t *testing.T) {
	mgr := &IndexerMgr{compiling: base.NewBaseIndexer(nil, &chaincfg.RegressionNetParams, 0, 1)}
	interrupt := make(chan struct{})
	close(interrupt)
	if err := mgr.WaitForInternalTip(1, chaincfg.RegressionNetParams.GenesisHash, interrupt); err == nil {
		t.Fatal("expected canceled internal tip wait")
	}
}
