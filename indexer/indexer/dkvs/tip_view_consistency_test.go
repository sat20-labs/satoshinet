package dkvs

import (
	"context"
	"testing"
)

func TestIndexedLagActiveViewUsesVerifiedTipForClient(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	record.IssueHeight = height
	SignRecord(priv, record)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	idx.height = func() uint64 { return height - 2 }
	idx.tipHeight = func() uint64 { return height }
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	page, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{
		Scope: ActiveScope{Prefix: path}, EndpointID: idx.EndpointID(), Full: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("known-tip record omitted: %d", len(page.Records))
	}
	if err := VerifyRecordForClient(page.Records[0], RecordVerificationOptions{ExpectedKey: record.Key, Height: page.Meta.ViewHeight}); err != nil {
		t.Fatalf("node accepted known-tip record but exposed a view height that makes the SDK reject it: indexed=%d tip=%d view=%d err=%v", idx.currentHeight(), height, page.Meta.ViewHeight, err)
	}
	if page.Meta.ViewHeight != height {
		t.Fatalf("client view height=%d, trusted tip=%d", page.Meta.ViewHeight, height)
	}
}

func TestIndexedLagPaidSnapshotRoundTripsToLaggingPeer(t *testing.T) {
	source, priv, height := newAutopayMirrorIndexer(t, 10)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	record.IssueHeight = height
	SignRecord(priv, record)
	if _, err := source.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	target, _, _ := newAutopayMirrorIndexerForPrivateKey(t, priv, 10)
	source.height = func() uint64 { return height - 2 }
	source.tipHeight = func() uint64 { return height }
	target.height = source.height
	target.tipHeight = source.tipHeight
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.GetPathSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 1 || snapshot.PathMeta.ViewHeight != height {
		t.Fatal("known-tip snapshot must include the original signed record")
	}
	if err := ValidatePathSnapshotForClient(snapshot, RecordVerificationOptions{Height: height}); err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyPathSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	got, err := target.Get(record.Key)
	if err != nil || RecordHash(got) != RecordHash(record) {
		t.Fatalf("lagging peer lost or changed the admitted record: %v", err)
	}
}
