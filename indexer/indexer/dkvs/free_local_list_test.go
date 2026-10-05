package dkvs

import (
	"context"
	"testing"
)

func TestListPrefixIncludesEndpointLocalRecordsOutsidePathMeta(t *testing.T) {
	idx, priv, _ := newAutopayMirrorIndexer(t, 10)
	record := signedFreePersonalRecord(t, priv, "free-local-list", 1, "value", 0)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatalf("put FREE_LOCAL record: %v", err)
	}
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.ActiveRecords != 0 {
		t.Fatalf("network PathMeta includes endpoint-local record: %+v", meta)
	}
	records, total, err := idx.ListPrefix(path, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(records) != 1 || records[0].Key != record.Key {
		t.Fatalf("local list total=%d records=%+v", total, records)
	}
}

func TestFreeLocalDeletePhysicallyRemovesApplicationState(t *testing.T) {
	idx, priv, _ := newAutopayMirrorIndexer(t, 10)
	record := signedFreePersonalRecord(t, priv, "free-local-floor", 1, "value", 0)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	command := signedCurrentDelete(t, priv, record, 10)
	if _, err := idx.PutLocal(command); err != nil {
		t.Fatal(err)
	}
	after, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.EndpointGeneration != before.EndpointGeneration+1 || after.Generation != before.Generation || after.StateRoot != before.StateRoot {
		t.Fatalf("FREE_LOCAL deletion changed the wrong view: before=%+v after=%+v", before, after)
	}
	canonical, err := idx.GetPathSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(canonical.Records) != 0 {
		t.Fatalf("FREE_LOCAL state leaked into canonical snapshot: %+v", canonical)
	}
	state, err := idx.GetKeyState(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != KeyStateNeverSeen || state.Seq != 0 || state.ETag != "" {
		t.Fatalf("delete retained key state=%+v", state)
	}
	snapshot, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: path}, EndpointID: idx.EndpointID(), Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Meta.EndpointID == "" || snapshot.Meta.ViewHeight != 10 || len(snapshot.Records) != 0 {
		t.Fatalf("application snapshot=%+v", snapshot)
	}
	assertNoDeleteRows(t, idx)
}

func TestFreeLocalBatchCASDeletePhysicallyRemovesState(t *testing.T) {
	idx, priv, _ := newAutopayMirrorIndexer(t, 10)
	record := signedFreePersonalRecord(t, priv, "free-local-cas-delete", 1, "value", 0)
	if result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{Record: record, Precondition: WritePrecondition{ExpectAbsent: true}}},
		BatchCASOptions{EndpointID: idx.EndpointID()}); err != nil || result.Applied != 1 {
		t.Fatalf("put result=%+v err=%v", result, err)
	}
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := RecordHash(record)
	command := signedCurrentDelete(t, priv, record, 10)
	if result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{Record: command, Precondition: WritePrecondition{ExpectedHash: &expected}}},
		BatchCASOptions{EndpointID: idx.EndpointID()}); err != nil || result.Applied != 1 {
		t.Fatalf("delete result=%+v err=%v", result, err)
	}
	state, err := idx.GetKeyState(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != KeyStateNeverSeen || state.ETag != "" {
		t.Fatalf("batch delete retained key state: %+v", state)
	}
	after, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.EndpointGeneration != before.EndpointGeneration+1 || after.Generation != before.Generation || after.StateRoot != before.StateRoot {
		t.Fatalf("batch delete path state before=%+v after=%+v", before, after)
	}
	snapshot, err := idx.GetPathSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 0 {
		t.Fatalf("batch delete leaked into canonical snapshot: %+v", snapshot)
	}
	assertNoDeleteRows(t, idx)
}
