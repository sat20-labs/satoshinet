package common

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

const MaxPOSApprovalSignatureSize = 72

// POSApprovalMessage is passed through the existing SignMsg/VerifyMessage
// APIs, which SHA256 the message themselves. Integers use little endian.
func POSApprovalMessage(net wire.BitcoinNet, height int32, hash chainhash.Hash) []byte {
	tag := chainhash.HashB([]byte("SatoshiNet/BlockApproval"))
	preimage := make([]byte, 0, 104)
	preimage = append(preimage, tag...)
	preimage = append(preimage, tag...)
	var context [8]byte
	binary.LittleEndian.PutUint32(context[:4], uint32(net))
	binary.LittleEndian.PutUint32(context[4:], uint32(height))
	preimage = append(preimage, context[:]...)
	preimage = append(preimage, hash[:]...)
	return chainhash.HashB(preimage)
}

// posSlotLocked is used only against an exact parent-state sequence. Historical
// forks must supply an isolated sequence, never rewind the live one by height.
func (b *MiningSequenceMgr) posSlotLocked(height int) (*MiningInfo, error) {
	if b.currMiningNode == nil || b.currHeight != height {
		return nil, fmt.Errorf("sorter height %d is not candidate height %d", b.currHeight, height)
	}
	return b.currMiningNode, nil
}

func posBootstrap(node *MiningInfo) (*MiningInfo, error) {
	for i := 0; node != nil && i < 3; i++ {
		if node.Father == nil {
			return node, nil
		}
		node = node.Father
	}
	return nil, fmt.Errorf("invalid mining hierarchy")
}

func posReward(slot, producer *MiningInfo) string {
	if slot.Father != nil && slot.Father.Father != nil && producer == slot.Father.Father {
		return slot.Father.MiningAddress // Bootstrap substitutes for Miner.
	}
	return slot.MiningAddress // Miner/Core share Miner–Core; Core uses Core–Bootstrap.
}

func (b *MiningSequenceMgr) POSBootstrap(height int) (string, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	slot, err := b.posSlotLocked(height)
	if err != nil {
		return "", err
	}
	bootstrap, err := posBootstrap(slot)
	if err != nil {
		return "", err
	}
	return bootstrap.PubKey, nil
}

func (b *MiningSequenceMgr) POSReward(height int, producer string) (string, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	slot, err := b.posSlotLocked(height)
	if err != nil {
		return "", err
	}
	for node, i := slot, 0; node != nil && i < 3; node, i = node.Father, i+1 {
		if node.PubKey == producer {
			return posReward(slot, node), nil
		}
	}
	return "", fmt.Errorf("producer %s is outside scheduled hierarchy", producer)
}

// POSMiningInfo identifies the producer by its coinbase signature, independently
// of the reward address. It returns producer, approving Bootstrap, reward.
func (b *MiningSequenceMgr) POSMiningInfo(tx *wire.MsgTx, height int) (string, string, string, error) {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	slot, err := b.posSlotLocked(height)
	if err != nil {
		return "", "", "", err
	}
	if tx == nil || len(tx.TxIn) != 1 || len(tx.TxOut) == 0 {
		return "", "", "", fmt.Errorf("invalid coinbase")
	}
	tokenizer := txscript.MakeScriptTokenizer(0, tx.TxIn[0].SignatureScript)
	// ExtractInt64 assumes at most eight bytes; reject oversized numbers before
	// decoding so an untrusted coinbase cannot panic or silently truncate.
	if !tokenizer.Next() || tokenizer.Err() != nil || len(tokenizer.Data()) > 8 || tokenizer.ExtractInt64() != int64(height) {
		return "", "", "", fmt.Errorf("incorrect coinbase height")
	}
	bootstrap, err := posBootstrap(slot)
	if err != nil {
		return "", "", "", err
	}
	for node, i := slot, 0; node != nil && i < 3; node, i = node.Father, i+1 {
		key, err := hex.DecodeString(node.PubKey)
		if err == nil && VerifyStandardCoinbaseScript(tx.TxIn[0].SignatureScript, key) == nil {
			return node.PubKey, bootstrap.PubKey, posReward(slot, node), nil
		}
	}
	return "", "", "", fmt.Errorf("coinbase is not signed by a scheduled producer")
}
