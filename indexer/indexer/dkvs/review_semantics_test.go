package dkvs

import (
	"context"
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/wire"
)

func reviewFreeLocalRecord(t *testing.T, priv *btcec.PrivateKey, key string, seq uint64, value string) *wire.DKVSRecord {
	t.Helper()
	record, err := NewSignedRecord(priv, key, []byte(value), RecordOptions{
		Seq: seq, IssueHeight: 100, TTL: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseKey(key)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewFreeLocalFeeProof(key, parsed.Namespace, uint32(RecordSize(record)), RecordExpiryHeight(record))
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	SignRecord(priv, record)
	return record
}

func TestServiceWriteAlwaysUsesCurrentDIDOwner(t *testing.T) {
	ownerA, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	const service = "reviewsvc.btc"
	resolver := StaticDIDResolver{Services: map[string]DIDIdentity{
		service: {
			CanonicalName: service,
			NameID:        NormalizeNameID(service),
			SigningKeys:   [][]byte{ownerA.PubKey().SerializeCompressed()},
			Active:        true,
		},
	}}
	idx := testIndexerWithConfig(t, Config{
		EndpointID:     "review-service-owner",
		AllowFreeLocal: true,
		FreeLocalCache: DefaultFreeLocalCachePolicy(),
		FeeVerifier:    JSONFeeVerifier{AllowFreeLocal: true},
		Resolver:       resolver,
		CurrentHeight:  func() uint64 { return 100 },
	})
	key, err := ServiceKey(service, "authenticity/pwa")
	if err != nil {
		t.Fatal(err)
	}
	first := reviewFreeLocalRecord(t, ownerA, key, 1, "owner-a")
	result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: first, Precondition: WritePrecondition{ExpectAbsent: true},
	}}, BatchCASOptions{EndpointID: idx.EndpointID()})
	if err != nil || result.Applied != 1 {
		t.Fatalf("initial service write result=%+v err=%v", result, err)
	}

	idx.SetResolver(StaticDIDResolver{Services: map[string]DIDIdentity{
		service: {
			CanonicalName: service,
			NameID:        NormalizeNameID(service),
			SigningKeys:   [][]byte{ownerB.PubKey().SerializeCompressed()},
			Active:        true,
		},
	}})

	hash := RecordHash(first)
	oldOwnerUpdate := reviewFreeLocalRecord(t, ownerA, key, 2, "revoked-owner")
	if _, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: oldOwnerUpdate, Precondition: WritePrecondition{ExpectedHash: &hash},
	}}, BatchCASOptions{EndpointID: idx.EndpointID()}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("previous service owner update err=%v", err)
	}
	// A structurally valid delete must reach the current-owner check rather
	// than fail earlier because its target hash is missing.
	oldOwnerDelete := signedCurrentDelete(t, ownerA, first, 100)
	if _, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: oldOwnerDelete, Precondition: WritePrecondition{ExpectedHash: &hash},
	}}, BatchCASOptions{EndpointID: idx.EndpointID()}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("previous service owner delete err=%v", err)
	}

	newOwnerUpdate := reviewFreeLocalRecord(t, ownerB, key, 2, "owner-b")
	result, err = idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: newOwnerUpdate, Precondition: WritePrecondition{ExpectedHash: &hash},
	}}, BatchCASOptions{EndpointID: idx.EndpointID()})
	if err != nil || result.Applied != 1 {
		t.Fatalf("new service owner takeover result=%+v err=%v", result, err)
	}
	stored, err := idx.Get(key)
	if err != nil || RecordHash(stored) != RecordHash(newOwnerUpdate) {
		t.Fatalf("service takeover stored=%+v err=%v", stored, err)
	}
}

func TestExplicitFreeLocalDeleteChangesEndpointWithoutRetainingHistory(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	idx := testIndexerWithConfig(t, Config{
		EndpointID:     "review-local-delete",
		AllowFreeLocal: true,
		FreeLocalCache: DefaultFreeLocalCachePolicy(),
		FeeVerifier:    JSONFeeVerifier{AllowFreeLocal: true},
		CurrentHeight:  func() uint64 { return 100 },
	})
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), "explicit-delete/value")
	if err != nil {
		t.Fatal(err)
	}
	first := reviewFreeLocalRecord(t, priv, key, 1, "live")
	result, err := idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: first, Precondition: WritePrecondition{ExpectAbsent: true},
	}}, BatchCASOptions{EndpointID: idx.EndpointID()})
	if err != nil || result.Applied != 1 {
		t.Fatalf("initial local write result=%+v err=%v", result, err)
	}
	prefix, err := CollectionPathForKey(key)
	if err != nil {
		t.Fatal(err)
	}
	beforeSnapshot, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: idx.EndpointID(), Full: true})
	if err != nil {
		t.Fatal(err)
	}
	beforeMeta, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	scope := ActiveScope{Prefix: prefix}
	beforeActive, err := idx.ActiveMetadata(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	command := signedCurrentDelete(t, priv, first, 100)
	hash := RecordHash(first)
	mutations := []CASMutation{{Record: command, Precondition: WritePrecondition{ExpectedHash: &hash}}}
	result, err = idx.PutLocalBatchCASResultWithOptions(mutations, BatchCASOptions{EndpointID: idx.EndpointID()})
	if err != nil || result.Applied != 1 || !result.LocalOnly {
		t.Fatalf("explicit local delete result=%+v err=%v", result, err)
	}
	state, err := idx.GetKeyState(key)
	if err != nil || state.Status != KeyStateNeverSeen || state.Seq != 0 || state.ETag != "" {
		t.Fatalf("explicit local delete retained state=%+v err=%v", state, err)
	}
	delta, err := idx.ActiveSyncPage(context.Background(), ActiveSyncRequest{Scope: ActiveScope{Prefix: prefix}, EndpointID: beforeSnapshot.Meta.EndpointID, After: beforeSnapshot.Meta.Generation})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Records) != 0 {
		t.Fatalf("current-record query returned deletion history: %+v", delta)
	}
	afterActive, err := idx.ActiveMetadata(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if afterActive.Generation <= beforeActive.Generation || afterActive.Root == beforeActive.Root {
		t.Fatalf("physical deletion did not invalidate endpoint view: before=%+v after=%+v", beforeActive, afterActive)
	}
	afterMeta, err := idx.GetPathMeta(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if afterMeta.Generation != beforeMeta.Generation || afterMeta.StateRoot != beforeMeta.StateRoot {
		t.Fatalf("local delete changed canonical state: before=%+v after=%+v", beforeMeta, afterMeta)
	}
	assertNoDeleteRows(t, idx)

	replay, err := idx.PutLocalBatchCASResultWithOptions(mutations, BatchCASOptions{EndpointID: idx.EndpointID()})
	if err != nil || replay.Applied != 0 {
		t.Fatalf("absent delete retry result=%+v err=%v", replay, err)
	}
	// A new lifetime starts at sequence 1 with an absent precondition, not a
	// CAS against a tombstone hash. The old command cannot delete this value.
	recreated := reviewFreeLocalRecord(t, priv, key, 1, "recreated")
	result, err = idx.PutLocalBatchCASResultWithOptions([]CASMutation{{
		Record: recreated, Precondition: WritePrecondition{ExpectAbsent: true},
	}}, BatchCASOptions{EndpointID: idx.EndpointID()})
	if err != nil || result.Applied != 1 {
		t.Fatalf("recreate result=%+v err=%v", result, err)
	}
	if _, err := idx.PutLocalBatchCASResultWithOptions(mutations, BatchCASOptions{EndpointID: idx.EndpointID()}); !errors.Is(err, ErrWriteConflict) {
		t.Fatalf("delayed old delete accepted against recreation: %v", err)
	}
	stored, err := idx.Get(key)
	if err != nil || RecordHash(stored) != RecordHash(recreated) {
		t.Fatalf("recreated local record=%+v err=%v", stored, err)
	}
}

func TestRealtimeNotifyOrdersCurrentVersionsByKeySequence(t *testing.T) {
	idx := testIndexerWithConfig(t, Config{EndpointID: "sequence", FeeVerifier: JSONFeeVerifier{AllowFreeLocal: true}, CurrentHeight: func() uint64 { return 100 }})
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	key := "/personal/" + AccountID(priv.PubKey().SerializeCompressed()) + "/sequence/value"
	makeRecord := func(seq, height uint64, value string) *Record {
		r, err := NewSignedRecord(priv, key, []byte(value), RecordOptions{Seq: seq, IssueHeight: height, TTL: 1000})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := makeRecord(1, 10, "first")
	if _, err := idx.PutLocal(first); err != nil {
		t.Fatal(err)
	}
	current := makeRecord(2, 20, "current")
	if _, err := idx.PutLocal(current); err != nil {
		t.Fatal(err)
	}
	old := makeRecord(1, 30, "old-lifecycle")
	if updated, err := idx.AcceptCurrentRecord(old); err != nil || updated {
		t.Fatalf("lower Seq with later height: updated=%v err=%v", updated, err)
	}
	equal := makeRecord(2, 30, "conflict")
	if updated, err := idx.AcceptCurrentRecord(equal); !errors.Is(err, ErrPathDiverged) || updated {
		t.Fatalf("equal Seq with different hash: updated=%v err=%v", updated, err)
	}
	higher := makeRecord(3, 15, "higher")
	if updated, err := idx.AcceptCurrentRecord(higher); err != nil || !updated {
		t.Fatalf("higher Seq with earlier height: updated=%v err=%v", updated, err)
	}
	if updated, err := idx.AcceptCurrentRecord(higher); err != nil || updated {
		t.Fatalf("identical retry: updated=%v err=%v", updated, err)
	}
	got, err := idx.Get(key)
	if err != nil || RecordHash(got) != RecordHash(higher) {
		t.Fatalf("current=%+v err=%v", got, err)
	}
}
