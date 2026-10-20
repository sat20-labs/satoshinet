package dkvs

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestReviewFailedMaintenancePreservesConcurrentPaidSnapshot(t *testing.T) {
	for _, prefixVerified := range []bool{false, true} {
		t.Run(fmt.Sprintf("prefix_verified_%v", prefixVerified), func(t *testing.T) {
			original, priv, height := newAutopayMirrorIndexer(t, 10)
			record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
			if _, err := original.PutLocal(record); err != nil {
				t.Fatal(err)
			}
			unavailable := errors.New("maintenance payment query failed")
			gate := &retentionRefreshGate{LocalCacheAutopayFeeVerifier: original.feeVerifier.(LocalCacheAutopayFeeVerifier),
				key: record.Key, started: make(chan struct{}), release: make(chan struct{}), failure: unavailable}
			// Restart over the real stored record, with no initial payment cache.
			source := New(original.db, Config{EndpointID: "source", FeeVerifier: gate, CurrentHeight: func() uint64 { return height }})
			target, _, _ := newAutopayMirrorIndexerForPrivateKey(t, priv, 10)
			if _, err := target.PutLocal(record); err != nil {
				t.Fatal(err)
			}
			path, err := CollectionPathForKey(record.Key)
			if err != nil {
				t.Fatal(err)
			}
			gate.armed.Store(true)
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			finished := make(chan error, 1)
			go func() { finished <- source.RefreshPaidRetentionAt(height) }()
			select {
			case <-gate.started:
			case <-time.After(3 * time.Second):
				t.Fatal("maintenance did not reach payment query")
			}
			if prefixVerified {
				// The failure is already captured by maintenance. A real prefix
				// snapshot now installs a successful proof at the same height,
				// without changing record content, index height, or policy.
				if snapshot, err := source.GetPathSnapshot(path); err != nil || len(snapshot.Records) != 1 {
					t.Fatalf("concurrent paid prefix refresh: snapshot=%+v err=%v", snapshot, err)
				}
			}
			release()
			select {
			case err := <-finished:
				if !errors.Is(err, unavailable) {
					t.Fatalf("maintenance lost original error: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("maintenance did not finish")
			}
			retention, known := paidRetentionCacheFor(source).get(record.Key)
			if prefixVerified {
				if !known || !paidRetentionCurrent(retention, height) {
					t.Errorf("failed maintenance overwrote concurrent paid proof: known=%v retention=%+v", known, retention)
				}
			} else if known {
				t.Errorf("failed query became confirmed unpaid state: %+v", retention)
			}
			baseline, err := target.NetworkSyncBaseline(path)
			if err != nil {
				t.Fatal(err)
			}
			// The provider is now unavailable. A successful concurrent proof
			// must suffice; without one, a snapshot must report unknown state.
			gate.LocalCacheAutopayFeeVerifier.StateProvider = testAutopayStateProvider{err: unavailable}
			snapshot, err := source.GetPathSnapshot(path)
			if !prefixVerified && (!errors.Is(err, unavailable) || snapshot != nil) {
				t.Fatalf("unknown payment must fail snapshot: snapshot=%+v err=%v", snapshot, err)
			}
			if prefixVerified && err != nil {
				t.Fatalf("concurrent paid proof was lost: %v", err)
			}
			if err == nil {
				if len(snapshot.Records) != 1 {
					t.Errorf("successful snapshot omitted the paid record: %+v", snapshot)
				}
				if _, err := target.ApplyPathSnapshotFrom(snapshot, baseline); err != nil {
					t.Fatal(err)
				}
			}
			got, err := target.Get(record.Key)
			if err != nil || RecordHash(got) != RecordHash(record) {
				t.Fatalf("snapshot deleted or changed receiver's paid record: %v", err)
			}
		})
	}
}

func TestReviewCASUsesReceiptViewForRecordLifetime(t *testing.T) {
	for _, walletRPC := range []bool{false, true} {
		for _, operation := range []string{"expired-new", "expired-retry", "recreate", "expired-expected-hash", "recreate-wrong-seq"} {
			t.Run(fmt.Sprintf("wallet_%v/%s", walletRPC, operation), func(t *testing.T) {
				base, priv, _ := newAutopayMirrorIndexer(t, 10)
				core, err := btcec.NewPrivateKey()
				if err != nil {
					t.Fatal(err)
				}
				indexed, tip := uint64(1), uint64(1)
				idx := New(base.db, Config{EndpointID: hex.EncodeToString(core.PubKey().SerializeCompressed()),
					FeeVerifier: base.feeVerifier, AllowFreeLocal: true, FreeLocalCache: base.freeLocal,
					CurrentHeight: func() uint64 { return indexed }, TipHeight: func() uint64 { return tip }})
				old := signedFreeTTLPersonalRecord(t, priv, "expiry-review/item", 1, 1, 100, "old")
				if _, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{Record: old, Precondition: WritePrecondition{ExpectAbsent: true}}}, BatchCASOptions{}); err != nil {
					t.Fatal(err)
				}
				indexed, tip = 100, 100
				binding := signedBindingRecord(t, priv, core, 1)
				binding.IssueHeight = tip
				SignRecord(priv, binding)
				if _, err := idx.PutLocal(binding); err != nil {
					t.Fatal(err)
				}
				admission := &WalletRPCAdmission{Indexer: idx, IsCoreNode: func() bool { return true },
					CurrentBinding: func(string) (*wire.DKVSRecord, error) { return idx.Get(binding.Key) }}
				tip = 102 // Ordinary TTL=100 record expired at 101; index is still 100.
				path, err := CollectionPathForKey(old.Key)
				if err != nil {
					t.Fatal(err)
				}
				read, err := idx.ReadPrefix(path)
				if err != nil || len(read.Records) != 0 {
					t.Fatalf("tip view must report absence: read=%+v err=%v", read, err)
				}
				if _, err := idx.getRaw(old.Key); err != nil {
					t.Fatal("fixture must retain the expired physical record")
				}
				record := signedFreeTTLPersonalRecord(t, priv, "expiry-review/item", 1, tip, 100, "new")
				condition := WritePrecondition{ExpectAbsent: true}
				var wantErr error
				switch operation {
				case "expired-new":
					record = signedFreeTTLPersonalRecord(t, priv, "expiry-review/new", 1, 100, 1, "expired")
					wantErr = ErrExpiredRecord
				case "expired-retry":
					record = old
					wantErr = ErrExpiredRecord
				case "expired-expected-hash":
					hash := RecordHash(old)
					condition = WritePrecondition{ExpectedHash: &hash}
					record.Seq = 2
					SignRecord(priv, record)
					wantErr = ErrWriteConflict
				case "recreate-wrong-seq":
					record.Seq = 2
					SignRecord(priv, record)
					wantErr = ErrInvalidSequence
				}
				mutations := []CASMutation{{Record: record, Precondition: condition}}
				options := BatchCASOptions{EndpointID: idx.EndpointID(), RequestID: "expiry-review"}
				var result *WriteResult
				var writeErr error
				if walletRPC {
					prefixes, err := WriteMutationPrefixes(mutations)
					if err != nil {
						t.Fatal(err)
					}
					ctx := WalletWriteContext{EndpointID: options.EndpointID}
					for _, prefix := range prefixes {
						meta, err := idx.ActiveMetadata(context.Background(), ActiveScope{Prefix: prefix})
						if err != nil {
							t.Fatal(err)
						}
						ctx.Prefixes = append(ctx.Prefixes, PrefixGeneration{Prefix: prefix, Generation: meta.Generation})
					}
					digest, err := WalletWriteDigest(mutations, options, ctx)
					if err != nil {
						t.Fatal(err)
					}
					sig, err := schnorr.Sign(priv, digest[:])
					if err != nil {
						t.Fatal(err)
					}
					result, writeErr = admission.PutRecords(mutations, options, &WalletWriteAuthorization{Context: ctx, Signature: sig.Serialize()})
				} else {
					result, writeErr = idx.PutLocalBatchCASResultWithOptions(mutations, options)
				}
				err = writeErr
				if wantErr != nil {
					staleRetry := walletRPC && operation == "expired-retry" && errors.Is(err, ErrStaleEndpoint)
					if !errors.Is(err, wantErr) && !staleRetry {
						t.Fatalf("result=%+v err=%v, want %v", result, err, wantErr)
					}
					stored, err := idx.getRaw(old.Key)
					if err != nil || RecordHash(stored) != RecordHash(old) {
						t.Fatalf("rejected mutation changed the stored old record: %v", err)
					}
					if operation == "expired-new" {
						if _, err := idx.getRaw(record.Key); !errors.Is(err, ErrRecordNotFound) {
							t.Fatalf("expired new record was persisted: %v", err)
						}
					}
					return
				}
				if err != nil {
					t.Fatalf("recreate after tip-view absence: %v", err)
				}
				if result.Applied != 1 || result.ViewHeight != tip || len(result.Records) != 1 {
					t.Fatalf("invalid recreate receipt: %+v", result)
				}
				if err := VerifyRecordForClient(result.Records[0], RecordVerificationOptions{ExpectedKey: record.Key, Height: result.ViewHeight}); err != nil {
					t.Fatal(err)
				}
				got, err := idx.Get(record.Key)
				if err != nil || RecordHash(got) != RecordHash(record) {
					t.Fatalf("recreate did not replace physical old record: %v", err)
				}
			})
		}
	}
}
