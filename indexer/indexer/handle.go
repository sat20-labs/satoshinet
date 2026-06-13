package indexer

import (
	"encoding/json"
	"fmt"
	"sort"

	db "github.com/sat20-labs/indexer/indexer/db"
	contractengine "github.com/sat20-labs/satoshinet/contract"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const templateContractIndexSnapshotKey = "template-contract-index-v1"

func (s *IndexerMgr) processBlock(block *common.Block) {
	s.indexContracts(block)
}

func (s *IndexerMgr) indexContracts(block *common.Block) {
	if block == nil || len(block.Transactions) == 0 {
		return
	}
	prefix := tmplcontract.ContractPrefixForNet(s.chaincfgParam.Net)
	summaries := make([]contractengine.ContractSummary, 0)
	history := make([]contractengine.ContractHistoryRecord, 0)
	execTxs := make([]*wire.MsgTx, 0)
	allTxs := make([]*wire.MsgTx, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		if tx == nil || tx.MsgTx == nil {
			continue
		}
		allTxs = append(allTxs, tx.MsgTx)

		txSummaries, txHistory, err := contractengine.BuildContractIndexRecords(tx.MsgTx, int64(block.Height), prefix, s.chaincfgParam)
		if err != nil {
			common.Log.Errorf("index contract tx %s at block %d failed: %v", tx.MsgTx.TxID(), block.Height, err)
		} else {
			summaries = append(summaries, txSummaries...)
			history = append(history, txHistory...)
		}

		info, err := tmplcontract.ClassifyTxForBlockOrder(tx.MsgTx, prefix)
		if err != nil || !info.IsTemplate {
			continue
		}
		if info.Type == tmplcontract.TxTypeDeploy || info.Type == tmplcontract.TxTypeInvoke {
			execTxs = append(execTxs, tx.MsgTx)
		}
	}

	if len(summaries) != 0 || len(history) != 0 {
		s.contractIndexMu.Lock()
		s.ensureContractIndexLocked()
		for _, summary := range summaries {
			s.mergeContractSummaryLocked(summary)
		}
		for _, record := range history {
			s.appendContractHistoryLocked(record)
		}
		s.contractIndexMu.Unlock()
	}

	if len(execTxs) == 0 {
		return
	}

	s.templateIndexMu.Lock()
	defer s.templateIndexMu.Unlock()
	s.ensureTemplateContractIndexLocked()

	result, err := tmplcontract.ExecuteBlock(tmplcontract.BlockExecutionRequest{
		Txs:            execTxs,
		Store:          s.templateRuntimeStore,
		Registry:       tmplcontract.NewDefaultRegistry(),
		ContractPrefix: prefix,
		GasConfig:      tmplcontract.DefaultGasConfig(),
		BlockHeight:    int64(block.Height),
		ResolveInvoker: tmplcontract.LastInputInvokerResolver(s.chaincfgParam),
	})
	if err != nil {
		common.Log.Errorf("index template contracts at block %d failed: %v", block.Height, err)
		return
	}

	resultTxIDs := matchTemplateResultTxIDs(allTxs, result.ResultPlans)
	for _, record := range tmplcontract.BlockHistoryRecords(result, resultTxIDs) {
		if record.Contract == "" {
			continue
		}
		s.templateContractHistory[record.Contract] = append(s.templateContractHistory[record.Contract], record)
		s.upsertTemplateContractHistoryLocked(record)
	}
	if err := s.updateTemplateContractSnapshotsLocked(block.Height); err != nil {
		common.Log.Errorf("snapshot template contracts at block %d failed: %v", block.Height, err)
		return
	}
	s.syncTemplateContractSummariesLocked(block.Height)
}

func (s *IndexerMgr) ensureTemplateContractIndexLocked() {
	if s.templateRuntimeStore == nil {
		s.templateRuntimeStore = tmplcontract.NewRuntimeStore()
	}
	if s.templateContractIndex == nil {
		s.templateContractIndex = make(map[string]*tmplcontract.ContractInfo)
	}
	if s.templateContractHistory == nil {
		s.templateContractHistory = make(map[string][]tmplcontract.HistoryRecord)
	}
}

func (s *IndexerMgr) updateTemplateContractSnapshotsLocked(height int) error {
	snapshots, err := s.templateRuntimeStore.Snapshots()
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		s.templateContractIndex[snapshot.Address] = &tmplcontract.ContractInfo{
			Address:       snapshot.Address,
			TemplateName:  snapshot.TemplateName,
			Version:       snapshot.Version,
			UpdatedHeight: int64(height),
			RuntimeState:  snapshot.State,
		}
	}
	return nil
}

func (s *IndexerMgr) persistTemplateContractIndexSnapshot(height int, runtimeStore *tmplcontract.RuntimeStore, contracts map[string]*tmplcontract.ContractInfo, history map[string][]tmplcontract.HistoryRecord) error {
	if s.baseDB == nil {
		return nil
	}
	if runtimeStore == nil {
		runtimeStore = tmplcontract.NewRuntimeStore()
	}
	runtime, err := runtimeStore.MarshalBinary()
	if err != nil {
		return err
	}
	snapshot := tmplcontract.IndexSnapshot{
		Height:    height,
		Runtime:   runtime,
		Contracts: cloneTemplateContractInfoMap(contracts),
		History:   cloneTemplateContractHistoryMap(history),
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return db.SetRawValueToDB([]byte(templateContractIndexSnapshotKey), encoded, s.baseDB)
}

func (s *IndexerMgr) loadTemplateContractIndex() {
	s.templateIndexMu.Lock()
	defer s.templateIndexMu.Unlock()
	s.templateRuntimeStore = tmplcontract.NewRuntimeStore()
	s.templateContractIndex = make(map[string]*tmplcontract.ContractInfo)
	s.templateContractHistory = make(map[string][]tmplcontract.HistoryRecord)
	if s.baseDB == nil {
		return
	}
	encoded, err := db.GetRawValueFromDB([]byte(templateContractIndexSnapshotKey), s.baseDB)
	if err != nil {
		return
	}
	var snapshot tmplcontract.IndexSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		common.Log.Errorf("decode template contract index snapshot failed: %v", err)
		return
	}
	if snapshot.Height > s.compiling.GetSyncHeight() {
		common.Log.Warnf("ignore template contract index snapshot at height %d above base sync height %d", snapshot.Height, s.compiling.GetSyncHeight())
		return
	}
	if len(snapshot.Runtime) != 0 {
		store, err := tmplcontract.DecodeRuntimeStore(snapshot.Runtime, tmplcontract.NewDefaultRegistry())
		if err != nil {
			common.Log.Errorf("decode template runtime store snapshot failed: %v", err)
		} else {
			s.templateRuntimeStore = store
		}
	}
	s.templateContractIndex = cloneTemplateContractInfoMap(snapshot.Contracts)
	s.templateContractHistory = cloneTemplateContractHistoryMap(snapshot.History)
}

func matchTemplateResultTxIDs(txs []*wire.MsgTx, plans []tmplcontract.ResultPlan) map[string]string {
	out := make(map[string]string)
	if len(plans) == 0 {
		return out
	}
	planInputs := make(map[string]map[tmplcontract.OutPoint]struct{})
	for _, plan := range plans {
		inputs := make(map[tmplcontract.OutPoint]struct{}, len(plan.Inputs))
		for _, input := range plan.Inputs {
			inputs[input] = struct{}{}
		}
		planInputs[plan.Contract] = inputs
	}
	for _, tx := range txs {
		parsedType, _, found, err := classifyTemplateIndexPayload(tx)
		if err != nil || !found || parsedType != tmplcontract.TxTypeResult {
			continue
		}
		txID := tx.TxID()
		for contract, inputs := range planInputs {
			if out[contract] != "" {
				continue
			}
			for _, txIn := range tx.TxIn {
				if txIn == nil {
					continue
				}
				input := tmplcontract.WireOutPointToTemplate(txIn.PreviousOutPoint)
				if _, ok := inputs[input]; ok {
					out[contract] = txID
					break
				}
			}
		}
	}
	return out
}

func classifyTemplateIndexPayload(tx *wire.MsgTx) (tmplcontract.TxType, []byte, bool, error) {
	if tx == nil {
		return 0, nil, false, fmt.Errorf("missing transaction")
	}
	var txType tmplcontract.TxType
	payload := make([]byte, 0)
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		nextType, content, err := contractcommon.ReadNullDataScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if txType == 0 {
			txType = nextType
		} else if txType != nextType {
			return 0, nil, false, fmt.Errorf("mixed contract payload types")
		} else if txType != tmplcontract.TxTypeDeploy && txType != tmplcontract.TxTypeInvoke {
			return 0, nil, false, fmt.Errorf("multiple singleton contract payloads")
		}
		payload = append(payload, content...)
	}
	return txType, payload, txType != 0, nil
}

func cloneTemplateContractInfoMap(in map[string]*tmplcontract.ContractInfo) map[string]*tmplcontract.ContractInfo {
	out := make(map[string]*tmplcontract.ContractInfo, len(in))
	for key, value := range in {
		if value == nil {
			continue
		}
		cloned := *value
		cloned.RuntimeState = cloneTemplateRuntimeState(value.RuntimeState)
		out[key] = &cloned
	}
	return out
}

func cloneTemplateContractHistoryMap(in map[string][]tmplcontract.HistoryRecord) map[string][]tmplcontract.HistoryRecord {
	out := make(map[string][]tmplcontract.HistoryRecord, len(in))
	for key, records := range in {
		out[key] = cloneTemplateHistoryRecords(records)
	}
	return out
}

func cloneTemplateHistoryRecords(in []tmplcontract.HistoryRecord) []tmplcontract.HistoryRecord {
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out []tmplcontract.HistoryRecord
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil
	}
	return out
}

func cloneTemplateRuntimeState(in tmplcontract.TemplateRuntimeState) tmplcontract.TemplateRuntimeState {
	encoded, err := json.Marshal(in)
	if err != nil {
		return tmplcontract.TemplateRuntimeState{}
	}
	var out tmplcontract.TemplateRuntimeState
	if err := json.Unmarshal(encoded, &out); err != nil {
		return tmplcontract.TemplateRuntimeState{}
	}
	return out
}

func (s *IndexerMgr) GetTemplateContracts(start, limit int) ([]*tmplcontract.ContractInfo, int) {
	s.templateIndexMu.RLock()
	defer s.templateIndexMu.RUnlock()
	contracts := make([]*tmplcontract.ContractInfo, 0, len(s.templateContractIndex))
	for _, contract := range s.templateContractIndex {
		if contract == nil {
			continue
		}
		cloned := *contract
		cloned.RuntimeState = cloneTemplateRuntimeState(contract.RuntimeState)
		contracts = append(contracts, &cloned)
	}
	sort.Slice(contracts, func(i, j int) bool {
		return contracts[i].Address < contracts[j].Address
	})
	total := len(contracts)
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = total
	}
	if start >= total {
		return nil, total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return contracts[start:end], total
}

func (s *IndexerMgr) GetTemplateContract(address string) (*tmplcontract.ContractInfo, bool) {
	s.templateIndexMu.RLock()
	defer s.templateIndexMu.RUnlock()
	contract, ok := s.templateContractIndex[address]
	if !ok || contract == nil {
		return nil, false
	}
	cloned := *contract
	cloned.RuntimeState = cloneTemplateRuntimeState(contract.RuntimeState)
	return &cloned, true
}

func (s *IndexerMgr) GetTemplateContractHistory(address string, start, limit int) ([]tmplcontract.HistoryRecord, int) {
	s.templateIndexMu.RLock()
	defer s.templateIndexMu.RUnlock()
	records := cloneTemplateHistoryRecords(s.templateContractHistory[address])
	total := len(records)
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = total
	}
	if start >= total {
		return nil, total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return records[start:end], total

}
