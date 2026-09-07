package dkvs

import (
	"errors"
	"strconv"
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/wire"
)

func testMessageAccount(t *testing.T) string {
	t.Helper()
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	account := AccountID(priv.PubKey().SerializeCompressed())
	if account == "" {
		t.Fatal("empty account id")
	}
	return account
}

func newMessageTestIndexer(t *testing.T, height *uint64) *Indexer {
	t.Helper()
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, Config{
		EndpointID:    "test-core-node",
		CurrentHeight: func() uint64 { return *height },
		MailboxPolicy: MailboxPolicy{
			MaxMsgBytes: 1 << 20, MaxMessages: 100,
			MaxMsgBytesPerSender: 1 << 20, MaxMessagesPerSender: 100,
			MaxMsgSize: 16 * 1024, MaxMsgTTL: 10_000,
			MaxShareBytes: 1 << 20, MaxShares: 100, MaxShareSize: 4096, MaxShareTTL: 10_000,
		},
	})
}

func internalDirectRecord(t *testing.T, recipient, sender string, msgID uint64, height uint64, value []byte) *wire.DKVSRecord {
	t.Helper()
	key, err := MailMsgKey(recipient, sender, strconv.FormatUint(msgID, 10))
	if err != nil {
		t.Fatal(err)
	}
	record := &wire.DKVSRecord{Version: Version, Key: key, Value: append([]byte(nil), value...), Seq: 1, IssueHeight: height, TTL: 100}
	proof, err := EncodeFeeProof(&FeeProof{Mode: FeeModeFreeLocal})
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof = proof
	return record
}

func TestMessageMailboxInternalAppendIsLocalReadableAndIdempotent(t *testing.T) {
	height := uint64(100)
	idx := newMessageTestIndexer(t, &height)
	recipient := testMessageAccount(t)
	sender := testMessageAccount(t)
	record := internalDirectRecord(t, recipient, sender, 0, height, []byte("signed-inner-envelope"))

	updated, err := idx.PutInternalMailbox(record)
	if err != nil || !updated {
		t.Fatalf("first append updated=%v err=%v", updated, err)
	}
	updated, err = idx.PutInternalMailbox(record)
	if err != nil || updated {
		t.Fatalf("idempotent append updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(record.Key)
	if err != nil || string(got.Value) != string(record.Value) {
		t.Fatalf("get internal mailbox: got=%v err=%v", got, err)
	}

	conflict := cloneRecord(record)
	conflict.Value = []byte("different")
	if _, err := idx.PutInternalMailbox(conflict); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("conflicting replay err=%v want %v", err, ErrWriteConflict)
	}
}

func TestMessageMailboxGenericPutAndRemoteReplicationRejected(t *testing.T) {
	height := uint64(100)
	idx := newMessageTestIndexer(t, &height)
	recipient := testMessageAccount(t)
	sender := testMessageAccount(t)
	record := internalDirectRecord(t, recipient, sender, 0, height, []byte("inner"))

	if _, err := idx.PutLocal(record); err == nil {
		t.Fatal("generic local DKVS PUT accepted MessageManager mailbox record")
	}
	if _, err := idx.AcceptRemoteRecord(record, "peer"); err == nil {
		t.Fatal("remote DKVS replication accepted account-bound mailbox record")
	}
}

func TestMessageMailboxFreeLocalQuotaIsSharedAcrossRecipients(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{
		FreeLocalCache: FreeLocalCachePolicy{
			MaxTTL: 1000, MaxRecordsPerSigner: 1, MaxBytesPerSigner: 1 << 20,
			MaxTotalRecords: 100, MaxTotalBytes: 8 << 20,
		},
		MailboxPolicy: MailboxPolicy{
			MaxMessages: 100, MaxMsgBytes: 8 << 20, MaxMessagesPerSender: 100,
			MaxMsgBytesPerSender: 8 << 20, MaxMsgSize: 1 << 20, MaxMsgTTL: 1000,
		},
	})
	sender := testMessageAccount(t)
	firstRecipient := testMessageAccount(t)
	secondRecipient := testMessageAccount(t)
	first := internalDirectRecord(t, firstRecipient, sender, 0, 1, []byte("first"))
	if updated, err := idx.PutInternalMailbox(first); err != nil || !updated {
		t.Fatalf("first free Direct updated=%v err=%v", updated, err)
	}
	second := internalDirectRecord(t, secondRecipient, sender, 1, 1, []byte("second"))
	if _, err := idx.PutInternalMailbox(second); !errors.Is(err, ErrFreeLocalQuotaExceeded) {
		t.Fatalf("cross-recipient free-local quota err=%v", err)
	}
}

func TestMessageMailboxExcludedFromRelayCheckpointAndSnapshot(t *testing.T) {
	height := uint64(100)
	idx := newMessageTestIndexer(t, &height)
	recipient := testMessageAccount(t)
	sender := testMessageAccount(t)
	record := internalDirectRecord(t, recipient, sender, 0, height, []byte("inner"))
	if _, err := idx.PutInternalMailbox(record); err != nil {
		t.Fatal(err)
	}

	if got, err := idx.GetForRelay(record.Key); err == nil && got != nil {
		t.Fatalf("mailbox exposed through GetForRelay: %s", got.Key)
	}
	if got, err := idx.GetByHashForRelay(RecordHash(record)); err == nil && got != nil {
		t.Fatalf("mailbox exposed through GetByHashForRelay: %s", got.Key)
	}
	checkpoint, err := idx.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.ActiveRecordCount != 0 {
		t.Fatalf("checkpoint includes account-bound mailbox: count=%d", checkpoint.ActiveRecordCount)
	}
	snapshot, err := idx.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 0 {
		t.Fatalf("snapshot includes account-bound mailbox: %d records", len(snapshot.Records))
	}
}

func TestMessageMailboxPrefixSnapshotSeesCommittedDelivery(t *testing.T) {
	height := uint64(100)
	idx := newMessageTestIndexer(t, &height)
	recipient := testMessageAccount(t)
	sender := testMessageAccount(t)
	prefix := "/mail/" + recipient

	initial, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Records) != 0 {
		t.Fatalf("initial mailbox snapshot has %d records", len(initial.Records))
	}
	record := internalDirectRecord(t, recipient, sender, 0, height, []byte("inner"))
	if _, err := idx.PutInternalMailbox(record); err != nil {
		t.Fatal(err)
	}

	read, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Records) != 1 || read.Records[0].Key != record.Key {
		t.Fatalf("prefix snapshot did not surface committed mailbox delivery: %#v", read)
	}
}

func TestMessageKeyShapesAndNoMailboxAlias(t *testing.T) {
	recipient := testMessageAccount(t)
	sender := testMessageAccount(t)
	if _, err := ParseKey("/mailbox/" + recipient + "/msg/" + sender + "/0"); !errors.Is(err, ErrInvalidNamespace) {
		t.Fatalf("/mailbox alias err=%v want invalid namespace", err)
	}
	keys := []func() (string, error){
		func() (string, error) { return MailMsgKey(recipient, sender, "0") },
		func() (string, error) { return MailTopicMessageKey(recipient, "alpha", sender, "0") },
		func() (string, error) { return MailTopicKeyKey(recipient, "alpha", "2") },
		func() (string, error) { return TopicMetaKey("alpha") },
		func() (string, error) { return TopicStateKey("alpha") },
		func() (string, error) { return TopicMemberKey("alpha", recipient) },
	}
	for _, makeKey := range keys {
		key, err := makeKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseKey(key); err != nil {
			t.Fatalf("ParseKey(%q): %v", key, err)
		}
	}
}
