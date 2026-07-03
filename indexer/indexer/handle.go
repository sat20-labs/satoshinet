package indexer

import "github.com/sat20-labs/satoshinet/indexer/common"

const dkvsPruneIntervalBlocks = 144

func (s *IndexerMgr) processBlock(block *common.Block) {
	if s.contractIndexer != nil {
		s.contractIndexer.ProcessBlock(block)
	}
	s.pruneExpiredDKVSOnBlock(block)
}

func (s *IndexerMgr) pruneExpiredDKVSOnBlock(block *common.Block) {
	if s == nil || s.dkvsIndexer == nil || block == nil || block.Height <= 0 {
		return
	}
	if block.Height%dkvsPruneIntervalBlocks != 0 || block.Height <= s.lastDKVSPruneHeight {
		return
	}
	pruned, err := s.dkvsIndexer.PruneExpiredAt(uint64(block.Height))
	if err != nil {
		common.Log.Warningf("DKVS prune at height %d failed: %v", block.Height, err)
		return
	}
	s.lastDKVSPruneHeight = block.Height
	if pruned > 0 {
		common.Log.Infof("DKVS pruned %d expired records at height %d", pruned, block.Height)
	}
}
