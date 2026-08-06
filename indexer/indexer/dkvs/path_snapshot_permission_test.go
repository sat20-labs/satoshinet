package dkvs

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func TestSnapshotPermissionAcceptsAccountSignedMailboxShare(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := CanonicalAccountID(priv.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	key, err := MailShareKey(accountID, "package", "share")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewSignedRecord(priv, key, []byte("encrypted-share"), RecordOptions{
		Seq: 1, IssueHeight: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(record.PubKey) != 0 {
		t.Fatalf("account-signed record unexpectedly carries a pubkey: %x", record.PubKey)
	}
	parsed, err := ParseKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSnapshotPermission(record, parsed, runtimeValidators{}); err != nil {
		t.Fatalf("account-signed mailbox share rejected by snapshot validation: %v", err)
	}
}
