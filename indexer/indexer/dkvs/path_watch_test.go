package dkvs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
)

func pathWatchFixture(t *testing.T, height *atomic.Uint64) (*Indexer, *btcec.PrivateKey, string) {
	t.Helper()
	idx := testIndexerWithConfig(t, Config{
		FeeVerifier:   testFeeVerifier{},
		CurrentHeight: func() uint64 { return height.Load() },
	})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/watch"
	return idx, priv, path
}

func putPathWatchRecord(t *testing.T, idx *Indexer, priv *btcec.PrivateKey, seq, issueHeight, ttl uint64) {
	t.Helper()
	record := signedPersonalRecordWithPath(t, priv, "watch/item", seq, "value", 0)
	record.IssueHeight = issueHeight
	record.TTL = ttl
	SignRecord(priv, record)
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("PutLocal updated=%v err=%v", updated, err)
	}
}

func TestWaitPathNoChangeHonorsContext(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, height.Load(), 100)
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	got, changed, err := idx.WaitPath(ctx, path, meta.Generation, meta.StateRoot, meta.ViewHeight)
	if !errors.Is(err, context.DeadlineExceeded) || changed || got == nil {
		t.Fatalf("got=%+v changed=%v err=%v", got, changed, err)
	}
}

func TestWaitPathMutationWakesImmediately(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, height.Load(), 100)
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		meta    *PathMeta
		changed bool
		err     error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		got, changed, waitErr := idx.WaitPath(ctx, path, meta.Generation, meta.StateRoot, meta.ViewHeight)
		done <- result{got, changed, waitErr}
	}()
	time.Sleep(20 * time.Millisecond)
	putPathWatchRecord(t, idx, priv, 2, height.Load(), 100)
	got := <-done
	if got.err != nil || !got.changed || got.meta == nil || got.meta.Generation == meta.Generation {
		t.Fatalf("mutation wait result=%+v", got)
	}
}

func TestWaitPathWakesAtNearestTTLHeight(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, height.Load(), 1)
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() {
		got, changed, waitErr := idx.WaitPath(ctx, path, meta.Generation, meta.StateRoot, meta.ViewHeight)
		done <- waitErr == nil && changed && got != nil && got.ActiveRecords == 0
	}()
	time.Sleep(20 * time.Millisecond)
	height.Store(2)
	if ok := <-done; !ok {
		t.Fatal("TTL expiry did not wake path watch")
	}
}

func TestWaitPathReadDoesNotRepairDirtyMeta(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, height.Load(), 100)
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	status := &PathLocalStatus{Path: path, Dirty: true}
	encoded, err := marshalPathStatus(status)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.db.Write(pathStatusDBKey(path), encoded); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _, _ = idx.WaitPath(ctx, path, meta.Generation, meta.StateRoot, meta.ViewHeight)
	stored, err := idx.GetPathLocalStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Dirty {
		t.Fatal("watch read rebuilt and cleared dirty path metadata")
	}
}

func TestWaitPathReadsLegacyMetaWithoutStatus(t *testing.T) {
	var height atomic.Uint64
	height.Store(1)
	idx, priv, path := pathWatchFixture(t, &height)
	putPathWatchRecord(t, idx, priv, 1, height.Load(), 100)
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.db.Delete(pathStatusDBKey(path)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	got, changed, err := idx.WaitPath(ctx, path, meta.Generation, meta.StateRoot, meta.ViewHeight)
	if !errors.Is(err, context.DeadlineExceeded) || changed || got == nil {
		t.Fatalf("got=%+v changed=%v err=%v", got, changed, err)
	}
	if _, err := idx.db.Read(pathStatusDBKey(path)); err == nil {
		t.Fatal("watch read recreated legacy path status")
	}
}
