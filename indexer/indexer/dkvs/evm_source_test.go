package dkvs

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestEVMSourceBlobIsImmutableAndDeploymentFunded(t *testing.T) {
	verify := func(record *wire.DKVSRecord) error {
		if string(record.Value) != "verified-source" {
			return ErrInvalidRecord
		}
		return nil
	}
	idx := testIndexerWithConfig(t, Config{EVMSourceVerifier: verify})
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	record, err := NewRecord("/blob/evm/source/testcontract", []byte("verified-source"), priv.PubKey().SerializeCompressed(), RecordOptions{Seq: 1})
	require.NoError(t, err)
	SignRecord(priv, record)
	updated, err := idx.PutLocalCAS(record, WritePrecondition{ExpectAbsent: true})
	require.NoError(t, err)
	require.True(t, updated)
	updated, err = idx.PutLocalCAS(record, WritePrecondition{ExpectAbsent: true})
	require.NoError(t, err)
	require.False(t, updated)
	got, err := idx.Get(record.Key)
	require.NoError(t, err)
	require.Equal(t, RecordHash(record), RecordHash(got))
	require.NoError(t, VerifyBlobRecordForClient(got, RecordVerificationOptions{ExpectedKey: record.Key}))
	for _, mode := range []string{"overwrite", "sequence", "ttl", "fee", "tombstone"} {
		t.Run(mode, func(t *testing.T) {
			next := cloneRecord(record)
			switch mode {
			case "overwrite":
				next.Value = []byte("different-source")
			case "sequence":
				next.Seq = 2
			case "ttl":
				next.TTL = 1
			case "fee":
				next.FeeProof = []byte("free")
			case "tombstone":
				next.Flags = FlagTombstone
			}
			SignRecord(priv, next)
			_, err := idx.PutLocal(next)
			require.Error(t, err)
			_, err = idx.AcceptCurrentRecord(next)
			require.Error(t, err)
		})
	}
	peer := testIndexerWithConfig(t, Config{EVMSourceVerifier: verify})
	updated, err = peer.AcceptCurrentRecord(record)
	require.NoError(t, err)
	require.True(t, updated)
	snapshot, err := idx.GetPathSnapshot(record.Key)
	require.NoError(t, err)
	snapshotPeer := testIndexerWithConfig(t, Config{EVMSourceVerifier: verify})
	_, err = snapshotPeer.ApplyPathSnapshot(snapshot)
	require.NoError(t, err)
	got, err = snapshotPeer.Get(record.Key)
	require.NoError(t, err)
	require.Equal(t, RecordHash(record), RecordHash(got))
	empty := testIndexerWithConfig(t, Config{EVMSourceVerifier: verify})
	missing, err := empty.GetPathSnapshot(record.Key)
	require.NoError(t, err)
	baseline, err := peer.NetworkSyncBaseline(record.Key)
	require.NoError(t, err)
	_, err = peer.ApplyPathSnapshotFrom(missing, baseline)
	require.ErrorIs(t, err, ErrWriteConflict)
	_, err = peer.DeleteMirrorKeys([]string{record.Key})
	require.ErrorIs(t, err, ErrPermissionDenied)
	_, err = peer.applyRecordSetAtomic(nil, []syncRange{{target: record.Key, exact: true}}, nil, true, true)
	require.ErrorIs(t, err, ErrWriteConflict)
	got, err = peer.Get(record.Key)
	require.NoError(t, err)
	require.Equal(t, RecordHash(record), RecordHash(got))
	closed := testIndexerWithConfig(t, Config{})
	_, err = closed.PutLocal(record)
	require.ErrorIs(t, err, ErrPermissionDenied)
	unsigned := cloneRecord(record)
	unsigned.Signature = nil
	_, err = empty.PutLocal(unsigned)
	require.ErrorIs(t, err, ErrInvalidSignature)
}
