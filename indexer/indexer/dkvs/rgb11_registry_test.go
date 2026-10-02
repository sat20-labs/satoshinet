package dkvs

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func rgb11TestContractID(n int) string { return fmt.Sprintf("%064x", n) }

func rgb11SignedRegistryRecord(t *testing.T, signer *btcec.PrivateKey, provider, ticker string,
	contractIDs []string, seq, height uint64) *Record {

	t.Helper()
	key, err := RGB11RegistryKey(provider, ticker)
	if err != nil {
		t.Fatal(err)
	}
	value, err := EncodeRGB11RegistryContracts(contractIDs)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewSignedRecord(signer, key, value, RecordOptions{Seq: seq, IssueHeight: height})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestRGB11RegistryCoreNodeInternalWriteAndDKVSSync(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	source := testIndexerWithConfig(t, Config{CurrentHeight: func() uint64 { return 100 }})

	first := rgb11SignedRegistryRecord(t, coreKey, "alice", "USDT",
		[]string{rgb11TestContractID(1)}, 1, 100)

	if _, err := source.PutLocal(first); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ordinary DKVS put created RGB11 registry: %v", err)
	}
	if updated, err := source.PutInternalRGB11Registry(first); err != nil || !updated {
		t.Fatalf("CoreNode internal first write updated=%v err=%v", updated, err)
	}

	second := rgb11SignedRegistryRecord(t, coreKey, "alice", "USDT",
		[]string{rgb11TestContractID(1), rgb11TestContractID(2)}, 2, 100)
	if updated, err := source.PutInternalRGB11Registry(second); err != nil || !updated {
		t.Fatalf("CoreNode internal append updated=%v err=%v", updated, err)
	}
	if updated, err := source.PutInternalRGB11Registry(second); err != nil || updated {
		t.Fatalf("idempotent retry updated=%v err=%v", updated, err)
	}

	reg, err := source.LookupRGB11Contract(rgb11TestContractID(2))
	if err != nil {
		t.Fatal(err)
	}
	if reg.AssetName != "rgb11:f:usdt_2@alice" || reg.Ordinal != 2 || reg.ProviderDID != "alice" {
		t.Fatalf("registration=%+v", reg)
	}

	// RGB11 naming is DKVS state. A fresh node learns it through DKVS path
	// synchronization; no SatoshiNet block replay participates in rebuilding it.
	snapshot, err := source.GetPathSnapshot("/rgb11/alice")
	if err != nil {
		t.Fatal(err)
	}
	target := testIndexerWithConfig(t, Config{CurrentHeight: func() uint64 { return 100 }})
	if applied, err := target.ApplyPathSnapshot(snapshot); err != nil || applied != 1 {
		t.Fatalf("apply RGB11 DKVS snapshot applied=%d err=%v", applied, err)
	}
	targetReg, err := target.LookupRGB11Contract(rgb11TestContractID(2))
	if err != nil || targetReg.AssetName != reg.AssetName || targetReg.Ordinal != reg.Ordinal {
		t.Fatalf("synced registration=%+v err=%v", targetReg, err)
	}
}

func TestRGB11RegistryRejectsMutationAndContractIDReuse(t *testing.T) {
	coreKey, _ := btcec.NewPrivateKey()
	idx := testIndexerWithConfig(t, Config{CurrentHeight: func() uint64 { return 100 }})

	first := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD",
		[]string{rgb11TestContractID(10)}, 1, 100)
	if _, err := idx.PutInternalRGB11Registry(first); err != nil {
		t.Fatal(err)
	}

	reordered := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD",
		[]string{rgb11TestContractID(11), rgb11TestContractID(10)}, 2, 100)
	if _, err := idx.PutInternalRGB11Registry(reordered); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("registry reorder accepted: %v", err)
	}

	otherNamespace := rgb11SignedRegistryRecord(t, coreKey, "company", "USD",
		[]string{rgb11TestContractID(10)}, 1, 100)
	if _, err := idx.PutInternalRGB11Registry(otherNamespace); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("same ContractID registered in a second namespace: %v", err)
	}

	reg, err := idx.LookupRGB11Contract(rgb11TestContractID(10))
	if err != nil || reg.AssetName != "rgb11:f:usd@alice" {
		t.Fatalf("mapping changed: %+v err=%v", reg, err)
	}
}

func TestRGB11RegistrySequenceAndKeyBoundaries(t *testing.T) {
	coreKey, _ := btcec.NewPrivateKey()
	idx := testIndexerWithConfig(t, Config{CurrentHeight: func() uint64 { return 100 }})

	if _, err := RGB11RegistryKey("abcdefghijk", "USD"); err == nil {
		t.Fatal("11-character DID accepted")
	}
	if _, err := RGB11RegistryKey("alice", "USD_2"); err == nil {
		t.Fatal("reserved ticker suffix accepted")
	}
	if _, err := EncodeRGB11RegistryContracts([]string{rgb11TestContractID(1), rgb11TestContractID(1)}); err == nil {
		t.Fatal("duplicate ContractID accepted")
	}

	badSeq := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD",
		[]string{rgb11TestContractID(20)}, 2, 100)
	if _, err := idx.PutInternalRGB11Registry(badSeq); !errors.Is(err, ErrInvalidSequence) {
		t.Fatalf("new registry accepted non-1 sequence: %v", err)
	}

	first := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD",
		[]string{rgb11TestContractID(20)}, 1, 100)
	if _, err := idx.PutInternalRGB11Registry(first); err != nil {
		t.Fatal(err)
	}
	skipSeq := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD",
		[]string{rgb11TestContractID(20), rgb11TestContractID(21)}, 3, 100)
	if _, err := idx.PutInternalRGB11Registry(skipSeq); !errors.Is(err, ErrInvalidSequence) {
		t.Fatalf("registry accepted sequence gap: %v", err)
	}
}
