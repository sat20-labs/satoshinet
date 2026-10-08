package node

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/database"
	"github.com/stretchr/testify/require"
)

func TestPersistedContractStateChunkBoundary(t *testing.T) {
	for _, size := range []int{MaxPersistedContractStateBytes - 1, MaxPersistedContractStateBytes, MaxPersistedContractStateBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			db := testEVMStateDB(t)
			defer db.Close()
			data := bytes.Repeat([]byte{7}, size)
			key := []byte("snapshot")
			require.NoError(t, db.Update(func(tx database.Tx) error {
				bucket, err := tx.Metadata().CreateBucket([]byte("boundary"))
				if err != nil {
					return err
				}
				return putStateBlob(bucket, key, data)
			}))
			require.NoError(t, db.View(func(tx database.Tx) error {
				bucket := tx.Metadata().Bucket([]byte("boundary"))
				require.LessOrEqual(t, len(bucket.Get(key)), MaxPersistedContractStateBytes)
				chunks := bucket.Bucket(stateChunkBucketKey(key))
				if size > MaxPersistedContractStateBytes {
					require.NotNil(t, chunks)
					require.NoError(t, chunks.ForEach(func(_ []byte, value []byte) error {
						require.LessOrEqual(t, len(value), MaxPersistedContractStateBytes)
						return nil
					}))
				} else {
					require.Nil(t, chunks)
				}
				actual, err := getStateBlob(bucket, key)
				require.NoError(t, err)
				require.Equal(t, data, actual)
				return nil
			}))
			require.NoError(t, db.Update(func(tx database.Tx) error { return deleteStateBlob(tx.Metadata().Bucket([]byte("boundary")), key) }))
			require.NoError(t, db.View(func(tx database.Tx) error {
				bucket := tx.Metadata().Bucket([]byte("boundary"))
				require.Nil(t, bucket.Get(key))
				require.Nil(t, bucket.Bucket(stateChunkBucketKey(key)))
				return nil
			}))
		})
	}
}
