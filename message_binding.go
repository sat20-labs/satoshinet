package main

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

var messageBindingAcceptBucketKey = []byte("account-bindings")

type databaseCoreBindingAcceptanceStore struct {
	db database.DB
	mu sync.Mutex
}

func bindingAcceptanceBucket(tx database.Tx, create bool) (database.Bucket, error) {
	root := tx.Metadata().Bucket(messageManagerBucketKey)
	if root == nil && create {
		var err error
		root, err = tx.Metadata().CreateBucketIfNotExists(messageManagerBucketKey)
		if err != nil {
			return nil, err
		}
	}
	if root == nil {
		return nil, nil
	}
	bucket := root.Bucket(messageBindingAcceptBucketKey)
	if bucket == nil && create {
		var err error
		bucket, err = root.CreateBucketIfNotExists(messageBindingAcceptBucketKey)
		if err != nil {
			return nil, err
		}
	}
	return bucket, nil
}

func (s *databaseCoreBindingAcceptanceStore) Accept(accountID string, record *wire.DKVSRecord) error {
	if s == nil || s.db == nil || !validateAccountID(accountID) || record == nil {
		return ErrMessageInvalidEnvelope
	}
	hash := dkvs.RecordHash(record)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx database.Tx) error {
		bucket, err := bindingAcceptanceBucket(tx, true)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(accountID), hash[:])
	})
}

func (s *databaseCoreBindingAcceptanceStore) Matches(accountID string, record *wire.DKVSRecord) bool {
	if s == nil || s.db == nil || !validateAccountID(accountID) || record == nil {
		return false
	}
	hash := dkvs.RecordHash(record)
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := false
	_ = s.db.View(func(tx database.Tx) error {
		bucket, err := bindingAcceptanceBucket(tx, false)
		if err != nil || bucket == nil {
			return err
		}
		matched = bytes.Equal(bucket.Get([]byte(accountID)), hash[:])
		return nil
	})
	return matched
}

type serverMessageBindingResolver struct {
	server *server
	accept *databaseCoreBindingAcceptanceStore
}

func newServerMessageBindingResolver(s *server) *serverMessageBindingResolver {
	return &serverMessageBindingResolver{server: s, accept: &databaseCoreBindingAcceptanceStore{db: s.db}}
}

func (r *serverMessageBindingResolver) current(accountID string) (*wire.DKVSRecord, string, error) {
	if r == nil || r.server == nil || r.server.assetIndexer == nil || !validateAccountID(accountID) {
		return nil, "", ErrMessageBindingNotFound
	}
	pubKey, err := dkvs.AccountPubKey(accountID)
	if err != nil {
		return nil, "", ErrMessageBindingNotFound
	}
	params := r.server.assetIndexer.GetChainParam()
	address, err := dkvs.P2TRAddressFromPubKeyBytes(pubKey, params)
	if err != nil || params == nil {
		return nil, "", ErrMessageBindingNotFound
	}
	key, err := dkvs.AccountMappingKey(params.Name, address)
	if err != nil {
		return nil, "", ErrMessageBindingNotFound
	}
	record, err := r.server.assetIndexer.GetDKVSRecord(key)
	if err != nil || record == nil {
		return nil, "", ErrMessageBindingNotFound
	}
	_, _, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(record)
	if err != nil || descriptor.AccountID != strings.ToLower(accountID) {
		return nil, "", ErrMessageBindingNotFound
	}
	return record, descriptor.CoreNodeID, nil
}

func (r *serverMessageBindingResolver) CoreNodeForAccount(accountID string) (string, error) {
	_, coreID, err := r.current(accountID)
	return coreID, err
}

func (r *serverMessageBindingResolver) AccountBoundToCore(accountID, coreNodeID string) bool {
	record, currentCore, err := r.current(accountID)
	if err != nil || currentCore != coreNodeID {
		return false
	}
	if r.server == nil || coreNodeID != r.server.miningPubKey {
		return true
	}
	return r.accept.Matches(accountID, record)
}

func (r *serverMessageBindingResolver) AcceptLocalBinding(record *wire.DKVSRecord) error {
	if r == nil || r.server == nil || r.server.assetIndexer == nil || record == nil {
		return ErrMessageInvalidEnvelope
	}
	_, _, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(record)
	if err != nil {
		return err
	}
	local := strings.ToLower(strings.TrimSpace(r.server.miningPubKey))
	if local == "" || descriptor.CoreNodeID != local || local == strings.ToLower(indexercommon.GetBootstrapPubKey()) {
		return fmt.Errorf("%w: binding target is not this CoreNode", ErrMessageNotBoundHere)
	}
	_, err = r.server.assetIndexer.PutDKVSRecord(record)
	if err != nil {
		return err
	}
	key := record.Key
	current, err := r.server.assetIndexer.GetDKVSRecord(key)
	if err != nil || current == nil || dkvs.RecordHash(current) != dkvs.RecordHash(record) {
		return ErrMessageInvalidSequence
	}
	return r.accept.Accept(descriptor.AccountID, current)
}
