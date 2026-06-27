package engine

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractSummary = contractcommon.ContractSummary
type ContractHistoryRecord = contractcommon.ContractHistoryRecord
type ContractAnalytics = contractframework.ContractAnalytics
type ContractUserStatus = contractframework.ContractUserStatus
type ContractOutcomeView = contractframework.ContractOutcomeView
type ContractTransferView = contractframework.ContractTransferView
type IndexEvent = contractcommon.IndexEvent

type ContractQueryStore interface {
	GetContractSummaries(start, limit int) ([]ContractSummary, int)
	GetContractSummary(address string) (ContractSummary, bool)
	GetContractHistory(address string, start, limit int) ([]ContractHistoryRecord, int)
}

type QueryService struct {
	store       ContractQueryStore
	chainParams *chaincfg.Params
}

func NewQueryService(store ContractQueryStore) QueryService {
	return NewQueryServiceForParams(store, nil)
}

func NewQueryServiceForParams(store ContractQueryStore, params *chaincfg.Params) QueryService {
	return QueryService{store: store, chainParams: params}
}

func (q QueryService) SupportedContracts() []string {
	contracts := []string{"evm", contractcommon.TemplateLimitOrder, contractcommon.TemplateAMM, contractcommon.TemplateExchange}
	if q.chainParams == nil || q.chainParams.Net != wire.MainNet {
		contracts = append(contracts[:1], append([]string{"agent:prediction"}, contracts[1:]...)...)
	}
	return contracts
}

func (q QueryService) DeployedContracts(start, limit int) ([]string, int) {
	contracts, total := q.Contracts(start, limit)
	out := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		out = append(out, contract.Address)
	}
	return out, total
}

func (q QueryService) Contracts(start, limit int) ([]ContractSummary, int) {
	if q.store == nil {
		return nil, 0
	}
	return q.store.GetContractSummaries(start, limit)
}

func (q QueryService) Contract(contractAddress string) (ContractSummary, error) {
	summary, err := q.requireContractType(contractAddress, "contract")
	if err != nil {
		return ContractSummary{}, err
	}
	return summary, nil
}

func (q QueryService) Analytics(contractAddress string) (*ContractAnalytics, error) {
	summary, err := q.requireContractType(contractAddress, "analytics")
	if err != nil {
		return nil, err
	}
	return q.contractAnalytics(summary)
}

func (q QueryService) InvokeItemByInUtxo(contractAddress, inUtxo string) (*ContractHistoryRecord, error) {
	_, err := q.requireContractType(contractAddress, "input utxo item")
	if err != nil {
		return nil, err
	}
	return q.invokeItemByInUtxo(contractAddress, inUtxo)
}

func (q QueryService) AllAddresses(contractAddress string, start, limit int) ([]string, int, error) {
	_, err := q.requireContractType(contractAddress, "users")
	if err != nil {
		return nil, 0, err
	}
	return q.allAddresses(contractAddress, start, limit)
}

func (q QueryService) UserStatus(contractAddress, address string) (*ContractUserStatus, error) {
	_, err := q.requireContractType(contractAddress, "user status")
	if err != nil {
		return nil, err
	}
	return q.userStatus(contractAddress, address)
}

func (q QueryService) HistoryByAddress(contractAddress, address string, start, limit int) ([]ContractHistoryRecord, int, error) {
	_, err := q.requireContractType(contractAddress, "user history")
	if err != nil {
		return nil, 0, err
	}
	return q.historyByAddress(contractAddress, address, start, limit)
}

func (q QueryService) History(contractAddress string, start, limit int) ([]ContractHistoryRecord, int, error) {
	if q.store == nil {
		return nil, 0, fmt.Errorf("contract query store is not available")
	}
	if _, err := q.requireContractType(contractAddress, "history"); err != nil {
		return nil, 0, err
	}
	history, total := q.store.GetContractHistory(contractAddress, start, limit)
	return history, total, nil
}

func (q QueryService) requireContractType(contractAddress string, view string) (ContractSummary, error) {
	contractAddr, err := contractcommon.DecodeContractAddress(contractAddress)
	if err != nil {
		return ContractSummary{}, err
	}
	if q.store != nil {
		if summary, ok := q.store.GetContractSummary(contractAddress); ok {
			return summary, nil
		}
	}
	return ContractSummary{
		Address:        contractAddr.EncodeAddress(),
		ContractType:   GetContractTypeName(contractAddr.ContractType()),
		ContractTypeID: contractAddr.ContractType(),
	}, nil
}

func BuildContractIndexRecords(tx *wire.MsgTx, height int64, prefix string, params *chaincfg.Params) ([]ContractSummary, []ContractHistoryRecord, error) {
	view, err := BuildTxView(tx, prefix)
	if err != nil || !view.IsContract {
		return nil, nil, err
	}
	outputByType := make(map[byte]string)
	outputValueByContract := make(map[string]int64)
	for _, output := range view.Outputs {
		if output.Contract == "" {
			continue
		}
		outputByType[output.ContractTypeID] = output.Contract
		outputValueByContract[output.Contract] += output.Value
	}
	summaries := make([]ContractSummary, 0)
	history := make([]ContractHistoryRecord, 0)
	for _, op := range view.Ops {
		contract := op.Contract
		if contract == "" {
			contract = outputByType[op.ContractTypeID]
		}
		if contract == "" {
			continue
		}
		details := cloneDetails(op.Details)
		enrichDetailsFromPayload(details, op)
		if value := outputValueByContract[contract]; value != 0 {
			details["contract_value"] = value
		}
		if op.Kind == "invoke" {
			actor, funding := contractIndexDetails(tx, prefix, params, op.ContractTypeID)
			if actor != "" {
				details["actor"] = actor
			}
			if len(funding) != 0 {
				details["funding_outputs"] = funding
			}
		}
		event := IndexEvent{
			Kind:           contractIndexEventKind(op.Kind),
			Height:         height,
			TxID:           view.TxID,
			Contract:       contract,
			ContractType:   op.ContractType,
			ContractTypeID: op.ContractTypeID,
			Subtype:        firstNonEmpty(op.Subtype, op.TemplateName),
			Name:           firstNonEmpty(op.TemplateName, op.Subtype),
			Version:        op.Version,
			Action:         op.Action,
			Status:         contractIndexStatus(op),
			Actor:          actorFromDetails(details),
			GasLimit:       op.GasLimit,
			Nonce:          op.Nonce,
			Details:        details,
		}
		summaries = append(summaries, contractSummaryFromIndexEvent(event))
		history = append(history, contractHistoryFromIndexEvent(event))
	}
	for _, output := range view.Outputs {
		if output.Contract == "" {
			continue
		}
		summaries = append(summaries, ContractSummary{
			Address:        output.Contract,
			ContractType:   output.ContractType,
			ContractTypeID: output.ContractTypeID,
			UpdatedHeight:  height,
		})
	}
	return summaries, history, nil
}

func contractIndexEventKind(kind string) contractcommon.IndexEventKind {
	switch kind {
	case "deploy":
		return contractcommon.IndexEventDeploy
	case "invoke":
		return contractcommon.IndexEventInvoke
	case "result":
		return contractcommon.IndexEventResult
	case "state_root":
		return contractcommon.IndexEventStateRoot
	default:
		return contractcommon.IndexEventKind(kind)
	}
}

func contractSummaryFromIndexEvent(event IndexEvent) ContractSummary {
	summary := ContractSummary{
		Address:        event.Contract,
		ContractType:   event.ContractType,
		ContractTypeID: event.ContractTypeID,
		Subtype:        event.Subtype,
		Name:           event.Name,
		Version:        event.Version,
		Status:         event.Status,
		UpdatedHeight:  event.Height,
		Details:        cloneDetails(event.Details),
	}
	if event.Kind == contractcommon.IndexEventDeploy {
		summary.CreatedHeight = event.Height
	}
	return summary
}

func contractHistoryFromIndexEvent(event IndexEvent) ContractHistoryRecord {
	return ContractHistoryRecord{
		Kind:           string(event.Kind),
		Height:         event.Height,
		TxID:           event.TxID,
		Contract:       event.Contract,
		ContractType:   event.ContractType,
		ContractTypeID: event.ContractTypeID,
		Subtype:        event.Subtype,
		Action:         event.Action,
		Status:         event.Status,
		Actor:          event.Actor,
		GasLimit:       event.GasLimit,
		Nonce:          event.Nonce,
		Details:        event.Details,
	}
}

func contractIndexDetails(tx *wire.MsgTx, prefix string, params *chaincfg.Params, contractType byte) (string, []map[string]interface{}) {
	_, txType, found, err := collectContractPayload(tx)
	if err != nil || !found || txType != contractcommon.TxTypeInvoke {
		return "", nil
	}
	actor := ""
	if params != nil {
		if resolved, err := lastInputInvokerAddress(tx, params); err == nil {
			actor = resolved
		}
	}
	txid := tx.TxID()
	funding := make([]map[string]interface{}, 0)
	for i, output := range tx.TxOut {
		if output == nil {
			continue
		}
		addr, ok, err := contractcommon.ParseContractPkScript(output.PkScript, prefix)
		if err != nil || !ok || addr.ContractType() != contractType {
			continue
		}
		vout := uint32(i)
		item := map[string]interface{}{
			"address":  addr.EncodeAddress(),
			"outpoint": fmt.Sprintf("%s:%d", txid, vout),
			"vout":     vout,
			"value":    output.Value,
		}
		if len(output.Assets) != 0 {
			item["assets"] = output.Assets
			item["asset_amounts"] = contractAssetAmounts(output.Assets)
		}
		funding = append(funding, item)
	}
	return actor, funding
}

func lastInputInvokerAddress(tx *wire.MsgTx, params *chaincfg.Params) (string, error) {
	if tx == nil || len(tx.TxIn) == 0 {
		return "", fmt.Errorf("missing contract invoker input")
	}
	input := tx.TxIn[len(tx.TxIn)-1]
	pubKey := extractInvokerPubKey(input.SignatureScript)
	if len(pubKey) == 0 {
		pubKey = extractInvokerPubKeyFromWitness(input.Witness)
	}
	if len(pubKey) == 0 {
		return "", fmt.Errorf("missing contract invoker public key")
	}
	parsedPubKey, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return "", err
	}
	tapKey := txscript.ComputeTaprootKeyNoScript(parsedPubKey)
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(tapKey), params)
	if err != nil {
		return "", err
	}
	return addr.EncodeAddress(), nil
}

func extractInvokerPubKey(script []byte) []byte {
	tokenizer := txscript.MakeScriptTokenizer(0, script)
	for tokenizer.Next() {
		data := tokenizer.Data()
		if len(data) == 33 || len(data) == 65 {
			return data
		}
	}
	return nil
}

func extractInvokerPubKeyFromWitness(witness wire.TxWitness) []byte {
	for _, data := range witness {
		if len(data) == 33 || len(data) == 65 {
			return data
		}
	}
	return nil
}

func contractAssetAmounts(assets wire.TxAssets) map[string]string {
	out := make(map[string]string, len(assets))
	for _, asset := range assets {
		out[asset.Name.String()] = asset.Amount.String()
	}
	return out
}

func contractIndexStatus(op TxOpView) string {
	if op.Status != "" {
		return op.Status
	}
	if op.ContractTypeID != contractcommon.ContractTypeAgent {
		return ""
	}
	switch op.Action {
	case contractcommon.AgentInvokeAPIReady:
		return contractcommon.AgentStatusReady
	case contractcommon.AgentInvokeAPIReject:
		return contractcommon.AgentStatusRejected
	case contractcommon.AgentInvokeAPIBet:
		return contractcommon.AgentPredictionStatusBetting
	case contractcommon.AgentInvokeAPIConfirm:
		return contractcommon.AgentPredictionStatusConfirmed
	default:
		if op.Kind == "deploy" && op.Subtype == contractcommon.SubtypePrediction {
			return contractcommon.AgentStatusPendingReady
		}
		return ""
	}
}

func enrichDetailsFromPayload(details map[string]interface{}, op TxOpView) {
	if details == nil {
		return
	}
	payload, err := hex.DecodeString(op.PayloadHex)
	if err != nil {
		return
	}
	if op.ContractTypeID == contractcommon.ContractTypeAgent && op.Kind == "deploy" {
		deploy, err := contractcommon.DecodeDeployPayload(payload)
		if err == nil && deploy.Type == contractcommon.ContractTypeAgent && deploy.SubType == contractcommon.SubtypePrediction {
			if prediction, err := contractcommon.DecodeAgentPredictionContract(deploy.ContractContent); err == nil {
				details["prediction"] = prediction
			}
		}
	}
	if op.ContractTypeID == contractcommon.ContractTypeAgent && op.Kind == "invoke" {
		invoke, err := contractcommon.DecodeInvokePayload(payload)
		if err != nil {
			return
		}
		switch invoke.Action {
		case contractcommon.AgentInvokeAPIBet:
			if bet, err := contractcommon.DecodeAgentPredictionBetParam(invoke.Param); err == nil {
				details["bet"] = bet
			}
		case contractcommon.AgentInvokeAPIConfirm:
			if confirm, err := contractcommon.DecodeAgentPredictionConfirmParam(invoke.Param); err == nil {
				details["confirm"] = confirm
			}
		case contractcommon.AgentInvokeAPIReject:
			if reject, err := contractcommon.DecodeAgentPredictionRejectParam(invoke.Param); err == nil {
				details["reject"] = reject
			}
		}
	}
}

func (q QueryService) contractHistory(address string, start, limit int) ([]ContractHistoryRecord, int, error) {
	if q.store == nil {
		return nil, 0, fmt.Errorf("contract query store is not available")
	}
	records, total := q.store.GetContractHistory(address, start, limit)
	return records, total, nil
}

func (q QueryService) contractAnalytics(summary ContractSummary) (*ContractAnalytics, error) {
	records, _, err := q.contractHistory(summary.Address, 0, 0)
	if err != nil {
		return nil, err
	}
	analytics := &ContractAnalytics{
		Address:        summary.Address,
		ContractType:   summary.ContractType,
		ContractTypeID: summary.ContractTypeID,
		Subtype:        summary.Subtype,
		Name:           summary.Name,
		Version:        summary.Version,
		Status:         summary.Status,
		UpdatedHeight:  summary.UpdatedHeight,
		Metrics:        contractRecordMetrics(records),
		Details:        cloneDetails(summary.Details),
	}
	mergeMaps(analytics.Metrics, templateMetrics(records))
	if summary.ContractTypeID == contractcommon.ContractTypeAgent && summary.Subtype == contractcommon.SubtypePrediction {
		prediction := predictionMetrics(summary, records, analytics.Details)
		mergeMaps(analytics.Metrics, prediction)
		applyPredictionAnalytics(analytics, prediction)
	}
	return analytics, nil
}

func applyPredictionAnalytics(analytics *ContractAnalytics, metrics map[string]interface{}) {
	if analytics == nil || metrics == nil {
		return
	}
	if v, ok := metrics["totalBets"].(int); ok {
		analytics.TotalBets = v
	}
	if v, ok := metrics["totalAmount"].(string); ok {
		analytics.TotalAmount = v
	}
	if v, ok := metrics["outcomeBets"].(map[string]int); ok {
		analytics.OutcomeBets = v
	}
	if v, ok := metrics["confirmations"].(int); ok {
		analytics.Confirmations = v
	}
	if v, ok := metrics["rejections"].(int); ok {
		analytics.Rejections = v
	}
	if v, ok := metrics["resultType"].(string); ok {
		analytics.ResultType = v
	}
	if v, ok := metrics["outcomeId"].(string); ok {
		analytics.OutcomeID = v
	}
}

func contractRecordMetrics(records []ContractHistoryRecord) map[string]interface{} {
	metrics := map[string]interface{}{
		"total_records": len(records),
		"kind_count":    make(map[string]int),
		"action_count":  make(map[string]int),
		"status_count":  make(map[string]int),
	}
	for _, record := range records {
		incrementMetricCount(metrics["kind_count"], record.Kind)
		incrementMetricCount(metrics["action_count"], record.Action)
		incrementMetricCount(metrics["status_count"], record.Status)
	}
	return metrics
}

func templateMetrics(records []ContractHistoryRecord) map[string]interface{} {
	totalItems := 0
	activeItems := 0
	finishedItems := 0
	statusCount := make(map[string]int)
	for _, record := range records {
		if record.Kind != "invoke" {
			continue
		}
		totalItems++
		statusKey := templateStatusCategory(record.Status)
		statusCount[statusKey]++
		if statusKey == "active" {
			activeItems++
		} else {
			finishedItems++
		}
	}
	if totalItems == 0 {
		return nil
	}
	return map[string]interface{}{
		"total_items":    totalItems,
		"active_items":   activeItems,
		"finished_items": finishedItems,
		"item_statuses":  statusCount,
	}
}

func mergeMaps(dst, src map[string]interface{}) {
	if dst == nil || len(src) == 0 {
		return
	}
	for key, value := range src {
		dst[key] = value
	}
}

func incrementMetricCount(value interface{}, key string) {
	if key == "" {
		key = "unknown"
	}
	counts, ok := value.(map[string]int)
	if !ok {
		return
	}
	counts[key]++
}

func predictionMetrics(summary ContractSummary, records []ContractHistoryRecord, details map[string]interface{}) map[string]interface{} {
	prediction, _ := predictionFromSummary(summary)
	outcomeBets := make(map[string]int)
	outcomes := make(map[string]ContractOutcomeView)
	if prediction != nil {
		for _, outcome := range prediction.Outcomes {
			outcomes[outcome.ID] = ContractOutcomeView{ID: outcome.ID, Text: outcome.Text}
		}
	}
	bets := make([]predictionBetView, 0)
	totalAmount := zeroQueryDecimal()
	confirmations := 0
	rejections := 0
	resultType := ""
	outcomeID := ""
	var lastConfirm *contractcommon.AgentPredictionConfirmParam
	var lastReject *contractcommon.AgentPredictionRejectParam
	for _, record := range records {
		switch record.Action {
		case contractcommon.AgentInvokeAPIBet:
			bet, ok := betFromRecord(record)
			if !ok {
				continue
			}
			amount := predictionBetAmount(record, prediction)
			totalAmount = scommon.DecimalAdd(totalAmount, amount)
			bets = append(bets, predictionBetView{
				Address:   record.Actor,
				OutcomeID: bet.OutcomeID,
				Amount:    amount,
			})
			outcomeBets[bet.OutcomeID]++
			outcome := outcomes[bet.OutcomeID]
			if outcome.ID == "" {
				outcome.ID = bet.OutcomeID
			}
			outcome.Count++
			outcome.Amount = scommon.DecimalAdd(decimalFromString(outcome.Amount), amount).String()
			outcomes[bet.OutcomeID] = outcome
		case contractcommon.AgentInvokeAPIConfirm:
			confirm, ok := confirmFromRecord(record)
			if !ok {
				continue
			}
			confirmations++
			resultType = confirm.ResultType
			outcomeID = confirm.OutcomeID
			cp := confirm
			lastConfirm = &cp
		case contractcommon.AgentInvokeAPIReject:
			reject, ok := rejectFromRecord(record)
			if !ok {
				continue
			}
			rejections++
			resultType = contractcommon.AgentStatusRejected
			cp := reject
			lastReject = &cp
		}
	}
	if details != nil {
		if lastConfirm != nil {
			details["last_confirm"] = lastConfirm
		}
		if lastReject != nil {
			details["last_reject"] = lastReject
		}
		if prediction != nil && lastConfirm != nil {
			fees, payouts := predictionSettlementView(*prediction, bets, *lastConfirm)
			if len(fees) != 0 {
				details["fees"] = fees
			}
			if len(payouts) != 0 {
				details["payouts"] = payouts
			}
		}
	}
	metrics := map[string]interface{}{
		"totalBets":     len(bets),
		"totalAmount":   totalAmount.String(),
		"outcomeBets":   outcomeBets,
		"outcomes":      outcomes,
		"confirmations": confirmations,
		"rejections":    rejections,
	}
	if resultType != "" {
		metrics["resultType"] = resultType
	}
	if outcomeID != "" {
		metrics["outcomeId"] = outcomeID
	}
	return metrics
}

func (q QueryService) historyByAddress(contractAddress, address string, start, limit int) ([]ContractHistoryRecord, int, error) {
	allHistory, _, err := q.contractHistory(contractAddress, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	filtered := make([]ContractHistoryRecord, 0)
	for _, record := range allHistory {
		if recordHasAddress(record, address) {
			filtered = append(filtered, record)
		}
	}
	total := len(filtered)
	return paginateContractHistory(filtered, start, limit), total, nil
}

func (q QueryService) allAddresses(contractAddress string, start, limit int) ([]string, int, error) {
	records, _, err := q.contractHistory(contractAddress, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[string]struct{})
	for _, record := range records {
		if record.Actor != "" {
			seen[record.Actor] = struct{}{}
		}
		for _, output := range fundingOutputsFromRecord(record) {
			if output.Address != "" {
				seen[output.Address] = struct{}{}
			}
		}
	}
	addresses := make([]string, 0, len(seen))
	for address := range seen {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	total := len(addresses)
	return paginateStrings(addresses, start, limit), total, nil
}

func (q QueryService) userStatus(contractAddress, address string) (*ContractUserStatus, error) {
	records, _, err := q.historyByAddress(contractAddress, address, 0, 0)
	if err != nil {
		return nil, err
	}
	status := &ContractUserStatus{
		Address:  address,
		Contract: contractAddress,
		Metrics:  contractRecordMetrics(records),
		History:  records,
		Details:  make(map[string]interface{}),
	}
	mergeMaps(status.Metrics, templateMetrics(records))
	summary, _ := q.requireContractType(contractAddress, "user status")
	if summary.ContractTypeID == contractcommon.ContractTypeAgent && summary.Subtype == contractcommon.SubtypePrediction {
		mergeMaps(status.Metrics, predictionUserMetrics(summary, records, status.Details))
	}
	return status, nil
}

func predictionUserMetrics(summary ContractSummary, records []ContractHistoryRecord, details map[string]interface{}) map[string]interface{} {
	prediction, _ := predictionFromSummary(summary)
	outcomeBets := make(map[string]int)
	totalAmount := zeroQueryDecimal()
	totalBets := 0
	confirmations := make([]ContractHistoryRecord, 0)
	rejections := make([]ContractHistoryRecord, 0)
	bets := make([]ContractHistoryRecord, 0)
	for _, record := range records {
		switch record.Action {
		case contractcommon.AgentInvokeAPIBet:
			totalBets++
			totalAmount = scommon.DecimalAdd(totalAmount, predictionBetAmount(record, prediction))
			if bet, ok := betFromRecord(record); ok {
				outcomeBets[bet.OutcomeID]++
			}
			bets = append(bets, record)
		case contractcommon.AgentInvokeAPIConfirm:
			confirmations = append(confirmations, record)
		case contractcommon.AgentInvokeAPIReject:
			rejections = append(rejections, record)
		}
	}
	if details != nil {
		details["bets"] = bets
		details["confirmations"] = confirmations
		details["rejections"] = rejections
	}
	return map[string]interface{}{
		"totalBets":     totalBets,
		"totalAmount":   totalAmount.String(),
		"outcomeBets":   outcomeBets,
		"confirmations": len(confirmations),
		"rejections":    len(rejections),
	}
}

func (q QueryService) invokeItemByInUtxo(contractAddress, inUtxo string) (*ContractHistoryRecord, error) {
	records, _, err := q.contractHistory(contractAddress, 0, 0)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		for _, output := range fundingOutputsFromRecord(record) {
			if output.OutPoint == inUtxo {
				cloned := record
				return &cloned, nil
			}
		}
	}
	return nil, fmt.Errorf("invoke item with input utxo %s not found", inUtxo)
}

func recordHasAddress(record ContractHistoryRecord, address string) bool {
	if record.Actor == address {
		return true
	}
	for _, output := range fundingOutputsFromRecord(record) {
		if output.Address == address {
			return true
		}
	}
	return false
}

func templateStatusCategory(status string) string {
	switch status {
	case "success":
		return "finished"
	case "revert", "out_of_gas", "invalid":
		return "finished"
	default:
		return "active"
	}
}

func predictionFromSummary(summary ContractSummary) (*contractcommon.AgentPredictionContract, bool) {
	if summary.Details == nil {
		return nil, false
	}
	var prediction contractcommon.AgentPredictionContract
	if !decodeDetail(summary.Details["prediction"], &prediction) {
		return nil, false
	}
	return &prediction, true
}

func betFromRecord(record ContractHistoryRecord) (contractcommon.AgentPredictionBetParam, bool) {
	var bet contractcommon.AgentPredictionBetParam
	if record.Details == nil || !decodeDetail(record.Details["bet"], &bet) {
		return bet, false
	}
	return bet, true
}

func confirmFromRecord(record ContractHistoryRecord) (contractcommon.AgentPredictionConfirmParam, bool) {
	var confirm contractcommon.AgentPredictionConfirmParam
	if record.Details == nil || !decodeDetail(record.Details["confirm"], &confirm) {
		return confirm, false
	}
	return confirm, true
}

func rejectFromRecord(record ContractHistoryRecord) (contractcommon.AgentPredictionRejectParam, bool) {
	var reject contractcommon.AgentPredictionRejectParam
	if record.Details == nil || !decodeDetail(record.Details["reject"], &reject) {
		return reject, false
	}
	return reject, true
}

type predictionBetView struct {
	Address   string
	OutcomeID string
	Amount    *scommon.Decimal
}

func predictionBetAmount(record ContractHistoryRecord, prediction *contractcommon.AgentPredictionContract) *scommon.Decimal {
	if prediction == nil {
		return zeroQueryDecimal()
	}
	if prediction.BetAsset == contractcommon.SatoshiAssetName {
		if value, ok := record.Details["contract_value"].(float64); ok {
			return scommon.NewDefaultDecimal(int64(value))
		}
		if value, ok := record.Details["contract_value"].(int64); ok {
			return scommon.NewDefaultDecimal(value)
		}
		return zeroQueryDecimal()
	}
	total := zeroQueryDecimal()
	for _, output := range fundingOutputsFromRecord(record) {
		if amount := output.AssetAmounts[prediction.BetAsset]; amount != "" {
			total = scommon.DecimalAdd(total, decimalFromString(amount))
		}
	}
	return total
}

type fundingOutputView struct {
	OutPoint     string            `json:"outpoint"`
	Address      string            `json:"address,omitempty"`
	Value        int64             `json:"value,omitempty"`
	AssetAmounts map[string]string `json:"asset_amounts"`
}

func fundingOutputsFromRecord(record ContractHistoryRecord) []fundingOutputView {
	if record.Details == nil {
		return nil
	}
	var outputs []fundingOutputView
	if !decodeDetail(record.Details["funding_outputs"], &outputs) {
		return nil
	}
	return outputs
}

func predictionSettlementView(contract contractcommon.AgentPredictionContract, bets []predictionBetView,
	confirm contractcommon.AgentPredictionConfirmParam) ([]ContractTransferView, []ContractTransferView) {

	total := zeroQueryDecimal()
	winnerTotal := zeroQueryDecimal()
	winners := make([]predictionBetView, 0)
	for _, bet := range bets {
		total = scommon.DecimalAdd(total, bet.Amount)
		if confirm.ResultType == contractcommon.ResultTypeOutcome && bet.OutcomeID == confirm.OutcomeID {
			winnerTotal = scommon.DecimalAdd(winnerTotal, bet.Amount)
			winners = append(winners, bet)
		}
	}
	if total.Sign() == 0 {
		return nil, nil
	}
	if confirm.ResultType != contractcommon.ResultTypeOutcome || len(winners) == 0 {
		refunds := make([]ContractTransferView, 0, len(bets))
		for _, bet := range bets {
			refunds = append(refunds, ContractTransferView{
				Address:   bet.Address,
				AssetName: contract.BetAsset,
				Amount:    bet.Amount.String(),
				Reason:    "refund",
			})
		}
		return nil, refunds
	}
	deployerFee := decimalMulBPSQuery(total, contractcommon.PredictionDeployerFeeBPS)
	agentFee := decimalMulBPSQuery(total, contractcommon.PredictionAgentFeeBPS)
	bootstrapFee := decimalMulBPSQuery(total, contractcommon.PredictionBootstrapBPS)
	winnerPool := scommon.DecimalSub(total, deployerFee)
	winnerPool = scommon.DecimalSub(winnerPool, agentFee)
	winnerPool = scommon.DecimalSub(winnerPool, bootstrapFee)

	fees := []ContractTransferView{
		{Address: "deployer", AssetName: contract.BetAsset, Amount: deployerFee.String(), Reason: "deployer_fee"},
		{Address: "agent", AssetName: contract.BetAsset, Amount: agentFee.String(), Reason: "agent_fee"},
		{Address: "bootstrap", AssetName: contract.BetAsset, Amount: bootstrapFee.String(), Reason: "bootstrap_fee"},
	}
	payouts := make([]ContractTransferView, 0, len(winners))
	for _, winner := range winners {
		shareValue := new(big.Int).Mul(winnerPool.Value, winner.Amount.Value)
		shareValue.Div(shareValue, winnerTotal.Value)
		share := &scommon.Decimal{Precision: winnerPool.Precision, Value: shareValue}
		payouts = append(payouts, ContractTransferView{
			Address:   winner.Address,
			AssetName: contract.BetAsset,
			Amount:    share.String(),
			Reason:    "winner",
		})
	}
	return fees, payouts
}

func zeroQueryDecimal() *scommon.Decimal {
	return scommon.NewDefaultDecimal(0)
}

func decimalFromString(value string) *scommon.Decimal {
	if strings.TrimSpace(value) == "" {
		return zeroQueryDecimal()
	}
	amount, err := scommon.NewDecimalFromString(value, contractcommon.MaxPredictionDecimalPrecision)
	if err != nil {
		return zeroQueryDecimal()
	}
	return amount
}

func decimalMulBPSQuery(value *scommon.Decimal, bps int) *scommon.Decimal {
	return value.MulBigInt(big.NewInt(int64(bps))).DivBigInt(big.NewInt(contractcommon.PredictionTotalBPS))
}

func cloneDetails(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return make(map[string]interface{})
	}
	out := make(map[string]interface{}, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value interface{}) interface{} {
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

func decodeDetail(value interface{}, out interface{}) bool {
	if value == nil {
		return false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return json.Unmarshal(encoded, out) == nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func actorFromDetails(details map[string]interface{}) string {
	if details == nil {
		return ""
	}
	actor, _ := details["actor"].(string)
	return actor
}

func paginateStrings(values []string, start, limit int) []string {
	total := len(values)
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
	return values[start:end]
}

func paginateContractHistory(history []ContractHistoryRecord, start, limit int) []ContractHistoryRecord {
	total := len(history)
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
	return history[start:end]
}
