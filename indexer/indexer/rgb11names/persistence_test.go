package rgb11names

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	idx "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

func flush(t *testing.T, s *Index, db idx.KVDB) {
	t.Helper()
	batch := db.NewWriteBatch()
	defer batch.Close()
	ack, err := s.Stage(batch)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Flush(); err != nil {
		t.Fatal(err)
	}
	ack()
}

func TestBufferedSnapshotFlushPreservesNewerLiveNames(t *testing.T) {
	s, db := newIndex(t)
		apply(t, s, 0, register(1, "alice", "USD"))
	backup := s.Clone()
	querySnapshot := s.Clone()
	apply(t, s, 1, register(2, "alice", "USD"))
	flush(t, backup, db)
	if lookup(t, s, 2).Ordinal != 2 {
		t.Fatal("backup flush lost newer live state")
	}
	if _, err := querySnapshot.Lookup(Query{Kind: "contract", Value: id(2)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot leaked new registration: %v", err)
	}
	reloaded, err := Open(db, &chaincfg.TestNetParams, Cursor{Height: 0, Hash: id(1000)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reloaded.Lookup(Query{Kind: "contract", Value: id(2)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unflushed registration survived restart: %v", err)
	}
	// A replay after dropping an uncommitted branch obtains the same ordinal.
	apply(t, reloaded, 1, register(2, "alice", "USD"))
	if lookup(t, reloaded, 2) != lookup(t, s, 2) {
		t.Fatal("replay diverged")
	}
	flush(t, s, db)
	if len(s.dirty) != 0 {
		t.Fatal("successful flush retained acknowledged deltas")
	}
	final, err := Open(db, &chaincfg.TestNetParams, Cursor{Height: 1, Hash: id(1001)})
	if err != nil {
		t.Fatal(err)
	}
	if lookup(t, final, 2) != lookup(t, s, 2) {
		t.Fatal("restart mapping mismatch")
	}
	// The database now contains a newer counter: the old RPC clone must not.
	counter, err := querySnapshot.Lookup(Query{Kind: "counter", Provider: "alice", Ticker: "USD"})
	if err != nil || counter.Counter.MaxOrdinal != 1 {
		t.Fatalf("old snapshot read new DB data: %+v %v", counter, err)
	}
	if _, err := Open(db, &chaincfg.TestNetParams, Cursor{Height: 0, Hash: id(1000)}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("accepted base/naming checkpoint mismatch: %v", err)
	}
}

var injectedIO = errors.New("injected naming storage IO failure")

type failingBatch struct {
	idx.WriteBatch
	putError bool
}

func (b *failingBatch) Put(key, value []byte) error {
	if b.putError {
		return injectedIO
	}
	return b.WriteBatch.Put(key, value)
}
func (b *failingBatch) Flush() error { return injectedIO }

func TestFailedStageOrFlushRetainsPendingChanges(t *testing.T) {
	for _, stageFailure := range []bool{true, false} {
		t.Run(fmt.Sprint(stageFailure), func(t *testing.T) {
			s, db := newIndex(t)
						apply(t, s, 0, register(1, "alice", "USD"))
			pending := len(s.dirty)
			batch := &failingBatch{WriteBatch: db.NewWriteBatch(), putError: stageFailure}
			_, err := s.Stage(batch)
			if stageFailure {
				if !errors.Is(err, injectedIO) {
					t.Fatalf("stage: %v", err)
				}
			} else if err != nil || !errors.Is(batch.Flush(), injectedIO) {
				t.Fatalf("flush: %v", err)
			}
			batch.Close()
			if len(s.dirty) != pending {
				t.Fatal("failed IO discarded pending writes")
			}
			empty, err := Open(db, &chaincfg.TestNetParams, Cursor{Height: -1})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := empty.Lookup(Query{Kind: "contract", Value: id(1)}); !errors.Is(err, ErrNotFound) {
				t.Fatal("partial batch became visible")
			}
			flush(t, s, db)
			restored, err := Open(db, &chaincfg.TestNetParams, s.tip.Cursor)
			if err != nil || lookup(t, restored, 1).Ordinal != 1 {
				t.Fatalf("retry failed: %v", err)
			}
		})
	}
}

func TestCorruptReverseMappingFailsStartup(t *testing.T) {
	s, db := newIndex(t)
		apply(t, s, 0, register(1, "alice", "USD"))
	flush(t, s, db)
	if err := db.Write([]byte(dbPrefix+"name/rgb11:f:usd@alice"), []byte(`"`+id(99)+`"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(db, &chaincfg.TestNetParams, s.tip.Cursor); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt reverse mapping accepted: %v", err)
	}
}

func TestLookupCopiesAndConcurrentSnapshotReaders(t *testing.T) {
	s, _ := newIndex(t)
		apply(t, s, 0, register(1, "alice", "USD"))
	r, err := s.Lookup(Query{Kind: "contract", Value: id(1)})
	if err != nil {
		t.Fatal(err)
	}
	r.Registration.AssetName = "attacker"
	if lookup(t, s, 1).AssetName != "rgb11:f:usd@alice" {
		t.Fatal("query mutated stored registration")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 40; n++ {
				view := s.Clone()
				if err := view.CheckSelf(); err != nil {
					t.Error(err)
					return
				}
				if _, err := view.Lookup(Query{Kind: "contract", Value: id(1)}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for n := 1; n < 15; n++ {
		apply(t, s, n, register(n+1, "alice", "USD"))
	}
	wg.Wait()
}
