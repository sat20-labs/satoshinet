package indexer

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/indexer/common"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func TestPruneExpiredDKVSOnBlock(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })

	idx := dkvs.New(database, dkvs.Config{AllowFreeLocal: true, CurrentHeight: func() uint64 { return 1 }})
	mgr := &IndexerMgr{dkvsIndexer: idx}
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	record := signedDKVSTestPersonalRecord(t, priv, 1)
	record.ExpiryHeight = 2
	signDKVSTestRecord(t, priv, record)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}

	mgr.pruneExpiredDKVSOnBlock(&common.Block{Height: dkvsPruneIntervalBlocks - 1})
	if pruned, err := idx.PruneExpiredAt(uint64(dkvsPruneIntervalBlocks - 1)); err != nil || pruned != 1 {
		t.Fatalf("manual prune before interval pruned=%d err=%v", pruned, err)
	}

	record = signedDKVSTestPersonalRecord(t, priv, 2)
	record.ExpiryHeight = 2
	signDKVSTestRecord(t, priv, record)
	if _, err := idx.PutLocal(record); err != nil {
		t.Fatal(err)
	}
	mgr.pruneExpiredDKVSOnBlock(&common.Block{Height: dkvsPruneIntervalBlocks})
	if mgr.lastDKVSPruneHeight != dkvsPruneIntervalBlocks {
		t.Fatalf("last prune height=%d", mgr.lastDKVSPruneHeight)
	}
	if _, err := idx.GetByHash(dkvs.RecordHash(record)); err != dkvs.ErrRecordNotFound {
		t.Fatalf("expired hash after auto prune err=%v", err)
	}
	if pruned, err := idx.PruneExpiredAt(uint64(dkvsPruneIntervalBlocks)); err != nil || pruned != 0 {
		t.Fatalf("manual prune after auto pruned=%d err=%v", pruned, err)
	}
	mgr.pruneExpiredDKVSOnBlock(&common.Block{Height: dkvsPruneIntervalBlocks})
	if mgr.lastDKVSPruneHeight != dkvsPruneIntervalBlocks {
		t.Fatalf("repeat prune changed height=%d", mgr.lastDKVSPruneHeight)
	}
}

func signedDKVSTestPersonalRecord(t *testing.T, priv *btcec.PrivateKey, seq uint64) *wire.DKVSRecord {
	t.Helper()
	key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "profile")
	if err != nil {
		t.Fatal(err)
	}
	record, err := dkvs.NewSignedRecord(priv, key, []byte("value"), dkvs.RecordOptions{
		Seq:          seq,
		TTL:          60_000,
		ExpiryHeight: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func signDKVSTestRecord(t *testing.T, priv *btcec.PrivateKey, record *wire.DKVSRecord) {
	t.Helper()
	hash := dkvs.SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
}
