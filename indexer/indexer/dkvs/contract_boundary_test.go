package dkvs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContractStoreTreatsValueAsOpaque(t *testing.T) {
	key := contractTestKey(t)
	idx := testIndexerWithConfig(t, contractTestConfig(key))
	record, err := NewSignedRecord(key, "/contract/example/"+"1111111111111111111111111111111111111111111111111111111111111111", []byte("opaque application data"), RecordOptions{Seq: 1, IssueHeight: 100})
	require.NoError(t, err)
	changed, err := idx.PutInternalContract(record)
	require.NoError(t, err)
	require.True(t, changed)
	got, err := idx.Get(record.Key)
	require.NoError(t, err)
	require.Equal(t, record.Value, got.Value)
}
