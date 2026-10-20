package dkvs

import (
	"bytes"
	"context"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
)

type refreshReadProbeDB struct {
	indexercommon.KVDB
	prefix []byte
	scans  int
	reads  int
	cancel context.CancelFunc
}

func (db *refreshReadProbeDB) Read(key []byte) ([]byte, error) {
	if bytes.HasPrefix(key, db.prefix) {
		db.reads++
	}
	return db.KVDB.Read(key)
}

func (db *refreshReadProbeDB) BatchReadV2(prefix, seek []byte, reverse bool, visit func([]byte, []byte) error) error {
	if !bytes.Equal(prefix, db.prefix) {
		return db.KVDB.BatchReadV2(prefix, seek, reverse, visit)
	}
	db.scans++
	return db.KVDB.BatchReadV2(prefix, seek, reverse, func(k, v []byte) error {
		if db.cancel != nil {
			db.cancel()
			db.cancel = nil
		}
		return visit(k, v)
	})
}

func TestPaidRefreshExactKeyDoesNotScanDirectory(t *testing.T) {
	source, _, scope, records, _ := reviewExactPaidSource(t, "wanted")
	probe := &refreshReadProbeDB{KVDB: source.db, prefix: recordDBKey(scope.Prefix)}
	source.db = probe
	require.NoError(t, source.refreshPaidRetentionAt(context.Background(), 10, scope))
	require.Zero(t, probe.scans, "one requested key must not load unrelated values")
	require.Greater(t, probe.reads, 0)
	retention, known := paidRetentionCacheFor(source).get(records[0].Key)
	require.True(t, known && paidRetentionCurrent(retention, 10))
}

func TestPaidRefreshDirectoryOnlyScansOnce(t *testing.T) {
	source, verifier, scope, _, _ := reviewExactPaidSource(t, "wanted")
	verifier.failKey = ""
	scope.Keys = nil
	probe := &refreshReadProbeDB{KVDB: source.db, prefix: recordDBKey(scope.Prefix)}
	source.db = probe
	require.NoError(t, source.refreshPaidRetentionAt(context.Background(), 10, scope))
	require.Equal(t, 1, probe.scans, "commit must recheck individual keys without rescanning the directory")
}

func TestPaidRefreshHonorsCancelledNetworkRead(t *testing.T) {
	source, verifier, scope, _, _ := reviewExactPaidSource(t, "wanted")
	verifier.failKey = ""
	scope.Keys = nil
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &refreshReadProbeDB{KVDB: source.db, prefix: recordDBKey(scope.Prefix), cancel: cancel}
	source.db = probe
	_, err := source.ActiveMetadata(ctx, scope)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, verifier.calls, "cancelled scans must stop before payment provider I/O")
}
