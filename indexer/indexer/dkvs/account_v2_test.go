package dkvs

import (
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

func signedAccountRecordV2(t *testing.T, priv *btcec.PrivateKey, key string, value []byte, seq uint64) *wire.DKVSRecord {
	t.Helper()
	record, err := NewRecordV2(key, value, RecordOptions{
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

func TestAccountIDV2AndAddressMapping(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := AccountIDV2(priv.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	if len(accountID) != 64 || accountID != strings.ToLower(accountID) {
		t.Fatalf("unexpected account ID %q", accountID)
	}
	pubKey, err := AccountPubKeyV2(accountID)
	if err != nil {
		t.Fatal(err)
	}
	address, err := P2TRAddressFromPubKeyBytes(pubKey, &chaincfg.TestNet4Params)
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
	record := signedAccountRecordV2(t, priv, key, value, 1)
	if len(record.PubKey) != 0 {
		t.Fatal("version 2 record repeated the signer public key")
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

func TestAccountV2PersonalAndMailboxPermissions(t *testing.T) {
	idx := testIndexer(t)
	sender, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderID, _ := AccountIDV2(sender.PubKey().SerializeCompressed())
	receiverID, _ := AccountIDV2(receiver.PubKey().SerializeCompressed())

	personalKey, err := PersonalKeyV2(senderID, "rgb11/receive")
	if err != nil {
		t.Fatal(err)
	}
	personal := signedAccountRecordV2(t, sender, personalKey, []byte{1, 1}, 1)
	if updated, err := idx.PutLocal(personal); err != nil || !updated {
		t.Fatalf("put personal updated=%v err=%v", updated, err)
	}

	mailKey, err := MailMsgKey(receiverID, senderID, "r11-transfer")
	if err != nil {
		t.Fatal(err)
	}
	mail := signedAccountRecordV2(t, sender, mailKey, []byte("ciphertext"), 1)
	if updated, err := idx.PutLocal(mail); err != nil || !updated {
		t.Fatalf("put mail updated=%v err=%v", updated, err)
	}
	if err := VerifyAccountRecordForClient(mail, RecordVerificationOptions{ExpectedKey: mailKey}); err != nil {
		t.Fatal(err)
	}

	forged := signedAccountRecordV2(t, receiver, mailKey, []byte("forged"), 2)
	if _, err := idx.PutLocal(forged); err == nil {
		t.Fatal("mail record signed by the wrong account was accepted")
	}
}

func TestAccountV2RejectsForgedAddressMapping(t *testing.T) {
	idx := testIndexer(t)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	accountID, _ := AccountIDV2(priv.PubKey().SerializeCompressed())
	otherID, _ := AccountIDV2(other.PubKey().SerializeCompressed())
	otherPub, _ := AccountPubKeyV2(otherID)
	otherAddress, err := P2TRAddressFromPubKeyBytes(otherPub, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatal(err)
	}
	key, err := AccountMappingKey("mainnet", otherAddress)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := EncodeAccountMappingValue(accountID)
	forged := signedAccountRecordV2(t, priv, key, value, 1)
	if _, err := idx.PutLocal(forged); err == nil {
		t.Fatal("mapping to an unrelated address was accepted")
	}
}

func TestAccountV2KeepsVersionOneCompatibility(t *testing.T) {
	idx := testIndexer(t)
	legacy := signedPersonalRecord(t, 1, "legacy", 0)
	if updated, err := idx.PutLocal(legacy); err != nil || !updated {
		t.Fatalf("legacy put updated=%v err=%v", updated, err)
	}
	if err := VerifySignature(legacy); err != nil {
		t.Fatalf("legacy signature failed: %v", err)
	}
}
