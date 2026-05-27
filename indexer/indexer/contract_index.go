package indexer

import (
	"encoding/json"
	"sort"

	db "github.com/sat20-labs/indexer/indexer/db"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/indexer/common"
)

const contractIndexSnapshotKey = "contract-index-v1"

type contractIndexSnapshot struct {
	Height    int                                               `json:"height"`
	Contracts map[string]*contractengine.ContractSummary        `json:"contracts"`
	History   map[string][]contractengine.ContractHistoryRecord `json:"history"`
}

func (s *IndexerMgr) indexContracts(block *common.Block) {
	if block == nil || len(block.Transactions) == 0 {
		return
	}
	prefix := tmplcontract.ContractPrefixForNet(s.chaincfgParam.Net)
	summaries := make([]contractengine.ContractSummary, 0)
	history := make([]contractengine.ContractHistoryRecord, 0)
	for _, tx := range block.Transactions {
		if tx == nil || tx.MsgTx == nil {
			continue
		}
		txSummaries, txHistory, err := contractengine.BuildContractIndexRecords(tx.MsgTx, int64(block.Height), prefix, s.chaincfgParam)
		if err != nil {
			common.Log.Errorf("index contract tx %s at block %d failed: %v", tx.MsgTx.TxID(), block.Height, err)
			continue
		}
		summaries = append(summaries, txSummaries...)
		history = append(history, txHistory...)
	}
	if len(summaries) == 0 && len(history) == 0 {
		return
	}
	s.contractIndexMu.Lock()
	defer s.contractIndexMu.Unlock()
	s.ensureContractIndexLocked()
	for _, summary := range summaries {
		s.mergeContractSummaryLocked(summary)
	}
	for _, record := range history {
		s.appendContractHistoryLocked(record)
	}
	if err := s.persistContractIndexLocked(block.Height); err != nil {
		common.Log.Errorf("persist contract index at block %d failed: %v", block.Height, err)
	}
}

func (s *IndexerMgr) ensureContractIndexLocked() {
	if s.contractIndex == nil {
		s.contractIndex = make(map[string]*contractengine.ContractSummary)
	}
	if s.contractHistory == nil {
		s.contractHistory = make(map[string][]contractengine.ContractHistoryRecord)
	}
}

func (s *IndexerMgr) mergeContractSummaryLocked(summary contractengine.ContractSummary) {
	if summary.Address == "" {
		return
	}
	summary = normalizeContractSummary(summary)
	existing := s.contractIndex[summary.Address]
	if existing == nil {
		cp := cloneContractSummary(summary)
		s.contractIndex[summary.Address] = &cp
		return
	}
	if existing.CreatedHeight == 0 || (summary.CreatedHeight != 0 && summary.CreatedHeight < existing.CreatedHeight) {
		existing.CreatedHeight = summary.CreatedHeight
	}
	if summary.UpdatedHeight > existing.UpdatedHeight {
		existing.UpdatedHeight = summary.UpdatedHeight
	}
	if summary.ContractType != "" {
		existing.ContractType = summary.ContractType
	}
	if summary.ContractTypeID != 0 {
		existing.ContractTypeID = summary.ContractTypeID
	}
	if summary.Subtype != "" {
		existing.Subtype = summary.Subtype
	}
	if summary.Name != "" {
		existing.Name = summary.Name
	}
	if summary.Version != 0 {
		existing.Version = summary.Version
	}
	if summary.Status != "" {
		existing.Status = summary.Status
	}
	if len(summary.Details) != 0 {
		if existing.Details == nil {
			existing.Details = make(map[string]interface{})
		}
		for key, value := range summary.Details {
			existing.Details[key] = cloneContractJSONValue(value)
		}
	}
}

func (s *IndexerMgr) appendContractHistoryLocked(record contractengine.ContractHistoryRecord) {
	if record.Contract == "" {
		return
	}
	record = normalizeContractHistoryRecord(record)
	s.contractHistory[record.Contract] = append(s.contractHistory[record.Contract], cloneContractHistoryRecord(record))
}

func normalizeContractSummary(summary contractengine.ContractSummary) contractengine.ContractSummary {
	if summary.ContractType == "" {
		summary.ContractType = contractengine.GetContractTypeName(summary.ContractTypeID)
	}
	if summary.ContractTypeID == contractcommon.ContractTypeAgent && summary.Subtype == agentcontract.SubtypePrediction {
		if summary.Name == "" {
			summary.Name = agentcontract.SubtypePrediction
		}
		if summary.Status == "" {
			if _, ok := summary.Details["prediction"]; ok {
				summary.Status = agentcontract.StatusPendingReady
			}
		}
	}
	return summary
}

func normalizeContractHistoryRecord(record contractengine.ContractHistoryRecord) contractengine.ContractHistoryRecord {
	if record.ContractType == "" {
		record.ContractType = contractengine.GetContractTypeName(record.ContractTypeID)
	}
	if record.ContractTypeID != contractcommon.ContractTypeAgent {
		return record
	}
	switch record.Action {
	case agentcontract.InvokeAPIReady:
		if record.Status == "" {
			record.Status = agentcontract.StatusReady
		}
	case agentcontract.InvokeAPIReject:
		if record.Status == "" {
			record.Status = agentcontract.StatusRejected
		}
	case agentcontract.InvokeAPIBet:
		if record.Status == "" {
			record.Status = agentcontract.PredictionStatusBetting
		}
	case agentcontract.InvokeAPIConfirm:
		if record.Status == "" {
			record.Status = agentcontract.PredictionStatusConfirmed
		}
	}
	return record
}

func (s *IndexerMgr) upsertTemplateContractHistoryLocked(record tmplcontract.HistoryRecord) {
	s.contractIndexMu.Lock()
	defer s.contractIndexMu.Unlock()
	s.ensureContractIndexLocked()
	unified := contractengine.ContractHistoryRecord{
		Kind:           record.Kind,
		Height:         record.Height,
		TxID:           record.TxID,
		Contract:       record.Contract,
		ContractType:   contractengine.GetContractTypeName(contractcommon.ContractTypeTemplate),
		ContractTypeID: contractcommon.ContractTypeTemplate,
		GasLimit:       record.GasLimit,
		Details: map[string]interface{}{
			"templateHistory": record,
		},
	}
	s.appendContractHistoryLocked(unified)
}

func (s *IndexerMgr) syncTemplateContractSummariesLocked(height int) {
	s.contractIndexMu.Lock()
	defer s.contractIndexMu.Unlock()
	s.ensureContractIndexLocked()
	for _, contract := range s.templateContractIndex {
		if contract == nil {
			continue
		}
		s.mergeContractSummaryLocked(contractengine.ContractSummary{
			Address:        contract.Address,
			ContractType:   contractengine.GetContractTypeName(contractcommon.ContractTypeTemplate),
			ContractTypeID: contractcommon.ContractTypeTemplate,
			Subtype:        contract.TemplateName,
			Name:           contract.TemplateName,
			Version:        contract.Version,
			UpdatedHeight:  int64(height),
			Details: map[string]interface{}{
				"template": contract,
			},
		})
	}
	if err := s.persistContractIndexLocked(height); err != nil {
		common.Log.Errorf("persist contract index at block %d failed: %v", height, err)
	}
}

func (s *IndexerMgr) persistContractIndexLocked(height int) error {
	if s.baseDB == nil {
		return nil
	}
	snapshot := contractIndexSnapshot{
		Height:    height,
		Contracts: cloneContractSummaryMap(s.contractIndex),
		History:   cloneContractHistoryMap(s.contractHistory),
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return db.SetRawValueToDB([]byte(contractIndexSnapshotKey), encoded, s.baseDB)
}

func (s *IndexerMgr) loadContractIndex() {
	s.contractIndexMu.Lock()
	defer s.contractIndexMu.Unlock()
	s.contractIndex = make(map[string]*contractengine.ContractSummary)
	s.contractHistory = make(map[string][]contractengine.ContractHistoryRecord)
	if s.baseDB == nil {
		return
	}
	encoded, err := db.GetRawValueFromDB([]byte(contractIndexSnapshotKey), s.baseDB)
	if err != nil {
		return
	}
	var snapshot contractIndexSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		common.Log.Errorf("decode contract index snapshot failed: %v", err)
		return
	}
	if snapshot.Height > s.compiling.GetSyncHeight() {
		common.Log.Warnf("ignore contract index snapshot at height %d above base sync height %d", snapshot.Height, s.compiling.GetSyncHeight())
		return
	}
	s.contractIndex = cloneContractSummaryMap(snapshot.Contracts)
	s.contractHistory = cloneContractHistoryMap(snapshot.History)
}

func (s *IndexerMgr) GetContractSummaries(start, limit int) ([]contractengine.ContractSummary, int) {
	s.contractIndexMu.RLock()
	defer s.contractIndexMu.RUnlock()
	contracts := make([]contractengine.ContractSummary, 0, len(s.contractIndex))
	for _, contract := range s.contractIndex {
		if contract == nil {
			continue
		}
		contracts = append(contracts, cloneContractSummary(*contract))
	}
	sort.Slice(contracts, func(i, j int) bool {
		return contracts[i].Address < contracts[j].Address
	})
	total := len(contracts)
	return paginateContractSummaries(contracts, start, limit), total
}

func (s *IndexerMgr) GetContractSummary(address string) (contractengine.ContractSummary, bool) {
	s.contractIndexMu.RLock()
	defer s.contractIndexMu.RUnlock()
	contract, ok := s.contractIndex[address]
	if !ok || contract == nil {
		return contractengine.ContractSummary{}, false
	}
	return cloneContractSummary(*contract), true
}

func (s *IndexerMgr) GetContractHistory(address string, start, limit int) ([]contractengine.ContractHistoryRecord, int) {
	s.contractIndexMu.RLock()
	defer s.contractIndexMu.RUnlock()
	records := cloneContractHistoryRecords(s.contractHistory[address])
	total := len(records)
	return paginateContractHistoryRecords(records, start, limit), total
}

func cloneContractSummaryMap(in map[string]*contractengine.ContractSummary) map[string]*contractengine.ContractSummary {
	out := make(map[string]*contractengine.ContractSummary, len(in))
	for key, value := range in {
		if value == nil {
			continue
		}
		cloned := cloneContractSummary(*value)
		out[key] = &cloned
	}
	return out
}

func cloneContractHistoryMap(in map[string][]contractengine.ContractHistoryRecord) map[string][]contractengine.ContractHistoryRecord {
	out := make(map[string][]contractengine.ContractHistoryRecord, len(in))
	for key, records := range in {
		out[key] = cloneContractHistoryRecords(records)
	}
	return out
}

func cloneContractSummary(in contractengine.ContractSummary) contractengine.ContractSummary {
	encoded, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out contractengine.ContractSummary
	if err := json.Unmarshal(encoded, &out); err != nil {
		return in
	}
	return out
}

func cloneContractHistoryRecord(in contractengine.ContractHistoryRecord) contractengine.ContractHistoryRecord {
	encoded, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out contractengine.ContractHistoryRecord
	if err := json.Unmarshal(encoded, &out); err != nil {
		return in
	}
	return out
}

func cloneContractHistoryRecords(in []contractengine.ContractHistoryRecord) []contractengine.ContractHistoryRecord {
	out := make([]contractengine.ContractHistoryRecord, 0, len(in))
	for _, record := range in {
		out = append(out, cloneContractHistoryRecord(record))
	}
	return out
}

func cloneContractJSONValue(value interface{}) interface{} {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out interface{}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return value
	}
	return out
}

func paginateContractSummaries(contracts []contractengine.ContractSummary, start, limit int) []contractengine.ContractSummary {
	total := len(contracts)
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = total
	}
	if start >= total {
		return nil
	}
	end := start + limit
	if end > total {
		end = total
	}
	return contracts[start:end]
}

func paginateContractHistoryRecords(records []contractengine.ContractHistoryRecord, start, limit int) []contractengine.ContractHistoryRecord {
	total := len(records)
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = total
	}
	if start >= total {
		return nil
	}
	end := start + limit
	if end > total {
		end = total
	}
	return records[start:end]
}
