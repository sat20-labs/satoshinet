package dkvs

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func rgb11TestContractID(n int) string { return fmt.Sprintf("%064x", n) }

func TestRGB11RegistryAppendOnlyAndSystemAuthorized(t *testing.T) {
	systemKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	other, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndexerWithConfig(t, Config{
		SystemVerifier: StaticSystemVerifier{Keys: [][]byte{systemKey.PubKey().SerializeCompressed()}},
		CurrentHeight:  func() uint64 { return 100 },
	})
	key, err := RGB11RegistryKey("alice", "USDT")
	if err != nil {
		t.Fatal(err)
	}

	value1, err := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(1)})
	if err != nil {
		t.Fatal(err)
	}
	record1, err := NewSignedRecord(systemKey, key, value1, RecordOptions{Seq: 1, IssueHeight: 100})
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
	record2, err := NewSignedRecord(systemKey, key, value2, RecordOptions{Seq: 2, IssueHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := idx.PutLocal(record2); err != nil || !updated {
		t.Fatalf("append registry write updated=%v err=%v", updated, err)
	}

	badValue, _ := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(2), rgb11TestContractID(1)})
	bad, _ := NewSignedRecord(systemKey, key, badValue, RecordOptions{Seq: 3, IssueHeight: 100})
	if _, err := idx.PutLocal(bad); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("registry reorder accepted: %v", err)
	}

	unauthorized, _ := NewSignedRecord(other, key, value2, RecordOptions{Seq: 3, IssueHeight: 100})
	if _, err := idx.PutLocal(unauthorized); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("non-system registry write accepted: %v", err)
	}

	reg, err := idx.LookupRGB11Contract(rgb11TestContractID(2))
	if err != nil {
		t.Fatal(err)
	}
	if reg.AssetName != "rgb11:f:usdt_2@alice" || reg.Ordinal != 2 || reg.ProviderDID != "alice" {
		t.Fatalf("registration=%+v", reg)
	}
}

func TestRGB11RegistryContractIDCannotMoveNamespaces(t *testing.T) {
	systemKey, _ := btcec.NewPrivateKey()
	idx := testIndexerWithConfig(t, Config{
		SystemVerifier: StaticSystemVerifier{Keys: [][]byte{systemKey.PubKey().SerializeCompressed()}},
		CurrentHeight:  func() uint64 { return 100 },
	})
	contractID := rgb11TestContractID(10)
	keyA, _ := RGB11RegistryKey("alice", "USD")
	valueA, _ := EncodeRGB11RegistryContracts([]string{contractID})
	r1, _ := NewSignedRecord(systemKey, keyA, valueA, RecordOptions{Seq: 1, IssueHeight: 100})
	if _, err := idx.PutLocal(r1); err != nil {
		t.Fatal(err)
	}

	keyB, _ := RGB11RegistryKey("company", "USD")
	valueB, _ := EncodeRGB11RegistryContracts([]string{contractID})
	r2, _ := NewSignedRecord(systemKey, keyB, valueB, RecordOptions{Seq: 1, IssueHeight: 100})
	if _, err := idx.PutLocal(r2); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("same ContractID registered in a second namespace: %v", err)
	}
	reg, err := idx.LookupRGB11Contract(contractID)
	if err != nil || reg.AssetName != "rgb11:f:usd@alice" {
		t.Fatalf("mapping changed: %+v err=%v", reg, err)
	}
}

func TestRGB11RegistryOrdinalContinuesIndependentOfDIDOwnership(t *testing.T) {
	systemKey, _ := btcec.NewPrivateKey()
	idx := testIndexerWithConfig(t, Config{
		SystemVerifier: StaticSystemVerifier{Keys: [][]byte{systemKey.PubKey().SerializeCompressed()}},
		CurrentHeight:  func() uint64 { return 100 },
	})
	key, _ := RGB11RegistryKey("alice", "USD")
	first, _ := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(20)})
	r1, _ := NewSignedRecord(systemKey, key, first, RecordOptions{Seq: 1, IssueHeight: 100})
	if _, err := idx.PutLocal(r1); err != nil {
		t.Fatal(err)
	}
	next, _ := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(20), rgb11TestContractID(21)})
	r2, _ := NewSignedRecord(systemKey, key, next, RecordOptions{Seq: 2, IssueHeight: 100})
	if _, err := idx.PutLocal(r2); err != nil {
		t.Fatal(err)
	}
	reg, err := idx.LookupRGB11Contract(rgb11TestContractID(21))
	if err != nil || reg.Ordinal != 2 || reg.AssetName != "rgb11:f:usd_2@alice" {
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
