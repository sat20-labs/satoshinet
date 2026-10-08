// Copyright (c) 2013-2017 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/database"
)

// maybeAcceptBlock potentially accepts a block into the block chain and, if
// accepted, returns whether or not it is on the main chain.  It performs
// several validation checks which depend on its position within the block chain
// before adding it.  The block is expected to have already gone through
// ProcessBlock before calling this function with it.
//
// The flags are also passed to checkBlockContext and connectBestChain.  See
// their documentation for how the flags modify their behavior.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) maybeAcceptBlock(block *btcutil.Block, flags BehaviorFlags) (bool, error) {
	// The height of this block is one more than the referenced previous
	// block.
	prevHash := &block.MsgBlock().Header.PrevBlock
	prevNode := b.index.LookupNode(prevHash)
	if prevNode == nil {
		str := fmt.Sprintf("previous block %s is unknown", prevHash)
		return false, ruleError(ErrPreviousBlockUnknown, str)
	} else if b.index.NodeStatus(prevNode).KnownInvalid() {
		str := fmt.Sprintf("previous block %s is known to be invalid", prevHash)
		return false, ruleError(ErrInvalidAncestorBlock, str)
	}

	blockHeight := prevNode.height + 1
	block.SetHeight(blockHeight)

	// Ordinary peers cannot replace an approved POS suffix. Reject competing
	// branches before storing bytes or an index node: legacy headers have no
	// expensive PoW that would otherwise bound nonce-variant disk growth.
	// Explicit administrator rollback still uses InvalidateBlock/ReconsiderBlock.
	if b.chainParams.POSV2Active(b.bestChain.Tip().height) && prevNode != b.bestChain.Tip() {
		return false, ruleError(ErrPrevBlockNotBest, "competing branch cannot replace an activated POS tip")
	}

	// The block must pass all of the validation rules which depend on the
	// position of the block within the block chain.
	err := b.checkBlockContext(block, prevNode, flags)
	if err != nil {
		return false, err
	}

	// Direct best-tip blocks on contract-enabled nodes are fully prevalidated
	// before any raw bytes or block-index status are persisted. This makes local
	// readiness failures retryable and keeps permanently invalid blocks out of
	// ffldb. Reexecute contracts even after a successful proposal: a hash does
	// not bind the witness, and retained execution state may have been released.
	blockHeader := &block.MsgBlock().Header
	newNode := newBlockNode(blockHeader, prevNode)
	prevalidated := false
	if b.contractBlockValidator != nil && prevNode == b.bestChain.Tip() {
		if err := b.requireContractParentReadyLocked(block); err != nil {
			return false, err
		}
		b.takePreparedBlock(*block.Hash(), *prevHash)
		view := NewUtxoViewpoint()
		view.SetBestHash(prevHash)
		if err := b.checkConnectBlock(newNode, block, view, nil); err != nil {
			b.releaseContractPostState(block.Hash())
			b.cacheRejectedBlock(*block.Hash(), err)
			return false, err
		}
		prevalidated = true
		newNode.status |= statusValid
	}
	releasePrepared := prevalidated
	defer func() {
		if releasePrepared {
			b.releaseContractPostState(block.Hash())
		}
	}()

	// Insert the block into the database if it's not already there.  Even
	// though it is possible the block will ultimately fail to connect, it
	// has already passed all proof-of-work and validity tests which means
	// it would be prohibitively expensive for an attacker to fill up the
	// disk with a bunch of blocks that fail to connect.  This is necessary
	// since it allows block download to be decoupled from the much more
	// expensive connection logic.  It also has some other nice properties
	// such as making blocks that never become part of the main chain or
	// blocks that fail to connect available for further analysis.
	err = b.db.Update(func(dbTx database.Tx) error {
		return dbStoreBlock(dbTx, block)
	})
	if err != nil {
		return false, b.posStorageError(block.Height(), err)
	}

	// Create a new block node for the block and add it to the node index. Even
	// if the block ultimately gets connected to the main chain, it starts out
	// on a side chain.
	newNode.status |= statusDataStored

	b.index.AddNode(newNode)
	err = b.index.flushToDB()
	if err != nil {
		return false, b.posStorageError(block.Height(), err)
	}

	// Connect the passed block to the chain while respecting proper chain
	// selection according to the chain with the most proof of work.  This
	// also handles validation of the transaction scripts.
	isMainChain, err := b.connectBestChain(newNode, block, flags)
	if err != nil {
		return false, err
	}
	releasePrepared = false
	if err := b.finishBlockAcceptance(block); err != nil {
		return false, err
	}
	return isMainChain, nil
}

// finishBlockAcceptance completes durability and relay notification for both
// newly stored blocks and stored POS candidates recovered after a restart.
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) finishBlockAcceptance(block *btcutil.Block) error {
	if b.chainParams.POSV2Active(block.Height()) {
		if err := database.Sync(b.db); err != nil {
			b.posDurabilityErr = fmt.Errorf("POS accepted-block sync failed: %w", err)
			return b.posDurabilityErr
		}
	}

	// Notify the caller that the new block was accepted into the block
	// chain.  The caller would typically want to react by relaying the
	// inventory to other peers.
	func() {
		b.chainLock.Unlock()
		defer b.chainLock.Lock()
		b.sendNotification(NTBlockAccepted, block)
	}()

	return nil
}
