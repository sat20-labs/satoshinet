package ffldb

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/syndtr/goleveldb/leveldb"
)

func TestPOSSyncPersistsBlockAndMetadata(t *testing.T) {
	idb, err := openDB(filepath.Join(t.TempDir(), "chain"), chaincfg.TestNetParams.Net, true)
	if err != nil {
		t.Fatal(err)
	}
	d := idb.(*db)
	defer d.Close()
	block := btcutil.NewBlock(chaincfg.TestNetParams.GenesisBlock.Copy())
	key, value := []byte("pos-canonical-tip"), block.Hash()[:]
	if err := d.Update(func(tx database.Tx) error {
		if err := tx.StoreBlock(block); err != nil {
			return err
		}
		return tx.Metadata().Put(key, value)
	}); err != nil {
		t.Fatal(err)
	}
	physicalKey := bucketizedKey(metadataBucketID, key)
	if _, err := d.cache.ldb.Get(physicalKey, nil); err != leveldb.ErrNotFound {
		t.Fatalf("fixture must still be cached: %v", err)
	}
	if err := database.Sync(d); err != nil {
		t.Fatal(err)
	}
	persisted, err := d.cache.ldb.Get(physicalKey, nil)
	if err != nil || !bytes.Equal(persisted, value) {
		t.Fatalf("metadata not durable: %x %v", persisted, err)
	}
	if d.cache.cachedKeys.Len() != 0 {
		t.Fatal("metadata remains only in cache")
	}
}

func TestPOSSyncBlockFileFailureDoesNotFlushMetadata(t *testing.T) {
	idb, err := openDB(filepath.Join(t.TempDir(), "chain"), chaincfg.TestNetParams.Net, true)
	if err != nil {
		t.Fatal(err)
	}
	d := idb.(*db)
	defer d.Close()
	key := []byte("pos-pending-tip")
	if err := d.Update(func(tx database.Tx) error { return tx.Metadata().Put(key, []byte("pending")) }); err != nil {
		t.Fatal(err)
	}
	original := d.store.writeCursor.curFile
	d.store.writeCursor.curFile = &lockableFile{file: &mockFile{forceSyncErr: true, maxSize: -1}}
	err = database.Sync(d)
	d.store.writeCursor.curFile = original
	if err == nil {
		t.Fatal("file sync error ignored")
	}
	if _, err := d.cache.ldb.Get(bucketizedKey(metadataBucketID, key), nil); err != leveldb.ErrNotFound {
		t.Fatalf("metadata committed before block-file sync: %v", err)
	}
	if !d.cache.cachedKeys.Has(bucketizedKey(metadataBucketID, key)) {
		t.Fatal("failed sync discarded pending metadata")
	}
	if err := database.Sync(d); err != nil {
		t.Fatal(err)
	}
}
