package dkvs

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
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
	if updated, err := minerA.PutLocal(initial); err != nil || !updated {
		t.Fatalf("miner A put initial updated=%v err=%v", updated, err)
	}
	pullByNotify(t, minerA, minerB, initial.Key)
	pullByNotify(t, minerA, minerC, initial.Key)
	for _, miner := range []*Indexer{minerB, minerC} {
		got, err := miner.Get(initial.Key)
		if err != nil {
			t.Fatalf("get converged initial: %v", err)
		}
		if string(got.Value) != "initial" {
			t.Fatalf("got %q", got.Value)
		}
	}

	updatedRecord := signedPersonalRecordWithKey(t, priv, 2, "updated", 0)
	if updated, err := minerA.PutLocal(updatedRecord); err != nil || !updated {
		t.Fatalf("miner A put updated updated=%v err=%v", updated, err)
	}
	pullByNotify(t, minerA, minerB, updatedRecord.Key)
	pullByNotify(t, minerA, minerC, updatedRecord.Key)
	for _, miner := range []*Indexer{minerB, minerC} {
		got, err := miner.Get(updatedRecord.Key)
		if err != nil {
			t.Fatalf("get converged update: %v", err)
		}
		if got.Seq != 2 || string(got.Value) != "updated" {
			t.Fatalf("bad update seq=%d value=%q", got.Seq, got.Value)
		}
	}

	newMiner := testIndexerWithHeight(t, 1)
	syncAllPages(t, minerA, newMiner, 1)
	got, err := newMiner.Get(updatedRecord.Key)
	if err != nil {
		t.Fatalf("new miner get: %v", err)
	}
	if got.Seq != 2 || string(got.Value) != "updated" {
		t.Fatalf("new miner bad seq=%d value=%q", got.Seq, got.Value)
	}

	tombstone := signedPersonalRecordWithKey(t, priv, 3, "", FlagTombstone)
	if updated, err := minerA.PutLocal(tombstone); err != nil || !updated {
		t.Fatalf("miner A put tombstone updated=%v err=%v", updated, err)
	}
	pullByNotify(t, minerA, minerB, tombstone.Key)
	pullByNotify(t, minerA, minerC, tombstone.Key)
	for _, miner := range []*Indexer{minerA, minerB, minerC} {
		if _, err := miner.Get(tombstone.Key); err != ErrRecordNotFound {
			t.Fatalf("deleted key should be absent: %v", err)
		}
		got, err := miner.GetForRelay(tombstone.Key)
		if err != nil || !IsTombstone(got.Flags) || len(got.Value) != 0 {
			t.Fatalf("delete relay record=%#v err=%v", got, err)
		}
	}
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

	// AccountBound mailbox data is read from the bound CoreNode only and must
	// not enter the miner/ordinary-node P2P mirror stream.
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
