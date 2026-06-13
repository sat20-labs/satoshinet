package indexer

import "github.com/sat20-labs/satoshinet/indexer/common"

func (s *IndexerMgr) processBlock(block *common.Block) {
	if s.contractIndexer != nil {
		s.contractIndexer.ProcessBlock(block)
	}
}
