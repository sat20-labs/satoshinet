package validatechaindb

import (
	"bytes"
	"fmt"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/mining/posminer_deprecated/utils"
	bolt "go.etcd.io/bbolt"
)

func (s *ValidateChainStore) GetVCBlockHash(height int64) (*chainhash.Hash, error) {
	var hashBytes []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(vcBlockHashBucket)
		if bkt == nil {
			return fmt.Errorf("no block hash bucket")
		}
		key := getBlockHashKey(height)
		hashBytes = bkt.Get(key)
		if hashBytes == nil {
			return fmt.Errorf("no block hash with height [%d]", height)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	hash, err := chainhash.NewHash(hashBytes)
	if err != nil {
		utils.Log.Errorf("Error creating hash with bytes[%x]: %v", hashBytes, err)
		return nil, err
	}

	return hash, err
}

func (s *ValidateChainStore) SaveVCBlockHash(height int64, hash *chainhash.Hash) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(vcBlockHashBucket)
		key := getBlockHashKey(height)
		if err := bkt.Put(key, hash[:]); err != nil {
			return fmt.Errorf("could write state to db with key %#x:%v", stateKey, err)
		}
		return nil
	})
	return err
}

func getBlockHashKey(height int64) []byte {
	var bw bytes.Buffer

	utils.WriteElements(&bw, uint64(height))

	return bw.Bytes()
}
