package indexer

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/stretchr/testify/require"
)

// A recovered panic in an HTTP handler must still terminate the node.
func TestIndexerReorgCannotBeRecovered(t *testing.T) {
	const childFlag = "SATOSHINET_TEST_REORG_FATAL"
	if os.Getenv(childFlag) == "1" {
		func() {
			defer func() { _ = recover() }()
			(&IndexerMgr{}).handleReorg(0)
		}()
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIndexerReorgCannotBeRecovered$")
	child.Env = append(os.Environ(), childFlag+"=1")
	output, err := child.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, string(output))
	require.Equal(t, 1, exit.ExitCode(), string(output))
}

// Reorg is an operator-handled fault. It must stop before closing databases
// still used by P2P, RPC or the DKVS maintenance timer.
func TestIndexerReorgStopsBeforeClosingDKVS(t *testing.T) {
	for _, params := range []*chaincfg.Params{&chaincfg.MainNetParams, &chaincfg.TestNetParams} {
		t.Run(params.Name, func(t *testing.T) {
			// Intercept fatal exit only to inspect the still-open databases.
			// The subprocess test above verifies the production exit behavior.
			logger := common.Log.Logger
			originalExit := logger.ExitFunc
			logger.ExitFunc = func(code int) { panic(code) }
			defer func() { logger.ExitFunc = originalExit }()
			mgr := &IndexerMgr{cfg: &Config{}, dbDir: t.TempDir() + "/", chaincfgParam: params,
				periodFlushToDB: 30}
			mgr.Init()
			t.Cleanup(func() {
				mgr.Stop()
				_ = mgr.baseDB.Close()
				_ = mgr.localDB.Close()
				_ = mgr.dkvsDB.Close()
			})
			originalDKVS, originalBase := mgr.dkvsIndexer, mgr.compiling
			baseDB, localDB, dkvsDB := mgr.baseDB, mgr.localDB, mgr.dkvsDB
			marker := []byte("reorg-database-marker")
			for _, database := range []indexercommon.KVDB{baseDB, localDB, dkvsDB} {
				require.NoError(t, database.Write(marker, []byte("intact")))
			}

			var stopped any
			func() {
				defer func() { stopped = recover() }()
				mgr.DisconnectBlock(0)
			}()
			require.Equal(t, 1, stopped, "unexpected reorg result: %v", stopped)
			require.Same(t, originalDKVS, mgr.dkvsIndexer)
			require.Same(t, originalBase, mgr.compiling)
			for name, database := range map[string]indexercommon.KVDB{
				"base": baseDB, "local": localDB, "dkvs": dkvsDB,
			} {
				value, err := database.Read(marker)
				require.NoError(t, err, name)
				require.Equal(t, []byte("intact"), value, name)
			}
			// Exercise the manager's P2P sync surface and the pre-reorg object,
			// rather than treating an unchanged pointer as proof the DB is open.
			_, _, _, _, err := mgr.SyncFilteredDKVSRecords(nil, 10, nil)
			require.NoError(t, err)
			_, _, _, _, err = originalDKVS.Sync(nil, 10)
			require.NoError(t, err)
			// The test's intercepted exit also unwinds the existing lock.
			require.True(t, mgr.connectMutex.TryLock(), "reorg retained connectMutex")
			mgr.connectMutex.Unlock()
		})
	}
}
