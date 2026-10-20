package dkvs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProRound1NetworkActiveMetadataAndWatchColdRestart(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		name := "paid"
		if unavailable {
			name = "provider-unavailable"
		}
		t.Run(name, func(t *testing.T) {
			source, _, record := restartedAutopaySource(t)
			prefix, err := CollectionPathForKey(record.Key)
			require.NoError(t, err)
			scope := ActiveScope{Prefix: prefix, Network: true}
			failure := errors.New("review: unavailable current payment")
			if unavailable {
				source.SetFeeVerifier(AutopayFeeVerifier{StateProvider: testAutopayStateProvider{err: failure}, Contract: "autopay"})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			meta, err := source.ActiveMetadata(ctx, scope)
			if unavailable {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
				require.NotZero(t, meta.Root)
			}
			// Generation ahead requests a full current page, so this checks the
			// Watch read path without waiting for an artificial mutation.
			watch, err := source.WatchActive(ctx, ActiveWatchRequest{EndpointID: source.EndpointID(), Scopes: []ActiveWatchScope{{Scope: scope, Generation: meta.Generation + 1}}})
			if unavailable {
				require.ErrorIs(t, err, failure)
				require.Nil(t, watch)
			} else {
				require.NoError(t, err)
				require.NotNil(t, watch.Page)
				require.Len(t, watch.Page.Records, 1)
				require.Equal(t, meta.Root, watch.Page.Meta.Root)
			}
			// An unrelated empty prefix must not query the unavailable payer.
			other := ActiveScope{Prefix: prefix + "-unrelated", Network: true}
			page, err := source.ActiveSyncPage(ctx, ActiveSyncRequest{Scope: other, EndpointID: source.EndpointID(), Full: true})
			require.NoError(t, err)
			require.True(t, page.Complete)
			require.Empty(t, page.Records)
		})
	}
}

func TestProRound1NetworkPaidPaginationReadiness(t *testing.T) {
	original, key, height := newAutopayMirrorIndexer(t, 10)
	verifier := original.feeVerifier.(LocalCacheAutopayFeeVerifier)
	verifier.FullRecordFeePerBlock = "0.5" // Existing rate 1 covers two records.
	original.SetFeeVerifier(verifier)
	var prefix string
	for _, suffix := range []string{"a", "b"} {
		name, err := PersonalKey(key.PubKey().SerializeCompressed(), "paid-pages/"+suffix)
		require.NoError(t, err)
		record := signedRecordWithAutopayFee(t, key, name, 1, "autopay", 0)
		_, err = original.PutLocal(record)
		require.NoError(t, err)
		prefix, err = CollectionPathForKey(name)
		require.NoError(t, err)
	}
	cfg := Config{EndpointID: "paid-pages-source", FeeVerifier: verifier, CurrentHeight: func() uint64 { return height }}
	source := New(original.db, cfg)
	req := ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix, Network: true}, EndpointID: source.EndpointID(), Full: true, PageSize: 1}
	first, err := source.ActiveSyncPage(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, first.Records, 1)
	require.False(t, first.Complete)
	require.NotNil(t, first.Next)
	req.Cursor = first.Next
	last, err := source.ActiveSyncPage(context.Background(), req)
	require.NoError(t, err)
	require.True(t, last.Complete)
	require.Len(t, last.Records, 1)
	require.NotEqual(t, first.Records[0].Key, last.Records[0].Key)
	require.Equal(t, first.Meta, last.Meta, "all pages must declare the same paid view")

	failure := errors.New("review: payment unavailable at continuation")
	broken := verifier
	broken.StateProvider = testAutopayStateProvider{err: failure}
	cfg.FeeVerifier = broken
	restarted := New(original.db, cfg)
	page, err := restarted.ActiveSyncPage(context.Background(), req)
	require.ErrorIs(t, err, failure)
	require.Nil(t, page, "cold continuation must not certify missing paid records")

	state, err := verifier.StateProvider.GetAutopayState("autopay")
	require.NoError(t, err)
	for payer, delegate := range state.Delegates {
		delegate.LastPayHeight = int64(height - 1)
		state.Delegates[payer] = delegate
	}
	unpaid := verifier
	unpaid.StateProvider = testAutopayStateProvider{states: map[string]*AutopayContractState{"autopay": state}}
	cfg.FeeVerifier = unpaid
	confirmed := New(original.db, cfg)
	req.Cursor = nil
	page, err = confirmed.ActiveSyncPage(context.Background(), req)
	require.NoError(t, err)
	require.True(t, page.Complete)
	require.Empty(t, page.Records, "confirmed unpaid state may certify a network empty set")
	_, err = confirmed.Get(first.Records[0].Key)
	require.NoError(t, err, "local retained data remains readable")
}
