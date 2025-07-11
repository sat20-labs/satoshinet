// Copyright (c) 2023 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"fmt"
	"sync"

	"github.com/sat20-labs/satoshinet/database"
)

// utxoCache is a cached utxo view in the chainstate of a BlockChain.
type AnchorTxCache struct {
	db database.DB

	// Anchor tx cache field stores all anchor tx infos.
	anchorTxInfoLock sync.RWMutex
	anchorTxInfoMap  map[string]*AnchorTxInfo
}

// newUtxoCache initiates a new utxo cache instance with its memory usage limited
// to the given maximum.
func newAnchorTxCache(db database.DB) *AnchorTxCache {

	anchorTxInfoMap := make(map[string]*AnchorTxInfo, 0)

	anchorTxCache := &AnchorTxCache{
		db:              db,
		anchorTxInfoMap: anchorTxInfoMap,
	}

	// load anchor tx infos from db
	var anchorTxInfos *[]AnchorTxInfo

	db.View(func(dbTx database.Tx) error {
		anchorTxInfosList, err := dbFetchAnchorTxInfos(dbTx)
		if err != nil {
			anchorTxInfosList := make([]AnchorTxInfo, 0)
			anchorTxInfos = &anchorTxInfosList
			return err
		}
		anchorTxInfos = anchorTxInfosList
		return err
	})

	anchorTxCache.addAnchorTxInfos(anchorTxInfos)
	return anchorTxCache
}

// FetchAnchorTxInfo returns the anchor tx info for the locked txid.
func (s *AnchorTxCache) FetchAnchorTxInfo(lockedUtxo string) (*AnchorTxInfo, error) {
	s.anchorTxInfoLock.RLock()
	defer s.anchorTxInfoLock.RUnlock()
	anchorTxInfo, ok := s.anchorTxInfoMap[lockedUtxo]
	if !ok {
		err := fmt.Errorf("no anchor tx info found")
		return nil, err
	}
	return anchorTxInfo, nil
}

// addAnchorTxInfos adds anchor tx infos to the cache.
func (s *AnchorTxCache) addAnchorTxInfos(anchorTxInfos *[]AnchorTxInfo) error {
	s.anchorTxInfoLock.RLock()
	defer s.anchorTxInfoLock.RUnlock()
	for _, anchorTxInfo := range *anchorTxInfos {
		_, ok := s.anchorTxInfoMap[anchorTxInfo.LockedUtxo]
		if ok {
			err := fmt.Errorf("locked tx has anchor tx info")
			return err
		}
		s.anchorTxInfoMap[anchorTxInfo.LockedUtxo] = &anchorTxInfo
	}

	return nil
}

// AddAnchorTx adds anchor tx infos to the cache.
func (s *AnchorTxCache) AddAnchorTx(anchorTxInfo *AnchorTxInfo) error {
	s.anchorTxInfoLock.RLock()
	defer s.anchorTxInfoLock.RUnlock()
	_, ok := s.anchorTxInfoMap[anchorTxInfo.LockedUtxo]
	if ok {
		err := fmt.Errorf("locked tx has anchor tx info")
		return err
	}
	s.anchorTxInfoMap[anchorTxInfo.LockedUtxo] = anchorTxInfo

	return nil
}

// FetchAnchorTx fetch anchor tx with given locked txid from the point of view of the end of the main chain.
// This function is safe for concurrent access however the returned view is NOT.
func (b *BlockChain) FetchAnchorTx(lockedUtxo string) (*AnchorTxInfo, error) {
	log.Infof("FetchAnchorTx: %s", lockedUtxo)
	return b.anchorTxCache.FetchAnchorTxInfo(lockedUtxo)
}

// AddAnchorTx add anchor to anchor tx cache and saved to db from the point of view of the end of the main chain.
// This function is safe for concurrent access however the returned view is NOT.
func (b *BlockChain) AddAnchorTx(anchorTxInfo *AnchorTxInfo) error {
	log.Infof("AddAnchorTx: %v", anchorTxInfo)
	b.anchorTxCache.AddAnchorTx(anchorTxInfo)
	return nil
}
