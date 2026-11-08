package indexer

import (
	"fmt"
	"strings"

	contract "github.com/sat20-labs/satoshinet/contract"
	contractengine "github.com/sat20-labs/satoshinet/contract/engine"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

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

func (s *IndexerMgr) GetEVMSourceMetadata(address string) (contractengine.EVMSourceMetadata, error) {
	if s.dkvsIndexer == nil {
		return contractengine.EVMSourceMetadata{}, dkvs.ErrStorageUnavailable
	}
	record, err := s.dkvsIndexer.Get("/contract/evm/source/" + strings.ToLower(strings.TrimSpace(address)))
	if err != nil {
		return contractengine.EVMSourceMetadata{}, err
	}
	metadata, err := contract.DecodeEVMSourceMetadata(record.Value)
	if err != nil {
		return metadata, fmt.Errorf("decode stored EVM source metadata: %w", err)
	}
	// Admission verified the complete init code and ABI. Client flags never
	// determine the returned verification status; runtime replay is not claimed.
	metadata.Verified = true
	metadata.VerifyStatus = "verified-init-code"
	metadata.VerifyError = ""
	return metadata, nil
}
