package dkvs

import (
	"errors"
	"fmt"

	"github.com/sat20-labs/indexer/common"
)

// Tag errors at the database boundary, before they share a return path
// with record validation. A missing row is a normal CAS precondition.
func storageError(err error) error {
	if err == nil || errors.Is(err, common.ErrKeyNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrStorageUnavailable, err)
}

type storageDB struct{ common.KVDB }

func (db storageDB) Read(key []byte) ([]byte, error) {
	value, err := db.KVDB.Read(key)
	return value, storageError(err)
}

func (db storageDB) Write(key, value []byte) error {
	return storageError(db.KVDB.Write(key, value))
}

func (db storageDB) Delete(key []byte) error {
	return storageError(db.KVDB.Delete(key))
}

func (db storageDB) NewWriteBatch() common.WriteBatch {
	return storageBatch{db.KVDB.NewWriteBatch()}
}

type storageBatch struct{ common.WriteBatch }

func (b storageBatch) Put(key, value []byte) error {
	return storageError(b.WriteBatch.Put(key, value))
}

func (b storageBatch) Delete(key []byte) error {
	return storageError(b.WriteBatch.Delete(key))
}

func (b storageBatch) Flush() error {
	return storageError(b.WriteBatch.Flush())
}

// Scan callbacks can return a business error or stop pagination early.
// Keep those results distinct from errors produced by the iterator itself.
type storageScan struct {
	visit func([]byte, []byte) error
	err   error
}

func (s *storageScan) row(key, value []byte) error {
	s.err = s.visit(key, value)
	return s.err
}

func (s *storageScan) result(err error) error {
	if s.err != nil {
		return err
	}
	return storageError(err)
}

func (db storageDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	s := storageScan{visit: visit}
	return s.result(db.KVDB.BatchRead(prefix, reverse, s.row))
}

func (db storageDB) BatchReadV2(prefix, seek []byte, reverse bool, visit func([]byte, []byte) error) error {
	s := storageScan{visit: visit}
	return s.result(db.KVDB.BatchReadV2(prefix, seek, reverse, s.row))
}
