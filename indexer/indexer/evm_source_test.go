package indexer

import (
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	contract "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestEVMSourceQueryDerivesVerificationFromAdmittedBlob(t *testing.T) {
	database := dbpkg.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	t.Cleanup(func() { _ = database.Close() })
	// Admission is stubbed only to isolate the manager's query contract.
	store := dkvs.New(database, dkvs.Config{EVMSourceVerifier: func(*wire.DKVSRecord) error { return nil }})
	manager := &IndexerMgr{dkvsIndexer: store}
	metadata := contract.EVMSourceMetadata{Version: 1, Source: "admitted", Verified: false, VerifyStatus: "client-claim"}
	value, err := contract.EncodeEVMSourceMetadata(metadata)
	require.NoError(t, err)
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	record, err := dkvs.NewRecord("/contract/evm/source/testcontract", value, priv.PubKey().SerializeCompressed(), dkvs.RecordOptions{Seq: 1})
	require.NoError(t, err)
	hash := dkvs.SigningHash(record)
	record.Signature = ecdsa.Sign(priv, hash[:]).Serialize()
	_, err = manager.PutDKVSRecordCAS(record, dkvs.WritePrecondition{ExpectAbsent: true})
	require.NoError(t, err)
	got, err := manager.GetEVMSourceMetadata(" TESTCONTRACT ")
	require.NoError(t, err)
	require.True(t, got.Verified)
	require.Equal(t, "verified-init-code", got.VerifyStatus)
	require.Empty(t, got.RuntimeCodeHash)
	require.Equal(t, metadata.Source, got.Source)
	_, err = manager.GetEVMSourceMetadata("other")
	require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
}
