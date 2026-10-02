package dkvs

import (
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestPrimaryDIDPersonalRecordValidation(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.PubKey().SerializeCompressed()
	accountID := AccountID(pub)
	key, err := AccountPrimaryDIDKey(accountID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	address, err := P2TRAddressFromPubKeyBytes(pub, &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	resolver := StaticDIDResolver{Names: map[string]DIDIdentity{
		"alice": {
			CanonicalName:  "alice",
			OwnerAddresses: []string{address},
			AddressParams: &chaincfg.TestNetParams,
			Active: true,
		},
	}}
	record := &wire.DKVSRecord{Key: key, PubKey: pub, Value: []byte("alice")}
	if err := validatePrimaryDIDRecordWith(record, parsed, resolver); err != nil {
		t.Fatalf("valid primary DID rejected: %v", err)
	}

	record.Value = []byte("abcdefghijk")
	if err := validatePrimaryDIDRecordWith(record, parsed, resolver); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("11-character DID accepted: %v", err)
	}

	record.Value = []byte("alice")
	wrongPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	wrongAddress, err := P2TRAddressFromPubKeyBytes(wrongPriv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	resolver.Names["alice"] = DIDIdentity{
		CanonicalName:  "alice",
		OwnerAddresses: []string{wrongAddress},
		AddressParams: &chaincfg.TestNetParams,
		Active: true,
	}
	if err := validatePrimaryDIDRecordWith(record, parsed, resolver); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("DID owned by another address accepted: %v", err)
	}

	record.Flags = FlagTombstone
	record.Value = nil
	if err := validatePrimaryDIDRecordWith(record, parsed, nil); err != nil {
		t.Fatalf("owner tombstone unexpectedly needs resolver: %v", err)
	}
}
