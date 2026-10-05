package dkvs

import (
	"errors"
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/wire"
)

func testIndexerWithHeight(t *testing.T, height uint64) *Indexer {
	t.Helper()
	db := dbpkg.NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db, Config{
		AllowFreeLocal: true,
		CurrentHeight:  func() uint64 { return height },
	})
}

func pullByNotify(t *testing.T, source, target *Indexer, key string) {
	t.Helper()
	record, err := source.GetForRelay(key)
	if err != nil {
		t.Fatalf("source get: %v", err)
	}
	if updated, err := target.PutRemote(record); err != nil || !updated {
		t.Fatalf("target put remote updated=%v err=%v", updated, err)
	}
}

func syncAllPages(t *testing.T, source, target *Indexer, limit uint32) {
	t.Helper()
	var cursor []byte
	for {
		records, next, done, _, err := source.Sync(cursor, limit)
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		for _, record := range records {
			if _, err := target.PutRemote(record); err != nil {
				t.Fatalf("put remote %s: %v", record.Key, err)
			}
		}
		if done {
			return
		}
		cursor = next
		if len(cursor) == 0 {
			t.Fatal("missing cursor for next sync page")
		}
	}
}

func syncFilteredPages(t *testing.T, source, target *Indexer, limit uint32, filters []Subscription) {
	t.Helper()
	var cursor []byte
	for {
		records, next, done, _, err := source.SyncFiltered(cursor, limit, filters)
		if err != nil {
			t.Fatalf("filtered sync: %v", err)
		}
		for _, record := range records {
			if !target.IsSubscribed(record.Key) {
				t.Fatalf("filtered sync returned unsubscribed key %s", record.Key)
			}
			if _, err := target.PutRemote(record); err != nil {
				t.Fatalf("put filtered record %s: %v", record.Key, err)
			}
		}
		if done {
			return
		}
		cursor = next
		if len(cursor) == 0 {
			t.Fatal("missing cursor for next filtered sync page")
		}
	}
}

func TestThreeMinerNotifyAndStartupSyncConverge(t *testing.T) {
	minerA := testIndexerWithHeight(t, 1)
	minerB := testIndexerWithHeight(t, 1)
	minerC := testIndexerWithHeight(t, 1)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	initial := signedPersonalRecordWithKey(t, priv, 1, "initial", 0)
	path, err := CollectionPathForKey(initial.Key)
	if err != nil {
		t.Fatal(err)
	}
	forward := func(record *wire.DKVSRecord) {
		t.Helper()
		for _, miner := range []*Indexer{minerB, minerC} {
			if updated, err := miner.AcceptCurrentRecord(record); err != nil || !updated {
				t.Fatalf("inline replication updated=%v err=%v", updated, err)
			}
		}
	}
	if updated, err := minerA.PutLocal(initial); err != nil || !updated {
		t.Fatalf("miner A put initial updated=%v err=%v", updated, err)
	}
	forward(initial)
	for _, miner := range []*Indexer{minerB, minerC} {
		got, err := miner.Get(initial.Key)
		if err != nil || RecordHash(got) != RecordHash(initial) {
			t.Fatalf("initial replication record=%+v err=%v", got, err)
		}
	}

	updatedRecord := signedPersonalRecordWithKey(t, priv, 2, "updated", 0)
	if updated, err := minerA.PutLocal(updatedRecord); err != nil || !updated {
		t.Fatalf("miner A put updated updated=%v err=%v", updated, err)
	}
	forward(updatedRecord)
	for _, miner := range []*Indexer{minerB, minerC} {
		got, err := miner.Get(updatedRecord.Key)
		if err != nil || RecordHash(got) != RecordHash(updatedRecord) {
			t.Fatalf("update replication record=%+v err=%v", got, err)
		}
	}

	newMiner := testIndexerWithHeight(t, 1)
	reconcile := func() {
		t.Helper()
		baseline, err := newMiner.NetworkSyncBaseline(path)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := minerA.GetPathSnapshot(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := newMiner.ApplyPathSnapshotFrom(snapshot, baseline); err != nil {
			t.Fatal(err)
		}
	}
	reconcile()
	got, err := newMiner.Get(updatedRecord.Key)
	if err != nil || RecordHash(got) != RecordHash(updatedRecord) {
		t.Fatalf("startup snapshot record=%+v err=%v", got, err)
	}

	// Online peers get the operation itself. It cannot be fetched from a
	// retained delete record afterward; the offline peer uses a current set.
	command := signedCurrentDelete(t, priv, updatedRecord, 1)
	if updated, err := minerA.PutLocal(command); err != nil || !updated {
		t.Fatalf("miner A delete updated=%v err=%v", updated, err)
	}
	forward(command)
	for _, miner := range []*Indexer{minerA, minerB, minerC} {
		if _, err := miner.Get(command.Key); !errors.Is(err, ErrRecordNotFound) {
			t.Fatalf("deleted key should be absent: %v", err)
		}
		if _, err := miner.GetForRelay(command.Key); !errors.Is(err, ErrRecordNotFound) {
			t.Fatalf("deletion history exposed for relay: %v", err)
		}
		assertNoDeleteRows(t, miner)
	}
	if _, err := newMiner.Get(command.Key); err != nil {
		t.Fatalf("offline fixture unexpectedly received the online delete: %v", err)
	}
	reconcile()
	if _, err := newMiner.Get(command.Key); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("reconnect did not remove omitted current key: %v", err)
	}
	assertNoDeleteRows(t, newMiner)
}

func TestOrdinaryNodeMailboxSubscriptionSyncAndNotify(t *testing.T) {
	bound := testIndexerWithHeight(t, 1)
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := AccountID(ownerPriv.PubKey().SerializeCompressed())
	msg1 := testMailMsgKey(t, ownerPriv.PubKey().SerializeCompressed(), senderPriv.PubKey().SerializeCompressed(), "msg-1")
	msg2 := testMailMsgKey(t, ownerPriv.PubKey().SerializeCompressed(), senderPriv.PubKey().SerializeCompressed(), "msg-2")
	for _, item := range []struct {
		key   string
		value string
	}{{msg1, "msg-1"}, {msg2, "msg-2"}} {
		if _, err := bound.PutInternalMailbox(internalMailboxTestRecord(item.key, []byte(item.value), 100)); err != nil {
			t.Fatal(err)
		}
	}

	records, total, err := bound.Subscribe(Subscription{Type: SubscriptionMailbox, Target: mailboxID})
	if err != nil || total != 2 || len(records) != 2 {
		t.Fatalf("mailbox subscribe records=%d total=%d err=%v", len(records), total, err)
	}
	clientRecords, _, done, _, err := bound.SyncFilteredForClient(nil, 10,
		[]Subscription{{Type: SubscriptionMailbox, Target: mailboxID}})
	if err != nil || !done || len(clientRecords) != 2 {
		t.Fatalf("client mailbox sync records=%d done=%v err=%v", len(clientRecords), done, err)
	}

	// AccountBound mailbox data is read from the bound CoreNode only.
	networkRecords, _, networkDone, _, err := bound.SyncFiltered(nil, 10,
		[]Subscription{{Type: SubscriptionMailbox, Target: mailboxID}})
	if err != nil || !networkDone || len(networkRecords) != 0 {
		t.Fatalf("network mailbox sync records=%d done=%v err=%v", len(networkRecords), networkDone, err)
	}
	if _, err := bound.GetForRelay(msg1); err != ErrRecordNotFound {
		t.Fatalf("mailbox record became relayable err=%v", err)
	}
}

func TestOrdinaryNodeKeyAndPrefixSubscriptionSyncAndNotify(t *testing.T) {
	miner := testIndexerWithHeight(t, 1)
	ordinary := testIndexerWithHeight(t, 1)
	tmpPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	personalPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherPersonalPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}

	exactKey := "/tmp/exact"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, tmpPriv, exactKey, 1, []byte("exact"), 0)); err != nil || !updated {
		t.Fatalf("put exact key updated=%v err=%v", updated, err)
	}
	otherTmp := "/tmp/other"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, tmpPriv, otherTmp, 1, []byte("other"), 0)); err != nil || !updated {
		t.Fatalf("put other tmp updated=%v err=%v", updated, err)
	}
	personalPrefix := "/personal/" + AccountID(personalPriv.PubKey().SerializeCompressed())
	profileKey := personalPrefix + "/profile"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, personalPriv, profileKey, 1, []byte("profile"), 0)); err != nil || !updated {
		t.Fatalf("put profile updated=%v err=%v", updated, err)
	}
	settingsKey := personalPrefix + "/settings"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, personalPriv, settingsKey, 2, []byte("settings"), 0)); err != nil || !updated {
		t.Fatalf("put settings updated=%v err=%v", updated, err)
	}
	otherPersonalPrefix := "/personal/" + AccountID(otherPersonalPriv.PubKey().SerializeCompressed())
	otherProfileKey := otherPersonalPrefix + "/profile"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, otherPersonalPriv, otherProfileKey, 1, []byte("other-profile"), 0)); err != nil || !updated {
		t.Fatalf("put other profile updated=%v err=%v", updated, err)
	}

	if records, total, err := ordinary.Subscribe(Subscription{Type: SubscriptionKey, Target: exactKey}); err != nil || len(records) != 0 || total != 0 {
		t.Fatalf("key subscribe records=%d total=%d err=%v", len(records), total, err)
	}
	if records, total, err := ordinary.Subscribe(Subscription{Type: SubscriptionPrefix, Target: personalPrefix}); err != nil || len(records) != 0 || total != 0 {
		t.Fatalf("prefix subscribe records=%d total=%d err=%v", len(records), total, err)
	}
	syncFilteredPages(t, miner, ordinary, 1, ordinary.Subscriptions())
	for _, key := range []string{exactKey, profileKey, settingsKey} {
		if _, err := ordinary.Get(key); err != nil {
			t.Fatalf("ordinary missing subscribed key %s: %v", key, err)
		}
	}
	for _, key := range []string{otherTmp, otherProfileKey} {
		if _, err := ordinary.Get(key); err != ErrRecordNotFound {
			t.Fatalf("ordinary stored unsubscribed key %s err=%v", key, err)
		}
	}

	notifyKey := personalPrefix + "/recovery"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, personalPriv, notifyKey, 3, []byte("recovery"), 0)); err != nil || !updated {
		t.Fatalf("put notify prefix key updated=%v err=%v", updated, err)
	}
	if !ordinary.IsSubscribed(notifyKey) {
		t.Fatalf("ordinary prefix subscription does not match %s", notifyKey)
	}
	pullByNotify(t, miner, ordinary, notifyKey)
	got, err := ordinary.Get(notifyKey)
	if err != nil {
		t.Fatalf("ordinary prefix notify get: %v", err)
	}
	if string(got.Value) != "recovery" {
		t.Fatalf("prefix notify value=%q", got.Value)
	}
}

func TestOrdinaryNodeServiceSubscriptionSyncAndNotify(t *testing.T) {
	servicePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherServicePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	resolver := StaticDIDResolver{
		Services: map[string]DIDIdentity{
			"wallet": testIdentity("wallet", servicePriv.PubKey().SerializeCompressed()),
			"market": testIdentity("market", otherServicePriv.PubKey().SerializeCompressed()),
		},
	}
	miner := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Resolver:       resolver,
	})
	ordinary := testIndexerWithConfig(t, Config{
		AllowFreeLocal: true,
		Resolver:       resolver,
	})

	walletConfig := "/svc/wallet/config"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, servicePriv, walletConfig, 1, []byte("wallet-config"), 0)); err != nil || !updated {
		t.Fatalf("put wallet config updated=%v err=%v", updated, err)
	}
	walletStatus := "/svc/wallet/status"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, servicePriv, walletStatus, 2, []byte("wallet-status"), 0)); err != nil || !updated {
		t.Fatalf("put wallet status updated=%v err=%v", updated, err)
	}
	marketConfig := "/svc/market/config"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, otherServicePriv, marketConfig, 1, []byte("market-config"), 0)); err != nil || !updated {
		t.Fatalf("put market config updated=%v err=%v", updated, err)
	}

	records, total, err := ordinary.Subscribe(Subscription{Type: SubscriptionService, Target: "wallet"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 || total != 0 {
		t.Fatalf("ordinary local service subscribe records=%d total=%d", len(records), total)
	}
	syncFilteredPages(t, miner, ordinary, 1, ordinary.Subscriptions())
	for _, key := range []string{walletConfig, walletStatus} {
		if _, err := ordinary.Get(key); err != nil {
			t.Fatalf("ordinary missing service key %s: %v", key, err)
		}
	}
	if _, err := ordinary.Get(marketConfig); err != ErrRecordNotFound {
		t.Fatalf("ordinary stored unrelated service err=%v", err)
	}

	walletUpdate := "/svc/wallet/release"
	if updated, err := miner.PutLocal(signedRecordWithValue(t, servicePriv, walletUpdate, 3, []byte("wallet-release"), 0)); err != nil || !updated {
		t.Fatalf("put wallet update updated=%v err=%v", updated, err)
	}
	if !ordinary.IsSubscribed(walletUpdate) {
		t.Fatalf("ordinary service subscription does not match %s", walletUpdate)
	}
	pullByNotify(t, miner, ordinary, walletUpdate)
	got, err := ordinary.Get(walletUpdate)
	if err != nil {
		t.Fatalf("ordinary service notify get: %v", err)
	}
	if string(got.Value) != "wallet-release" {
		t.Fatalf("service notify value=%q", got.Value)
	}
}
