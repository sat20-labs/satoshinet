package indexer

import (
	"os"
	"path/filepath"
	"testing"

	idxcommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestDKVSDatabaseUsesIndependentNetworkDirectory(t *testing.T) {
	previousInstance, previousChain := instance, idxcommon.CHAIN
	t.Cleanup(func() { instance, idxcommon.CHAIN = previousInstance, previousChain })
	root := t.TempDir()
	for _, params := range []*chaincfg.Params{&chaincfg.MainNetParams, &chaincfg.TestNetParams, &chaincfg.RegressionNetParams} {
		t.Run(params.Name, func(t *testing.T) {
			instance = nil
			mgr := NewIndexerMgr(&Config{DataPath: root}, params, nil)
			mgr.Init()
			t.Cleanup(func() { require.NoError(t, mgr.Close()) })
			_, err := os.Stat(filepath.Join(root, "db", "dkvs", params.Name))
			require.NoError(t, err)
			_, err = os.Stat(filepath.Join(mgr.dbDir, "dkvs"))
			require.True(t, os.IsNotExist(err), "must not create DKVS inside the rebuildable indexer directory")
			_, err = mgr.dkvsDB.Read([]byte("network-marker"))
			require.ErrorIs(t, err, idxcommon.ErrKeyNotFound, "networks must not share a database")
			require.NoError(t, mgr.dkvsDB.Write([]byte("network-marker"), []byte(params.Name)))
		})
	}
}

func TestDKVSDatabaseSurvivesIndexerRebuild(t *testing.T) {
	previousInstance, previousChain := instance, idxcommon.CHAIN
	t.Cleanup(func() { instance, idxcommon.CHAIN = previousInstance, previousChain })
	for _, rebuild := range []bool{false, true} {
		name := "restart"
		if rebuild {
			name = "delete_indexer_and_restart"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cfg := &Config{DataPath: root, DKVS: &DKVSIntegrationConfig{
				FeeVerifier: dkvs.JSONFeeVerifier{AllowFreeLocal: true},
			}}
			open := func() *IndexerMgr {
				instance = nil
				mgr := NewIndexerMgr(cfg, &chaincfg.RegressionNetParams, nil)
				mgr.Init()
				t.Cleanup(func() { require.NoError(t, mgr.Close()) })
				return mgr
			}
			mgr := open()
			priv, err := btcec.NewPrivateKey()
			require.NoError(t, err)
			key, err := dkvs.PersonalKey(priv.PubKey().SerializeCompressed(), "preserved/item")
			require.NoError(t, err)
			record, err := dkvs.NewAccountRecord(key, []byte("original"), dkvs.RecordOptions{Seq: 1, TTL: 100})
			require.NoError(t, err)
			proof, err := dkvs.NewFreeLocalFeeProof(key, "personal", uint32(dkvs.RecordSize(record)), dkvs.RecordExpiryHeight(record))
			require.NoError(t, err)
			record.FeeProof, err = dkvs.EncodeFeeProof(proof)
			require.NoError(t, err)
			signDKVSTestRecord(t, priv, record)
			changed, err := mgr.PutDKVSRecord(record)
			require.NoError(t, err)
			require.True(t, changed)
			oldHash := dkvs.RecordHash(record)
			record.Seq = 2
			record.Value = []byte("retained")
			signDKVSTestRecord(t, priv, record)
			changed, err = mgr.PutDKVSRecordCAS(record, dkvs.WritePrecondition{ExpectedHash: &oldHash})
			require.NoError(t, err)
			require.True(t, changed)
			path, err := dkvs.CollectionPathForKey(key)
			require.NoError(t, err)
			meta, err := mgr.dkvsIndexer.GetPathMeta(path)
			require.NoError(t, err)
			require.NotZero(t, meta.EndpointGeneration)
			require.NoError(t, mgr.baseDB.Write([]byte("indexer-marker"), []byte("base")))
			require.NoError(t, mgr.Close())
			if rebuild {
				require.NoError(t, os.RemoveAll(filepath.Join(root, "db", "indexer")))
			}
			mgr = open()
			got, err := mgr.GetDKVSRecord(key)
			require.NoError(t, err)
			require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(got))
			require.EqualValues(t, 2, got.Seq)
			restoredMeta, err := mgr.dkvsIndexer.GetPathMeta(path)
			require.NoError(t, err)
			require.Equal(t, meta, restoredMeta, "DKVS metadata must survive alongside records")
			_, err = mgr.baseDB.Read([]byte("indexer-marker"))
			if rebuild {
				require.ErrorIs(t, err, idxcommon.ErrKeyNotFound, "the chain index must actually have been rebuilt")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, 0, mgr.compiling.GetHeight())
			// Continue through the public CAS API after reopening the database.
			hash := dkvs.RecordHash(record)
			record.Seq++
			signDKVSTestRecord(t, priv, record)
			changed, err = mgr.PutDKVSRecordCAS(record, dkvs.WritePrecondition{ExpectedHash: &hash})
			require.NoError(t, err)
			require.True(t, changed)
		})
	}
}
