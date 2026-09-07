package dkvs

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

func signedBindingRecord(t *testing.T, accountPriv *btcec.PrivateKey, corePriv *btcec.PrivateKey, seq uint64) *wire.DKVSRecord {
	t.Helper()
	accountID := AccountID(schnorr.SerializePubKey(accountPriv.PubKey()))
	address, err := P2TRAddressFromPubKeyBytes(accountPriv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	key, err := AccountMappingKey("testnet4", address)
	if err != nil {
		t.Fatal(err)
	}
	value, err := EncodeAccountServiceDescriptor(AccountServiceDescriptor{
		AccountID: accountID, CoreNodeID: hex.EncodeToString(corePriv.PubKey().SerializeCompressed()),
		Capabilities: AccountServiceCapabilityRGB11Direct,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewAccountRecord(key, value, RecordOptions{Seq: seq})
	if err != nil {
		t.Fatal(err)
	}
	hash := SigningHash(record)
	sig, err := schnorr.Sign(accountPriv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = sig.Serialize()
	return record
}

func TestAccountCoreNodeBindingCanonicalNoFee(t *testing.T) {
	accountPriv, _ := btcec.NewPrivateKey()
	corePriv, _ := btcec.NewPrivateKey()
	record := signedBindingRecord(t, accountPriv, corePriv, 0)
	_, _, descriptor, err := ValidateAccountMappingBindingRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.AccountID == "" || descriptor.CoreNodeID != hex.EncodeToString(corePriv.PubKey().SerializeCompressed()) {
		t.Fatalf("unexpected binding %s %s", descriptor.AccountID, descriptor.CoreNodeID)
	}
	parsed, _ := ParseKey(record.Key)
	if err := verifyFeeProofWith(nil, record, parsed); err != nil {
		t.Fatalf("binding must not require fee: %v", err)
	}

	bad := *record
	bad.Value = []byte("not-a-core")
	if _, _, _, err := ValidateAccountMappingBindingRecord(&bad); err == nil {
		t.Fatal("malformed core id accepted")
	}
}

func TestAccountServiceDescriptorCanonicalExtensions(t *testing.T) {
	accountPriv, _ := btcec.NewPrivateKey()
	corePriv, _ := btcec.NewPrivateKey()
	accountID := AccountID(schnorr.SerializePubKey(accountPriv.PubKey()))
	descriptor := AccountServiceDescriptor{
		AccountID:    accountID,
		CoreNodeID:   hex.EncodeToString(corePriv.PubKey().SerializeCompressed()),
		Capabilities: AccountServiceCapabilityRGB11Direct | 1<<9,
		Extensions: []AccountServiceExtension{
			{Type: 1, Value: []byte("first")},
			{Type: 7, Value: []byte{0, 1, 2}},
		},
	}
	encoded, err := EncodeAccountServiceDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeAccountServiceDescriptor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Version != AccountServiceDescriptorVersion || decoded.AccountID != descriptor.AccountID ||
		decoded.CoreNodeID != descriptor.CoreNodeID || decoded.Capabilities != descriptor.Capabilities ||
		len(decoded.Extensions) != 2 || decoded.Extensions[1].Type != 7 ||
		!bytes.Equal(decoded.Extensions[1].Value, descriptor.Extensions[1].Value) {
		t.Fatalf("descriptor round trip mismatch: %+v", decoded)
	}
	reencoded, err := EncodeAccountServiceDescriptor(*decoded)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		t.Fatalf("descriptor encoding is not canonical: %x %x err=%v", encoded, reencoded, err)
	}

	descriptor.Extensions = []AccountServiceExtension{
		{Type: 7, Value: []byte("later")}, {Type: 1, Value: []byte("earlier")},
	}
	if _, err := EncodeAccountServiceDescriptor(descriptor); err == nil {
		t.Fatal("out-of-order extensions were accepted")
	}
	descriptor.Extensions = []AccountServiceExtension{{Type: 1, Value: make([]byte, maxAccountServiceExtensionSize+1)}}
	if _, err := EncodeAccountServiceDescriptor(descriptor); err == nil {
		t.Fatal("oversized extension was accepted")
	}
}
