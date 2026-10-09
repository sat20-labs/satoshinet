package dkvs

import (
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"testing"
)

func contractTestKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	key, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	return key
}
func contractTestConfig(key *btcec.PrivateKey) Config {
	return Config{CurrentHeight: func() uint64 { return 100 }, SystemVerifier: StaticSystemVerifier{Keys: [][]byte{key.PubKey().SerializeCompressed()}}}
}
func contractTestRecord(t *testing.T, key *btcec.PrivateKey, id string, value []byte, height uint64) *wire.DKVSRecord {
	t.Helper()
	r, e := NewSignedRecord(key, "/contract/example/"+id, value, RecordOptions{Seq: 1, IssueHeight: height})
	require.NoError(t, e)
	return r
}
func contractTestPut(t *testing.T, idx *Indexer, r *wire.DKVSRecord) {
	t.Helper()
	changed, e := idx.PutInternalContract(r)
	require.NoError(t, e)
	require.True(t, changed)
}
func contractTestSnapshot(t *testing.T, records []*wire.DKVSRecord) *Snapshot {
	t.Helper()
	cp, e := CheckpointFromRecords(records, 100)
	require.NoError(t, e)
	s := &Snapshot{Checkpoint: cp, Records: records, CreatedAt: currentUnixMilli()}
	require.NoError(t, ValidateSnapshot(s))
	return s
}

func TestContractStoreAuthorityEnvelopeAndRecovery(t *testing.T) {
	key := contractTestKey(t)
	cfg := contractTestConfig(key)
	cfg.FeeVerifier = AutopayFeeVerifier{}
	source := testIndexerWithConfig(t, cfg)
	r := contractTestRecord(t, key, "opaque", []byte{0xff, 0, 0x80}, 100)
	outsider := cloneRecord(r)
	SignRecord(contractTestKey(t), outsider)
	_, e := source.PutInternalContract(outsider)
	require.ErrorIs(t, e, ErrPermissionDenied)
	_, e = source.PutLocal(r)
	require.ErrorIs(t, e, ErrPermissionDenied)
	_, e = source.PutLocalCAS(r, WritePrecondition{ExpectAbsent: true})
	require.ErrorIs(t, e, ErrPermissionDenied)
	contractTestPut(t, source, r)
	changed, e := source.PutInternalContract(r)
	require.NoError(t, e)
	require.False(t, changed)
	for _, mutate := range []func(*wire.DKVSRecord){func(r *wire.DKVSRecord) { r.Seq = 2 }, func(r *wire.DKVSRecord) { r.TTL = 10 }, func(r *wire.DKVSRecord) { r.Flags = FlagTombstone }, func(r *wire.DKVSRecord) { r.FeeProof = []byte{1} }} {
		bad := cloneRecord(r)
		mutate(bad)
		SignRecord(key, bad)
		_, e = source.PutInternalContract(bad)
		require.Error(t, e)
		_, e = source.AcceptCurrentRecord(bad)
		require.Error(t, e)
	}
	_, e = source.DeleteMirrorKeys([]string{r.Key})
	require.ErrorIs(t, e, ErrPermissionDenied)
	snapshot, e := source.GetPathSnapshot("/contract/example")
	require.NoError(t, e)
	target := testIndexerWithConfig(t, cfg)
	n, e := target.ApplyPathSnapshot(snapshot)
	require.NoError(t, e)
	require.Equal(t, 1, n)
	reopened := New(target.db, cfg)
	got, e := reopened.Get(r.Key)
	require.NoError(t, e)
	require.Equal(t, r.Value, got.Value)
	read, e := reopened.ReadPrefix("/contract/example")
	require.NoError(t, e)
	require.Len(t, read.Records, 1)
}

func TestContractStoreRejectsMutationBeforeMergeAndAtomicCommit(t *testing.T) {
	for _, height := range []uint64{98, 100} {
		for _, mode := range []string{"full", "mirror", "path", "realtime"} {
			t.Run(mode+string(rune(height)), func(t *testing.T) {
				key := contractTestKey(t)
				cfg := contractTestConfig(key)
				target := testIndexerWithConfig(t, cfg)
				source := testIndexerWithConfig(t, cfg)
				old := contractTestRecord(t, key, "id", []byte("original bytes"), 99)
				contractTestPut(t, target, old)
				replacement := contractTestRecord(t, key, "id", []byte("different bytes"), height)
				tail := contractTestRecord(t, key, "tail", []byte("other value"), 100)
				contractTestPut(t, source, replacement)
				contractTestPut(t, source, tail)
				records := []*wire.DKVSRecord{replacement, tail}
				var e error
				switch mode {
				case "full":
					_, e = target.ApplySnapshot(contractTestSnapshot(t, records))
				case "mirror":
					root, x := DirectoryRootFromRecords(records, 100)
					require.NoError(t, x)
					_, e = target.ApplyMirror([]Subscription{{Type: SubscriptionPrefix, Target: "/contract/example"}}, records, root)
				case "path":
					s, x := source.GetPathSnapshot("/contract/example")
					require.NoError(t, x)
					baseline, x := target.NetworkSyncBaseline("/contract/example")
					require.NoError(t, x)
					_, e = target.ApplyPathSnapshotFrom(s, baseline)
				case "realtime":
					_, e = target.AcceptCurrentRecord(replacement)
				}
				if mode == "realtime" {
					require.ErrorIs(t, e, ErrPathDiverged)
				} else {
					require.ErrorIs(t, e, ErrWriteConflict)
				}
				got, x := target.Get(old.Key)
				require.NoError(t, x)
				require.Equal(t, RecordHash(old), RecordHash(got))
				_, x = target.Get(tail.Key)
				require.ErrorIs(t, x, ErrRecordNotFound)
			})
		}
	}
}

func TestContractStoreSnapshotCannotOmitPermanentValue(t *testing.T) {
	key := contractTestKey(t)
	cfg := contractTestConfig(key)
	target := testIndexerWithConfig(t, cfg)
	empty := testIndexerWithConfig(t, cfg)
	old := contractTestRecord(t, key, "id", []byte("keep forever"), 100)
	contractTestPut(t, target, old)
	s, e := empty.GetPathSnapshot("/contract/example")
	require.NoError(t, e)
	baseline, e := target.NetworkSyncBaseline("/contract/example")
	require.NoError(t, e)
	_, e = target.ApplyPathSnapshotFrom(s, baseline)
	require.ErrorIs(t, e, ErrWriteConflict)
	root, e := DirectoryRootFromRecords(nil, 100)
	require.NoError(t, e)
	_, e = target.ApplyMirror([]Subscription{{Type: SubscriptionPrefix, Target: "/contract/example"}}, nil, root)
	require.ErrorIs(t, e, ErrWriteConflict)
	got, e := target.Get(old.Key)
	require.NoError(t, e)
	require.Equal(t, old.Value, got.Value)
}
