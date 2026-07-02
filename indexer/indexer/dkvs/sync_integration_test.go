package dkvs

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
)

func testIndexerWithHeight(t *testing.T, height uint64) *Indexer {
	t.Helper()
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return height },
	})
}

func pullByNotify(t *testing.T, source, target *Indexer, key string) {
	t.Helper()
	record, err := source.Get(key)
	if err != nil {
		t.Fatalf("source get: %v", err)
	}
	hash := RecordHash(record)
	byHash, err := source.GetByHash(hash)
	if err != nil {
		t.Fatalf("source get by hash: %v", err)
	}
	if updated, err := target.PutRemote(byHash); err != nil || !updated {
		t.Fatalf("target put remote updated=%v err=%v", updated, err)
	}
}

func syncAllPages(t *testing.T, source, target *Indexer, limit uint32) {
	t.Helper()
	var cursor []byte
	for {
		records, next, done, _, err := source.Sync(cursor, limit)
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		for _, record := range records {
			if _, err := target.PutRemote(record); err != nil {
				t.Fatalf("put remote %s: %v", record.Key, err)
			}
		}
		if done {
			return
		}
		cursor = next
		if len(cursor) == 0 {
			t.Fatal("missing cursor for next sync page")
		}
	}
}

func TestThreeMinerNotifyAndStartupSyncConverge(t *testing.T) {
	minerA := testIndexerWithHeight(t, 1)
	minerB := testIndexerWithHeight(t, 1)
	minerC := testIndexerWithHeight(t, 1)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	initial := signedPersonalRecordWithKey(t, priv, 1, "initial", 0)
	if updated, err := minerA.PutLocal(initial); err != nil || !updated {
		t.Fatalf("miner A put initial updated=%v err=%v", updated, err)
	}
	pullByNotify(t, minerA, minerB, initial.Key)
	pullByNotify(t, minerA, minerC, initial.Key)
	for _, miner := range []*Indexer{minerB, minerC} {
		got, err := miner.Get(initial.Key)
		if err != nil {
			t.Fatalf("get converged initial: %v", err)
		}
		if string(got.Value) != "initial" {
			t.Fatalf("got %q", got.Value)
		}
	}

	updatedRecord := signedPersonalRecordWithKey(t, priv, 2, "updated", 0)
	if updated, err := minerA.PutLocal(updatedRecord); err != nil || !updated {
		t.Fatalf("miner A put updated updated=%v err=%v", updated, err)
	}
	pullByNotify(t, minerA, minerB, updatedRecord.Key)
	pullByNotify(t, minerA, minerC, updatedRecord.Key)
	for _, miner := range []*Indexer{minerB, minerC} {
		got, err := miner.Get(updatedRecord.Key)
		if err != nil {
			t.Fatalf("get converged update: %v", err)
		}
		if got.Seq != 2 || string(got.Value) != "updated" {
			t.Fatalf("bad update seq=%d value=%q", got.Seq, got.Value)
		}
	}

	newMiner := testIndexerWithHeight(t, 1)
	syncAllPages(t, minerA, newMiner, 1)
	got, err := newMiner.Get(updatedRecord.Key)
	if err != nil {
		t.Fatalf("new miner get: %v", err)
	}
	if got.Seq != 2 || string(got.Value) != "updated" {
		t.Fatalf("new miner bad seq=%d value=%q", got.Seq, got.Value)
	}

	tombstone := signedPersonalRecordWithKey(t, priv, 3, "", FlagTombstone)
	if updated, err := minerA.PutLocal(tombstone); err != nil || !updated {
		t.Fatalf("miner A put tombstone updated=%v err=%v", updated, err)
	}
	pullByNotify(t, minerA, minerB, tombstone.Key)
	pullByNotify(t, minerA, minerC, tombstone.Key)
	for _, miner := range []*Indexer{minerA, minerB, minerC} {
		got, err := miner.Get(tombstone.Key)
		if err != nil {
			t.Fatalf("get tombstone: %v", err)
		}
		if !IsTombstone(got.Flags) || len(got.Value) != 0 {
			t.Fatalf("old value returned after tombstone: flags=%d value=%q", got.Flags, got.Value)
		}
	}
}
