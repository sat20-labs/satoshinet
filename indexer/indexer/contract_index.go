package indexer

import contractengine "github.com/sat20-labs/satoshinet/contract/engine"

func (s *IndexerMgr) GetContractSummaries(start, limit int) ([]contractengine.ContractSummary, int) {
	if s.contractIndexer == nil {
		return nil, 0
	}
	return s.contractIndexer.GetContractSummaries(start, limit)
}

func (s *IndexerMgr) GetContractSummary(address string) (contractengine.ContractSummary, bool) {
	if s.contractIndexer == nil {
		return contractengine.ContractSummary{}, false
	}
	return s.contractIndexer.GetContractSummary(address)
}

func (s *IndexerMgr) GetContractHistory(address string, start, limit int) ([]contractengine.ContractHistoryRecord, int) {
	if s.contractIndexer == nil {
		return nil, 0
	}
	return s.contractIndexer.GetContractHistory(address, start, limit)
}

func (s *IndexerMgr) GetEVMSourceMetadata(address string) (contractengine.EVMSourceMetadata, bool) {
	if s.contractIndexer == nil {
		return contractengine.EVMSourceMetadata{}, false
	}
	return s.contractIndexer.GetEVMSourceMetadata(address)
}

func (s *IndexerMgr) PutEVMSourceMetadata(metadata contractengine.EVMSourceMetadata) error {
	if s.contractIndexer == nil {
		return nil
	}
	return s.contractIndexer.PutEVMSourceMetadata(metadata)
}
