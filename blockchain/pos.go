package blockchain

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/database"
	scommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

// POSApprovalError is deliberately not a RuleError cached by block hash:
// coinbase approval witness is not part of that hash, and a valid version may
// follow an invalid one. It is still rejected at every formal acceptance path.
type POSApprovalError struct{ Cause error }

func (e POSApprovalError) Error() string { return "POS v2: " + e.Cause.Error() }
func (e POSApprovalError) Unwrap() error { return e.Cause }

func (b *BlockChain) posSequenceLocked(block *btcutil.Block) (*scommon.MiningSequenceMgr, error) {
	height := int(block.Height() - 1)
	parent := block.MsgBlock().Header.PrevBlock
	if b.assetIndexReadiness != nil && b.assetIndexReadiness.InternalTipReady(height, &parent) {
		if provider, ok := b.assetIndexReadiness.(interface {
			GetSeqMgr() *scommon.MiningSequenceMgr
		}); ok {
			return provider.GetSeqMgr(), nil
		}
	}
	view, err := b.contractParentAssetViewLocked(block)
	if err != nil {
		return nil, AssetIndexerNotReadyError{TargetHeight: height, TargetHash: parent, Cause: err}
	}
	provider, ok := view.(interface {
		GetSeqMgr() *scommon.MiningSequenceMgr
	})
	if !ok {
		return nil, fmt.Errorf("parent asset view does not expose mining sequence")
	}
	return provider.GetSeqMgr(), nil
}

func (b *BlockChain) checkPOSBlockLocked(block *btcutil.Block, proposal bool) error {
	if !b.chainParams.POSV2Active(block.Height()) {
		return nil
	}
	if b.posDurabilityErr != nil {
		return b.posDurabilityErr
	}
	seq, err := b.posSequenceLocked(block)
	if err != nil {
		return err
	}
	if seq == nil {
		return fmt.Errorf("parent sorter unavailable")
	}
	msg := block.MsgBlock()
	if len(msg.Transactions) == 0 {
		return POSApprovalError{fmt.Errorf("missing coinbase")}
	}
	_, bootstrap, reward, err := seq.POSMiningInfo(msg.Transactions[0], int(block.Height()))
	if err != nil {
		return POSApprovalError{err}
	}
	if scommon.GetMiningAddress(msg, b.chainParams) != reward {
		return POSApprovalError{fmt.Errorf("incorrect scheduled reward, expected %s", reward)}
	}
	address, err := btcutil.DecodeAddress(reward, b.chainParams)
	if err != nil {
		return POSApprovalError{err}
	}
	rewardScript, err := txscript.PayToAddrScript(address)
	if err != nil {
		return POSApprovalError{err}
	}
	for i, output := range msg.Transactions[0].TxOut {
		// Empty commitments remain allowed; every monetary output must pay
		// the scheduled channel, including BTC and asset fees split across outputs.
		if (output.Value != 0 || len(output.Assets) != 0) && !bytes.Equal(output.PkScript, rewardScript) {
			return POSApprovalError{fmt.Errorf("incorrect reward output %d, expected %s", i, reward)}
		}
	}
	if CalcMerkleRoot(block.Transactions(), false) != msg.Header.MerkleRoot {
		return POSApprovalError{fmt.Errorf("transaction merkle root mismatch")}
	}
	witness := msg.Transactions[0].TxIn[0].Witness
	unsigned := proposal && len(witness) == 1
	if err := ValidatePOSWitnessCommitment(block, unsigned); err != nil {
		return POSApprovalError{err}
	}
	weight := GetBlockWeight(block)
	if unsigned {
		weight += 1 + scommon.MaxPOSApprovalSignatureSize
	}
	if weight > MaxBlockWeight {
		return POSApprovalError{fmt.Errorf("approval exceeds block weight limit")}
	}
	if unsigned {
		return nil
	}
	if len(witness[1]) == 0 || len(witness[1]) > scommon.MaxPOSApprovalSignatureSize {
		return POSApprovalError{fmt.Errorf("invalid approval signature length")}
	}
	sig, err := ecdsa.ParseDERSignature(witness[1])
	if err != nil || !bytes.Equal(sig.Serialize(), witness[1]) {
		return POSApprovalError{fmt.Errorf("approval must be canonical DER")}
	}
	key, err := hex.DecodeString(bootstrap)
	if err != nil {
		return POSApprovalError{err}
	}
	pub, err := secp256k1.ParsePubKey(key)
	if err != nil {
		return POSApprovalError{err}
	}
	if !scommon.VerifyMessage(pub, scommon.POSApprovalMessage(b.chainParams.Net, block.Height(), *block.Hash()), sig) {
		return POSApprovalError{fmt.Errorf("invalid Bootstrap approval")}
	}
	return nil
}

// ApprovePOSBlock is the sole local approval entry. The candidate is copied;
// its approval witness cannot escape until canonical commit and sync succeed.
// Checking the parent, signing and committing share one chain write lock.
func (b *BlockChain) ApprovePOSBlock(candidate *wire.MsgBlock, bootstrap string,
	sign func([]byte) ([]byte, error)) (*btcutil.Block, error) {
	if candidate == nil || sign == nil {
		return nil, fmt.Errorf("missing POS candidate or signer")
	}
	block := btcutil.NewBlock(candidate.Copy())
	if err := b.waitForDirectTipReadiness(block); err != nil {
		return nil, err
	}
	b.chainLock.Lock()
	defer b.chainLock.Unlock()
	if b.posDurabilityErr != nil {
		return nil, b.posDurabilityErr
	}
	if node := b.index.LookupNode(block.Hash()); node != nil && b.bestChain.Contains(node) {
		var stored *btcutil.Block
		err := b.db.View(func(tx database.Tx) error {
			var err error
			stored, err = dbFetchBlockByNode(tx, node)
			return err
		})
		if err != nil {
			return nil, err
		}
		if !b.chainParams.POSV2Active(stored.Height()) {
			return nil, fmt.Errorf("POS v2 is not active")
		}
		return stored, nil
	}
	if !b.directTipReadinessLocked(block) {
		return nil, fmt.Errorf("parent index changed during approval")
	}
	tip := b.bestChain.Tip()
	block.SetHeight(tip.height + 1)
	if !b.chainParams.POSV2Active(block.Height()) {
		return nil, fmt.Errorf("POS v2 is not active")
	}
	if block.MsgBlock().Header.PrevBlock != tip.hash {
		return nil, ruleError(ErrPrevBlockNotBest, "candidate does not extend tip")
	}
	seq, err := b.posSequenceLocked(block)
	if err != nil {
		return nil, err
	}
	if seq == nil {
		return nil, fmt.Errorf("parent sorter unavailable")
	}
	approver, err := seq.POSBootstrap(int(block.Height()))
	if err != nil {
		return nil, err
	}
	if approver != bootstrap {
		return nil, fmt.Errorf("candidate belongs to Bootstrap %s", approver)
	}
	storedNode := b.index.LookupNode(block.Hash())
	if storedNode != nil {
		if !b.recoverablePOSNodeLocked(storedNode) {
			return nil, fmt.Errorf("stored POS candidate is not recoverable at the current tip")
		}
		return b.recoverStoredPOSBlockLocked(storedNode)
	}
	if err := b.checkConnectBlockTemplateLocked(block); err != nil {
		b.releaseContractPostState(block.Hash())
		return nil, err
	}
	sig, err := sign(scommon.POSApprovalMessage(b.chainParams.Net, block.Height(), *block.Hash()))
	if err != nil {
		b.releaseContractPostState(block.Hash())
		return nil, err
	}
	block.MsgBlock().Transactions[0].TxIn[0].Witness = append(block.MsgBlock().Transactions[0].TxIn[0].Witness[:1], sig)
	// Use a fresh wrapper so cached witness bytes/weight from precheck are gone.
	block = btcutil.NewBlock(block.MsgBlock())
	main, orphan, err := b.processBlockLocked(block, BFNone)
	if err != nil {
		return nil, err
	}
	node := b.index.LookupNode(block.Hash())
	if !main || orphan || node == nil || !b.bestChain.Contains(node) {
		return nil, fmt.Errorf("approved candidate did not become canonical")
	}
	return block, nil
}

// recoverablePOSNodeLocked identifies incomplete direct-tip commits. It is
// also used by HaveBlock so normal synchronization can request a retry.
func (b *BlockChain) recoverablePOSNodeLocked(node *blockNode) bool {
	if node == nil || b.posDurabilityErr != nil || !b.chainParams.POSV2Active(node.height) ||
		node.parent != b.bestChain.Tip() || b.bestChain.Contains(node) {
		return false
	}
	status := b.index.NodeStatus(node)
	return status.HaveData() && !status.KnownInvalid()
}

// recoverStoredPOSBlockLocked is shared by Bootstrap approval retries and
// ordinary reception. Preserve original approved bytes and node ancestry,
// fully revalidate even KnownValid nodes, then finish the canonical commit.
// The chain state lock MUST be held for writes.
func (b *BlockChain) recoverStoredPOSBlockLocked(node *blockNode) (*btcutil.Block, error) {
	var block *btcutil.Block
	if err := b.db.View(func(tx database.Tx) error {
		var err error
		block, err = dbFetchBlockByNode(tx, node)
		return err
	}); err != nil {
		return nil, err
	}
	if err := b.checkConnectBlockTemplateLocked(block); err != nil {
		b.releaseContractPostState(block.Hash())
		return nil, err
	}
	// Template validation permits unsigned proposals; recovery does not.
	if err := b.checkPOSBlockLocked(block, false); err != nil {
		b.releaseContractPostState(block.Hash())
		return nil, err
	}
	main, err := b.connectBestChain(node, block, BFNone)
	if err != nil {
		b.releaseContractPostState(block.Hash())
		return nil, err
	}
	if !main || !b.bestChain.Contains(node) {
		return nil, fmt.Errorf("stored candidate did not become canonical")
	}
	if err := b.finishBlockAcceptance(block); err != nil {
		return nil, err
	}
	if err := b.processOrphans(block.Hash(), BFNone); err != nil {
		return nil, err
	}
	if !b.bestChain.Contains(node) {
		return nil, fmt.Errorf("stored candidate is no longer canonical")
	}
	return block, nil
}

// CanServeBlock guards the raw DB-serving paths with the chain lock so they
// cannot expose a v2 block while its commit/sync is still in progress.
func (b *BlockChain) CanServeBlock(hash *chainhash.Hash) bool {
	b.chainLock.RLock()
	defer b.chainLock.RUnlock()
	if b.posDurabilityErr != nil {
		return false
	}
	node := b.index.LookupNode(hash)
	if node == nil {
		return false
	}
	return !b.chainParams.POSV2Active(node.height) || (b.bestChain.Contains(node) && b.index.NodeStatus(node).KnownValid())
}

func (b *BlockChain) POSReady() bool {
	b.chainLock.RLock()
	defer b.chainLock.RUnlock()
	if b.posDurabilityErr != nil {
		return false
	}
	tip := b.bestChain.Tip()
	return b.assetIndexReadiness != nil && b.assetIndexReadiness.InternalTipReady(int(tip.height), &tip.hash)
}

// A failed v2 write may have already changed in-memory UTXO/index caches.
// Stop subsequent approval and serving until restart rather than retry in
// that partially committed state. The existing canonical DB is the record.
func (b *BlockChain) posStorageError(height int32, err error) error {
	if err != nil && b.chainParams.POSV2Active(height) {
		b.posDurabilityErr = fmt.Errorf("POS storage failure; restart after repair: %w", err)
		return b.posDurabilityErr
	}
	return err
}
