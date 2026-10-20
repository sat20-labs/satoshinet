package dkvs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func reviewExactPaidSource(t *testing.T, wanted ...string) (*Indexer, *snapshotRetentionProbe, ActiveScope, []*wire.DKVSRecord, *btcec.PrivateKey) {
	t.Helper()
	original, priv, height := newAutopayMirrorIndexer(t, 10)
	provider := original.feeVerifier.(LocalCacheAutopayFeeVerifier).StateProvider.(*HeightCachedAutopayStateProvider).Provider.(*mutableAutopayStateProvider)
	for payer, delegate := range provider.state.Delegates {
		delegate.AmountPerBlock = "10"
		provider.state.Delegates[payer] = delegate
	}
	path := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/prefs"
	other := signedRecordWithAutopayFee(t, priv, path+"/other", 1, "autopay", height)
	_, err := original.PutLocal(other)
	require.NoError(t, err)
	var records []*wire.DKVSRecord
	for _, name := range wanted {
		record := signedRecordWithAutopayFee(t, priv, path+"/"+name, 1, "autopay", height)
		_, err := original.PutLocal(record)
		require.NoError(t, err)
		records = append(records, record)
	}
	probe := &snapshotRetentionProbe{LocalCacheAutopayFeeVerifier: original.feeVerifier.(LocalCacheAutopayFeeVerifier),
		failKey: other.Key, failure: errors.New("out-of-scope payment unavailable"), calls: make(map[string]int)}
	// A real restart over signed, publicly admitted data has no retention cache.
	source := New(original.db, Config{EndpointID: "exact-paid-review", FeeVerifier: probe,
		CurrentHeight: func() uint64 { return height }, FreeLocalCache: original.freeLocal})
	return source, probe, ActiveScope{Prefix: path, Keys: []string{path + "/wanted"}, Network: true}, records, priv
}

func TestReviewExactPaidScopeMissingKey(t *testing.T) {
	for _, operation := range []string{"metadata", "page", "watch-empty", "watch-quiet"} {
		t.Run(operation, func(t *testing.T) {
			source, probe, scope, _, _ := reviewExactPaidSource(t)
			defer func() { t.Logf("payment_queries=%v", probe.calls) }()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			switch operation {
			case "metadata":
				meta, err := source.ActiveMetadata(ctx, scope)
				require.NoError(t, err)
				require.Equal(t, chainhash.Hash{}, meta.Root)
			case "page":
				page, err := source.ActiveSyncPage(ctx, ActiveSyncRequest{Scope: scope, EndpointID: source.EndpointID(), Full: true})
				require.NoError(t, err)
				require.True(t, page.Complete)
				require.Empty(t, page.Records)
				require.Equal(t, chainhash.Hash{}, page.Meta.Root)
			default:
				root := chainhash.Hash{}
				if operation == "watch-empty" {
					root[0] = 1 // A stale nonempty client view must be replaced by the exact empty view.
				}
				result, err := source.WatchActive(ctx, ActiveWatchRequest{EndpointID: source.EndpointID(),
					Scopes: []ActiveWatchScope{{Scope: scope, Root: root}}})
				require.NoError(t, err)
				if operation == "watch-quiet" {
					require.Nil(t, result.Page)
				} else {
					require.NotNil(t, result.Page)
					require.Empty(t, result.Page.Records)
					require.Equal(t, chainhash.Hash{}, result.Page.Meta.Root)
				}
			}
			require.Empty(t, probe.calls, "an unrelated stored record must not cause payment I/O")
			for _, requested := range []ActiveScope{{Prefix: scope.Prefix, Network: true},
				{Prefix: scope.Prefix, Keys: []string{probe.failKey}, Network: true}} {
				_, err := source.ActiveMetadata(context.Background(), requested)
				require.ErrorIs(t, err, probe.failure, "requested unknown payment must still fail closed")
			}
		})
	}
}

func TestReviewExactPaidScopePagesAndWatch(t *testing.T) {
	source, probe, scope, records, _ := reviewExactPaidSource(t, "wanted", "wanted-two")
	scope.Keys = []string{records[1].Key, records[0].Key} // Normalize unsorted public input.
	meta, err := source.ActiveMetadata(context.Background(), scope)
	require.NoError(t, err)
	want, err := ActiveRecordsRoot(records)
	require.NoError(t, err)
	require.Equal(t, want, meta.Root)
	request := ActiveSyncRequest{Scope: scope, EndpointID: source.EndpointID(), Full: true, PageSize: 1}
	var got []*wire.DKVSRecord
	for {
		page, err := source.ActiveSyncPage(context.Background(), request)
		require.NoError(t, err)
		require.Equal(t, meta, page.Meta)
		got = append(got, page.Records...)
		if page.Complete {
			break
		}
		require.NotNil(t, page.Next)
		request.Cursor = page.Next
	}
	gotRoot, err := ActiveRecordsRoot(got)
	require.NoError(t, err)
	require.Equal(t, want, gotRoot)
	watch, err := source.WatchActive(context.Background(), ActiveWatchRequest{EndpointID: source.EndpointID(),
		Scopes: []ActiveWatchScope{{Scope: scope}}})
	require.NoError(t, err)
	require.Equal(t, meta, watch.Page.Meta)
	require.Len(t, watch.Page.Records, 2)
	require.Zero(t, probe.calls[probe.failKey])
	for _, record := range records {
		require.Equal(t, 1, probe.calls[record.Key], "a current proof can be reused across pages and watch")
	}
	_, otherKnown := paidRetentionCacheFor(source).get(probe.failKey)
	require.False(t, otherKnown)
	t.Logf("scoped_payment_queries=%v", probe.calls)
}

func TestReviewExactPaidScopeConfirmedUnpaidRetainsLocalRecord(t *testing.T) {
	source, probe, scope, records, _ := reviewExactPaidSource(t, "wanted")
	state := cloneAutopayState(probe.StateProvider.(*HeightCachedAutopayStateProvider).Provider.(*mutableAutopayStateProvider).state)
	for payer, delegate := range state.Delegates {
		delegate.LastPayHeight = 9
		state.Delegates[payer] = delegate
	}
	probe.StateProvider = testAutopayStateProvider{states: map[string]*AutopayContractState{"autopay": state}}
	page, err := source.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: scope, EndpointID: source.EndpointID(), Full: true})
	require.NoError(t, err)
	require.Empty(t, page.Records)
	require.Equal(t, chainhash.Hash{}, page.Meta.Root)
	local, err := source.Get(records[0].Key)
	require.NoError(t, err)
	require.Equal(t, RecordHash(records[0]), RecordHash(local))
	require.Zero(t, probe.calls[probe.failKey])
}

func TestReviewExactPaidScopePreservesOtherProofs(t *testing.T) {
	source, probe, scope, _, _ := reviewExactPaidSource(t, "wanted")
	otherKey := probe.failKey
	probe.failKey = ""
	_, err := source.ActiveMetadata(context.Background(), ActiveScope{Prefix: scope.Prefix, Keys: []string{otherKey}, Network: true})
	require.NoError(t, err)
	previous, known := paidRetentionCacheFor(source).get(otherKey)
	require.True(t, known)
	probe.failKey = otherKey
	source.height = func() uint64 { return 11 }
	state := cloneAutopayState(probe.StateProvider.(*HeightCachedAutopayStateProvider).Provider.(*mutableAutopayStateProvider).state)
	state.CurrentBlock = 11
	for payer, delegate := range state.Delegates {
		delegate.LastPayHeight = 11
		state.Delegates[payer] = delegate
	}
	probe.StateProvider = testAutopayStateProvider{states: map[string]*AutopayContractState{"autopay": state}}
	_, err = source.ActiveMetadata(context.Background(), scope)
	require.NoError(t, err)
	after, known := paidRetentionCacheFor(source).get(otherKey)
	require.True(t, known)
	require.Equal(t, previous, after, "a precise read must not replace another scope's proof")
	require.ErrorIs(t, source.RefreshPaidRetentionAt(11), probe.failure, "maintenance still visits the full stored set")
	require.Equal(t, 2, probe.calls[otherKey], "one initial proof and one maintenance query")
}

func TestReviewExactPaidScopeConcurrentChanges(t *testing.T) {
	for _, change := range []string{"policy", "record"} {
		t.Run(change, func(t *testing.T) {
			source, probe, scope, records, priv := reviewExactPaidSource(t, "wanted")
			gate := &retentionRefreshGate{LocalCacheAutopayFeeVerifier: probe.LocalCacheAutopayFeeVerifier,
				key: records[0].Key, started: make(chan struct{}), release: make(chan struct{})}
			source.SetFeeVerifier(gate)
			gate.armed.Store(true)
			defer close(gate.release)
			finished := make(chan error, 1)
			var meta ActiveMeta
			go func() {
				var err error
				meta, err = source.ActiveMetadata(context.Background(), scope)
				finished <- err
			}()
			select {
			case <-gate.started:
			case <-time.After(3 * time.Second):
				t.Fatal("precise refresh did not reach the requested payment query")
			}
			updated := records[0]
			if change == "policy" {
				source.SetFeeVerifier(probe)
			} else {
				updated = signedRecordWithAutopayFee(t, priv, updated.Key, 2, "autopay", 10)
				_, err := source.PutLocal(updated)
				require.NoError(t, err)
			}
			gate.release <- struct{}{}
			select {
			case err := <-finished:
				if change == "policy" {
					require.ErrorIs(t, err, ErrConcurrentUpdate)
				} else {
					require.NoError(t, err)
					root, err := ActiveRecordsRoot([]*wire.DKVSRecord{updated})
					require.NoError(t, err)
					require.Equal(t, root, meta.Root)
					retention, known := paidRetentionCacheFor(source).get(updated.Key)
					require.True(t, known && paidRetentionCurrent(retention, 10))
				}
			case <-time.After(3 * time.Second):
				t.Fatal("precise refresh did not finish")
			}
			_, otherKnown := paidRetentionCacheFor(source).get(probe.failKey)
			require.False(t, otherKnown)
		})
	}
}
