package indexer

import (
	"errors"
	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/common"
	baseindexer "github.com/sat20-labs/satoshinet/indexer/indexer/base"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"testing"
)

func TestDKVSRecordIssueHeightIgnoresAdvertisedSyncTarget(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("base DB unavailable")
	}
	t.Cleanup(func() { database.Close() })
	compiling := baseindexer.NewBaseIndexer(database, &chaincfg.TestNetParams, -1, 100)
	compiling.SetBlockCallback(func(*common.Block) {})
	compiling.SetUpdateDBCallback(func() {})
	compiling.Init()
	if err := compiling.SyncBlock(chaincfg.TestNetParams.GenesisBlock, 0, 14, false); err != nil {
		t.Fatal(err)
	}
	if err := compiling.SyncBlock(&wire.MsgBlock{Header: wire.BlockHeader{PrevBlock: *chaincfg.TestNetParams.GenesisHash}}, 1, 14, false); err != nil {
		t.Fatal(err)
	}
	if compiling.GetHeight() != 1 || compiling.GetChainTip() != 14 {
		t.Fatal("fixture must have applied height 1 and advertised sync target 14")
	}
	mgr := &IndexerMgr{cfg: &Config{DKVS: &DKVSIntegrationConfig{FeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true}}}, chaincfgParam: &chaincfg.TestNetParams, compiling: compiling}
	cfg := mgr.dkvsConfig()
	if cfg.CurrentHeight() != 1 {
		t.Fatal("local applied height must be 1")
	}
	store := dbpkg.NewKVDB(t.TempDir())
	if store == nil {
		t.Fatal("DKVS DB unavailable")
	}
	t.Cleanup(func() { store.Close() })
	idx := dkvs.New(store, cfg)
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		issue    uint64
		accepted bool
	}{{"at_local_tip", 1, true}, {"beyond_local_tip", 2, false}, {"at_advertised_target", 14, false}} {
		t.Run(tc.name, func(t *testing.T) {
			key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "issue/"+tc.name)
			if err != nil {
				t.Fatal(err)
			}
			record, err := dkvs.NewAccountRecord(key, []byte("intact"), dkvs.RecordOptions{Seq: 1, IssueHeight: tc.issue, TTL: 5})
			if err != nil {
				t.Fatal(err)
			}
			proof, err := dkvs.NewFreeLocalFeeProof(key, "personal", uint32(dkvs.RecordSize(record)), dkvs.RecordExpiryHeight(record))
			if err != nil {
				t.Fatal(err)
			}
			record.FeeProof, err = dkvs.EncodeFeeProof(proof)
			if err != nil {
				t.Fatal(err)
			}
			signDKVSTestRecord(t, priv, record)
			changed, err := idx.PutLocal(record)
			if !tc.accepted {
				if !errors.Is(err, dkvs.ErrInvalidRecord) {
					t.Fatalf("future record must be rejected: changed=%t err=%v", changed, err)
				}
				return
			}
			if err != nil || !changed {
				t.Fatalf("known chain-tip record rejected while indexer catches up: changed=%t err=%v", changed, err)
			}
			got, err := idx.Get(key)
			if err != nil || dkvs.RecordHash(got) != dkvs.RecordHash(record) {
				t.Fatalf("admitted record must remain readable and unchanged: %v", err)
			}
			state, err := idx.GetKeyState(key)
			if err != nil || state.Status != dkvs.KeyStateActive {
				t.Fatalf("sync target must not expire a locally active record: %+v %v", state, err)
			}
		})
	}
}
