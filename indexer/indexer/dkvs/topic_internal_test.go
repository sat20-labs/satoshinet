package dkvs

import (
	"errors"
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/wire"
)

func newMessageTopicTestIndexer(t *testing.T) *Indexer {
	t.Helper()
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, Config{CurrentHeight: func() uint64 { return 100 }})
}

func TestMessageTopicInternalBatchPersistsButDoesNotRelay(t *testing.T) {
	idx := newMessageTopicTestIndexer(t)
	account := "0101010101010101010101010101010101010101010101010101010101010101"
	values := map[string][]byte{
		"/topic/developers/meta":              []byte(`{"topic":"developers"}`),
		"/topic/developers/state":             []byte(`{"key_seq":1}`),
		"/topic/developers/members/" + account: []byte(`{"status":"ACTIVE"}`),
	}
	applied, err := idx.PutInternalTopicValues(values)
	if err != nil || applied != 3 {
		t.Fatalf("applied=%d err=%v", applied, err)
	}
	for key, value := range values {
		record, err := idx.Get(key)
		if err != nil || string(record.Value) != string(value) || record.Seq != 1 || record.TTL != 0 {
			t.Fatalf("key=%s record=%#v err=%v", key, record, err)
		}
		if _, err := idx.GetForRelay(key); !errors.Is(err, ErrRecordNotFound) {
			t.Fatalf("topic key relayed %s err=%v", key, err)
		}
	}
	records, err := idx.ListInternalTopicRecords()
	if err != nil || len(records) != 3 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	checkpoint, err := idx.Checkpoint()
	if err != nil || checkpoint.ActiveRecordCount != 0 {
		t.Fatalf("checkpoint=%#v err=%v", checkpoint, err)
	}
	snapshot, err := idx.Snapshot()
	if err != nil || len(snapshot.Records) != 0 {
		t.Fatalf("snapshot records=%d err=%v", len(snapshot.Records), err)
	}
	synced, _, done, _, err := idx.Sync(nil, 100)
	if err != nil || !done || len(synced) != 0 {
		t.Fatalf("sync records=%d done=%v err=%v", len(synced), done, err)
	}
}

func TestMessageTopicInternalBatchUpdatesSeqAndRejectsGenericPut(t *testing.T) {
	idx := newMessageTopicTestIndexer(t)
	key := "/topic/developers/state"
	if applied, err := idx.PutInternalTopicValues(map[string][]byte{key: []byte("one")}); err != nil || applied != 1 {
		t.Fatalf("initial applied=%d err=%v", applied, err)
	}
	if applied, err := idx.PutInternalTopicValues(map[string][]byte{key: []byte("one")}); err != nil || applied != 0 {
		t.Fatalf("idempotent applied=%d err=%v", applied, err)
	}
	if applied, err := idx.PutInternalTopicValues(map[string][]byte{key: []byte("two")}); err != nil || applied != 1 {
		t.Fatalf("update applied=%d err=%v", applied, err)
	}
	stored, err := idx.Get(key)
	if err != nil || stored.Seq != 2 || string(stored.Value) != "two" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	generic := &wire.DKVSRecord{Version: Version, Key: key, Value: []byte("attack"), Seq: 3}
	if _, err := idx.PutLocal(generic); err == nil {
		t.Fatal("generic topic put unexpectedly accepted")
	}
	if _, err := idx.AcceptRemoteRecord(generic, "peer"); err == nil {
		t.Fatal("remote topic record unexpectedly accepted")
	}
}

func TestMessageMailboxDefaultTTLUsesBlocks(t *testing.T) {
	policy := normalizeMailboxPolicy(MailboxPolicy{})
	if policy.MaxMsgTTL != 30*24*60*60/12 {
		t.Fatalf("message ttl blocks=%d", policy.MaxMsgTTL)
	}
	if policy.MaxShareTTL != 365*24*60*60/12 {
		t.Fatalf("share ttl blocks=%d", policy.MaxShareTTL)
	}
}
