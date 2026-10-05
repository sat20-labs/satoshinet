package dkvs

import (
	"errors"
	"testing"
)

func TestDKVSReviewPathSnapshotPreservesUnpaidAutopayGrace(t *testing.T) {
	target, priv, _ := newAutopayMirrorIndexer(t, 10)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, err := target.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	// Advance the existing fee verifier's contract state without paying this
	// block. This uses the normal retention refresh, rather than replacing its
	// cache or relaxing record verification.
	height := uint64(11)
	verifier := target.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
	cached := verifier.AutopayFeeVerifier.StateProvider.(*HeightCachedAutopayStateProvider)
	provider := cached.Provider.(*mutableAutopayStateProvider)
	provider.state.CurrentBlock = int64(height)
	target.height = func() uint64 { return height }
	cached.CurrentHeight = target.height
	if pruned, err := target.PruneExpiredAutopayAt(height); err != nil || pruned != 0 {
		t.Fatalf("inside grace: pruned=%d err=%v", pruned, err)
	}
	if _, err := target.Get(record.Key); err != nil {
		t.Fatalf("record was not retained before synchronization: %v", err)
	}
	if target.paidRecordRelayable(record) {
		t.Fatal("unpaid record was still relayable")
	}
	source, _, _ := newAutopayMirrorIndexerForPrivateKey(t, priv, 10)
	source.height = target.height
	snapshot, err := source.GetPathSnapshot(path)
	if err != nil || len(snapshot.Records) != 0 {
		t.Fatalf("empty network snapshot=%+v err=%v", snapshot, err)
	}
	baseline, err := target.NetworkSyncBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyPathSnapshotFrom(snapshot, baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Get(record.Key); err != nil {
		t.Fatalf("network snapshot removed node-local AUTOPAY retention before grace expired: %v", err)
	}
	if network, err := target.GetPathSnapshot(path); err != nil || len(network.Records) != 0 {
		t.Fatalf("retained unpaid data must remain outside the network snapshot: snapshot=%+v err=%v", network, err)
	}
	// A verified current record for this key must still replace the retained
	// copy, even before the receiver's old relay cache has been refreshed.
	height = 12
	provider.state.CurrentBlock = int64(height)
	for payer, delegate := range provider.state.Delegates {
		delegate.LastPayHeight = int64(height)
		provider.state.Delegates[payer] = delegate
	}
	source, _, _ = newAutopayMirrorIndexerForPrivateKey(t, priv, int64(height))
	source.height = target.height
	replacement := signedAutopayPersonalRecord(t, priv, "autopay", 2)
	if _, err := source.PutLocal(replacement); err != nil {
		t.Fatal(err)
	}
	snapshot, err = source.GetPathSnapshot(path)
	if err != nil || len(snapshot.Records) != 1 {
		t.Fatalf("replacement snapshot=%+v err=%v", snapshot, err)
	}
	baseline, err = target.NetworkSyncBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyPathSnapshotFrom(snapshot, baseline); err != nil {
		t.Fatal(err)
	}
	if got, err := target.Get(record.Key); err != nil || RecordHash(got) != RecordHash(replacement) {
		t.Fatalf("retained record blocked its verified replacement: record=%+v err=%v", got, err)
	}
	height += paidRetentionGraceBlocks(target.freeLocal) + 1
	provider.state.CurrentBlock = int64(height)
	if pruned, err := target.PruneExpiredAutopayAt(height); err != nil || pruned != 1 {
		t.Fatalf("retention prune after grace: pruned=%d err=%v", pruned, err)
	}
	if _, err := target.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("record survived after grace: %v", err)
	}
}
