package dkvs

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

// Pause an actual record scan while its caller holds the DKVS lock. The
// independent indexed-height source can still advance, as BaseIndexer does.
type snapshotScanGateDB struct {
	indexercommon.KVDB
	prefix  []byte
	pauseAt int
	scans   int
	caller  string
	started chan struct{}
	release chan struct{}
}

func (db *snapshotScanGateDB) BatchReadV2(prefix, seek []byte, reverse bool, visit func([]byte, []byte) error) error {
	if bytes.Equal(prefix, db.prefix) {
		db.scans++
		if db.scans == db.pauseAt {
			pc, _, _, _ := runtime.Caller(1)
			db.caller = runtime.FuncForPC(pc).Name()
			close(db.started)
			<-db.release
		}
	}
	return db.KVDB.BatchReadV2(prefix, seek, reverse, visit)
}

func TestPaidViewsRemainCompleteWhenHeightAdvancesDuringScan(t *testing.T) {
	for _, operation := range []string{"snapshot", "metadata", "page-root", "page-records", "path-meta"} {
		t.Run(operation, func(t *testing.T) {
			source, priv, height := newAutopayMirrorIndexer(t, 10)
			if operation == "path-meta" {
				source.height = func() uint64 { return height - 1 }
			}
			record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
			if _, err := source.PutLocal(record); err != nil {
				t.Fatal(err)
			}
			target, _, _ := newAutopayMirrorIndexerForPrivateKey(t, priv, 10)
			if _, err := target.PutLocal(record); err != nil {
				t.Fatal(err)
			}
			path, err := CollectionPathForKey(record.Key)
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := target.NetworkSyncBaseline(path)
			if err != nil {
				t.Fatal(err)
			}
			var indexed atomic.Uint64
			indexed.Store(height)
			source.height, source.tipHeight = indexed.Load, indexed.Load
			// Network reads prepare payment using one streaming scan. Pause only after
			// that preparation and its height check, in the named view phase.
			pauseAt, wantCaller := 2, "activeMetaLocked"
			switch operation {
			case "snapshot":
				wantCaller = "GetPathSnapshot"
			case "page-records":
				pauseAt, wantCaller = 3, "ActiveSyncPage"
			case "path-meta":
				pauseAt, wantCaller = 1, "scanLocked"
			}
			gate := &snapshotScanGateDB{KVDB: source.db, prefix: recordDBKey(path), pauseAt: pauseAt,
				started: make(chan struct{}), release: make(chan struct{})}
			source.db = gate
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			var snapshot *PathSnapshot
			var meta ActiveMeta
			var page *ActivePage
			var pathMeta *PathMeta
			finished := make(chan error, 1)
			go func() {
				var err error
				scope := ActiveScope{Prefix: path, Network: true}
				switch operation {
				case "snapshot":
					snapshot, err = source.GetPathSnapshot(path)
				case "metadata":
					meta, err = source.ActiveMetadata(context.Background(), scope)
				case "path-meta":
					pathMeta, err = source.GetPathMeta(path)
				default:
					page, err = source.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: scope, EndpointID: source.EndpointID(), Full: true})
				}
				finished <- err
			}()
			select {
			case <-gate.started:
			case <-time.After(3 * time.Second):
				t.Fatal("view did not reach the selected locked scan")
			}
			if !strings.HasSuffix(gate.caller, "."+wantCaller) {
				t.Fatalf("paused in %s, want %s", gate.caller, wantCaller)
			}
			t.Logf("paused in %s after %d record scans", gate.caller, gate.pauseAt)
			indexed.Store(height + 1)
			release()
			select {
			case err := <-finished:
				if err != nil {
					t.Fatalf("captured view failed after reaching %s: %v", wantCaller, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("view did not finish")
			}
			expectedRoot := baseline.Root
			switch operation {
			case "snapshot":
				if snapshot.PathMeta.ViewHeight != height || len(snapshot.Records) != 1 || snapshot.PathMeta.StateRoot != expectedRoot {
					t.Errorf("height advance produced an incomplete successful snapshot: %+v", snapshot)
				}
				if _, err := target.ApplyPathSnapshotFrom(snapshot, baseline); err != nil {
					t.Fatal(err)
				}
				got, err := target.Get(record.Key)
				if err != nil || RecordHash(got) != RecordHash(record) {
					t.Fatalf("incomplete snapshot deleted receiver's paid record: %v", err)
				}
			case "metadata":
				if meta.ViewHeight != height || meta.Root != expectedRoot {
					t.Fatalf("metadata lost the captured paid view: %+v", meta)
				}
			case "path-meta":
				if pathMeta.ViewHeight != height || pathMeta.StateRoot != expectedRoot || pathMeta.ActiveRecords != 1 {
					t.Fatalf("path metadata rebuild mixed view heights: %+v", pathMeta)
				}
			default:
				if page.Meta.ViewHeight != height || page.Meta.Root != expectedRoot || len(page.Records) != 1 || !page.Complete {
					t.Fatalf("page root and records must describe the same captured paid view: %+v", page)
				}
				if RecordHash(page.Records[0]) != RecordHash(record) {
					t.Fatal("page changed the original signed record")
				}
			}
			if expectedRoot == (chainhash.Hash{}) {
				t.Fatal("fixture must have a nonempty root")
			}
		})
	}
}
