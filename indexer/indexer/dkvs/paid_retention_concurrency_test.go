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
			t.Fatalf("wallet admission committed using the previous block's payment: %v", err)
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
		if err != nil {
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
		t.Fatalf("old unpaid view deleted a currently paid record: pruned=%d err=%v", result.count, result.err)
	}
	if got, err := idx.Get(record.Key); err != nil || RecordHash(got) != RecordHash(record) {
		t.Fatalf("currently paid record was physically removed: record=%v err=%v", got, err)
	}
}
