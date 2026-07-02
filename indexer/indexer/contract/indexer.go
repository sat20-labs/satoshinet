package contractindex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	idxcommon "github.com/sat20-labs/indexer/common"
	db "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	contractengine "github.com/sat20-labs/satoshinet/contract/engine"
	sncommon "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	dbPrefixContractSummary = "contract:v1:summary:"
	dbPrefixContractHistory = "contract:v1:history:"
	dbPrefixEVMSource       = "contract:v1:evm:source:"
)

type Indexer struct {
	db            idxcommon.KVDB
	chaincfgParam *chaincfg.Params

	mutex     sync.RWMutex
	contracts map[string]*contractengine.ContractSummary
	history   map[string][]contractengine.ContractHistoryRecord
}

func NewIndexer(kvdb idxcommon.KVDB, params *chaincfg.Params) *Indexer {
	return &Indexer{
		db:            kvdb,
		chaincfgParam: params,
		contracts:     make(map[string]*contractengine.ContractSummary),
		history:       make(map[string][]contractengine.ContractHistoryRecord),
	}
}

func (s *Indexer) Clone() *Indexer {
	if s == nil {
		return nil
	}
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return &Indexer{
		db:            s.db,
		chaincfgParam: s.chaincfgParam,
		contracts:     cloneContractSummaryMap(s.contracts),
		history:       cloneContractHistoryMap(s.history),
	}
}

func (s *Indexer) Subtract(backup *Indexer) {
	if s == nil || backup == nil {
		return
	}
	backup.mutex.RLock()
	backupContracts := cloneContractSummaryMap(backup.contracts)
	backupHistory := cloneContractHistoryMap(backup.history)
	backup.mutex.RUnlock()

	s.mutex.Lock()
	defer s.mutex.Unlock()
	for address, oldSummary := range backupContracts {
		current := s.contracts[address]
		if current == nil || oldSummary == nil {
			continue
		}
		if current.UpdatedHeight == oldSummary.UpdatedHeight && current.Status == oldSummary.Status {
			delete(s.contracts, address)
		}
	}
	for address, oldRecords := range backupHistory {
		current := s.history[address]
		if len(current) <= len(oldRecords) {
			delete(s.history, address)
			continue
		}
		s.history[address] = cloneContractHistoryRecords(current[len(oldRecords):])
	}
}

func (s *Indexer) UpdateDB() {
	if s == nil || s.db == nil {
		return
	}
	s.mutex.Lock()
	contracts := cloneContractSummaryMap(s.contracts)
	history := cloneContractHistoryMap(s.history)
	s.contracts = make(map[string]*contractengine.ContractSummary)
	s.history = make(map[string][]contractengine.ContractHistoryRecord)
	s.mutex.Unlock()

	if len(contracts) == 0 && len(history) == 0 {
		return
	}
	wb := s.db.NewWriteBatch()
	defer wb.Close()
	for address, summary := range contracts {
		if summary == nil {
			continue
		}
		if err := setJSON(wb, []byte(contractSummaryKey(address)), summary); err != nil {
			sncommon.Log.Panicf("contract index set summary %s failed: %v", address, err)
		}
	}
	for address, records := range history {
		for i, record := range records {
			key := contractHistoryKey(address, record, i)
			if err := setJSON(wb, []byte(key), &record); err != nil {
				sncommon.Log.Panicf("contract index set history %s failed: %v", key, err)
			}
		}
	}
	if err := wb.Flush(); err != nil {
		sncommon.Log.Panicf("contract index flush failed: %v", err)
	}
}

func (s *Indexer) ProcessBlock(block *sncommon.Block) {
	if s == nil || block == nil || len(block.Transactions) == 0 {
		return
	}
	prefix := contractIndexPrefixForParams(s.chaincfgParam)
	ctx := contractengine.NewContractIndexContext()
	summaries := make([]contractengine.ContractSummary, 0)
	history := make([]contractengine.ContractHistoryRecord, 0)
	for _, tx := range block.Transactions {
		if tx == nil || tx.MsgTx == nil {
			continue
		}
		if !isContractInteractionTx(tx.MsgTx, prefix) {
			continue
		}

		txSummaries, txHistory, err := contractengine.BuildContractIndexRecordsWithContext(tx.MsgTx, int64(block.Height), prefix, s.chaincfgParam, ctx)
		if err != nil {
			sncommon.Log.Errorf("index contract tx %s at block %d failed: %v", tx.MsgTx.TxID(), block.Height, err)
			continue
		}
		ctx.AddFundingRefs(tx.MsgTx, txHistory, prefix)
		summaries = append(summaries, txSummaries...)
		history = append(history, txHistory...)
	}

	if len(summaries) == 0 && len(history) == 0 {
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, summary := range summaries {
		s.mergeContractSummaryLocked(summary)
	}
	for _, record := range history {
		s.appendContractHistoryLocked(record)
	}
}

func (s *Indexer) CheckSelf() bool {
	if s == nil {
		return true
	}
	start := time.Now()
	sncommon.Log.Info("ContractIndexer->checkSelf ... ")

	checked := &contractIndexCheckState{
		summaries: make(map[string]struct{}),
	}
	if !s.checkDBSummaries(checked) {
		return false
	}
	if !s.checkMemorySummaries(checked) {
		return false
	}
	if !s.checkDBHistory(checked) {
		return false
	}
	if !s.checkMemoryHistory(checked) {
		return false
	}

	sncommon.Log.Infof("ContractIndexer.checkSelf takes %v", time.Since(start))
	return true
}

type contractIndexCheckState struct {
	summaries map[string]struct{}
}

func (s *Indexer) checkDBSummaries(checked *contractIndexCheckState) bool {
	if s.db == nil {
		return true
	}
	valid := true
	if err := s.db.BatchRead([]byte(dbPrefixContractSummary), false, func(k, v []byte) error {
		keyAddress, ok := decodeSummaryAddressFromKey(string(k))
		if !ok {
			sncommon.Log.Errorf("contract summary key %s is invalid", string(k))
			valid = false
			return nil
		}
		var summary contractengine.ContractSummary
		if err := json.Unmarshal(cloneBytes(v), &summary); err != nil {
			sncommon.Log.Errorf("decode contract summary %s failed: %v", string(k), err)
			valid = false
			return nil
		}
		if summary.Address == "" || summary.Address != keyAddress {
			sncommon.Log.Errorf("contract summary key %s address mismatch: key=%s value=%s", string(k), keyAddress, summary.Address)
			valid = false
			return nil
		}
		checked.summaries[summary.Address] = struct{}{}
		return nil
	}); err != nil {
		sncommon.Log.Errorf("check contract summaries failed: %v", err)
		return false
	}
	return valid
}

func (s *Indexer) checkMemorySummaries(checked *contractIndexCheckState) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	for address, summary := range s.contracts {
		if address == "" || summary == nil || summary.Address == "" || summary.Address != address {
			sncommon.Log.Errorf("contract memory summary address mismatch: key=%s value=%v", address, summary)
			return false
		}
		checked.summaries[address] = struct{}{}
	}
	return true
}

func (s *Indexer) checkDBHistory(checked *contractIndexCheckState) bool {
	if s.db == nil {
		return true
	}
	valid := true
	if err := s.db.BatchRead([]byte(dbPrefixContractHistory), false, func(k, v []byte) error {
		keyAddress, ok := decodeHistoryAddressFromKey(string(k))
		if !ok {
			sncommon.Log.Errorf("contract history key %s is invalid", string(k))
			valid = false
			return nil
		}
		var record contractengine.ContractHistoryRecord
		if err := json.Unmarshal(cloneBytes(v), &record); err != nil {
			sncommon.Log.Errorf("decode contract history %s failed: %v", string(k), err)
			valid = false
			return nil
		}
		if !checkContractHistoryRecord(keyAddress, record, checked) {
			valid = false
		}
		return nil
	}); err != nil {
		sncommon.Log.Errorf("check contract history failed: %v", err)
		return false
	}
	return valid
}

func (s *Indexer) checkMemoryHistory(checked *contractIndexCheckState) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	for address, records := range s.history {
		if address == "" {
			sncommon.Log.Errorf("contract memory history has empty address")
			return false
		}
		for _, record := range records {
			if !checkContractHistoryRecord(address, record, checked) {
				return false
			}
		}
	}
	return true
}

func checkContractHistoryRecord(keyAddress string, record contractengine.ContractHistoryRecord, checked *contractIndexCheckState) bool {
	if record.Contract == "" || record.Contract != keyAddress {
		sncommon.Log.Errorf("contract history address mismatch: key=%s value=%s", keyAddress, record.Contract)
		return false
	}
	if record.TxID == "" {
		sncommon.Log.Errorf("contract history %s has empty txid", keyAddress)
		return false
	}
	if _, ok := checked.summaries[keyAddress]; !ok {
		sncommon.Log.Errorf("contract history %s has no summary", keyAddress)
		return false
	}
	return true
}

func isContractInteractionTx(tx *wire.MsgTx, prefix string) bool {
	if tx == nil {
		return false
	}
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		if _, _, err := contractcommon.ReadNullDataScript(txOut.PkScript); err == nil {
			return true
		}
		if _, ok, err := contractcommon.ParseContractPkScript(txOut.PkScript, prefix); ok || err != nil {
			return true
		}
	}
	return false
}

func (s *Indexer) GetContractSummaries(start, limit int) ([]contractengine.ContractSummary, int) {
	contracts := make(map[string]*contractengine.ContractSummary)
	for address, summary := range s.loadContractSummariesFromDB() {
		contracts[address] = summary
	}
	s.mutex.RLock()
	for address, summary := range s.contracts {
		if summary == nil {
			continue
		}
		cp := cloneContractSummary(*summary)
		contracts[address] = &cp
	}
	s.mutex.RUnlock()

	result := make([]contractengine.ContractSummary, 0, len(contracts))
	for _, summary := range contracts {
		if summary != nil {
			result = append(result, cloneContractSummary(*summary))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	total := len(result)
	return paginateContractSummaries(result, start, limit), total
}

func (s *Indexer) GetContractSummary(address string) (contractengine.ContractSummary, bool) {
	s.mutex.RLock()
	if summary := s.contracts[address]; summary != nil {
		out := cloneContractSummary(*summary)
		s.mutex.RUnlock()
		return out, true
	}
	s.mutex.RUnlock()
	return s.loadContractSummaryFromDB(address)
}

func (s *Indexer) GetContractHistory(address string, start, limit int) ([]contractengine.ContractHistoryRecord, int) {
	records := s.loadContractHistoryFromDB(address)
	s.mutex.RLock()
	records = append(records, cloneContractHistoryRecords(s.history[address])...)
	s.mutex.RUnlock()
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].Height != records[j].Height {
			return records[i].Height < records[j].Height
		}
		return records[i].TxID < records[j].TxID
	})
	total := len(records)
	return paginateContractHistoryRecords(records, start, limit), total
}

func (s *Indexer) GetEVMSourceMetadata(address string) (contractcommon.EVMSourceMetadata, bool) {
	if s == nil || s.db == nil || address == "" {
		return contractcommon.EVMSourceMetadata{}, false
	}
	var metadata contractcommon.EVMSourceMetadata
	if err := getJSON(s.db, []byte(evmSourceKey(address)), &metadata); err != nil {
		if err != idxcommon.ErrKeyNotFound {
			sncommon.Log.Errorf("load EVM source metadata %s failed: %v", address, err)
		}
		return contractcommon.EVMSourceMetadata{}, false
	}
	return metadata, true
}

func (s *Indexer) PutEVMSourceMetadata(metadata contractcommon.EVMSourceMetadata) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("missing contract indexer db")
	}
	if strings.TrimSpace(metadata.ContractAddress) == "" {
		return fmt.Errorf("missing contract address")
	}
	now := time.Now().Unix()
	if metadata.SubmittedAt == 0 {
		metadata.SubmittedAt = now
	}
	metadata.UpdatedAt = now
	wb := s.db.NewWriteBatch()
	defer wb.Close()
	if err := setJSON(wb, []byte(evmSourceKey(metadata.ContractAddress)), &metadata); err != nil {
		return err
	}
	return wb.Flush()
}

func (s *Indexer) mergeContractSummaryLocked(summary contractengine.ContractSummary) {
	if summary.Address == "" {
		return
	}
	summary = normalizeContractSummary(summary)
	existing := s.contracts[summary.Address]
	if existing == nil {
		if persisted, ok := s.loadContractSummaryFromDB(summary.Address); ok {
			cp := cloneContractSummary(persisted)
			s.contracts[summary.Address] = &cp
			existing = &cp
		}
	}
	if existing == nil {
		cp := cloneContractSummary(summary)
		s.contracts[summary.Address] = &cp
		return
	}
	mergeContractSummary(existing, summary)
}

func (s *Indexer) appendContractHistoryLocked(record contractengine.ContractHistoryRecord) {
	if record.Contract == "" {
		return
	}
	record = normalizeContractHistoryRecord(record)
	s.history[record.Contract] = append(s.history[record.Contract], cloneContractHistoryRecord(record))
}

func (s *Indexer) loadContractSummaryFromDB(address string) (contractengine.ContractSummary, bool) {
	if s == nil || s.db == nil || address == "" {
		return contractengine.ContractSummary{}, false
	}
	var summary contractengine.ContractSummary
	if err := getJSON(s.db, []byte(contractSummaryKey(address)), &summary); err != nil {
		if err != idxcommon.ErrKeyNotFound {
			sncommon.Log.Errorf("load contract summary %s failed: %v", address, err)
		}
		return contractengine.ContractSummary{}, false
	}
	return cloneContractSummary(summary), true
}

func (s *Indexer) loadContractSummariesFromDB() map[string]*contractengine.ContractSummary {
	out := make(map[string]*contractengine.ContractSummary)
	if s == nil || s.db == nil {
		return out
	}
	if err := s.db.BatchRead([]byte(dbPrefixContractSummary), false, func(k, v []byte) error {
		var summary contractengine.ContractSummary
		if err := json.Unmarshal(cloneBytes(v), &summary); err != nil {
			sncommon.Log.Errorf("decode contract summary %s failed: %v", string(k), err)
			return nil
		}
		if summary.Address != "" {
			cp := cloneContractSummary(summary)
			out[summary.Address] = &cp
		}
		return nil
	}); err != nil {
		sncommon.Log.Errorf("load contract summaries failed: %v", err)
	}
	return out
}

func (s *Indexer) loadContractHistoryFromDB(address string) []contractengine.ContractHistoryRecord {
	out := make([]contractengine.ContractHistoryRecord, 0)
	if s == nil || s.db == nil || address == "" {
		return out
	}
	prefix := []byte(contractHistoryPrefix(address))
	if err := s.db.BatchRead(prefix, false, func(k, v []byte) error {
		var record contractengine.ContractHistoryRecord
		if err := json.Unmarshal(cloneBytes(v), &record); err != nil {
			sncommon.Log.Errorf("decode contract history %s failed: %v", string(k), err)
			return nil
		}
		out = append(out, cloneContractHistoryRecord(record))
		return nil
	}); err != nil {
		sncommon.Log.Errorf("load contract history %s failed: %v", address, err)
	}
	return out
}

func mergeContractSummary(existing *contractengine.ContractSummary, summary contractengine.ContractSummary) {
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

func contractIndexPrefixForNet(net wire.BitcoinNet) string {
	if net == wire.MainNet {
		return contractcommon.MainnetContractPrefix
	}
	return contractcommon.TestnetContractPrefix
}

func contractIndexPrefixForParams(params *chaincfg.Params) string {
	if params == nil {
		return contractcommon.TestnetContractPrefix
	}
	return contractIndexPrefixForNet(params.Net)
}

func contractSummaryKey(address string) string {
	return dbPrefixContractSummary + encodeKeyPart(address)
}

func contractHistoryPrefix(address string) string {
	return dbPrefixContractHistory + encodeKeyPart(address) + ":"
}

func contractHistoryKey(address string, record contractengine.ContractHistoryRecord, index int) string {
	return fmt.Sprintf("%s%016x:%s:%06d", contractHistoryPrefix(address), uint64(record.Height), encodeKeyPart(record.TxID), index)
}

func evmSourceKey(address string) string {
	return dbPrefixEVMSource + encodeKeyPart(address)
}

func encodeKeyPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeSummaryAddressFromKey(key string) (string, bool) {
	encodedAddress, ok := strings.CutPrefix(key, dbPrefixContractSummary)
	if !ok || encodedAddress == "" {
		return "", false
	}
	return decodeKeyPart(encodedAddress)
}

func decodeHistoryAddressFromKey(key string) (string, bool) {
	encodedAndRest, ok := strings.CutPrefix(key, dbPrefixContractHistory)
	if !ok || encodedAndRest == "" {
		return "", false
	}
	encodedAddress, _, ok := strings.Cut(encodedAndRest, ":")
	if !ok || encodedAddress == "" {
		return "", false
	}
	return decodeKeyPart(encodedAddress)
}

func decodeKeyPart(value string) (string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

func setJSON(wb idxcommon.WriteBatch, key []byte, value interface{}) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.SetRawDB(key, encoded, wb)
}

func getJSON(kvdb idxcommon.KVDB, key []byte, value interface{}) error {
	encoded, err := db.GetRawValueFromDB(key, kvdb)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, value)
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

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
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

func HistoryDebugKeyPrefix(address string) string {
	return strings.TrimSuffix(contractHistoryPrefix(address), ":")
}
