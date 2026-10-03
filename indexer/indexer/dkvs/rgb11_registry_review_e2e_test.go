package dkvs

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/wire"
)

// These tests exercise real DKVS signing, storage and node-to-node snapshot
// application. They need no public network, STP transaction, private build tag
// or environment switch. The existing RGB11 workflow's test regex includes them.
func rgb11ReviewNodeKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func rgb11ReviewNodePut(t *testing.T, idx *Indexer, record *wire.DKVSRecord) {
	t.Helper()
	if updated, err := idx.PutInternalRGB11Registry(record); err != nil || !updated {
		t.Fatalf("insert %s: updated=%v err=%v", record.Key, updated, err)
	}
}

func rgb11ReviewNodeSnapshot(t *testing.T, records []*wire.DKVSRecord) *Snapshot {
	t.Helper()
	checkpoint, err := CheckpointFromRecords(records, 100)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &Snapshot{Checkpoint: checkpoint, Records: records, CreatedAt: currentUnixMilli()}
	// A rejection below must concern registry invariants, not an accidentally
	// stale or invalid checkpoint hash in the test fixture.
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("self-consistent snapshot fixture rejected: %v", err)
	}
	return snapshot
}

func rgb11ReviewAssertUnchanged(t *testing.T, idx *Indexer, original, absent *wire.DKVSRecord) {
	t.Helper()
	got, err := idx.Get(original.Key)
	if err != nil || got == nil || RecordHash(got) != RecordHash(original) {
		t.Errorf("immutable record changed: got=%+v err=%v", got, err)
	}
	_, id, err := DecodeRGB11RegistryValue(original.Value)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := idx.LookupRGB11Contract(id)
	if err != nil || registration == nil || registration.AssetType != "f" ||
		registration.AssetName != "rgb11:f:usd@alice" || registration.Ordinal != 1 {
		t.Errorf("frozen registration changed: got=%+v err=%v", registration, err)
	}
	if count, err := idx.RGB11RegistryCount("alice", "USD"); err != nil || count != 1 {
		t.Errorf("rejected snapshot changed namespace: count=%d err=%v", count, err)
	}
	if absent != nil {
		if got, err := idx.Get(absent.Key); !errors.Is(err, ErrRecordNotFound) || got != nil {
			t.Errorf("rejected batch partially committed %s: got=%+v err=%v", absent.Key, got, err)
		}
	}
}

// Pick a second type accepted by the registry under test, not an assumed NFT
// encoding. The strict SDK NFT encoding requirement is independently tested in
// the wallet PR; accepting "n" here must never make that regression pass.
func rgb11ReviewAlternateStoredType(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{indexercommon.ASSET_TYPE_NFT, indexercommon.ASSET_TYPE_NS} {
		if _, err := EncodeRGB11RegistryValue(candidate, rgb11TestContractID(899)); err == nil {
			return candidate
		}
	}
	t.Fatal("registry exposes no second supported type for immutability testing")
	return ""
}

func TestRGB11RegistryReviewE2ERejectTypeRewriteInMirror(t *testing.T) {
	// The PR already covers path/full snapshots. Cover the remaining
	// authoritative-mirror entrypoint without duplicating those regressions.
	core := rgb11ReviewNodeKey(t)
	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(core))
	source := testIndexerWithConfig(t, rgb11RegistryTestConfig(core))
	id := rgb11TestContractID(900)
	original := rgb11SignedRegistryRecordType(t, core, "alice", "USD", "f", 1, id, 99)
	replacement := rgb11SignedRegistryRecordType(t, core, "alice", "USD", rgb11ReviewAlternateStoredType(t), 1, id, 100)
	trailing := rgb11SignedRegistryRecord(t, core, "alice", "USD", 2, rgb11TestContractID(901), 100)
	rgb11ReviewNodePut(t, target, original)
	rgb11ReviewNodePut(t, source, replacement)
	rgb11ReviewNodePut(t, source, trailing)
	if CompareRecords(original, replacement) >= 0 {
		t.Fatal("fixture must select the newer conflicting record in generic merge order")
	}
	records := []*wire.DKVSRecord{replacement, trailing}
	root, err := DirectoryRootFromRecords(records, 100)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := target.ApplyMirror(
		[]Subscription{{Type: SubscriptionPrefix, Target: "/rgb11/alice/usd"}}, records, root)
	if err == nil || applied != 0 {
		t.Errorf("mirror changed immutable type/ContractID: applied=%d err=%v", applied, err)
	}
	rgb11ReviewAssertUnchanged(t, target, original, trailing)
}

func TestRGB11RegistryReviewE2ERejectOrdinalGapsInAtomicSnapshots(t *testing.T) {
	for _, mode := range []string{"full_snapshot", "authoritative_mirror"} {
		for _, existingFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing_first_%t", mode, existingFirst), func(t *testing.T) {
				core := rgb11ReviewNodeKey(t)
				target := testIndexerWithConfig(t, rgb11RegistryTestConfig(core))
				source := testIndexerWithConfig(t, rgb11RegistryTestConfig(core))
				first := rgb11SignedRegistryRecord(t, core, "alice", "USD", 1, rgb11TestContractID(910), 100)
				second := rgb11SignedRegistryRecord(t, core, "alice", "USD", 2, rgb11TestContractID(911), 100)
				third := rgb11SignedRegistryRecord(t, core, "alice", "USD", 3, rgb11TestContractID(912), 100)
				for _, record := range []*wire.DKVSRecord{first, second, third} {
					rgb11ReviewNodePut(t, source, record)
				}
				// All records are genuinely signed and accepted by a source node.
				// Omit ordinal 2 in transit, then create a consistent checkpoint.
				records := []*wire.DKVSRecord{first, third}
				if existingFirst {
					rgb11ReviewNodePut(t, target, first)
					if mode == "full_snapshot" {
						// A merge snapshot may omit records already at the target.
						records = []*wire.DKVSRecord{third}
					}
				}
				var applied int
				var err error
				if mode == "full_snapshot" {
					applied, err = target.ApplySnapshot(rgb11ReviewNodeSnapshot(t, records))
				} else {
					root, rootErr := DirectoryRootFromRecords(records, 100)
					if rootErr != nil {
						t.Fatal(rootErr)
					}
					applied, err = target.ApplyMirror(
						[]Subscription{{Type: SubscriptionPrefix, Target: "/rgb11/alice/usd"}}, records, root)
				}
				if err == nil || applied != 0 {
					t.Errorf("atomic snapshot accepted missing ordinal: applied=%d err=%v", applied, err)
				}
				if existingFirst {
					rgb11ReviewAssertUnchanged(t, target, first, third)
				} else {
					for _, record := range []*wire.DKVSRecord{first, second, third} {
						if got, err := target.Get(record.Key); !errors.Is(err, ErrRecordNotFound) || got != nil {
							t.Errorf("invalid snapshot partially initialized target: key=%s got=%+v err=%v", record.Key, got, err)
						}
					}
					if count, err := target.RGB11RegistryCount("alice", "USD"); err != nil || count != 0 {
						t.Errorf("rejected import must leave empty namespace: count=%d err=%v", count, err)
					}
				}
			})
		}
	}
}

func TestRGB11RegistryReviewE2EAcceptContiguousPartialMerge(t *testing.T) {
	core := rgb11ReviewNodeKey(t)
	target := testIndexerWithConfig(t, rgb11RegistryTestConfig(core))
	first := rgb11SignedRegistryRecord(t, core, "alice", "USD", 1, rgb11TestContractID(920), 100)
	secondType := rgb11ReviewAlternateStoredType(t)
	second := rgb11SignedRegistryRecordType(t, core, "alice", "USD", secondType, 2, rgb11TestContractID(921), 100)
	rgb11ReviewNodePut(t, target, first)
	// Continuity must be checked over existing + incoming state, not incoming
	// records alone: a valid partial merge does not need to resend ordinal 1.
	snapshot := rgb11ReviewNodeSnapshot(t, []*wire.DKVSRecord{second})
	if applied, err := target.ApplySnapshot(snapshot); err != nil || applied != 1 {
		t.Fatalf("contiguous partial merge rejected: applied=%d err=%v", applied, err)
	}
	if applied, err := target.ApplySnapshot(snapshot); err != nil || applied != 0 {
		t.Fatalf("idempotent partial merge: applied=%d err=%v", applied, err)
	}
	got, err := target.LookupRGB11Contract(rgb11TestContractID(921))
	if err != nil || got == nil || got.AssetName != "rgb11:"+secondType+":usd_2@alice" || got.Ordinal != 2 {
		t.Fatalf("partial merge lost type/shared ordinal: got=%+v err=%v", got, err)
	}
	stored, err := target.Get(first.Key)
	if err != nil || stored == nil || RecordHash(stored) != RecordHash(first) {
		t.Fatalf("partial merge changed earlier immutable record: got=%+v err=%v", stored, err)
	}
}

func TestRGB11RegistryReviewE2EConcurrentOrdinal(t *testing.T) {
	core := rgb11ReviewNodeKey(t)
	idx := testIndexerWithConfig(t, rgb11RegistryTestConfig(core))
	records := []*wire.DKVSRecord{
		rgb11SignedRegistryRecord(t, core, "alice", "USD", 1, rgb11TestContractID(930), 100),
		rgb11SignedRegistryRecordType(t, core, "alice", "USD", rgb11ReviewAlternateStoredType(t), 1, rgb11TestContractID(931), 100),
	}
	type result struct {
		index   int
		updated bool
		err     error
	}
	results := make(chan result, len(records))
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index, record := range records {
		workers.Add(1)
		go func(index int, record *wire.DKVSRecord) {
			defer workers.Done()
			<-start
			updated, err := idx.PutInternalRGB11Registry(record)
			results <- result{index: index, updated: updated, err: err}
		}(index, record)
	}
	close(start)
	workers.Wait()
	close(results)
	winner, successes := -1, 0
	for result := range results {
		if result.err == nil && result.updated {
			winner, successes = result.index, successes+1
		} else if result.updated || !errors.Is(result.err, ErrWriteConflict) {
			t.Errorf("losing writer must report conflict without writing: %+v", result)
		}
	}
	if successes != 1 {
		t.Fatalf("same ordinal allocated more/less than once: successes=%d", successes)
	}
	stored, err := idx.Get(records[winner].Key)
	if err != nil || stored == nil || RecordHash(stored) != RecordHash(records[winner]) {
		t.Fatalf("stored ordinal does not match successful writer: got=%+v err=%v", stored, err)
	}
	loser := records[1-winner]
	_, loserID, err := DecodeRGB11RegistryValue(loser.Value)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := idx.LookupRGB11Contract(loserID); !errors.Is(err, ErrRecordNotFound) || got != nil {
		t.Fatalf("losing ContractID leaked into registry: got=%+v err=%v", got, err)
	}
	next := rgb11SignedRegistryRecord(t, core, "alice", "USD", 2, rgb11TestContractID(932), 100)
	rgb11ReviewNodePut(t, idx, next)
	if count, err := idx.RGB11RegistryCount("alice", "USD"); err != nil || count != 2 {
		t.Fatalf("failed writer consumed an ordinal: count=%d err=%v", count, err)
	}
}

func TestRGB11RegistryReviewE2EPrimaryDIDChangesDoNotRename(t *testing.T) {
	core, owner, nextOwner := rgb11ReviewNodeKey(t), rgb11ReviewNodeKey(t), rgb11ReviewNodeKey(t)
	ownerAddress, err := P2TRAddressFromPubKeyBytes(owner.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	nextAddress, err := P2TRAddressFromPubKeyBytes(nextOwner.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	identity := func(name, address string) DIDIdentity {
		return DIDIdentity{CanonicalName: name, OwnerAddresses: []string{address}, AddressParams: &chaincfg.TestNetParams, Active: true}
	}
	cfg := rgb11RegistryTestConfig(core)
	// Use an explicit, valid FREE_LOCAL storage proof for personal metadata;
	// this fixture does not exercise paid activation or the public RPC gate.
	cfg.AllowFreeLocal = true
	cfg.FeeVerifier = JSONFeeVerifier{AllowFreeLocal: true}
	cfg.FreeLocalCache = FreeLocalCachePolicy{
		Enabled: true, MaxTTL: 1000,
		MaxRecordsPerSigner: 10, MaxBytesPerSigner: 1 << 20,
		MaxTotalRecords: 100, MaxTotalBytes: 4 << 20,
	}
	cfg.Resolver = StaticDIDResolver{Names: map[string]DIDIdentity{
		"alice": identity("alice", ownerAddress), "alice2": identity("alice2", ownerAddress),
	}}
	idx := testIndexerWithConfig(t, cfg)
	key, err := AccountPrimaryDIDKey(AccountID(owner.PubKey().SerializeCompressed()))
	if err != nil {
		t.Fatal(err)
	}
	primaryRecord := func(value string, seq uint64, flags uint32) *wire.DKVSRecord {
		opts := RecordOptions{Seq: seq, IssueHeight: 100, TTL: 100, Flags: flags}
		if flags&FlagTombstone == 0 {
			proof, err := NewFreeLocalFeeProof(key, "personal", wire.MaxDKVSRecordSize, 200)
			if err != nil {
				t.Fatal(err)
			}
			opts.FeeProof, err = EncodeFeeProof(proof)
			if err != nil {
				t.Fatal(err)
			}
		}
		record, err := NewSignedRecord(owner, key, []byte(value), opts)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	if updated, err := idx.PutLocal(primaryRecord("alice", 1, 0)); err != nil || !updated {
		t.Fatalf("bind first primary DID: updated=%v err=%v", updated, err)
	}
	registered := rgb11SignedRegistryRecord(t, core, "alice", "USD", 1, rgb11TestContractID(940), 100)
	rgb11ReviewNodePut(t, idx, registered)
	secondPrimary := primaryRecord("alice2", 2, 0)
	if updated, err := idx.PutLocal(secondPrimary); err != nil || !updated {
		t.Fatalf("change primary DID: updated=%v err=%v", updated, err)
	}
	rgb11ReviewAssertUnchanged(t, idx, registered, nil)
	// Changing the L1 resolver models an actual DID ownership transfer. No
	// historical owner lookup is necessary to retain an existing RGB11 name.
	idx.SetResolver(StaticDIDResolver{Names: map[string]DIDIdentity{
		"alice": identity("alice", nextAddress), "alice2": identity("alice2", ownerAddress),
	}})
	if updated, err := idx.PutLocal(primaryRecord("alice", 3, 0)); !errors.Is(err, ErrPermissionDenied) || updated {
		t.Fatalf("old owner rebound transferred DID: updated=%v err=%v", updated, err)
	}
	stored, err := idx.Get(key)
	if err != nil || stored == nil || RecordHash(stored) != RecordHash(secondPrimary) {
		t.Fatalf("failed rebind changed personal parameter: got=%+v err=%v", stored, err)
	}
	rgb11ReviewAssertUnchanged(t, idx, registered, nil)
	if updated, err := idx.PutLocal(primaryRecord("", 3, FlagTombstone)); err != nil || !updated {
		t.Fatalf("owner cannot remove primary DID selection: updated=%v err=%v", updated, err)
	}
	rgb11ReviewAssertUnchanged(t, idx, registered, nil)
}
