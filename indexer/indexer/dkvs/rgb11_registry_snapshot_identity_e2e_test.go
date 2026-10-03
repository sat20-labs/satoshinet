package dkvs

import (
	"errors"
	"fmt"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
)

// A registry value is type + ContractID, not just ContractID. Both snapshot
// entrypoints must preserve the complete canonical name and reject atomically.
// Registry conflicts must not be hidden by ordinary DKVS merge ordering.
func TestRGB11RegistrySnapshotPreservesCompleteAssetIdentityE2E(t *testing.T) {
	for _, mode := range []string{"path", "full"} {
		for _, incomingHeight := range []uint64{98, 100} {
			t.Run(fmt.Sprintf("%s/incoming_height_%d", mode, incomingHeight), func(t *testing.T) {
				coreKey, err := btcec.NewPrivateKey()
				if err != nil {
					t.Fatal(err)
				}
				target := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
				source := testIndexerWithConfig(t, rgb11RegistryTestConfig(coreKey))
				contractID := rgb11TestContractID(9101)
				nextID := rgb11TestContractID(9102)
				original := rgb11SignedRegistryRecordType(t, coreKey, "alice", "USD", indexercommon.ASSET_TYPE_FT, 1, contractID, 99)
				if updated, err := target.PutInternalRGB11Registry(original); err != nil || !updated {
					t.Fatalf("seed original: updated=%v err=%v", updated, err)
				}
				beforeHash := RecordHash(original)
				replacement := rgb11SignedRegistryRecordType(t, coreKey, "alice", "USD", indexercommon.ASSET_TYPE_NFT, 1, contractID, incomingHeight)
				// Exercise both sides of merge ordering deterministically, rather
				// than relying on random signatures/hashes at an equal height.
				if (CompareRecords(original, replacement) < 0) != (incomingHeight > original.IssueHeight) {
					t.Fatal("fixture must exercise the requested merge order")
				}
				if _, err := target.PutInternalRGB11Registry(replacement); !errors.Is(err, ErrWriteConflict) {
					t.Fatalf("control: local type replacement must be rejected, got %v", err)
				}
				if _, err := source.PutInternalRGB11Registry(replacement); err != nil {
					t.Fatal(err)
				}
				second := rgb11SignedRegistryRecordType(t, coreKey, "alice", "USD", indexercommon.ASSET_TYPE_FT, 2, nextID, 100)
				if _, err := source.PutInternalRGB11Registry(second); err != nil {
					t.Fatal(err)
				}

				var applied int
				switch mode {
				case "path":
					snapshot, snapshotErr := source.GetPathSnapshot("/rgb11/alice/usd")
					if snapshotErr != nil {
						t.Fatal(snapshotErr)
					}
					applied, err = target.ApplyPathSnapshot(snapshot)
				case "full":
					snapshot, snapshotErr := source.Snapshot()
					if snapshotErr != nil {
						t.Fatal(snapshotErr)
					}
					applied, err = target.ApplySnapshot(snapshot)
				}
				if err == nil || applied != 0 {
					t.Errorf("snapshot must reject type substitution atomically: applied=%d err=%v", applied, err)
				}
				stored, readErr := target.Get(original.Key)
				if readErr != nil || stored == nil || RecordHash(stored) != beforeHash {
					t.Errorf("snapshot changed the original signed registry record: record=%+v err=%v", stored, readErr)
				}
				registration, lookupErr := target.LookupRGB11Contract(contractID)
				if lookupErr != nil || registration == nil || registration.AssetName != "rgb11:f:usd@alice" {
					t.Errorf("snapshot renamed the existing asset: registration=%+v err=%v", registration, lookupErr)
				}
				if registration, lookupErr := target.LookupRGB11Contract(nextID); !errors.Is(lookupErr, ErrRecordNotFound) {
					t.Errorf("rejected snapshot partially appended ordinal 2: registration=%+v err=%v", registration, lookupErr)
				}
				if count, countErr := target.RGB11RegistryCount("alice", "USD"); countErr != nil || count != 1 {
					t.Errorf("rejected snapshot changed ordinal count: count=%d err=%v", count, countErr)
				}
			})
		}
	}
}
