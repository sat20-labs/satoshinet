// Copyright (c) 2024 The sats20 developers

package mempool

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	// 65 txid,
	// 35 output script, it should be p2tr
	// 8 amount for Anchor
	// 8 extra nonce
	MIN_LEN_ANCHORTX_SCRIPT = 102
	MAX_LEN_ANCHORTX_SCRIPT = 116
)

// txscript.NewScriptBuilder().AddData(txid).AddData(pkScript).
// AddInt64(int64(amount)).AddInt64(int64(extraNonce)).Script()
// type LockedTxInfo struct {
// 	TxId     string // the txid with locked in lnd
// 	Index    int32  // the index with locked in lnd
// 	PkScript []byte // pkScript for locked in lnd
// 	Amount   int64  // the amount with locked in lnd
// }

func (mp *TxPool) CheckAnchorTxValid(tx *wire.MsgTx, isNew bool, txHeight int32) error {
	log.Debug("CheckAnchorTxValid ...\n")

	// Check the locked tx out is valid
	txInfo, err := anchortx.CheckAnchorTxValid(tx, isNew)
	if err != nil {
		log.Errorf("invalid Anchor tx: %s, %v", tx.TxHash().String(), err)
		return err
	}
	log.Debugf("The locked txInfo: %v", txInfo)

	// Check the locked tx is is not anchor in sats net
	if info, _ := mp.cfg.FetchAnchorTx(txInfo.Utxo); info != nil {
		log.Errorf("The anchor is exist, anchorTx %s, utxo %s", info.AnchorTxid, txInfo.Utxo)
		if info.AnchorTxid == tx.TxID() {
			log.Infof("The anchor tx %s is valid and accepted before", tx.TxID())
			return nil
		}
		// The anchor tx is found in sats net
		err = fmt.Errorf("the locked tx is anchored already in sats net, anchorTx %s, utxo %s", info.AnchorTxid, txInfo.Utxo)
		return err
	}

	// Check the locked tx has completed, all the assets is locked in lnd will be mapped to sats net only one times

	log.Infof("The anchor tx %s is valid", tx.TxID())
	return nil
}
