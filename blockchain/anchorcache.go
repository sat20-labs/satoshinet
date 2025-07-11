// Copyright (c) 2023 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (

	"github.com/sat20-labs/satoshinet/database"
)

// FetchAnchorTx fetch anchor tx with given locked txid from the point of view of the end of the main chain.
// This function is safe for concurrent access however the returned view is NOT.
func (b *BlockChain) FetchAnchorTx(lockedUtxo string) (*AnchorTxInfo, error) {
	log.Infof("FetchAnchorTx: %s", lockedUtxo)
	
	var anchorTxInfo *AnchorTxInfo
	err := b.db.View(func(dbTx database.Tx) error {
		var err error
		anchorTxInfo, err = dbFetchAnchorTxInfo(dbTx, lockedUtxo)
		return err
	})
	if err != nil {
		return nil, err
	}
	return anchorTxInfo, nil
}
