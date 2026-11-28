package indexer

import (
	"time"

	"github.com/sat20-labs/satoshinet/indexer/common"
)

const dkvsPruneIntervalBlocks = 144
const dkvsPruneInterval = time.Hour

func (s *IndexerMgr) processBlock(block *common.Block) {
	if s.contractIndexer != nil && block != nil && s.chaincfgParam.ContractsActive(int32(block.Height)) {
		s.contractIndexer.ProcessBlock(block)
	}
	if s != nil && s.dkvsIndexer != nil && block != nil && block.Height > 0 {
		if err := s.dkvsIndexer.RefreshPaidRetentionAt(uint64(block.Height)); err != nil {
			common.Log.Warningf("DKVS paid-retention refresh at height %d failed: %v", block.Height, err)
		}
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
	pruned, err := s.dkvsIndexer.PruneExpiredTmpAt(uint64(block.Height))
	if err != nil {
		common.Log.Warningf("DKVS temporary-record prune at height %d failed: %v", block.Height, err)
		return
	}
	s.lastDKVSPruneHeight = block.Height
	if pruned > 0 {
		common.Log.Infof("DKVS pruned %d expired temporary records at height %d", pruned, block.Height)
	}
}

func (s *IndexerMgr) startDKVSPruneTimer() {
	if s == nil || s.dkvsIndexer == nil || s.dkvsPruneStop != nil {
		return
	}
	s.dkvsPruneStop = make(chan struct{})
	stop := s.dkvsPruneStop
	interrupt := s.interrupt
	s.backgroundWG.Add(1)
	go func() {
		defer s.backgroundWG.Done()
		timer := time.NewTicker(dkvsPruneInterval)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				if err := s.dkvsIndexer.RefreshPaidRetention(); err != nil {
					common.Log.Warningf("DKVS timed paid-retention refresh failed: %v", err)
				}
				pruned, err := s.dkvsIndexer.PruneExpiredTmp()
				if err != nil {
					common.Log.Warningf("DKVS timed temporary-record prune failed: %v", err)
					continue
				}
				if pruned > 0 {
					common.Log.Infof("DKVS timed prune removed %d expired temporary records", pruned)
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
