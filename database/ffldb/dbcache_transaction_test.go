package ffldb

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"
)

// Fail SST creation inside Commit, before LevelDB's manifest commit path.
type failingTableStorage struct {
	storage.Storage
	fail atomic.Bool
	err  error
}

func (s *failingTableStorage) Create(fd storage.FileDesc) (storage.Writer, error) {
	if fd.Type == storage.TypeTable && s.fail.Load() {
		return nil, s.err
	}
	return s.Storage.Create(fd)
}

func TestCacheUpdateDBReleasesFailedTransaction(t *testing.T) {
	for _, failure := range []string{"callback", "commit", "panic"} {
		t.Run(failure, func(t *testing.T) {
			path := t.TempDir()
			files, err := storage.OpenFile(path, false)
			require.NoError(t, err)
			defer files.Close()
			injected := errors.New("injected transaction failure")
			faults := &failingTableStorage{Storage: files, err: injected}
			ldb, err := leveldb.Open(faults, nil)
			require.NoError(t, err)
			defer ldb.Close()
			cache := &dbCache{ldb: ldb}
			key := []byte("existing")
			require.NoError(t, cache.updateDB(func(tx *leveldb.Transaction) error {
				return tx.Put(key, []byte("original"), nil)
			}))

			var failedTx *leveldb.Transaction
			// Also release the transaction when testing the unfixed implementation,
			// so a regression reports a failure instead of hanging the test process.
			defer func() {
				if failedTx != nil {
					failedTx.Discard()
				}
			}()
			update := func() error {
				return cache.updateDB(func(tx *leveldb.Transaction) error {
					failedTx = tx
					if err := tx.Put(key, []byte("uncommitted"), nil); err != nil {
						return err
					}
					switch failure {
					case "callback":
						return injected
					case "commit":
						faults.fail.Store(true)
					case "panic":
						panic(injected)
					}
					return nil
				})
			}
			if failure == "panic" {
				require.PanicsWithValue(t, injected, func() { _ = update() })
			} else {
				require.ErrorContains(t, update(), injected.Error())
			}
			faults.fail.Store(false)
			value, err := ldb.Get(key, nil)
			require.NoError(t, err)
			require.Equal(t, "original", string(value), "failed transaction became visible")

			done := make(chan error, 1)
			go func() {
				done <- cache.updateDB(func(tx *leveldb.Transaction) error {
					return tx.Put([]byte("recovered"), []byte("durable"), nil)
				})
			}()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				failedTx.Discard()
				<-done
				t.Fatal("failed transaction retained the LevelDB write lock")
			}
			require.NoError(t, ldb.Close())
			require.NoError(t, files.Close())
			reopened, err := leveldb.OpenFile(path, nil)
			require.NoError(t, err)
			defer reopened.Close()
			value, err = reopened.Get(key, nil)
			require.NoError(t, err)
			require.Equal(t, "original", string(value))
			value, err = reopened.Get([]byte("recovered"), nil)
			require.NoError(t, err)
			require.Equal(t, "durable", string(value))
		})
	}
}
