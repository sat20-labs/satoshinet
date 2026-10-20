package indexer

import (
	idxcommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"path/filepath"
	"testing"
)

func TestRegressionIndexerUsesSelectedNetwork(t *testing.T) {
	previousInstance, previousChain := instance, idxcommon.CHAIN
	instance = nil
	t.Cleanup(func() { instance, idxcommon.CHAIN = previousInstance, previousChain })
	dir := t.TempDir()
	mgr := NewIndexerMgr(&Config{DataPath: dir}, &chaincfg.RegressionNetParams, nil)
	if mgr.GetChainParam() != &chaincfg.RegressionNetParams || idxcommon.CHAIN != "regtest" || filepath.Clean(mgr.dbDir) != filepath.Join(dir, "db/indexer/regtest") {
		t.Fatalf("indexer lost selected network: %s %s %s", mgr.GetChainParam().Name, idxcommon.CHAIN, mgr.dbDir)
	}
	mgr.Init()
	defer mgr.Close()
	if mgr.compiling.GetHeight() != 0 {
		t.Fatalf("genesis initialization height=%d", mgr.compiling.GetHeight())
	}
	if mgr.GetSeqMgr() == nil {
		t.Fatal("regtest mining sequence missing")
	}
}
