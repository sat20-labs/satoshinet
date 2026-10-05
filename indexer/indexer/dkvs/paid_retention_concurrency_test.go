package dkvs

import (
	"context"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/wire"
)

// Pause only the maintenance read of the old record. Admission of a new key
// still uses the real AUTOPAY verifier and the existing fixture's contract.
type retentionRefreshGate struct {
	LocalCacheAutopayFeeVerifier
	key     string
	armed   atomic.Bool
	started chan struct{}
	release chan struct{}
	failure error
}

type retentionCapacityGate struct {
	LocalCacheAutopayFeeVerifier
	started chan struct{}
	release chan struct{}
	armed   atomic.Bool
}

func (g *retentionCapacityGate) FeeCapacity(record *wire.DKVSRecord, parsed ParsedKey) (FeeCapacityDescriptor, error) {
	capacity, err := g.LocalCacheAutopayFeeVerifier.FeeCapacity(record, parsed)
	if g.armed.CompareAndSwap(true, false) {
		close(g.started)
		<-g.release
	}
	return capacity, err
}

func TestPaidWalletAdmissionRechecksPaymentAfterHeightAdvance(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	var currentHeight atomic.Uint64
	currentHeight.Store(height)
	idx.height = currentHeight.Load
	verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
	cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
	cached.CurrentHeight = currentHeight.Load
	provider := cached.Provider.(*mutableAutopayStateProvider)
	gate := &retentionCapacityGate{LocalCacheAutopayFeeVerifier: verifier,
		started: make(chan struct{}), release: make(chan struct{})}
	core, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	coreID := hex.EncodeToString(core.PubKey().SerializeCompressed())
	idx = New(idx.db, Config{EndpointID: coreID, FeeVerifier: gate,
		CurrentHeight: currentHeight.Load, AllowFreeLocal: true, FreeLocalCache: idx.freeLocal})
	binding := signedBindingRecord(t, priv, core, 1)
	binding.IssueHeight = height
	SignRecord(priv, binding)
	admission := &WalletRPCAdmission{Indexer: idx, IsCoreNode: func() bool { return true },
		CurrentBinding: func(string) (*wire.DKVSRecord, error) { return idx.Get(binding.Key) }}
	if _, err := admission.PutRecords([]CASMutation{{Record: binding, Precondition: WritePrecondition{ExpectAbsent: true}}},
		BatchCASOptions{EndpointID: coreID}, nil); err != nil {
		t.Fatal(err)
	}
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	record.IssueHeight = height
	SignRecord(priv, record)
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := idx.ActiveMetadata(context.Background(), ActiveScope{Prefix: path})
	if err != nil {
		t.Fatal(err)
	}
	mutations := []CASMutation{{Record: record, Precondition: WritePrecondition{ExpectAbsent: true}}}
	options := BatchCASOptions{EndpointID: coreID, RequestID: "height-payment-admission"}
	writeContext := WalletWriteContext{EndpointID: coreID, Prefixes: []PrefixGeneration{{Prefix: path, Generation: meta.Generation}}}
	digest, err := WalletWriteDigest(mutations, options, writeContext)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := schnorr.Sign(priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	authorization := &WalletWriteAuthorization{Context: writeContext, Signature: signature.Serialize()}
	gate.armed.Store(true)
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	defer release()
	finished := make(chan error, 1)
	go func() { _, err := admission.PutRecords(mutations, options, authorization); finished <- err }()
	select {
	case <-gate.started:
	case <-time.After(3 * time.Second):
		t.Fatal("authorized admission did not reach the capacity read")
	}
	height++
	currentHeight.Store(height)
	provider.state.CurrentBlock = int64(height)
	parsed, err := ParseKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.VerifyRecordFeeProof(record, parsed); !errors.Is(err, ErrInvalidFeeProof) {
		t.Fatalf("current block must be unpaid in this scenario: %v", err)
	}
	release()
	select {
	case err := <-finished:
		if !errors.Is(err, ErrInvalidFeeProof) && !errors.Is(err, ErrConcurrentUpdate) {
			got, getErr := idx.Get(record.Key)
			t.Fatalf("wallet admission committed using the previous block's payment: admitted=%v stored=%v get_err=%v",
				err, got != nil && RecordHash(got) == RecordHash(record), getErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("authorized admission did not finish")
	}
	if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("unpaid wallet record was persisted: %v", err)
	}
}

func (g *retentionRefreshGate) PaidRecordRetention(record *wire.DKVSRecord, parsed ParsedKey) (PaidRecordRetention, error) {
	retention, err := g.LocalCacheAutopayFeeVerifier.PaidRecordRetention(record, parsed)
	if record.Key == g.key && g.armed.CompareAndSwap(true, false) {
		close(g.started)
		<-g.release
		if g.failure != nil {
			return retention, g.failure
		}
	}
	return retention, err
}

func TestPaidRetentionRefreshPreservesConcurrentNewRecord(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
	cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
	provider := cached.Provider.(*mutableAutopayStateProvider)
	for payer, delegate := range provider.state.Delegates {
		delegate.AmountPerBlock = "4"
		provider.state.Delegates[payer] = delegate
	}
	old := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	gate := &retentionRefreshGate{LocalCacheAutopayFeeVerifier: verifier, key: old.Key,
		started: make(chan struct{}), release: make(chan struct{})}
	idx.SetFeeVerifier(gate)
	if _, err := idx.PutLocal(old); err != nil {
		t.Fatal(err)
	}
	gate.armed.Store(true)
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	defer release()
	finished := make(chan error, 1)
	go func() { finished <- idx.RefreshPaidRetentionAt(height) }()
	select {
	case <-gate.started:
	case <-time.After(3 * time.Second):
		t.Fatal("maintenance did not reach the retention read")
	}

	created := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	created.Key += "/new"
	proof, err := NewAutopayFeeProof(created.Key, "personal", uint32(RecordSize(created)), 0, "autopay", "")
	if err != nil {
		t.Fatal(err)
	}
	created.FeeProof, err = EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	SignRecord(priv, created)
	if updated, err := idx.PutLocal(created); err != nil || !updated {
		t.Fatalf("concurrent creation: updated=%v err=%v", updated, err)
	}
	if !idx.paidRecordRelayable(created) {
		t.Fatal("newly admitted record was not relayable before refresh completion")
	}
	release()
	select {
	case err := <-finished:
		if err != nil && !errors.Is(err, ErrConcurrentUpdate) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("maintenance did not finish")
	}
	if got, err := idx.Get(created.Key); err != nil || RecordHash(got) != RecordHash(created) {
		t.Fatalf("concurrent record did not remain committed: got=%v err=%v", got, err)
	}
	path, err := CollectionPathForKey(created.Key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := idx.GetPathSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 2 {
		t.Fatalf("old maintenance snapshot removed a newly paid key from the network view: records=%d relayable=%v err=%v",
			len(snapshot.Records), idx.paidRecordRelayable(created), err)
	}
}

func TestPaidRetentionPrunePreservesRecreatedKey(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	var currentHeight atomic.Uint64
	currentHeight.Store(height)
	idx.height = currentHeight.Load
	verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
	cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
	cached.CurrentHeight = currentHeight.Load
	provider := cached.Provider.(*mutableAutopayStateProvider)
	old := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, err := idx.PutLocal(old); err != nil {
		t.Fatal(err)
	}
	height += paidRetentionGraceBlocks(idx.freeLocal) + 1
	currentHeight.Store(height)
	provider.state.CurrentBlock = int64(height)

	// Pause the existing post-commit notification boundary. The prune has
	// released the indexer mutex, so a newly funded lifetime can now commit.
	idx.watchMutex.Lock()
	var once sync.Once
	release := func() { once.Do(idx.watchMutex.Unlock) }
	defer release()
	pruned := make(chan error, 1)
	pruneHeight := height
	go func() {
		count, err := idx.PruneExpiredAutopayAt(pruneHeight)
		if err == nil && count != 1 {
			err = errors.New("expected one expired AUTOPAY record")
		}
		pruned <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := idx.Get(old.Key); errors.Is(err, ErrRecordNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prune did not commit before its notification")
		}
		time.Sleep(time.Millisecond)
	}

	height++
	currentHeight.Store(height)
	provider.state.CurrentBlock = int64(height)
	for payer, delegate := range provider.state.Delegates {
		delegate.LastPayHeight = int64(height)
		provider.state.Delegates[payer] = delegate
	}
	created := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	created.IssueHeight, created.Value = height, []byte("new lifetime")
	SignRecord(priv, created)
	written := make(chan error, 1)
	go func() { _, err := idx.PutLocal(created); written <- err }()
	deadline = time.Now().Add(3 * time.Second)
	for {
		got, err := idx.Get(created.Key)
		if err == nil && RecordHash(got) == RecordHash(created) && idx.paidRecordRelayable(created) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("new lifetime did not commit with paid retention: record=%v err=%v", got, err)
		}
		time.Sleep(time.Millisecond)
	}
	release()
	for _, finished := range []<-chan error{pruned, written} {
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent operation did not finish")
		}
	}
	if got, err := idx.Get(created.Key); err != nil || RecordHash(got) != RecordHash(created) {
		t.Fatalf("new lifetime did not remain committed: record=%v err=%v", got, err)
	}
	if _, err := idx.GetForRelay(created.Key); err != nil {
		t.Fatalf("old prune removed the recreated key's current payment verification: %v", err)
	}
}

func TestPaidRetentionPruneRechecksPaymentAfterHeightAdvance(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	var currentHeight atomic.Uint64
	currentHeight.Store(height)
	idx.height = currentHeight.Load
	verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
	cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
	cached.CurrentHeight = currentHeight.Load
	provider := cached.Provider.(*mutableAutopayStateProvider)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	gate := &retentionRefreshGate{LocalCacheAutopayFeeVerifier: verifier, key: record.Key,
		started: make(chan struct{}), release: make(chan struct{})}
	idx.SetFeeVerifier(gate)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	height += paidRetentionGraceBlocks(idx.freeLocal) + 1
	currentHeight.Store(height)
	provider.state.CurrentBlock = int64(height)
	gate.armed.Store(true)
	var once sync.Once
	release := func() { once.Do(func() { close(gate.release) }) }
	defer release()
	type pruneResult struct {
		count int
		err   error
	}
	finished := make(chan pruneResult, 1)
	pruneHeight := height
	go func() {
		count, err := idx.PruneExpiredAutopayAt(pruneHeight)
		finished <- pruneResult{count, err}
	}()
	select {
	case <-gate.started:
	case <-time.After(3 * time.Second):
		t.Fatal("prune did not reach its expired-payment read")
	}
	// A new block resumes this payer before the old prune can commit. No KV
	// rewrite is needed for a currently paid TTL=0 record to become relayable.
	height++
	currentHeight.Store(height)
	provider.state.CurrentBlock = int64(height)
	for payer, delegate := range provider.state.Delegates {
		delegate.LastPayHeight = int64(height)
		provider.state.Delegates[payer] = delegate
	}
	if err := idx.RefreshPaidRetentionAt(height); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.GetForRelay(record.Key); err != nil {
		t.Fatalf("resumed payer did not make the existing record relayable: %v", err)
	}
	release()
	var result pruneResult
	select {
	case result = <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("old prune did not finish")
	}
	if result.count != 0 || (result.err != nil && !errors.Is(result.err, ErrConcurrentUpdate)) {
		_, getErr := idx.Get(record.Key)
		t.Fatalf("old unpaid view deleted a currently paid record: pruned=%d err=%v get_err=%v", result.count, result.err, getErr)
	}
	if got, err := idx.Get(record.Key); err != nil || RecordHash(got) != RecordHash(record) {
		t.Fatalf("currently paid record was physically removed: record=%v err=%v", got, err)
	}
}

func TestPaidAdmissionRechecksPaymentAfterHeightAdvance(t *testing.T) {
	for _, operation := range []string{"local", "cas", "p2p", "mirror"} {
		for _, paid := range []bool{false, true} {
			name := "unpaid"
			if paid {
				name = "still-paid"
			}
			t.Run(operation+"/"+name, func(t *testing.T) {
				idx, priv, height := newAutopayMirrorIndexer(t, 10)
				var currentHeight atomic.Uint64
				currentHeight.Store(height)
				idx.height = currentHeight.Load
				verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
				cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
				cached.CurrentHeight = currentHeight.Load
				provider := cached.Provider.(*mutableAutopayStateProvider)
				gate := &retentionCapacityGate{LocalCacheAutopayFeeVerifier: verifier,
					started: make(chan struct{}), release: make(chan struct{})}
				idx.SetFeeVerifier(gate)
				record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
				record.IssueHeight = height
				SignRecord(priv, record)
				path, err := CollectionPathForKey(record.Key)
				if err != nil {
					t.Fatal(err)
				}
				root, err := recordsRoot([]*wire.DKVSRecord{record}, height)
				if err != nil {
					t.Fatal(err)
				}
				gate.armed.Store(true)
				var once sync.Once
				release := func() { once.Do(func() { close(gate.release) }) }
				defer release()
				finished := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "local":
						_, err = idx.PutLocal(record)
					case "cas":
						_, err = idx.PutLocalCAS(record, WritePrecondition{ExpectAbsent: true})
					case "p2p":
						_, err = idx.AcceptCurrentRecord(record)
					case "mirror":
						_, err = idx.ApplyMirror([]Subscription{{Type: SubscriptionPrefix, Target: path}}, []*wire.DKVSRecord{record}, root)
					}
					finished <- err
				}()
				select {
				case <-gate.started:
				case <-time.After(3 * time.Second):
					t.Fatal("admission did not reach its last capacity read")
				}
				height++
				currentHeight.Store(height)
				provider.state.CurrentBlock = int64(height)
				if paid {
					for payer, delegate := range provider.state.Delegates {
						delegate.LastPayHeight = int64(height)
						provider.state.Delegates[payer] = delegate
					}
				}
				release()
				select {
				case err = <-finished:
				case <-time.After(3 * time.Second):
					t.Fatal("admission did not finish")
				}
				if !paid {
					if !errors.Is(err, ErrInvalidFeeProof) && !errors.Is(err, ErrConcurrentUpdate) {
						t.Fatalf("unpaid current block was admitted with old validation: %v", err)
					}
					if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
						t.Fatalf("unpaid record was persisted: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("current payment should pass revalidation: %v", err)
				}
				if got, err := idx.GetForRelay(record.Key); err != nil || RecordHash(got) != RecordHash(record) {
					t.Fatalf("currently paid record lost its identity or relay qualification: record=%v err=%v", got, err)
				}
				retention, ok := paidRetentionCacheFor(idx).get(record.Key)
				if !ok || retention.CurrentBlock != height {
					t.Fatalf("committed payment cache is from the old block: %+v", retention)
				}
			})
		}
	}
}

func TestPaidPathSnapshotRechecksPaymentAfterHeightAdvance(t *testing.T) {
	for _, paid := range []bool{false, true} {
		name := "unpaid"
		if paid {
			name = "still-paid"
		}
		t.Run(name, func(t *testing.T) {
			source, priv, height := newAutopayMirrorIndexer(t, 10)
			record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
			if _, err := source.PutLocal(record); err != nil {
				t.Fatal(err)
			}
			path, err := CollectionPathForKey(record.Key)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := source.GetPathSnapshot(path)
			if err != nil {
				t.Fatal(err)
			}
			idx, _, _ := newAutopayMirrorIndexerForPrivateKey(t, priv, 10)
			var currentHeight atomic.Uint64
			currentHeight.Store(height)
			idx.height = currentHeight.Load
			verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
			cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
			cached.CurrentHeight = currentHeight.Load
			provider := cached.Provider.(*mutableAutopayStateProvider)
			gate := &retentionRefreshGate{LocalCacheAutopayFeeVerifier: verifier, key: record.Key,
				started: make(chan struct{}), release: make(chan struct{})}
			idx.SetFeeVerifier(gate)
			baseline, err := idx.NetworkSyncBaseline(path)
			if err != nil {
				t.Fatal(err)
			}
			gate.armed.Store(true)
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			finished := make(chan error, 1)
			go func() { _, err := idx.ApplyPathSnapshotFrom(snapshot, baseline); finished <- err }()
			select {
			case <-gate.started:
			case <-time.After(3 * time.Second):
				t.Fatal("snapshot validation did not reach the retention read")
			}
			height++
			currentHeight.Store(height)
			provider.state.CurrentBlock = int64(height)
			if paid {
				for payer, delegate := range provider.state.Delegates {
					delegate.LastPayHeight = int64(height)
					provider.state.Delegates[payer] = delegate
				}
			}
			release()
			select {
			case err = <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("snapshot installation did not finish")
			}
			if !errors.Is(err, ErrConcurrentUpdate) {
				t.Fatalf("snapshot installed stale local payment validation: %v", err)
			}
			if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
				t.Fatalf("rejected installation changed current KV: %v", err)
			}
			_, err = idx.ApplyPathSnapshotFrom(snapshot, baseline)
			if !paid {
				if !errors.Is(err, ErrInvalidFeeProof) {
					t.Fatalf("fresh local verification must reject the unpaid snapshot: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("fixed source snapshot must survive current paid local height: %v", err)
			}
			if snapshot.PathMeta.ViewHeight != height-1 {
				t.Fatal("installation changed the captured source view")
			}
			if _, err := idx.GetForRelay(record.Key); err != nil {
				t.Fatalf("revalidated snapshot is not relayable: %v", err)
			}
		})
	}
}

func TestPaidRetentionMaintenanceRejectsChangedValidation(t *testing.T) {
	for _, operation := range []string{"refresh-height", "refresh-policy", "prune-policy"} {
		t.Run(operation, func(t *testing.T) {
			idx, priv, height := newAutopayMirrorIndexer(t, 10)
			var currentHeight atomic.Uint64
			currentHeight.Store(height)
			idx.height = currentHeight.Load
			verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
			cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
			cached.CurrentHeight = currentHeight.Load
			provider := cached.Provider.(*mutableAutopayStateProvider)
			record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
			gate := &retentionRefreshGate{LocalCacheAutopayFeeVerifier: verifier, key: record.Key,
				started: make(chan struct{}), release: make(chan struct{})}
			idx.SetFeeVerifier(gate)
			if _, err := idx.PutLocal(record); err != nil {
				t.Fatal(err)
			}
			if operation == "prune-policy" {
				height += paidRetentionGraceBlocks(idx.freeLocal) + 1
				currentHeight.Store(height)
				provider.state.CurrentBlock = int64(height)
			}
			gate.armed.Store(true)
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			finished := make(chan error, 1)
			validationHeight := height
			go func() {
				if operation == "prune-policy" {
					_, err := idx.PruneExpiredAutopayAt(validationHeight)
					finished <- err
					return
				}
				finished <- idx.RefreshPaidRetentionAt(validationHeight)
			}()
			select {
			case <-gate.started:
			case <-time.After(3 * time.Second):
				t.Fatal("maintenance did not reach the retention read")
			}
			if operation == "refresh-height" {
				height++
				currentHeight.Store(height)
				provider.state.CurrentBlock = int64(height)
				if err := idx.RefreshPaidRetentionAt(height); err != nil {
					t.Fatal(err)
				}
			} else {
				idx.SetFeeVerifier(verifier)
			}
			release()
			select {
			case err := <-finished:
				if !errors.Is(err, ErrConcurrentUpdate) {
					t.Fatalf("maintenance committed results after validation conditions changed: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("maintenance did not finish")
			}
			if _, err := idx.Get(record.Key); err != nil {
				t.Fatalf("stale maintenance removed current KV: %v", err)
			}
			if operation == "refresh-height" {
				retention, ok := paidRetentionCacheFor(idx).get(record.Key)
				if !ok || retention.CurrentBlock != height {
					t.Fatalf("stale refresh replaced the newer payment view: %+v", retention)
				}
			}
		})
	}
}

func TestPaidRetentionRefreshPreservesConcurrentMutation(t *testing.T) {
	for _, operation := range []string{"rewrite", "delete"} {
		t.Run(operation, func(t *testing.T) {
			idx, priv, height := newAutopayMirrorIndexer(t, 10)
			verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
			record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
			gate := &retentionRefreshGate{LocalCacheAutopayFeeVerifier: verifier, key: record.Key,
				started: make(chan struct{}), release: make(chan struct{})}
			idx.SetFeeVerifier(gate)
			if _, err := idx.PutLocal(record); err != nil {
				t.Fatal(err)
			}
			if operation == "rewrite" {
				gate.failure = ErrInvalidFeeProof
			}
			gate.armed.Store(true)
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			finished := make(chan error, 1)
			go func() { finished <- idx.RefreshPaidRetentionAt(height) }()
			select {
			case <-gate.started:
			case <-time.After(3 * time.Second):
				t.Fatal("refresh did not reach the old version")
			}
			if operation == "rewrite" {
				record = signedAutopayPersonalRecord(t, priv, "autopay", 2)
				if _, err := idx.PutLocal(record); err != nil {
					t.Fatal(err)
				}
			} else {
				command := signedCurrentDelete(t, priv, record, height)
				hash := RecordHash(record)
				if _, err := idx.PutLocalCAS(command, WritePrecondition{ExpectedHash: &hash}); err != nil {
					t.Fatal(err)
				}
			}
			release()
			select {
			case err := <-finished:
				if err != nil && !errors.Is(err, ErrInvalidFeeProof) {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("refresh did not finish")
			}
			if operation == "rewrite" {
				if got, err := idx.GetForRelay(record.Key); err != nil || RecordHash(got) != RecordHash(record) {
					t.Fatalf("old failed read removed the new version's payment: record=%v err=%v", got, err)
				}
			} else if _, ok := paidRetentionCacheFor(idx).get(record.Key); ok {
				t.Fatal("old refresh restored retention for a deleted key")
			}
		})
	}
}

func TestTTLPrunePreservesRecreatedPaidKey(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	var currentHeight atomic.Uint64
	currentHeight.Store(height)
	idx.height = currentHeight.Load
	verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
	cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
	cached.CurrentHeight = currentHeight.Load
	provider := cached.Provider.(*mutableAutopayStateProvider)
	old := signedFreeTTLPersonalRecord(t, priv, "paid-retention", 1, height, 1, "old local lifetime")
	if _, err := idx.PutLocal(old); err != nil {
		t.Fatal(err)
	}
	height++
	currentHeight.Store(height)
	provider.state.CurrentBlock = int64(height)
	for payer, delegate := range provider.state.Delegates {
		delegate.LastPayHeight = int64(height)
		provider.state.Delegates[payer] = delegate
	}
	idx.watchMutex.Lock()
	var once sync.Once
	release := func() { once.Do(idx.watchMutex.Unlock) }
	defer release()
	pruned := make(chan error, 1)
	go func() { _, err := idx.PruneExpiredAt(height); pruned <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		idx.mutex.RLock()
		_, err := idx.getRaw(old.Key)
		idx.mutex.RUnlock()
		if errors.Is(err, ErrRecordNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TTL prune did not commit before its notification")
		}
		time.Sleep(time.Millisecond)
	}
	created := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	created.IssueHeight = height
	SignRecord(priv, created)
	written := make(chan error, 1)
	go func() { _, err := idx.PutLocal(created); written <- err }()
	deadline = time.Now().Add(3 * time.Second)
	for {
		if got, err := idx.GetForRelay(created.Key); err == nil && RecordHash(got) == RecordHash(created) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("recreated AUTOPAY key did not commit")
		}
		time.Sleep(time.Millisecond)
	}
	release()
	for _, finished := range []<-chan error{pruned, written} {
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("operation did not finish")
		}
	}
	if _, err := idx.GetForRelay(created.Key); err != nil {
		t.Fatalf("old TTL cleanup removed the new AUTOPAY lifetime's payment: %v", err)
	}
}

func TestPaidRetentionPruneFlushFailurePreservesCache(t *testing.T) {
	idx, priv, height := newAutopayMirrorIndexer(t, 10)
	var currentHeight atomic.Uint64
	currentHeight.Store(height)
	idx.height = currentHeight.Load
	verifier := idx.snapshotValidators().feeVerifier.(LocalCacheAutopayFeeVerifier)
	cached := verifier.StateProvider.(*HeightCachedAutopayStateProvider)
	cached.CurrentHeight = currentHeight.Load
	provider := cached.Provider.(*mutableAutopayStateProvider)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	before, ok := paidRetentionCacheFor(idx).get(record.Key)
	if !ok {
		t.Fatal("initial write did not commit retention")
	}
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	watch := idx.subscribePath(path)
	defer idx.unsubscribePath(path, watch)
	signal := idx.pathSignal(watch)
	height += paidRetentionGraceBlocks(idx.freeLocal) + 1
	currentHeight.Store(height)
	provider.state.CurrentBlock = int64(height)
	db := &watchTestDB{KVDB: idx.db}
	idx.db = db
	db.failFlush.Store(true)
	if count, err := idx.PruneExpiredAutopayAt(height); count != 0 || !errors.Is(err, errWatchTestFlush) {
		t.Fatalf("failed DB batch was reported as committed: count=%d err=%v", count, err)
	}
	if got, err := idx.Get(record.Key); err != nil || RecordHash(got) != RecordHash(record) {
		t.Fatalf("failed DB batch removed current KV: record=%v err=%v", got, err)
	}
	if after, ok := paidRetentionCacheFor(idx).get(record.Key); !ok || after != before {
		t.Fatalf("failed DB batch cleared retention: before=%+v after=%+v", before, after)
	}
	assertPathSignal(t, signal, false)
}
