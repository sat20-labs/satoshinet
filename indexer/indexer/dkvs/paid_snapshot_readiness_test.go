package dkvs

import (
	"errors"
	"github.com/sat20-labs/satoshinet/wire"
	"testing"
)

// Recreate the indexer over the same database, as during a normal restart.
// No cache clearing or record editing is used to produce the cold view.
func restartedAutopaySource(t *testing.T) (*Indexer, *Indexer, *wire.DKVSRecord) {
	t.Helper()
	original, priv, height := newAutopayMirrorIndexer(t, 10)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, err := original.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	source := New(original.db, Config{EndpointID: "restarted-source", FeeVerifier: original.feeVerifier, CurrentHeight: func() uint64 { return height }})
	target, _, _ := newAutopayMirrorIndexerForPrivateKey(t, priv, 10)
	if _, err := target.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	return source, target, record
}

func TestColdPaidSnapshotMustPreserveConfirmedPeerRecord(t *testing.T) {
	source, target, record := restartedAutopaySource(t)
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.GetPathSnapshot(path)
	if err != nil {
		t.Fatalf("normal paid restart should verify and serve the complete snapshot: %v", err)
	}
	baseline, err := target.NetworkSyncBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyPathSnapshotFrom(snapshot, baseline); err != nil {
		t.Fatal(err)
	}
	got, err := target.Get(record.Key)
	if err != nil {
		t.Fatalf("cold source snapshot deleted confirmed peer record: %v", err)
	}
	if len(snapshot.Records) != 1 || RecordHash(got) != RecordHash(record) {
		t.Fatal("complete paid snapshot must preserve the exact original record")
	}
}

func TestFailedPaidSnapshotVerificationMustReturnError(t *testing.T) {
	source, _, record := restartedAutopaySource(t)
	unavailable := errors.New("contract state unavailable")
	source.SetFeeVerifier(AutopayFeeVerifier{StateProvider: testAutopayStateProvider{err: unavailable}, Contract: "autopay"})
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.GetPathSnapshot(path)
	if !errors.Is(err, unavailable) || snapshot != nil {
		t.Fatalf("unknown payment must not become an authoritative empty snapshot: snapshot=%v err=%v", snapshot, err)
	}
	got, err := source.Get(record.Key)
	if err != nil || RecordHash(got) != RecordHash(record) {
		t.Fatalf("failed verification must preserve stored record: %v", err)
	}
}
