package main

import (
	"testing"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestCoreBindingAcceptanceMatchesExactRecord(t *testing.T) {
	db := testMessageDatabase(t)
	_, account := testAccount(t)
	record := &wire.DKVSRecord{Version: dkvs.Version, Key: "/personal/" + account + "/service/corenode", Value: []byte("core-a"), Seq: 1, Signature: []byte{1}}
	store := &databaseCoreBindingAcceptanceStore{db: db}
	if err := store.Accept(account, record); err != nil {
		t.Fatal(err)
	}
	if !store.Matches(account, record) {
		t.Fatal("accepted binding not matched")
	}
	changed := *record
	changed.Seq = 2
	if store.Matches(account, &changed) {
		t.Fatal("new binding sequence must require a new service acceptance")
	}
}
