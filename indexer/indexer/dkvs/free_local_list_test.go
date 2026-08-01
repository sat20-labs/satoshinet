package dkvs

import "testing"

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
		t.Fatalf("local application list total=%d records=%+v", total, records)
	}
}
