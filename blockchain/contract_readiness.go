package blockchain

import (
	"fmt"

	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/database"
)

// AssetIndexerNotReadyError is transient local dependency state. It must never
// be converted to RuleError or persisted as block invalidity.
type AssetIndexerNotReadyError struct {
	TargetHeight int
	TargetHash   chainhash.Hash
	Cause        error
}

func (e AssetIndexerNotReadyError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("asset indexer is not ready at %d/%s: %v",
			e.TargetHeight, e.TargetHash, e.Cause)
	}
	return fmt.Sprintf("asset indexer is not ready at %d/%s",
		e.TargetHeight, e.TargetHash)
}

func (e AssetIndexerNotReadyError) Unwrap() error { return e.Cause }

func (b *BlockChain) hasAssetReadinessDependency() bool {
	return b.contractBlockValidator != nil && b.assetIndexReadiness != nil
}

// directTipReadinessTargetLocked returns the AIDX state required before this
// block can be validated as a direct extension of the current best chain.
func (b *BlockChain) directTipReadinessTargetLocked(block *btcutil.Block) (int, chainhash.Hash, bool) {
	if !b.hasAssetReadinessDependency() || block == nil {
		return 0, chainhash.Hash{}, false
	}
	prevHash := block.MsgBlock().Header.PrevBlock
	tip := b.bestChain.Tip()
	if tip == nil || tip.hash != prevHash {
		return 0, chainhash.Hash{}, false
	}
	return int(tip.height), prevHash, true
}

// waitForDirectTipReadiness waits without holding chainLock. The caller must
// still perform a locked final check because the best tip can change meanwhile.
func (b *BlockChain) waitForDirectTipReadiness(block *btcutil.Block) error {
	for {
		b.chainLock.RLock()
		height, hash, required := b.directTipReadinessTargetLocked(block)
		b.chainLock.RUnlock()
		if !required || b.assetIndexReadiness.InternalTipReady(height, &hash) {
			return nil
		}
		// The candidate block does not have a canonical height yet. Repair AIDX
		// against the active-chain parent height instead of block.Height(), which
		// is commonly -1 before acceptance. Genesis is always present, so the
		// canonical tip passed into BaseIndexer is never negative here.
		if err := b.assetIndexReadiness.EnsureInternalTip(height, &hash, height); err != nil {
			return AssetIndexerNotReadyError{TargetHeight: height, TargetHash: hash, Cause: err}
		}
	}
}

func (b *BlockChain) directTipReadinessLocked(block *btcutil.Block) bool {
	height, hash, required := b.directTipReadinessTargetLocked(block)
	return !required || b.assetIndexReadiness.InternalTipReady(height, &hash)
}

type contractAssetIndexViewFactory interface {
	NewValidationAssetView() (ContractAssetIndexView, error)
}

func (b *BlockChain) newContractValidationAssetViewLocked() (ContractAssetIndexView, error) {
	if b.assetIndexerMgr != nil {
		return b.assetIndexerMgr.NewValidationAssetView()
	}
	// Tests may supply an isolated view factory independently from the real
	// IndexerMgr. Production always takes the concrete path above.
	if factory, ok := b.assetIndexReadiness.(contractAssetIndexViewFactory); ok {
		return factory.NewValidationAssetView()
	}
	return nil, fmt.Errorf("asset validation view is unavailable")
}

// contractParentAssetViewLocked reconstructs the exact AIDX state at the
// candidate block's parent without changing the live compiling index. The
// durable AIDX snapshot is expected to be an ancestor of the candidate parent;
// candidate-branch blocks are loaded directly from the blockchain DB, avoiding
// RPC/chain-lock recursion during reorg validation.
func (b *BlockChain) contractParentAssetViewLocked(block *btcutil.Block) (ContractAssetIndexView, error) {
	if block == nil || block.Height() <= 0 {
		return nil, fmt.Errorf("invalid contract validation block")
	}
	targetHeight := int(block.Height() - 1)
	targetHash := block.MsgBlock().Header.PrevBlock
	targetNode := b.index.LookupNode(&targetHash)
	if targetNode == nil || int(targetNode.height) != targetHeight {
		return nil, fmt.Errorf("candidate parent %d/%s is not in block index", targetHeight, targetHash)
	}

	assetView, err := b.newContractValidationAssetViewLocked()
	if err != nil {
		return nil, err
	}
	currentHeight, currentHash, hashKnown := assetView.GetInternalTip()
	if currentHeight > targetHeight {
		return nil, fmt.Errorf("durable AIDX tip %d is ahead of candidate parent %d", currentHeight, targetHeight)
	}
	if currentHeight >= 0 {
		if !hashKnown {
			return nil, fmt.Errorf("durable AIDX hash is unavailable at height %d", currentHeight)
		}
		ancestor := targetNode.Ancestor(int32(currentHeight))
		if ancestor == nil || ancestor.hash != currentHash {
			return nil, fmt.Errorf("durable AIDX tip %d/%s is not an ancestor of candidate parent %d/%s",
				currentHeight, currentHash, targetHeight, targetHash)
		}
	}

	tipHeight := b.tipHeight
	if targetHeight > tipHeight {
		tipHeight = targetHeight
	}
	startHeight := currentHeight + 1
	if startHeight < 0 {
		startHeight = 0
	}
	for height := startHeight; height <= targetHeight; height++ {
		node := targetNode.Ancestor(int32(height))
		if node == nil {
			return nil, fmt.Errorf("candidate branch is missing ancestor at height %d", height)
		}
		var candidate *btcutil.Block
		err := b.db.View(func(dbTx database.Tx) error {
			var fetchErr error
			candidate, fetchErr = dbFetchBlockByNode(dbTx, node)
			return fetchErr
		})
		if err != nil {
			return nil, fmt.Errorf("load candidate AIDX ancestor %d/%s: %w", height, node.hash, err)
		}
		if err := assetView.ConnectBlock(candidate.MsgBlock(), height, tipHeight); err != nil {
			return nil, fmt.Errorf("replay candidate AIDX ancestor %d/%s: %w", height, node.hash, err)
		}
	}

	finalHeight, finalHash, finalKnown := assetView.GetInternalTip()
	if !finalKnown || finalHeight != targetHeight || finalHash != targetHash {
		return nil, fmt.Errorf("candidate AIDX parent mismatch: got %d/%s want %d/%s",
			finalHeight, finalHash, targetHeight, targetHash)
	}
	return assetView, nil
}

func (b *BlockChain) requireContractParentReadyLocked(block *btcutil.Block) error {
	if !b.hasAssetReadinessDependency() || block == nil || block.Height() <= 0 {
		return nil
	}
	height := int(block.Height() - 1)
	hash := block.MsgBlock().Header.PrevBlock
	if b.assetIndexReadiness.InternalTipReady(height, &hash) {
		return nil
	}

	// A direct extension must wait for/repair the live AIDX. Only a block
	// whose parent is off the current best tip is allowed to use an isolated
	// branch view; otherwise a lagging live index could be silently bypassed.
	tip := b.bestChain.Tip()
	if tip != nil && int(tip.height) == height && tip.hash == hash {
		return AssetIndexerNotReadyError{TargetHeight: height, TargetHash: hash}
	}

	assetView, err := b.contractParentAssetViewLocked(block)
	if err != nil {
		return AssetIndexerNotReadyError{TargetHeight: height, TargetHash: hash, Cause: err}
	}
	preparer, ok := b.contractBlockValidator.(ContractAssetIndexViewPreparer)
	if !ok {
		return AssetIndexerNotReadyError{
			TargetHeight: height, TargetHash: hash,
			Cause: fmt.Errorf("contract validator does not support branch-scoped AIDX validation"),
		}
	}
	if err := preparer.PrepareContractAssetIndexView(block.Hash(), assetView); err != nil {
		return AssetIndexerNotReadyError{TargetHeight: height, TargetHash: hash, Cause: err}
	}
	return nil
}

func (b *BlockChain) releaseContractPostState(hash *chainhash.Hash) {
	releaser, ok := b.contractBlockValidator.(ContractBlockStateReleaser)
	if !ok || hash == nil {
		return
	}
	for _, module := range []contractframework.ModuleType{
		contractframework.ModuleTemplate,
		contractframework.ModuleEVM,
		contractframework.ModuleAgent,
	} {
		releaser.ReleaseContractBlockPostState(module, hash)
	}
}

func (b *BlockChain) cacheRejectedBlock(hash chainhash.Hash, err error) {
	ruleErr, ok := err.(RuleError)
	if !ok || ruleErr.ErrorCode == ErrAnchorTXVerifyFailed {
		return
	}
	if b.validationCache == nil {
		b.validationCache = newBlockValidationCache()
	}
	b.validationCache.addRejected(hash, ruleErr)
}

func (b *BlockChain) rejectedBlockError(hash chainhash.Hash) (RuleError, bool) {
	if b.validationCache == nil {
		return RuleError{}, false
	}
	return b.validationCache.rejectedError(hash)
}

func (b *BlockChain) rememberPreparedBlock(hash, parent chainhash.Hash) {
	if b.validationCache == nil {
		b.validationCache = newBlockValidationCache()
	}
	for _, evicted := range b.validationCache.addPrepared(hash, parent) {
		b.releaseContractPostState(&evicted)
	}
}

func (b *BlockChain) takePreparedBlock(hash, parent chainhash.Hash) bool {
	if b.validationCache == nil {
		return false
	}
	ready, stale := b.validationCache.takePrepared(hash, parent)
	if stale {
		b.releaseContractPostState(&hash)
	}
	return ready
}
