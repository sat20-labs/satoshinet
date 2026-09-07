package dkvs

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg"
)

type mutableAutopayStateProvider struct {
	state *AutopayContractState
}

func (p *mutableAutopayStateProvider) GetAutopayState(contract string) (*AutopayContractState, error) {
	state := cloneAutopayState(p.state)
	if state != nil && state.Contract == "" {
		state.Contract = contract
	}
	return state, nil
}

func signedAutopayPersonalRecord(t *testing.T, priv *btcec.PrivateKey, contract string, seq uint64) *Record {
	t.Helper()
	key, err := PersonalKey(priv.PubKey().SerializeCompressed(), "paid-retention")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewAccountRecord(key, []byte("value"), RecordOptions{Seq: seq})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := NewAutopayFeeProof(key, "personal", uint32(RecordSize(record)), 0, contract, "")
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = EncodeFeeProof(proof)
	if err != nil {
		t.Fatal(err)
	}
	hash := SigningHash(record)
	sig, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = sig.Serialize()
	return record
}

func TestAutopayRequiresCurrentBlockPayment(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	payer, err := P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	height := uint64(10)
	provider := &mutableAutopayStateProvider{state: &AutopayContractState{
		TemplateName: autopayTemplateName,
		CurrentBlock: int64(height),
		ServiceName:  "dkvs",
		Recipient:    "recipient",
		FeeAssetName: "sgas",
		Status:       "active",
		Delegates: map[string]AutopayDelegateState{
			payer: {AmountPerBlock: "1", Balance: "100", LastPayHeight: int64(height - 1), Status: "active"},
		},
	}}
	cached := &HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return height }}
	verifier := LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: AutopayFeeVerifier{
		StateProvider: cached, Contract: "autopay", ServiceName: "dkvs", Recipient: "recipient",
		FeeAssetName: "sgas", FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
	}, AllowFreeLocal: true}
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	parsed, err := ParseKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.VerifyRecordFeeProof(record, parsed); err == nil {
		t.Fatal("record was accepted before the current block was paid")
	}
	delegate := provider.state.Delegates[payer]
	delegate.LastPayHeight = int64(height)
	delegate.Balance = "0"
	delegate.Status = "funding"
	provider.state.Delegates[payer] = delegate
	height++
	provider.state.CurrentBlock = int64(height)
	delegate.LastPayHeight = int64(height)
	provider.state.Delegates[payer] = delegate
	if err := verifier.VerifyRecordFeeProof(record, parsed); err != nil {
		t.Fatalf("current-block paid record rejected: %v", err)
	}
}

func TestPruneExpiredAutopayAfterNodeCacheGrace(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	payer, err := P2TRAddressFromPubKeyBytes(priv.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	if err != nil {
		t.Fatal(err)
	}
	height := uint64(10)
	provider := &mutableAutopayStateProvider{state: &AutopayContractState{
		TemplateName: autopayTemplateName,
		CurrentBlock: int64(height),
		ServiceName:  "dkvs",
		Recipient:    "recipient",
		FeeAssetName: "sgas",
		Status:       "active",
		Delegates: map[string]AutopayDelegateState{
			payer: {AmountPerBlock: "1", Balance: "100", LastPayHeight: int64(height), Status: "active"},
		},
	}}
	cached := &HeightCachedAutopayStateProvider{Provider: provider, CurrentHeight: func() uint64 { return height }}
	verifier := LocalCacheAutopayFeeVerifier{AutopayFeeVerifier: AutopayFeeVerifier{
		StateProvider: cached, Contract: "autopay", ServiceName: "dkvs", Recipient: "recipient",
		FeeAssetName: "sgas", FullRecordFeePerBlock: "1", AddressParams: &chaincfg.TestNetParams,
	}, AllowFreeLocal: true}
	idx := New(database, Config{
		EndpointID:     "test-core-node",
		AllowFreeLocal: true,
		FreeLocalCache: FreeLocalCachePolicy{Enabled: true, MaxTTL: 2, MaxRecordsPerSigner: 10,
			MaxBytesPerSigner: 1 << 20, MaxTotalRecords: 100, MaxTotalBytes: 1 << 20},
		FeeVerifier:   verifier,
		CurrentHeight: func() uint64 { return height },
	})
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatalf("put paid record: %v", err)
	}
	prefix, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := idx.PrefixSnapshot(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Records) != 1 {
		t.Fatalf("snapshot records=%d", len(snapshot.Records))
	}

	height = 12
	provider.state.CurrentBlock = int64(height)
	if pruned, err := idx.PruneExpiredAutopayAt(height); err != nil || pruned != 0 {
		t.Fatalf("record pruned inside two-block grace: pruned=%d err=%v", pruned, err)
	}
	if _, err := idx.Get(record.Key); err != nil {
		t.Fatalf("record missing inside grace: %v", err)
	}

	height = 13
	provider.state.CurrentBlock = int64(height)
	if pruned, err := idx.PruneExpiredAutopayAt(height); err != nil || pruned != 1 {
		t.Fatalf("expired paid record prune: pruned=%d err=%v", pruned, err)
	}
	if _, err := idx.Get(record.Key); err != ErrRecordNotFound {
		t.Fatalf("record still present after grace: %v", err)
	}
	state, err := idx.GetKeyState(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != KeyStateDeleted || state.Seq != record.Seq || state.ETag == "" {
		t.Fatalf("autopay expired key state=%+v", state)
	}
	status, err := idx.PrefixStatus(snapshot.EndpointID, []PrefixGeneration{{
		Prefix: prefix, Generation: snapshot.Generation,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Changed) != 1 || status.Changed[0].Prefix != prefix ||
		status.Changed[0].Generation == snapshot.Generation {
		t.Fatalf("autopay expiry generation status=%+v", status)
	}
}

func TestDefaultAutopayMinimumIsOne(t *testing.T) {
	defaults := NetworkDefaultsForParams(&chaincfg.TestNetParams)
	if defaults.AutopayMinAmountPerBlock != "1" {
		t.Fatalf("minimum amount per block=%s", defaults.AutopayMinAmountPerBlock)
	}
}

func TestAutopayDeleteRemainsCanonicalWhileRelayCacheIsCold(t *testing.T) {
	idx, priv, _ := newAutopayMirrorIndexer(t, 10)
	record := signedAutopayPersonalRecord(t, priv, "autopay", 1)
	if updated, err := idx.PutLocal(record); err != nil || !updated {
		t.Fatalf("put paid record updated=%v err=%v", updated, err)
	}
	path, err := CollectionPathForKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	// Model the fail-closed window after a node restart: AUTOPAY placement is
	// still canonical even though relay authorization has not been refreshed.
	paidRetentionCacheFor(idx).remove([]string{record.Key})
	if idx.paidRecordRelayable(record) {
		t.Fatal("AUTOPAY relay cache unexpectedly remained warm")
	}
	tombstone := signedRecordWithValue(t, priv, record.Key, 2, nil, FlagTombstone)
	if updated, err := idx.PutLocal(tombstone); err != nil || !updated {
		t.Fatalf("delete paid record updated=%v err=%v", updated, err)
	}
	idx.mutex.RLock()
	state, err := idx.getDeleteStateLocked(record.Key)
	idx.mutex.RUnlock()
	if err != nil || state == nil || state.LocalOnly {
		t.Fatalf("canonical AUTOPAY delete state=%#v err=%v", state, err)
	}
	after, err := idx.GetPathMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation+1 || after.StateRoot == before.StateRoot {
		t.Fatalf("canonical path state before=%+v after=%+v", before, after)
	}
	if relayed, err := idx.GetForRelay(record.Key); err != nil || !IsTombstone(relayed.Flags) {
		t.Fatalf("canonical AUTOPAY tombstone=%#v err=%v", relayed, err)
	}
}
