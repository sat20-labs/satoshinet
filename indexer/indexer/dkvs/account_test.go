package dkvs

import (
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

func signedAccountRecord(t *testing.T, priv *btcec.PrivateKey, key string, value []byte, seq uint64) *wire.DKVSRecord {
	t.Helper()
	record, err := NewAccountRecord(key, value, RecordOptions{
		Seq:          seq,
		TTL:          60_000,
		ExpiryHeight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	hash := SigningHash(record)
	sig, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = sig.Serialize()
	return record
}

func TestAccountIDAndAddressMapping(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := CanonicalAccountID(priv.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	if len(accountID) != 64 || accountID != strings.ToLower(accountID) {
		t.Fatalf("unexpected account ID %q", accountID)
	}
	pubKey, err := AccountPubKey(accountID)
	if err != nil {
		t.Fatal(err)
	}
	address, err := P2TRAddressFromPubKeyBytes(pubKey, &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	key, err := AccountMappingKey("testnet4", address)
	if err != nil {
		t.Fatal(err)
	}
	value, err := EncodeAccountMappingValue(accountID)
	if err != nil {
		t.Fatal(err)
	}
	record := signedAccountRecord(t, priv, key, value, 1)
	if len(record.PubKey) != 0 {
		t.Fatal("account record repeated the signer public key")
	}
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("put mapping updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAccountRecordForClient(got, RecordVerificationOptions{ExpectedKey: key}); err != nil {
		t.Fatalf("client verification failed: %v", err)
	}
	mapped, err := DecodeAccountMappingValue(got.Value)
	if err != nil || mapped != accountID {
		t.Fatalf("mapped account=%q err=%v", mapped, err)
	}
}

func TestAccountPersonalAndMailboxPermissions(t *testing.T) {
	idx := testIndexer(t)
	sender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderID, _ := CanonicalAccountID(sender.PubKey().SerializeCompressed())
	receiverID, _ := CanonicalAccountID(receiver.PubKey().SerializeCompressed())

	personalKey, err := AccountPersonalKey(senderID, "rgb11/receive")
	if err != nil {
		t.Fatal(err)
	}
	personal := signedAccountRecord(t, sender, personalKey, []byte{1, 1}, 1)
	if updated, err := idx.PutLocal(personal); err != nil || !updated {
		t.Fatalf("put personal updated=%v err=%v", updated, err)
	}

	mailKey, err := MailMsgKey(receiverID, senderID, "r11-transfer")
	if err != nil {
		t.Fatal(err)
	}
	mail := signedAccountRecord(t, sender, mailKey, []byte("ciphertext"), 1)
	if updated, err := idx.PutLocal(mail); err != nil || !updated {
		t.Fatalf("put mail updated=%v err=%v", updated, err)
	}
	if err := VerifyAccountRecordForClient(mail, RecordVerificationOptions{ExpectedKey: mailKey}); err != nil {
		t.Fatal(err)
	}

	forged := signedAccountRecord(t, receiver, mailKey, []byte("forged"), 2)
	if _, err := idx.PutLocal(forged); err == nil {
		t.Fatal("mail record signed by the wrong account was accepted")
	}
}

func TestAccountRejectsForgedAddressMapping(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	accountID, _ := CanonicalAccountID(priv.PubKey().SerializeCompressed())
	otherID, _ := CanonicalAccountID(other.PubKey().SerializeCompressed())
	otherPub, _ := AccountPubKey(otherID)
	otherAddress, err := P2TRAddressFromPubKeyBytes(otherPub, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	key, err := AccountMappingKey("mainnet", otherAddress)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := EncodeAccountMappingValue(accountID)
	forged := signedAccountRecord(t, priv, key, value, 1)
	if _, err := idx.PutLocal(forged); err == nil {
		t.Fatal("mapping to an unrelated address was accepted")
	}
}
