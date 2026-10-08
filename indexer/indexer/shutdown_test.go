package indexer

import (
	"errors"
	"sync"
	"testing"
	"time"

	idxcommon "github.com/sat20-labs/indexer/common"
	idxdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/stretchr/testify/require"
)

type shutdownDB struct {
	idxcommon.KVDB
	name     string
	events   *[]string
	closeErr error
	gcErr    error
}

func (d *shutdownDB) RunGC() error { *d.events = append(*d.events, "gc:"+d.name); return d.gcErr }
func (d *shutdownDB) Close() error { *d.events = append(*d.events, "close:"+d.name); return d.closeErr }

func TestIndexerCloseWaitsBeforeGCAndClosesEveryDBOnce(t *testing.T) {
	var events []string
	failure := errors.New("close local failed")
	mgr := &IndexerMgr{}
	mgr.baseDB = &shutdownDB{name: "base", events: &events}
	mgr.localDB = &shutdownDB{name: "local", events: &events, closeErr: failure, gcErr: errors.New("GC failed")}
	mgr.dkvsDB = &shutdownDB{name: "dkvs", events: &events}
	rpcStopped := make(chan struct{})
	mgr.SetRPCShutdown(func() error { events = append(events, "rpc-stopped"); close(rpcStopped); return nil })
	mgr.backgroundWG.Add(1)
	mgr.connectMutex.Lock()
	var workerRelease, connectRelease sync.Once
	unblockWorker := func() { workerRelease.Do(mgr.backgroundWG.Done) }
	unblockConnect := func() { connectRelease.Do(mgr.connectMutex.Unlock) }
	defer unblockWorker()
	defer unblockConnect()
	closed := make(chan error, 1)
	go func() { closed <- mgr.Close() }()
	select {
	case <-rpcStopped:
	case <-time.After(2 * time.Second):
		t.Fatal("RPC shutdown was not reached")
	}
	select {
	case err := <-closed:
		t.Fatalf("closed before in-flight work completed: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblockWorker()
	select {
	case err := <-closed:
		t.Fatalf("closed under an active index connection: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	unblockConnect()
	require.ErrorIs(t, <-closed, failure)
	require.Equal(t, []string{"rpc-stopped", "gc:local", "gc:base", "gc:dkvs", "close:base", "close:local", "close:dkvs"}, events)
	require.ErrorIs(t, mgr.Close(), failure)
	require.Len(t, events, 7, "repeated close must neither GC nor close twice")
}

func TestIndexerCloseReleasesRealDatabases(t *testing.T) {
	mgr := &IndexerMgr{cfg: &Config{}, dbDir: t.TempDir() + "/", chaincfgParam: &chaincfg.TestNetParams, periodFlushToDB: 30}
	mgr.Init()
	require.NoError(t, mgr.baseDB.Write([]byte("shutdown-marker"), []byte("base")))
	require.NoError(t, mgr.localDB.Write([]byte("shutdown-marker"), []byte("local")))
	require.NoError(t, mgr.dkvsDB.Write([]byte("shutdown-marker"), []byte("dkvs")))
	mgr.Stop() // Signaling shutdown must not close a DB still used by a caller.
	for _, database := range []idxcommon.KVDB{mgr.baseDB, mgr.localDB, mgr.dkvsDB} {
		_, err := database.Read([]byte("shutdown-marker"))
		require.NoError(t, err)
	}
	require.NoError(t, mgr.Close())
	require.NoError(t, mgr.Close())
	for _, name := range []string{"base", "local", "dkvs"} {
		reopened := idxdb.NewKVDB(mgr.dbDir + name)
		require.NotNil(t, reopened)
		data, err := reopened.Read([]byte("shutdown-marker"))
		require.NoError(t, err)
		require.Equal(t, name, string(data))
		require.NoError(t, reopened.Close())
	}
}

func TestIndexerRPCShutdownFailureKeepsDatabasesOpen(t *testing.T) {
	var events []string
	failure := errors.New("RPC shutdown failed")
	mgr := &IndexerMgr{baseDB: &shutdownDB{name: "base", events: &events}}
	mgr.SetRPCShutdown(func() error { return failure })
	require.ErrorIs(t, mgr.Close(), failure)
	require.Empty(t, events, "failed reader shutdown must not close its database")
}
