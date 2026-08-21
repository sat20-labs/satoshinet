package dkvs

import (
	"errors"
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
		Seq: seq, TTL: 100,
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

func signedFreeAccountRecord(t *testing.T, priv *btcec.PrivateKey, key string, value []byte,
	seq uint64) *wire.DKVSRecord {

	t.Helper()
	record := signedAccountRecord(t, priv, key, value, seq)
	parsed, err := ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewFreeLocalFeeProof(key, parsed.Namespace, wire.MaxDKVSRecordSize,
		RecordExpiryHeight(record))
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = EncodeFeeProof(proof)
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

func accountMappingCapabilityRecords(t *testing.T, signer *btcec.PrivateKey,
	mappingAddress string) (*wire.DKVSRecord, *wire.DKVSRecord) {

	t.Helper()
	accountID, err := CanonicalAccountID(signer.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	mappingKey, err := AccountMappingKey("testnet4", mappingAddress)
	if err != nil {
		t.Fatal(err)
	}
	mappingValue, err := EncodeAccountMappingValue(accountID)
	if err != nil {
		t.Fatal(err)
	}
	capabilityKey, err := AccountPersonalKey(accountID, "rgb11/receive")
	if err != nil {
		t.Fatal(err)
	}
	return signedFreeAccountRecord(t, signer, mappingKey, mappingValue, 1),
		signedFreeAccountRecord(t, signer, capabilityKey, []byte{1, 3}, 1)
}

func accountAddress(t *testing.T, priv *btcec.PrivateKey) string {
	t.Helper()
	address, err := P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(),
		&chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func requireAccountBatchAbsent(t *testing.T, idx *Indexer, records ...*wire.DKVSRecord) {
	t.Helper()
	for _, record := range records {
		if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
			t.Fatalf("record %s was partially written: %v", record.Key, err)
		}
	}
}

func accountBatchTestIndexer(t *testing.T) *Indexer {
	t.Helper()
	return testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		FreeLocalCache: DefaultFreeLocalCachePolicy(),
		FeeVerifier:    JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight:  func() uint64 { return 1 },
	})
}

func TestAccountMappingAndCapabilityBatchSameOwner(t *testing.T) {
	idx := accountBatchTestIndexer(t)
	owner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mapping, capability := accountMappingCapabilityRecords(t, owner, accountAddress(t, owner))

	applied, err := idx.PutLocalBatchCAS([]CASMutation{
		{Record: mapping, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: capability, Precondition: WritePrecondition{ExpectAbsent: true}},
	})
	if err != nil || applied != 2 {
		t.Fatalf("same-account batch applied=%d err=%v", applied, err)
	}
	for _, record := range []*wire.DKVSRecord{mapping, capability} {
		if _, err := idx.Get(record.Key); err != nil {
			t.Fatalf("record %s missing after atomic write: %v", record.Key, err)
		}
	}
}

func TestAccountMappingAndCapabilityBatchRejectsCrossAccount(t *testing.T) {
	idx := accountBatchTestIndexer(t)
	mappingOwner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	capabilityOwner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mapping, _ := accountMappingCapabilityRecords(t, mappingOwner, accountAddress(t, mappingOwner))
	_, capability := accountMappingCapabilityRecords(t, capabilityOwner, accountAddress(t, capabilityOwner))

	if _, err := idx.PutLocalBatchCAS([]CASMutation{
		{Record: mapping, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: capability, Precondition: WritePrecondition{ExpectAbsent: true}},
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("cross-account batch err=%v", err)
	}
	requireAccountBatchAbsent(t, idx, mapping, capability)
}

func TestAccountMappingAndCapabilityBatchRejectsForgedMapping(t *testing.T) {
	idx := accountBatchTestIndexer(t)
	owner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mapping, capability := accountMappingCapabilityRecords(t, owner, accountAddress(t, other))

	if _, err := idx.PutLocalBatchCAS([]CASMutation{
		{Record: mapping, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: capability, Precondition: WritePrecondition{ExpectAbsent: true}},
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("forged mapping batch err=%v", err)
	}
	requireAccountBatchAbsent(t, idx, mapping, capability)
}

func TestAccountMappingAndCapabilityBatchFailureIsAtomic(t *testing.T) {
	idx := accountBatchTestIndexer(t)
	owner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mapping, capability := accountMappingCapabilityRecords(t, owner, accountAddress(t, owner))
	capability.Signature[0] ^= 0xff

	if _, err := idx.PutLocalBatchCAS([]CASMutation{
		{Record: mapping, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: capability, Precondition: WritePrecondition{ExpectAbsent: true}},
	}); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("invalid capability signature batch err=%v", err)
	}
	requireAccountBatchAbsent(t, idx, mapping, capability)
}

func TestNewAccountRecordPreservesIssueHeight(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := CanonicalAccountID(priv.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	key, err := AccountPersonalKey(accountID, "account/recovery/item")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewAccountRecord(key, []byte("value"), RecordOptions{
		Seq: 1, IssueHeight: 1234, TTL: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.IssueHeight != 1234 || RecordExpiryHeight(record) != 1334 {
		t.Fatalf("issue=%d expiry=%d", record.IssueHeight, RecordExpiryHeight(record))
	}
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
