package dkvs

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func rgb11TestContractID(n int) string { return fmt.Sprintf("%064x", n) }

func rgb11RegistryTestConfig(coreKey *btcec.PrivateKey) Config {
	return Config{
		CurrentHeight:  func() uint64 { return 100 },
		SystemVerifier: StaticSystemVerifier{Keys: [][]byte{
			coreKey.PubKey().SerializeCompressed(),
		}},
	}
}

func rgb11SignedRegistryRecord(t *testing.T, signer *btcec.PrivateKey, provider, ticker string,
	ordinal uint64, contractID string, height uint64) *Record {

	t.Helper()
	key, err := RGB11RegistryKey(provider, ticker, ordinal)
	if err != nil {
		t.Fatal(err)
	}
	value, err := EncodeRGB11ContractID(contractID)
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewSignedRecord(signer, key, value, RecordOptions{Seq: 1, IssueHeight: height})
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
	source := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))

	first := rgb11SignedRegistryRecord(
		t, coreKey, "alice", "USDT", 1, rgb11TestContractID(1), 100,
	)
	ordinaryKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := rgb11SignedRegistryRecord(
		t, ordinaryKey, "alice", "USDT", 1, rgb11TestContractID(1), 100,
	)
	if _, err := source.PutLocal(unauthorized); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ordinary wallet signer created RGB11 registry: %v", err)
	}
	if updated, err := source.PutInternalRGB11Registry(first); err != nil || !updated {
		t.Fatalf("CoreNode internal first write updated=%v err=%v", updated, err)
	}

	second := rgb11SignedRegistryRecord(
		t, coreKey, "alice", "USDT", 2, rgb11TestContractID(2), 100,
	)
	if updated, err := source.PutInternalRGB11Registry(second); err != nil || !updated {
		t.Fatalf("CoreNode internal second write updated=%v err=%v", updated, err)
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

	// RGB11 naming is DKVS state. A fresh node learns the provider/ticker path
	// through DKVS synchronization; no SatoshiNet block replay rebuilds names.
	snapshot, err := source.GetPathSnapshot("/rgb11/alice/usdt")
	if err != nil {
		t.Fatal(err)
	}
	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	if applied, err := target.ApplyPathSnapshot(snapshot); err != nil || applied != 2 {
		t.Fatalf("apply RGB11 DKVS snapshot applied=%d err=%v", applied, err)
	}
	targetReg, err := target.LookupRGB11Contract(rgb11TestContractID(2))
	if err != nil || targetReg.AssetName != reg.AssetName || targetReg.Ordinal != reg.Ordinal {
		t.Fatalf("synced registration=%+v err=%v", targetReg, err)
	}
}

func TestRGB11RegistryRejectsMutationContractReuseAndOrdinalGaps(t *testing.T) {
	coreKey, _ := btcec.NewPrivateKey()
	idx := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))

	first := rgb11SignedRegistryRecord(
		t, coreKey, "alice", "USD", 1, rgb11TestContractID(10), 100,
	)
	if _, err := idx.PutInternalRGB11Registry(first); err != nil {
		t.Fatal(err)
	}

	conflict := rgb11SignedRegistryRecord(
		t, coreKey, "alice", "USD", 1, rgb11TestContractID(11), 100,
	)
	if _, err := idx.PutInternalRGB11Registry(conflict); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("immutable registration changed: %v", err)
	}

	gap := rgb11SignedRegistryRecord(
		t, coreKey, "alice", "USD", 3, rgb11TestContractID(11), 100,
	)
	if _, err := idx.PutInternalRGB11Registry(gap); !errors.Is(err, ErrInvalidSequence) {
		t.Fatalf("ordinal gap accepted: %v", err)
	}

	otherNamespace := rgb11SignedRegistryRecord(
		t, coreKey, "company", "USD", 1, rgb11TestContractID(10), 100,
	)
	if _, err := idx.PutInternalRGB11Registry(otherNamespace); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("same ContractID registered in a second namespace: %v", err)
	}

	reg, err := idx.LookupRGB11Contract(rgb11TestContractID(10))
	if err != nil || reg.AssetName != "rgb11:f:usd@alice" {
		t.Fatalf("mapping changed: %+v err=%v", reg, err)
	}
}

func TestRGB11RegistryKeyAndValueBoundaries(t *testing.T) {
	if _, err := RGB11RegistryKey("abcdefghijk", "USD", 1); err == nil {
		t.Fatal("11-character DID accepted")
	}
	if _, err := RGB11RegistryKey("alice", "USD_2", 1); err == nil {
		t.Fatal("reserved ticker suffix accepted")
	}
	if _, err := RGB11RegistryKey("alice", "USD", 0); err == nil {
		t.Fatal("zero ordinal accepted")
	}
	if _, err := EncodeRGB11ContractID(strings64("0")); err == nil {
		t.Fatal("zero ContractID accepted")
	}
}

func strings64(value string) string {
	result := ""
	for len(result) < 64 {
		result += value
	}
	return result[:64]
}

func TestRGB11RegistrySnapshotSupportsDoubleDigitOrdinals(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	source := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	for ordinal := uint64(1); ordinal <= 12; ordinal++ {
		record := rgb11SignedRegistryRecord(
			t, coreKey, "alice", "USD", ordinal, rgb11TestContractID(int(100+ordinal)), 100,
		)
		if _, err := source.PutInternalRGB11Registry(record); err != nil {
			t.Fatalf("ordinal %d: %v", ordinal, err)
		}
	}
	snapshot, err := source.GetPathSnapshot("/rgb11/alice/usd")
	if err != nil {
		t.Fatal(err)
	}
	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	if applied, err := target.ApplyPathSnapshot(snapshot); err != nil || applied != 12 {
		t.Fatalf("apply double-digit snapshot applied=%d err=%v", applied, err)
	}
	reg, err := target.LookupRGB11Contract(rgb11TestContractID(112))
	if err != nil || reg.Ordinal != 12 || reg.AssetName != "rgb11:f:usd_12@alice" {
		t.Fatalf("registration=%+v err=%v", reg, err)
	}
}

func TestRGB11RegistrySnapshotRejectsUnknownSigner(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	maliciousSource := testIndexerWithConfig(t, rgb11RegistryTestConfig(attacker))
	record := rgb11SignedRegistryRecord(
		t, attacker, "alice", "USD", 1, rgb11TestContractID(200), 100,
	)
	if _, err := maliciousSource.PutInternalRGB11Registry(record); err != nil {
		t.Fatal(err)
	}
	snapshot, err := maliciousSource.GetPathSnapshot("/rgb11/alice/usd")
	if err != nil {
		t.Fatal(err)
	}

	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	if _, err := target.ApplyPathSnapshot(snapshot); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("unknown signer snapshot accepted: %v", err)
	}
}

func TestRGB11RegistryRemoteRelayRequiresCoreSigner(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	record := rgb11SignedRegistryRecord(
		t, coreKey, "alice", "USD", 1, rgb11TestContractID(300), 100,
	)
	if updated, err := target.PutRemote(record); err != nil || !updated {
		t.Fatalf("authorized CoreNode relay updated=%v err=%v", updated, err)
	}

	attacker, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	forged := rgb11SignedRegistryRecord(
		t, attacker, "alice", "USD", 2, rgb11TestContractID(301), 100,
	)
	if _, err := target.PutRemote(forged); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("unknown signer relay accepted: %v", err)
	}
}

func TestRGB11RegistrySnapshotRejectsContractIDInAnotherPath(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	contractID := rgb11TestContractID(500)

	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	existing := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD", 1, contractID, 100)
	if _, err := target.PutInternalRGB11Registry(existing); err != nil {
		t.Fatal(err)
	}

	source := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	conflict := rgb11SignedRegistryRecord(t, coreKey, "company", "USD", 1, contractID, 100)
	if _, err := source.PutInternalRGB11Registry(conflict); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.GetPathSnapshot("/rgb11/company/usd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyPathSnapshot(snapshot); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("cross-path ContractID collision accepted: %v", err)
	}
}

func TestRGB11RegistryFullSnapshotRejectsContractIDInAnotherPath(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	contractID := rgb11TestContractID(501)

	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	existing := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD", 1, contractID, 100)
	if _, err := target.PutInternalRGB11Registry(existing); err != nil {
		t.Fatal(err)
	}

	source := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	conflict := rgb11SignedRegistryRecord(t, coreKey, "company", "USD", 1, contractID, 100)
	if _, err := source.PutInternalRGB11Registry(conflict); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplySnapshot(snapshot); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("full snapshot cross-path ContractID collision accepted: %v", err)
	}
}

func TestRGB11RegistryPathSnapshotCannotRemoveExistingOrdinal(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	first := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD", 1, rgb11TestContractID(600), 100)
	second := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD", 2, rgb11TestContractID(601), 100)
	if _, err := target.PutInternalRGB11Registry(first); err != nil {
		t.Fatal(err)
	}
	if _, err := target.PutInternalRGB11Registry(second); err != nil {
		t.Fatal(err)
	}

	source := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	if _, err := source.PutInternalRGB11Registry(first); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.GetPathSnapshot("/rgb11/alice/usd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyPathSnapshot(snapshot); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("snapshot removed existing ordinal: %v", err)
	}
}

func TestRGB11RegistryPathSnapshotCannotReplaceExistingContract(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	first := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD", 1, rgb11TestContractID(610), 100)
	if _, err := target.PutInternalRGB11Registry(first); err != nil {
		t.Fatal(err)
	}

	source := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
	replacement := rgb11SignedRegistryRecord(t, coreKey, "alice", "USD", 1, rgb11TestContractID(611), 100)
	if _, err := source.PutInternalRGB11Registry(replacement); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.GetPathSnapshot("/rgb11/alice/usd")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.ApplyPathSnapshot(snapshot); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("snapshot replaced immutable ContractID: %v", err)
	}
}
