package dkvs

import (
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestFinalReviewWalletRPCUsesTipHeight(t *testing.T) {
	for _, business := range []bool{false, true} {
		for _, issue := range []uint64{10, 9, 8, 11, 7} {
			t.Run(fmt.Sprintf("business_%v/issue_%d", business, issue), func(t *testing.T) {
				idx, priv, _ := newAutopayMirrorIndexer(t, 10)
				idx.height = func() uint64 { return 8 }
				idx.tipHeight = func() uint64 { return 10 }
				core, err := btcec.NewPrivateKey()
				if err != nil {
					t.Fatal(err)
				}
				idx = New(idx.db, Config{EndpointID: hex.EncodeToString(core.PubKey().SerializeCompressed()),
					FeeVerifier: idx.feeVerifier, CurrentHeight: idx.height, TipHeight: idx.tipHeight})
				binding := signedBindingRecord(t, priv, core, 1)
				binding.IssueHeight = 8
				SignRecord(priv, binding)
				admission := &WalletRPCAdmission{Indexer: idx, IsCoreNode: func() bool { return true },
					CurrentBinding: func(string) (*wire.DKVSRecord, error) { return idx.Get(binding.Key) }}
				record := binding
				if business {
					// Install the control binding independently so bootstrap failure
					// cannot mask the business RPC's own height check.
					if _, err := idx.PutLocal(binding); err != nil {
						t.Fatal(err)
					}
					record = signedAutopayPersonalRecord(t, priv, "autopay", 1)
				}
				record.IssueHeight = issue
				SignRecord(priv, record)
				mutations := []CASMutation{{Record: record, Precondition: WritePrecondition{ExpectAbsent: true}}}
				options := BatchCASOptions{EndpointID: idx.EndpointID(), RequestID: "tip-height"}
				var auth *WalletWriteAuthorization
				if business {
					path, err := CollectionPathForKey(record.Key)
					if err != nil {
						t.Fatal(err)
					}
					ctx := WalletWriteContext{EndpointID: options.EndpointID, Prefixes: []PrefixGeneration{{Prefix: path}}}
					digest, err := WalletWriteDigest(mutations, options, ctx)
					if err != nil {
						t.Fatal(err)
					}
					sig, err := schnorr.Sign(priv, digest[:])
					if err != nil {
						t.Fatal(err)
					}
					auth = &WalletWriteAuthorization{Context: ctx, Signature: sig.Serialize()}
				}
				result, err := admission.PutRecords(mutations, options, auth)
				if issue > 10 || issue < 8 {
					if err == nil {
						t.Fatalf("out-of-window issue height %d accepted", issue)
					}
					if issue < 8 && !errors.Is(err, ErrStaleEndpoint) {
						t.Fatalf("old issue height returned %v", err)
					}
					if _, getErr := idx.Get(record.Key); !errors.Is(getErr, ErrRecordNotFound) {
						t.Fatalf("rejected write changed state: %v", getErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("indexed=8 tip=10 issue=%d: %v", issue, err)
				}
				if result.Applied != 1 || result.ViewHeight != 10 {
					t.Fatalf("unexpected receipt: %+v", result)
				}
				for _, got := range result.Records {
					if err := VerifyRecordForClient(got, RecordVerificationOptions{ExpectedKey: got.Key, Height: result.ViewHeight}); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestFinalReviewPrefixAndCASReceiptUseTipHeight(t *testing.T) {
	idx, priv, _ := newAutopayMirrorIndexer(t, 10)
	idx.height = func() uint64 { return 8 }
	idx.tipHeight = func() uint64 { return 10 }
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	record.IssueHeight = 10
	SignRecord(priv, record)
	result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{Record: record, Precondition: WritePrecondition{ExpectAbsent: true}}}, BatchCASOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ViewHeight != 10 {
		t.Errorf("CAS receipt view=%d, want 10", result.ViewHeight)
	}
	for _, got := range result.Records {
		if err := VerifyRecordForClient(got, RecordVerificationOptions{ExpectedKey: got.Key, Height: result.ViewHeight}); err != nil {
			t.Errorf("receipt: %v", err)
		}
	}
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	read, err := idx.ReadPrefix(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Records) != 1 || read.ViewHeight != 10 {
		t.Errorf("prefix records=%d view=%d", len(read.Records), read.ViewHeight)
	}
	for _, got := range read.Records {
		if err := VerifyRecordForClient(got, RecordVerificationOptions{ExpectedKey: got.Key, Height: read.ViewHeight}); err != nil {
			t.Errorf("prefix: %v", err)
		}
	}
}

func TestFinalReviewFreeLocalReceiptMatchesTipView(t *testing.T) {
	idx, priv, _ := newAutopayMirrorIndexer(t, 10)
	idx.height = func() uint64 { return 8 }
	idx.tipHeight = func() uint64 { return 10 }
	record := signedFreeTTLPersonalRecord(t, priv, "free-local/item", 1, 8, 100, "value")
	result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{Record: record, Precondition: WritePrecondition{ExpectAbsent: true}}}, BatchCASOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied != 1 || len(result.Records) != 1 || result.ViewHeight != 10 {
		t.Fatalf("unexpected FREE_LOCAL receipt: %+v", result)
	}
	for _, got := range result.Records {
		if err := VerifyRecordForClient(got, RecordVerificationOptions{ExpectedKey: got.Key, Height: result.ViewHeight}); err != nil {
			t.Fatalf("successful receipt cannot verify its FREE_LOCAL record: indexed=8 tip=10 issue=8 ttl=100 receipt_view=%d err=%v", result.ViewHeight, err)
		}
	}
}

func TestFinalReviewPaidSnapshotWithStaleContractHeight(t *testing.T) {
	for _, tc := range []struct {
		rawHeight, paid int64
		wantRecords     int
		unknown         bool
	}{
		{3, 10, 1, false}, {3, 8, 0, true}, {10, 8, 0, false},
	} {
		t.Run(fmt.Sprintf("contract_%d/last_paid_%d", tc.rawHeight, tc.paid), func(t *testing.T) {
			idx, priv, _ := newAutopayMirrorIndexer(t, 10)
			record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
			if _, err := idx.PutLocal(record); err != nil {
				t.Fatal(err)
			}
			verifier := idx.feeVerifier.(LocalCacheAutopayFeeVerifier)
			provider := verifier.StateProvider.(*HeightCachedAutopayStateProvider).Provider.(*mutableAutopayStateProvider)
			provider.state.CurrentBlock = tc.rawHeight
			for key, delegate := range provider.state.Delegates {
				delegate.LastPayHeight = tc.paid
				provider.state.Delegates[key] = delegate
			}
			verifier.StateProvider = &HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return 8 }}
			// Cold cache models a restart: admission's previous payment proof
			// must not accidentally supply this snapshot's verification.
			source := New(idx.db, Config{EndpointID: idx.EndpointID(), FeeVerifier: verifier,
				CurrentHeight: func() uint64 { return 8 }, TipHeight: func() uint64 { return 10 }})
			path, err := CollectionPathForKey(record.Key)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := source.GetPathSnapshot(path)
			if tc.unknown {
				if !errors.Is(err, ErrConcurrentUpdate) || snapshot != nil {
					t.Fatalf("unverified tip payment produced snapshot: %+v, err=%v", snapshot, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("payment covers tip despite stale contract height: %v", err)
			}
			if len(snapshot.Records) != tc.wantRecords || snapshot.PathMeta.ViewHeight != 10 {
				t.Fatalf("invalid paid snapshot: %+v", snapshot)
			}
			if err := ValidatePathSnapshotForClient(snapshot, RecordVerificationOptions{Height: 10}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type snapshotRetentionProbe struct {
	LocalCacheAutopayFeeVerifier
	failKey string
	failure error
	calls   map[string]int
}

func (p *snapshotRetentionProbe) PaidRecordRetention(record *wire.DKVSRecord, parsed ParsedKey) (PaidRecordRetention, error) {
	p.calls[record.Key]++
	if record.Key == p.failKey {
		return PaidRecordRetention{}, p.failure
	}
	return p.LocalCacheAutopayFeeVerifier.PaidRecordRetention(record, parsed)
}

func TestFinalReviewPaidSnapshotRefreshIsPrefixScoped(t *testing.T) {
	idx, priv, _ := newAutopayMirrorIndexer(t, 10)
	provider := idx.feeVerifier.(LocalCacheAutopayFeeVerifier).StateProvider.(*HeightCachedAutopayStateProvider).Provider.(*mutableAutopayStateProvider)
	for key, delegate := range provider.state.Delegates {
		delegate.AmountPerBlock = "10"
		provider.state.Delegates[key] = delegate
	}
	a := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, err := idx.PutLocal(a); err != nil {
		t.Fatal(err)
	}
	b := signedRecordWithAutopayFee(t, priv, "/personal/"+AccountID(priv.PubKey().SerializeCompressed())+"/other/item", 1, "autopay", 10)
	if _, err := idx.PutLocal(b); err != nil {
		t.Fatal(err)
	}
	unavailable := errors.New("prefix A payment unavailable")
	probe := &snapshotRetentionProbe{LocalCacheAutopayFeeVerifier: idx.feeVerifier.(LocalCacheAutopayFeeVerifier), failKey: a.Key, failure: unavailable, calls: make(map[string]int)}
	source := New(idx.db, Config{EndpointID: idx.EndpointID(), FeeVerifier: probe, CurrentHeight: func() uint64 { return 10 }})
	pathA, _ := CollectionPathForKey(a.Key)
	pathB, _ := CollectionPathForKey(b.Key)
	for _, path := range []string{pathB, "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/empty"} {
		snapshot, err := source.GetPathSnapshot(path)
		if err != nil {
			t.Errorf("unrelated prefix %s blocked: %v", path, err)
			continue
		}
		want := 0
		if path == pathB {
			want = 1
		}
		if len(snapshot.Records) != want {
			t.Errorf("prefix %s has %d records, want %d", path, len(snapshot.Records), want)
		}
	}
	if probe.calls[a.Key] != 0 || probe.calls[b.Key] != 1 {
		t.Errorf("unrelated records refreshed: %+v", probe.calls)
	}
	before := probe.calls[b.Key]
	if _, err := source.GetPathSnapshot(pathB); err != nil {
		t.Errorf("cached B: %v", err)
	}
	if probe.calls[b.Key] != before {
		t.Error("valid payment cache was not reused")
	}
	for attempt := 0; attempt < 2; attempt++ {
		if snapshot, err := source.GetPathSnapshot(pathA); !errors.Is(err, unavailable) || snapshot != nil {
			t.Fatalf("unknown target payment must fail on every retry: snapshot=%+v err=%v", snapshot, err)
		}
	}
	if _, err := source.GetPathSnapshot(pathB); err != nil {
		t.Fatal(err)
	}
	if probe.calls[b.Key] != before {
		t.Error("failed A refresh discarded B's verified payment")
	}
	// Changing verification policy invalidates cached payment evidence.
	source.SetFeeVerifier(probe)
	if _, err := source.GetPathSnapshot(pathB); err != nil {
		t.Fatal(err)
	}
	if probe.calls[b.Key] != before+1 {
		t.Error("new fee verifier reused old policy's payment evidence")
	}
}
