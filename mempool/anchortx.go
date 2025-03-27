// Copyright (c) 2024 The sats20 developers

package mempool

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/blockchain"
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
	fmt.Printf("CheckAnchorTxValid ...\n")

	// Check the locked tx out is valid
	txInfo, err := anchortx.CheckAnchorTxValid(tx, isNew)
	if err != nil {
		log.Errorf("invalid Anchor tx: %s", tx.TxHash().String())
		return err
	}
	fmt.Printf("The locked txInfo: %v\n", txInfo)

	// Check the locked tx is is not anchor in sats net
	anchorTxInfo, err := mp.cfg.FetchAnchorTx(txInfo.Utxo)
	if err == nil && anchorTxInfo != nil {
		fmt.Printf("The anchor is exist, anchor txInfo: %v\n", txInfo)
		// The anchor tx is found in sats net
		err = fmt.Errorf("the locked tx is anchored already in sats net")
		return err
	}

	// for _, out := range tx.TxOut {
	// 	// Get the range size of the out
	// 	rangeSize := out.SatsRanges.GetSize()
	// 	if rangeSize != out.Value {
	// 		err = fmt.Errorf("the txOut TxRanges of anchor tx  is invalid")
	// 		return err
	// 	}

	// }

	// Check the locked tx has completed, all the assets is locked in lnd will be mapped to sats net only one times

	fmt.Printf("The anchor tx is valid\n")
	return nil
}

func (mp *TxPool) AddAnchorTx(tx *wire.MsgTx) error {
	fmt.Printf("CheckAnchorTxValid ...\n")

	txInfo, err := anchortx.GetLockedTxInfo(tx, false)
	if err != nil {
		return err
	}
	fmt.Printf("The locked txInfo: %v\n", txInfo)

	// Check the locked tx is is not anchor in sats net
	anchorTxInfo, err := mp.cfg.FetchAnchorTx(txInfo.Utxo)
	if err == nil && anchorTxInfo != nil {
		fmt.Printf("The anchor is exist, anchor txInfo: %v\n", txInfo)
		// The anchor tx is found in sats net
		err = fmt.Errorf("the locked tx is anchored already in sats net")
		return err
	}

	// Check the locked tx has completed, all the assets is locked in lnd will be mapped to sats net only one times

	//lockedUtxo := fmt.Sprintf("%s:%d", txInfo.TxId, txInfo.Index)
	// Add the anchor tx to db
	anchorTxInfo = &blockchain.AnchorTxInfo{
		LockedUtxo:    txInfo.Utxo,
		WitnessScript: txInfo.WitnessScript,
		Value:         txInfo.Value,
		AnchorTxid:    tx.TxHash().String(),
	}
	err = mp.cfg.AddAnchorTx(anchorTxInfo)
	if err != nil {
		return err
	}

	fmt.Printf("The anchor tx is valid\n")
	return nil
}
