package dkvs

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func rgb11TestContractID(n int) string { return fmt.Sprintf("%064x", n) }

func TestRGB11RegistryAppendOnlyAndDIDOwned(t *testing.T) {
	owner, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := StaticDIDResolver{Names: map[string]DIDIdentity{
		"alice": {
			CanonicalName: "alice",
			SigningKeys:   [][]byte{owner.PubKey().SerializeCompressed()},
			Active:        true,
		},
	}}
	idx := testIndexerWithConfig(t, Config{Resolver: resolver, CurrentHeight: func() uint64 { return 100 }})
	key, err := RGB11RegistryKey("alice", "USDT")
	if err != nil {
		t.Fatal(err)
	}

	value1, err := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(1)})
	if err != nil {
		t.Fatal(err)
	}
	record1, err := NewSignedRecord(owner, key, value1, RecordOptions{Seq: 1, IssueHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := idx.PutLocal(record1); err != nil || !updated {
		t.Fatalf("first registry write updated=%v err=%v", updated, err)
	}

	value2, err := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(1), rgb11TestContractID(2)})
	if err != nil {
		t.Fatal(err)
	}
	record2, err := NewSignedRecord(owner, key, value2, RecordOptions{Seq: 2, IssueHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := idx.PutLocal(record2); err != nil || !updated {
		t.Fatalf("append registry write updated=%v err=%v", updated, err)
	}

	badValue, _ := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(2), rgb11TestContractID(1)})
	bad, _ := NewSignedRecord(owner, key, badValue, RecordOptions{Seq: 3, IssueHeight: 100})
	if _, err := idx.PutLocal(bad); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("registry reorder accepted: %v", err)
	}

	unauthorized, _ := NewSignedRecord(other, key, value2, RecordOptions{Seq: 3, IssueHeight: 100})
	if _, err := idx.PutLocal(unauthorized); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("non-owner registry write accepted: %v", err)
	}

	reg, err := idx.LookupRGB11Contract(rgb11TestContractID(2))
	if err != nil {
		t.Fatal(err)
	}
	if reg.AssetName != "rgb11:f:usdt_2@alice" || reg.Ordinal != 2 || reg.ProviderDID != "alice" {
		t.Fatalf("registration=%+v", reg)
	}
}

func TestRGB11RegistryOwnerTransferContinuesOrdinal(t *testing.T) {
	oldOwner, _ := btcec.NewPrivateKey()
	newOwner, _ := btcec.NewPrivateKey()
	idx := testIndexerWithConfig(t, Config{
		Resolver: StaticDIDResolver{Names: map[string]DIDIdentity{
			"alice": {CanonicalName: "alice", SigningKeys: [][]byte{oldOwner.PubKey().SerializeCompressed()}, Active: true},
		}},
		CurrentHeight: func() uint64 { return 100 },
	})
	key, _ := RGB11RegistryKey("alice", "USD")
	first, _ := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(10)})
	r1, _ := NewSignedRecord(oldOwner, key, first, RecordOptions{Seq: 1, IssueHeight: 100})
	if _, err := idx.PutLocal(r1); err != nil {
		t.Fatal(err)
	}

	idx.SetResolver(StaticDIDResolver{Names: map[string]DIDIdentity{
		"alice": {CanonicalName: "alice", SigningKeys: [][]byte{newOwner.PubKey().SerializeCompressed()}, Active: true},
	}})
	next, _ := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(10), rgb11TestContractID(11)})
	r2, _ := NewSignedRecord(newOwner, key, next, RecordOptions{Seq: 2, IssueHeight: 100})
	if _, err := idx.PutLocal(r2); err != nil {
		t.Fatalf("new DID owner could not continue registry: %v", err)
	}
	oldAttempt, _ := NewSignedRecord(oldOwner, key, next, RecordOptions{Seq: 3, IssueHeight: 100})
	if _, err := idx.PutLocal(oldAttempt); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("old DID owner retained registry write access: %v", err)
	}
	reg, err := idx.LookupRGB11Contract(rgb11TestContractID(11))
	if err != nil || reg.Ordinal != 2 {
		t.Fatalf("ordinal continuity registration=%+v err=%v", reg, err)
	}
}

func TestRGB11RegistryKeyAndValueBoundaries(t *testing.T) {
	if _, err := RGB11RegistryKey("abcdefghijk", "USD"); err == nil {
		t.Fatal("11-character DID accepted")
	}
	if _, err := RGB11RegistryKey("alice", "USD_2"); err == nil {
		t.Fatal("reserved ticker suffix accepted")
	}
	if _, err := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(1), rgb11TestContractID(1)}); err == nil {
		t.Fatal("duplicate ContractID accepted")
	}
}
