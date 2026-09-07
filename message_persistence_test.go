package main

import (
	"crypto/sha256"
	"testing"

	"github.com/sat20-labs/satoshinet/database"
	_ "github.com/sat20-labs/satoshinet/database/ffldb"
	"github.com/sat20-labs/satoshinet/wire"
)

func testMessageDatabase(t *testing.T) database.DB {
	t.Helper()
	db, err := database.Create("ffldb", t.TempDir(), wire.TestNet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDKVSMessagePersistentSenderSequencePendingAndRetarget(t *testing.T) {
	db := testMessageDatabase(t)
	_, sender := testAccount(t)
	data := []byte("accepted-envelope")
	digest := sha256.Sum256(data)
	messageID := testStableMessageID("persist", sender)
	charges := 0
	store := newDatabaseMessageAcceptanceStore(db)
	accepted, duplicate, err := store.Accept(sender, 0, messageID, digest, "core-b", data, func() error {
		charges++
		return nil
	})
	if err != nil || duplicate || accepted.Target != "core-b" {
		t.Fatalf("accept=%#v duplicate=%v err=%v", accepted, duplicate, err)
	}
	if store.NextSenderMsgID(sender) != 1 || charges != 1 {
		t.Fatalf("next=%d charges=%d", store.NextSenderMsgID(sender), charges)
	}

	// Reconstruct the store as a node restart would. The same accepted
	// MessageID remains pending and does not re-charge.
	restarted := newDatabaseMessageAcceptanceStore(db)
	accepted, duplicate, err = restarted.Accept(sender, 0, messageID, digest, "core-b", data, func() error {
		charges++
		return nil
	})
	if err != nil || !duplicate || charges != 1 || restarted.NextSenderMsgID(sender) != 1 {
		t.Fatalf("restart duplicate=%v charges=%d next=%d err=%v", duplicate, charges, restarted.NextSenderMsgID(sender), err)
	}
	pendingStore := restarted.(interface {
		Pending() ([]acceptedMessage, error)
	})
	pending, err := pendingStore.Pending()
	if err != nil || len(pending) != 1 || pending[0].Target != "core-b" {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}

	// Recipient service-node movement retargets the same durable job without
	// consuming another sender sequence or another MESSAGE_SEND usage.
	accepted, duplicate, err = restarted.Accept(sender, 0, messageID, digest, "core-c", data, func() error {
		charges++
		return nil
	})
	if err != nil || !duplicate || accepted.Target != "core-c" || charges != 1 {
		t.Fatalf("retarget=%#v duplicate=%v charges=%d err=%v", accepted, duplicate, charges, err)
	}
	pending, err = pendingStore.Pending()
	if err != nil || len(pending) != 1 || pending[0].Target != "core-c" {
		t.Fatalf("retarget pending=%#v err=%v", pending, err)
	}

	if err := restarted.MarkAck(sender, messageID, 0, "core-c", MessageAckRetryable); err != nil {
		t.Fatal(err)
	}
	pending, err = pendingStore.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("retryable pending=%#v err=%v", pending, err)
	}
	if err := restarted.MarkAck(sender, messageID, 0, "core-c", MessageAckOK); err != nil {
		t.Fatal(err)
	}
	pending, err = pendingStore.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("final pending=%#v err=%v", pending, err)
	}
}
