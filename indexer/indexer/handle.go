package indexer

import (
	"time"

	"github.com/sat20-labs/satoshinet/indexer/common"
)

const dkvsPruneIntervalBlocks = 144
const dkvsPruneInterval = time.Hour

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
	freePruned, err := s.dkvsIndexer.PruneExpiredAt(uint64(block.Height))
	if err != nil {
		common.Log.Warningf("DKVS free-record prune at height %d failed: %v", block.Height, err)
		return
	}
	paidPruned, err := s.dkvsIndexer.PruneExpiredAutopayAt(uint64(block.Height))
	if err != nil {
		common.Log.Warningf("DKVS AUTOPAY prune at height %d failed: %v", block.Height, err)
		return
	}
	s.lastDKVSPruneHeight = block.Height
	if pruned := freePruned + paidPruned; pruned > 0 {
		common.Log.Infof("DKVS pruned %d expired records at height %d", pruned, block.Height)
	}
}

func (s *IndexerMgr) startDKVSPruneTimer() {
	if s == nil || s.dkvsIndexer == nil || s.dkvsPruneStop != nil {
		return
	}
	s.dkvsPruneStop = make(chan struct{})
	stop := s.dkvsPruneStop
	interrupt := s.interrupt
	go func() {
		timer := time.NewTicker(dkvsPruneInterval)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				freePruned, err := s.dkvsIndexer.PruneExpired()
				if err != nil {
					common.Log.Warningf("DKVS timed free-record prune failed: %v", err)
					continue
				}
				paidPruned, err := s.dkvsIndexer.PruneExpiredAutopay()
				if err != nil {
					common.Log.Warningf("DKVS timed AUTOPAY prune failed: %v", err)
					continue
				}
				if pruned := freePruned + paidPruned; pruned > 0 {
					common.Log.Infof("DKVS timed prune removed %d expired records", pruned)
				}
			case <-stop:
				return
			case <-interrupt:
				return
			}
		}
	}()
}

func (s *IndexerMgr) stopDKVSPruneTimer() {
	if s == nil || s.dkvsPruneStop == nil {
		return
	}
	close(s.dkvsPruneStop)
	s.dkvsPruneStop = nil
}
