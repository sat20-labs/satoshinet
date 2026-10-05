package dkvs

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestWalletRPCBindingIsBootstrapExceptionAndRelayable(t *testing.T) {
	accountPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	corePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	coreID := hex.EncodeToString(corePriv.PubKey().SerializeCompressed())
	var events []*NotifyEvent
	idx := testIndexerWithConfig(t, Config{
		EndpointID:    coreID,
		CurrentHeight: func() uint64 { return 1 },
		Notify:        func(event *NotifyEvent) { events = append(events, event) },
	})
	record := signedBindingRecord(t, accountPriv, corePriv, 1)
	accountID := AccountID(accountPriv.PubKey().SerializeCompressed())

	policy := &WalletRPCAdmission{
		Indexer:    idx,
		IsCoreNode: func() bool { return true },
		CurrentBinding: func(account string) (*wire.DKVSRecord, error) {
			if account != accountID {
				return nil, ErrRecordNotFound
			}
			return idx.Get(record.Key)
		},
	}
	mutation := CASMutation{Record: record, Precondition: WritePrecondition{ExpectAbsent: true}}
	result, err := policy.PutRecords([]CASMutation{mutation}, BatchCASOptions{EndpointID: coreID}, nil)
	if err != nil || result == nil || result.Applied != 1 {
		t.Fatalf("bootstrap binding result=%+v err=%v", result, err)
	}
	if len(events) != 1 || !events[0].Relay {
		t.Fatalf("binding must emit one relayable DKVS event: %#v", events)
	}
	relay, err := idx.GetForRelay(record.Key)
	if err != nil || RecordHash(relay) != RecordHash(record) {
		t.Fatalf("binding is not available to P2P relay: record=%+v err=%v", relay, err)
	}
	currentBinding, err := policy.CurrentBinding(accountID)
	if err != nil || RecordHash(currentBinding) != RecordHash(record) {
		t.Fatalf("committed binding is not current: record=%+v err=%v", currentBinding, err)
	}

	// An idempotent retry does not create a fake second state transition.
	result, err = policy.PutRecords([]CASMutation{mutation}, BatchCASOptions{EndpointID: coreID}, nil)
	if err != nil || result == nil || result.Applied != 0 {
		t.Fatalf("idempotent binding result=%+v err=%v", result, err)
	}
	if len(events) != 1 {
		t.Fatalf("idempotent binding unexpectedly emitted another transition: %d", len(events))
	}
}

func TestWalletRPCBindingBootstrapRejectsWrongTargetAndMixedBatch(t *testing.T) {
	accountPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	corePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherCore, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	coreID := hex.EncodeToString(corePriv.PubKey().SerializeCompressed())
	idx := testIndexerWithConfig(t, Config{EndpointID: coreID, CurrentHeight: func() uint64 { return 1 }})

	policy := &WalletRPCAdmission{
		Indexer:        idx,
		IsCoreNode:     func() bool { return true },
		CurrentBinding: func(string) (*wire.DKVSRecord, error) { return nil, ErrRecordNotFound },
	}
	wrongTarget := signedBindingRecord(t, accountPriv, otherCore, 1)
	if _, err := policy.PutRecords([]CASMutation{{
		Record: wrongTarget, Precondition: WritePrecondition{ExpectAbsent: true},
	}}, BatchCASOptions{EndpointID: coreID}, nil); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("binding to another CoreNode err=%v", err)
	}

	first := signedBindingRecord(t, accountPriv, corePriv, 1)
	secondAccount, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	second := signedBindingRecord(t, secondAccount, corePriv, 1)
	if _, err := policy.PutRecords([]CASMutation{
		{Record: first, Precondition: WritePrecondition{ExpectAbsent: true}},
		{Record: second, Precondition: WritePrecondition{ExpectAbsent: true}},
	}, BatchCASOptions{EndpointID: coreID}, nil); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("mixed/multi binding batch err=%v", err)
	}
	if _, err := idx.Get(first.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("rejected mixed batch changed state: %v", err)
	}
}

func TestWalletBindingBootstrapEnforcesHeightWindowWithoutRestrictingP2P(t *testing.T) {
	accountPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	corePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	coreID := hex.EncodeToString(corePriv.PubKey().SerializeCompressed())
	height := uint64(100)
	idx := testIndexerWithConfig(t, Config{EndpointID: coreID, CurrentHeight: func() uint64 { return height }})
	policy := &WalletRPCAdmission{Indexer: idx, IsCoreNode: func() bool { return true }, CurrentBinding: func(string) (*wire.DKVSRecord, error) { return nil, ErrRecordNotFound }}
	record := signedBindingRecord(t, accountPriv, corePriv, 1)
	record.IssueHeight = 97
	SignRecord(accountPriv, record)
	mutation := CASMutation{Record: record, Precondition: WritePrecondition{ExpectAbsent: true}}
	if _, err := policy.PutRecords([]CASMutation{mutation}, BatchCASOptions{EndpointID: coreID}, nil); !errors.Is(err, ErrStaleEndpoint) {
		t.Fatalf("old new binding accepted: %v", err)
	}
	if _, err := idx.Get(record.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("rejected binding installed: %v", err)
	}
	// Still-valid current binding records received from a CoreNode do not
	// inherit the new-wallet-request height restriction.
	if updated, err := idx.AcceptCurrentRecord(record); err != nil || !updated {
		t.Fatalf("old P2P binding updated=%v err=%v", updated, err)
	}
	if result, err := policy.PutRecords([]CASMutation{mutation}, BatchCASOptions{EndpointID: coreID}, nil); err != nil || result.Applied != 0 {
		t.Fatalf("exact current retry result=%+v err=%v", result, err)
	}
	refreshed := signedBindingRecord(t, accountPriv, corePriv, 2)
	refreshed.IssueHeight = 98
	SignRecord(accountPriv, refreshed)
	hash := RecordHash(record)
	mutation = CASMutation{Record: refreshed, Precondition: WritePrecondition{ExpectedHash: &hash}}
	if result, err := policy.PutRecords([]CASMutation{mutation}, BatchCASOptions{EndpointID: coreID}, nil); err != nil || result.Applied != 1 {
		t.Fatalf("height-2 binding result=%+v err=%v", result, err)
	}
	height = 101
	if result, err := policy.PutRecords([]CASMutation{mutation}, BatchCASOptions{EndpointID: coreID}, nil); err != nil || result.Applied != 0 {
		t.Fatalf("delayed exact retry result=%+v err=%v", result, err)
	}
}
