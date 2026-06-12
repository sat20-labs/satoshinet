package contract

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	scommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg"
	agentcontract "github.com/sat20-labs/satoshinet/contract/agent"
	contractcommon "github.com/sat20-labs/satoshinet/contract/common"
	tmplcontract "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/wire"
)

type ContractSummary = contractcommon.ContractSummary
type ContractHistoryRecord = contractcommon.ContractHistoryRecord

type ContractQueryStore interface {
	GetContractSummaries(start, limit int) ([]ContractSummary, int)
	GetContractSummary(address string) (ContractSummary, bool)
	GetContractHistory(address string, start, limit int) ([]ContractHistoryRecord, int)
}

type TemplateContractQueryStore interface {
	GetTemplateContract(address string) (*tmplcontract.ContractInfo, bool)
	GetTemplateContractHistory(address string, start, limit int) ([]tmplcontract.HistoryRecord, int)
}

type QueryService struct {
	store ContractQueryStore
}

func NewQueryService(store ContractQueryStore) QueryService {
	return QueryService{store: store}
}

type TemplateContractAnalytics struct {
	Address        string                  `json:"address"`
	TemplateName   string                  `json:"templateName"`
	Version        uint32                  `json:"version"`
	UpdatedHeight  int64                   `json:"updatedHeight"`
	Running        TemplateRunningDataJSON `json:"running"`
	TotalItems     int                     `json:"totalItems"`
	ActiveItems    int                     `json:"activeItems"`
	FinishedItems  int                     `json:"finishedItems"`
	StatusCount    map[int]int             `json:"statusCount"`
	OrderTypeCount map[int]int             `json:"orderTypeCount"`
}

type TemplateRunningDataJSON struct {
	AssetAInPool      string            `json:"assetAInPool,omitempty"`
	AssetBInPool      string            `json:"assetBInPool,omitempty"`
	RequiredAssetA    string            `json:"requiredAssetA,omitempty"`
	RequiredAssetB    string            `json:"requiredAssetB,omitempty"`
	K                 string            `json:"k,omitempty"`
	TradingReady      bool              `json:"tradingReady,omitempty"`
	GasBalance        string            `json:"gasBalance,omitempty"`
	TotalInputAssetA  string            `json:"totalInputAssetA,omitempty"`
	TotalInputAssetB  string            `json:"totalInputAssetB,omitempty"`
	TotalDealAssetA   string            `json:"totalDealAssetA,omitempty"`
	TotalDealAssetB   string            `json:"totalDealAssetB,omitempty"`
	TotalDealCount    int               `json:"totalDealCount"`
	TotalRefundAssetB string            `json:"totalRefundAssetB,omitempty"`
	TotalLPTAmt       string            `json:"totalLptAmt,omitempty"`
	LPBalances        map[string]string `json:"lpBalances,omitempty"`
	LPCosts           map[string]int64  `json:"lpCosts,omitempty"`
	Closed            bool              `json:"closed,omitempty"`
}

func templateRunningDataJSON(r tmplcontract.RunningData) TemplateRunningDataJSON {
	return TemplateRunningDataJSON{
		AssetAInPool:      decimalJSON(r.AssetAInPool),
		AssetBInPool:      decimalJSON(r.AssetBInPool),
		RequiredAssetA:    decimalJSON(r.RequiredAssetA),
		RequiredAssetB:    decimalJSON(r.RequiredAssetB),
		K:                 decimalJSON(r.K),
		TradingReady:      r.TradingReady,
		GasBalance:        decimalJSON(r.GasBalance),
		TotalInputAssetA:  decimalJSON(r.TotalInputAssetA),
		TotalInputAssetB:  decimalJSON(r.TotalInputAssetB),
		TotalDealAssetA:   decimalJSON(r.TotalDealAssetA),
		TotalDealAssetB:   decimalJSON(r.TotalDealAssetB),
		TotalDealCount:    r.TotalDealCount,
		TotalRefundAssetB: decimalJSON(r.TotalRefundAssetB),
		TotalLPTAmt:       decimalJSON(r.TotalLPTAmt),
		LPBalances:        decimalMapJSON(r.LPBalances),
		LPCosts:           cloneInt64Map(r.LPCosts),
		Closed:            r.Closed,
	}
}

func decimalJSON(d *scommon.Decimal) string {
	if d == nil || d.Sign() == 0 {
		return ""
	}
	return d.String()
}

func decimalMapJSON(in map[string]*scommon.Decimal) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		if text := decimalJSON(value); text != "" {
			out[key] = text
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneInt64Map(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

type TemplateContractUserStatus struct {
	Address       string                    `json:"address"`
	Contract      string                    `json:"contract"`
	TotalItems    int                       `json:"totalItems"`
	ActiveItems   int                       `json:"activeItems"`
	FinishedItems int                       `json:"finishedItems"`
	Items         []tmplcontract.InvokeItem `json:"items"`
}

type AgentPredictionAnalytics struct {
	Address       string                                `json:"address"`
	Title         string                                `json:"title,omitempty"`
	Status        string                                `json:"status,omitempty"`
	BetAsset      string                                `json:"betAsset,omitempty"`
	MinBetUnit    string                                `json:"minBetUnit,omitempty"`
	TotalBets     int                                   `json:"totalBets"`
	TotalAmount   string                                `json:"totalAmount,omitempty"`
	OutcomeBets   map[string]int                        `json:"outcomeBets"`
	Outcomes      map[string]AgentPredictionOutcomeView `json:"outcomes,omitempty"`
	Confirmations int                                   `json:"confirmations"`
	ResultType    string                                `json:"resultType,omitempty"`
	OutcomeID     string                                `json:"outcomeId,omitempty"`
	LastConfirm   *agentcontract.PredictionConfirmParam `json:"lastConfirm,omitempty"`
	Rejections    int                                   `json:"rejections"`
	LastReject    *agentcontract.PredictionRejectParam  `json:"lastReject,omitempty"`
	Fees          []AgentPredictionTransferView         `json:"fees,omitempty"`
	Payouts       []AgentPredictionTransferView         `json:"payouts,omitempty"`
	Contract      *agentcontract.PredictionContract     `json:"contract,omitempty"`
	UpdatedHeight int64                                 `json:"updatedHeight,omitempty"`
}

type AgentPredictionOutcomeView struct {
	ID     string `json:"id"`
	Text   string `json:"text,omitempty"`
	Bets   int    `json:"bets"`
	Amount string `json:"amount,omitempty"`
}

type AgentPredictionTransferView struct {
	Address   string `json:"address"`
	AssetName string `json:"assetName,omitempty"`
	Amount    string `json:"amount,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type AgentPredictionUserStatus struct {
	Address       string                  `json:"address"`
	Contract      string                  `json:"contract"`
	TotalBets     int                     `json:"totalBets"`
	TotalAmount   string                  `json:"totalAmount,omitempty"`
	OutcomeBets   map[string]int          `json:"outcomeBets"`
	Bets          []ContractHistoryRecord `json:"bets,omitempty"`
	Confirmations []ContractHistoryRecord `json:"confirmations,omitempty"`
	Rejections    []ContractHistoryRecord `json:"rejections,omitempty"`
}

func (q QueryService) SupportedContracts() []string {
	return []string{"evm", "agent:prediction", tmplcontract.TemplateLimitOrder, tmplcontract.TemplateAMM, tmplcontract.TemplateExchange}
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

func (q QueryService) Analytics(contractAddress string) (any, error) {
	summary, err := q.requireContractType(contractAddress, "analytics")
	if err != nil {
		return nil, err
	}
	switch summary.ContractTypeID {
	case contractcommon.ContractTypeTemplate:
		return q.templateAnalytics(contractAddress)
	case contractcommon.ContractTypeAgent:
		return q.agentPredictionAnalytics(contractAddress)
	default:
		return nil, fmt.Errorf("analytics query is not supported for %s contracts", summary.ContractType)
	}
}

func (q QueryService) InvokeItemByInUtxo(contractAddress, inUtxo string) (any, error) {
	summary, err := q.requireContractType(contractAddress, "input utxo item")
	if err != nil {
		return nil, err
	}
	if summary.ContractTypeID != contractcommon.ContractTypeTemplate {
		return nil, fmt.Errorf("input utxo item query is not supported for %s contracts", summary.ContractType)
	}
	return q.templateInvokeItemByInUtxo(contractAddress, inUtxo)
}

func (q QueryService) AllAddresses(contractAddress string, start, limit int) (any, int, error) {
	summary, err := q.requireContractType(contractAddress, "users")
	if err != nil {
		return nil, 0, err
	}
	switch summary.ContractTypeID {
	case contractcommon.ContractTypeTemplate:
		return q.templateAllAddresses(contractAddress, start, limit)
	case contractcommon.ContractTypeAgent:
		return q.agentPredictionUsers(contractAddress, start, limit)
	default:
		return nil, 0, fmt.Errorf("users query is not supported for %s contracts", summary.ContractType)
	}
}

func (q QueryService) UserStatus(contractAddress, address string) (any, error) {
	summary, err := q.requireContractType(contractAddress, "user status")
	if err != nil {
		return nil, err
	}
	switch summary.ContractTypeID {
	case contractcommon.ContractTypeTemplate:
		return q.templateUserStatus(contractAddress, address)
	case contractcommon.ContractTypeAgent:
		return q.agentPredictionUserStatus(contractAddress, address)
	default:
		return nil, fmt.Errorf("user status query is not supported for %s contracts", summary.ContractType)
	}
}

func (q QueryService) HistoryByAddress(contractAddress, address string, start, limit int) (any, int, error) {
	summary, err := q.requireContractType(contractAddress, "user history")
	if err != nil {
		return nil, 0, err
	}
	switch summary.ContractTypeID {
	case contractcommon.ContractTypeTemplate:
		return q.templateHistoryByAddress(contractAddress, address, start, limit)
	case contractcommon.ContractTypeAgent:
		return q.agentPredictionUserHistory(contractAddress, address, start, limit)
	default:
		return nil, 0, fmt.Errorf("user history query is not supported for %s contracts", summary.ContractType)
	}
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
	agentActor, agentFunding := agentIndexDetails(tx, prefix, params)
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
		if op.ContractTypeID == contractcommon.ContractTypeTemplate {
			continue
		}
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
		if op.ContractTypeID == contractcommon.ContractTypeAgent {
			if agentActor != "" {
				details["actor"] = agentActor
			}
			if len(agentFunding) != 0 {
				details["funding_outputs"] = agentFunding
			}
		}
		summary := ContractSummary{
			Address:        contract,
			ContractType:   op.ContractType,
			ContractTypeID: op.ContractTypeID,
			Subtype:        firstNonEmpty(op.Subtype, op.TemplateName),
			Name:           firstNonEmpty(op.TemplateName, op.Subtype),
			Version:        op.Version,
			Status:         contractIndexStatus(op),
			UpdatedHeight:  height,
			Details:        cloneDetails(details),
		}
		if op.Kind == "deploy" {
			summary.CreatedHeight = height
		}
		summaries = append(summaries, summary)
		history = append(history, ContractHistoryRecord{
			Kind:           op.Kind,
			Height:         height,
			TxID:           view.TxID,
			Contract:       contract,
			ContractType:   op.ContractType,
			ContractTypeID: op.ContractTypeID,
			Subtype:        firstNonEmpty(op.Subtype, op.TemplateName),
			Action:         op.Action,
			Status:         contractIndexStatus(op),
			Actor:          actorFromDetails(details),
			GasLimit:       op.GasLimit,
			Nonce:          op.Nonce,
			Details:        details,
		})
	}
	for _, output := range view.Outputs {
		if output.Contract == "" {
			continue
		}
		if output.ContractTypeID == contractcommon.ContractTypeTemplate {
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

func agentIndexDetails(tx *wire.MsgTx, prefix string, params *chaincfg.Params) (string, []map[string]interface{}) {
	parsed, err := agentcontract.ParseTx(tx, agentcontract.StandardContractScriptResolver(prefix))
	if err != nil || parsed.Type != agentcontract.TxTypeInvoke {
		return "", nil
	}
	actor := ""
	if params != nil {
		if resolved, err := agentcontract.LastInputInvokerResolver(params)(tx, parsed); err == nil {
			actor = resolved
		}
	}
	funding := make([]map[string]interface{}, 0, len(parsed.ContractOutputs))
	for _, output := range parsed.ContractOutputs {
		item := map[string]interface{}{
			"outpoint": output.OutPoint.String(),
			"vout":     output.Vout,
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
	case agentcontract.InvokeAPIReady:
		return agentcontract.StatusReady
	case agentcontract.InvokeAPIReject:
		return agentcontract.StatusRejected
	case agentcontract.InvokeAPIBet:
		return agentcontract.PredictionStatusBetting
	case agentcontract.InvokeAPIConfirm:
		return agentcontract.PredictionStatusConfirmed
	default:
		if op.Kind == "deploy" && op.Subtype == agentcontract.SubtypePrediction {
			return agentcontract.StatusPendingReady
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
		deploy, err := agentcontract.DecodeDeployPayload(payload)
		if err == nil && deploy.Subtype == agentcontract.SubtypePrediction {
			if prediction, err := agentcontract.DecodePredictionContract(deploy.ContractContent); err == nil {
				details["prediction"] = prediction
			}
		}
	}
	if op.ContractTypeID == contractcommon.ContractTypeAgent && op.Kind == "invoke" {
		invoke, err := agentcontract.DecodeInvokePayload(payload)
		if err != nil {
			return
		}
		switch invoke.Action {
		case agentcontract.InvokeAPIBet:
			if bet, err := agentcontract.DecodePredictionBetParam(invoke.Param); err == nil {
				details["bet"] = bet
			}
		case agentcontract.InvokeAPIConfirm:
			if confirm, err := agentcontract.DecodePredictionConfirmParam(invoke.Param); err == nil {
				details["confirm"] = confirm
			}
		case agentcontract.InvokeAPIReject:
			if reject, err := agentcontract.DecodePredictionRejectParam(invoke.Param); err == nil {
				details["reject"] = reject
			}
		}
	}
}

func (q QueryService) templateContract(address string) (*tmplcontract.ContractInfo, error) {
	if q.store == nil {
		return nil, fmt.Errorf("template contract store is not available")
	}
	if store, ok := q.store.(TemplateContractQueryStore); ok {
		if contract, found := store.GetTemplateContract(address); found && contract != nil {
			return contract, nil
		}
	}
	summary, ok := q.store.GetContractSummary(address)
	if !ok || summary.Details == nil {
		return nil, fmt.Errorf("template contract %s not found", address)
	}
	var contract tmplcontract.ContractInfo
	if !decodeDetail(summary.Details["template"], &contract) {
		return nil, fmt.Errorf("template contract %s not found", address)
	}
	return &contract, nil
}

func (q QueryService) templateHistory(address string, start, limit int) ([]tmplcontract.HistoryRecord, int, error) {
	if q.store == nil {
		return nil, 0, fmt.Errorf("contract query store is not available")
	}
	if _, err := q.templateContract(address); err != nil {
		return nil, 0, err
	}
	if store, ok := q.store.(TemplateContractQueryStore); ok {
		records, total := store.GetTemplateContractHistory(address, start, limit)
		return records, total, nil
	}
	records, total := q.store.GetContractHistory(address, start, limit)
	out := make([]tmplcontract.HistoryRecord, 0, len(records))
	for _, record := range records {
		if record.Details == nil {
			continue
		}
		var templateRecord tmplcontract.HistoryRecord
		if decodeDetail(record.Details["templateHistory"], &templateRecord) {
			out = append(out, templateRecord)
		}
	}
	return out, total, nil
}

func (q QueryService) agentPredictionAnalytics(contractAddress string) (*AgentPredictionAnalytics, error) {
	summary, err := q.requireContractType(contractAddress, "agent prediction analytics")
	if err != nil {
		return nil, err
	}
	prediction, _ := predictionFromSummary(summary)
	analytics := &AgentPredictionAnalytics{
		Address:       summary.Address,
		Status:        summary.Status,
		OutcomeBets:   make(map[string]int),
		Outcomes:      make(map[string]AgentPredictionOutcomeView),
		UpdatedHeight: summary.UpdatedHeight,
	}
	if prediction != nil {
		analytics.Title = prediction.Title
		analytics.BetAsset = prediction.BetAsset
		analytics.MinBetUnit = prediction.MinBetUnit
		analytics.Contract = prediction
		for _, outcome := range prediction.Outcomes {
			analytics.Outcomes[outcome.ID] = AgentPredictionOutcomeView{
				ID:   outcome.ID,
				Text: outcome.Text,
			}
		}
	}
	records, _, err := q.agentPredictionHistory(contractAddress, 0, 0)
	if err != nil {
		return nil, err
	}
	bets := make([]agentPredictionBetView, 0)
	totalAmount := zeroQueryDecimal()
	for _, record := range records {
		switch record.Action {
		case agentcontract.InvokeAPIBet:
			bet, ok := betFromRecord(record)
			if !ok {
				continue
			}
			amount := predictionBetAmount(record, prediction)
			totalAmount = scommon.DecimalAdd(totalAmount, amount)
			bets = append(bets, agentPredictionBetView{
				Address:   record.Actor,
				OutcomeID: bet.OutcomeID,
				Amount:    amount,
			})
			analytics.TotalBets++
			analytics.OutcomeBets[bet.OutcomeID]++
			outcome := analytics.Outcomes[bet.OutcomeID]
			if outcome.ID == "" {
				outcome.ID = bet.OutcomeID
			}
			outcome.Bets++
			outcome.Amount = scommon.DecimalAdd(decimalFromString(outcome.Amount), amount).String()
			analytics.Outcomes[bet.OutcomeID] = outcome
		case agentcontract.InvokeAPIConfirm:
			confirm, ok := confirmFromRecord(record)
			if !ok {
				continue
			}
			analytics.Confirmations++
			analytics.ResultType = confirm.ResultType
			analytics.OutcomeID = confirm.OutcomeID
			cp := confirm
			analytics.LastConfirm = &cp
		case agentcontract.InvokeAPIReject:
			analytics.ResultType = agentcontract.StatusRejected
			reject, ok := rejectFromRecord(record)
			if !ok {
				continue
			}
			analytics.Rejections++
			cp := reject
			analytics.LastReject = &cp
		}
	}
	analytics.TotalAmount = totalAmount.String()
	if prediction != nil && analytics.LastConfirm != nil {
		analytics.Fees, analytics.Payouts = agentPredictionSettlementView(*prediction, bets, *analytics.LastConfirm)
	}
	return analytics, nil
}

func (q QueryService) agentPredictionUsers(contractAddress string, start, limit int) ([]string, int, error) {
	records, _, err := q.agentPredictionHistory(contractAddress, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[string]struct{})
	for _, record := range records {
		if record.Actor == "" {
			continue
		}
		if record.Action == agentcontract.InvokeAPIBet || record.Action == agentcontract.InvokeAPIConfirm ||
			record.Action == agentcontract.InvokeAPIReject {
			seen[record.Actor] = struct{}{}
		}
	}
	users := make([]string, 0, len(seen))
	for user := range seen {
		users = append(users, user)
	}
	sort.Strings(users)
	total := len(users)
	return paginateStrings(users, start, limit), total, nil
}

func (q QueryService) agentPredictionUserStatus(contractAddress, address string) (*AgentPredictionUserStatus, error) {
	records, _, err := q.agentPredictionHistory(contractAddress, 0, 0)
	if err != nil {
		return nil, err
	}
	status := &AgentPredictionUserStatus{
		Address:     address,
		Contract:    contractAddress,
		OutcomeBets: make(map[string]int),
	}
	summary, _ := q.requireContractType(contractAddress, "agent prediction user status")
	prediction, _ := predictionFromSummary(summary)
	totalAmount := zeroQueryDecimal()
	for _, record := range records {
		if record.Actor != address {
			continue
		}
		switch record.Action {
		case agentcontract.InvokeAPIBet:
			status.TotalBets++
			totalAmount = scommon.DecimalAdd(totalAmount, predictionBetAmount(record, prediction))
			if bet, ok := betFromRecord(record); ok {
				status.OutcomeBets[bet.OutcomeID]++
			}
			status.Bets = append(status.Bets, record)
		case agentcontract.InvokeAPIConfirm:
			status.Confirmations = append(status.Confirmations, record)
		case agentcontract.InvokeAPIReject:
			status.Rejections = append(status.Rejections, record)
		}
	}
	status.TotalAmount = totalAmount.String()
	return status, nil
}

func (q QueryService) agentPredictionUserHistory(contractAddress, address string, start, limit int) ([]ContractHistoryRecord, int, error) {
	records, _, err := q.agentPredictionHistory(contractAddress, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	filtered := make([]ContractHistoryRecord, 0)
	for _, record := range records {
		if record.Actor == address {
			filtered = append(filtered, record)
		}
	}
	total := len(filtered)
	return paginateContractHistory(filtered, start, limit), total, nil
}

func (q QueryService) agentPredictionHistory(contractAddress string, start, limit int) ([]ContractHistoryRecord, int, error) {
	if q.store == nil {
		return nil, 0, fmt.Errorf("contract query store is not available")
	}
	summary, err := q.requireContractType(contractAddress, "agent prediction history")
	if err != nil {
		return nil, 0, err
	}
	if summary.ContractTypeID != contractcommon.ContractTypeAgent || summary.Subtype != agentcontract.SubtypePrediction {
		return nil, 0, fmt.Errorf("agent prediction query is not supported for %s/%s", summary.ContractType, summary.Subtype)
	}
	records, total := q.store.GetContractHistory(contractAddress, start, limit)
	return records, total, nil
}

func (q QueryService) templateHistoryByAddress(contractAddress, address string, start, limit int) ([]tmplcontract.HistoryRecord, int, error) {
	contract, err := q.templateContract(contractAddress)
	if err != nil {
		return nil, 0, err
	}
	itemAddress := make(map[int64]string)
	for _, item := range contract.RuntimeState.Items {
		itemAddress[item.ID] = item.Address
	}
	allHistory, _, err := q.templateHistory(contractAddress, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	filtered := make([]tmplcontract.HistoryRecord, 0)
	for _, record := range allHistory {
		if templateRecordHasAddress(record, itemAddress, address) {
			filtered = append(filtered, record)
		}
	}
	total := len(filtered)
	return paginateTemplateHistory(filtered, start, limit), total, nil
}

func (q QueryService) templateAllAddresses(contractAddress string, start, limit int) ([]string, int, error) {
	contract, err := q.templateContract(contractAddress)
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[string]struct{})
	for _, item := range contract.RuntimeState.Items {
		if item.Address == "" {
			continue
		}
		seen[item.Address] = struct{}{}
	}
	addresses := make([]string, 0, len(seen))
	for address := range seen {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	total := len(addresses)
	if start < 0 {
		start = 0
	}
	if limit <= 0 {
		limit = total
	}
	if start >= total {
		return nil, total, nil
	}
	end := start + limit
	if end > total {
		end = total
	}
	return addresses[start:end], total, nil
}

func (q QueryService) templateAnalytics(contractAddress string) (*TemplateContractAnalytics, error) {
	contract, err := q.templateContract(contractAddress)
	if err != nil {
		return nil, err
	}
	analytics := &TemplateContractAnalytics{
		Address:        contract.Address,
		TemplateName:   contract.TemplateName,
		Version:        contract.Version,
		UpdatedHeight:  contract.UpdatedHeight,
		Running:        templateRunningDataJSON(contract.RuntimeState.Running),
		StatusCount:    make(map[int]int),
		OrderTypeCount: make(map[int]int),
	}
	for _, item := range contract.RuntimeState.Items {
		analytics.TotalItems++
		analytics.StatusCount[item.Done]++
		analytics.OrderTypeCount[item.OrderType]++
		if item.Finished() {
			analytics.FinishedItems++
		} else {
			analytics.ActiveItems++
		}
	}
	return analytics, nil
}

func (q QueryService) templateUserStatus(contractAddress, address string) (*TemplateContractUserStatus, error) {
	contract, err := q.templateContract(contractAddress)
	if err != nil {
		return nil, err
	}
	status := &TemplateContractUserStatus{
		Address:  address,
		Contract: contractAddress,
		Items:    make([]tmplcontract.InvokeItem, 0),
	}
	for _, item := range contract.RuntimeState.Items {
		if item.Address != address {
			continue
		}
		status.TotalItems++
		status.Items = append(status.Items, item)
		if item.Finished() {
			status.FinishedItems++
		} else {
			status.ActiveItems++
		}
	}
	return status, nil
}

func (q QueryService) templateInvokeItemByInUtxo(contractAddress, inUtxo string) (*tmplcontract.InvokeItem, error) {
	contract, err := q.templateContract(contractAddress)
	if err != nil {
		return nil, err
	}
	for _, item := range contract.RuntimeState.Items {
		for _, raw := range strings.Split(item.InUtxos, ",") {
			if strings.TrimSpace(raw) == inUtxo {
				cloned := item
				return &cloned, nil
			}
		}
	}
	return nil, fmt.Errorf("invoke item with input utxo %s not found", inUtxo)
}

func templateRecordHasAddress(record tmplcontract.HistoryRecord, itemAddress map[int64]string, address string) bool {
	for _, itemID := range record.ItemIDs {
		if itemAddress[itemID] == address {
			return true
		}
	}
	if record.Settlement != nil {
		for _, transfer := range record.Settlement.Transfers {
			if transfer.To == address {
				return true
			}
		}
	}
	if record.Result != nil {
		for _, output := range record.Result.Outputs {
			if output.To == address {
				return true
			}
		}
	}
	return false
}

func paginateTemplateHistory(history []tmplcontract.HistoryRecord, start, limit int) []tmplcontract.HistoryRecord {
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

func predictionFromSummary(summary ContractSummary) (*agentcontract.PredictionContract, bool) {
	if summary.Details == nil {
		return nil, false
	}
	var prediction agentcontract.PredictionContract
	if !decodeDetail(summary.Details["prediction"], &prediction) {
		return nil, false
	}
	return &prediction, true
}

func betFromRecord(record ContractHistoryRecord) (agentcontract.PredictionBetParam, bool) {
	var bet agentcontract.PredictionBetParam
	if record.Details == nil || !decodeDetail(record.Details["bet"], &bet) {
		return bet, false
	}
	return bet, true
}

func confirmFromRecord(record ContractHistoryRecord) (agentcontract.PredictionConfirmParam, bool) {
	var confirm agentcontract.PredictionConfirmParam
	if record.Details == nil || !decodeDetail(record.Details["confirm"], &confirm) {
		return confirm, false
	}
	return confirm, true
}

func rejectFromRecord(record ContractHistoryRecord) (agentcontract.PredictionRejectParam, bool) {
	var reject agentcontract.PredictionRejectParam
	if record.Details == nil || !decodeDetail(record.Details["reject"], &reject) {
		return reject, false
	}
	return reject, true
}

type agentPredictionBetView struct {
	Address   string
	OutcomeID string
	Amount    *scommon.Decimal
}

func predictionBetAmount(record ContractHistoryRecord, prediction *agentcontract.PredictionContract) *scommon.Decimal {
	if prediction == nil {
		return zeroQueryDecimal()
	}
	if prediction.BetAsset == agentcontract.SatoshiAssetName {
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

type agentFundingOutputView struct {
	AssetAmounts map[string]string `json:"asset_amounts"`
}

func fundingOutputsFromRecord(record ContractHistoryRecord) []agentFundingOutputView {
	if record.Details == nil {
		return nil
	}
	var outputs []agentFundingOutputView
	if !decodeDetail(record.Details["funding_outputs"], &outputs) {
		return nil
	}
	return outputs
}

func agentPredictionSettlementView(contract agentcontract.PredictionContract, bets []agentPredictionBetView,
	confirm agentcontract.PredictionConfirmParam) ([]AgentPredictionTransferView, []AgentPredictionTransferView) {

	total := zeroQueryDecimal()
	winnerTotal := zeroQueryDecimal()
	winners := make([]agentPredictionBetView, 0)
	for _, bet := range bets {
		total = scommon.DecimalAdd(total, bet.Amount)
		if confirm.ResultType == agentcontract.ResultTypeOutcome && bet.OutcomeID == confirm.OutcomeID {
			winnerTotal = scommon.DecimalAdd(winnerTotal, bet.Amount)
			winners = append(winners, bet)
		}
	}
	if total.Sign() == 0 {
		return nil, nil
	}
	if confirm.ResultType != agentcontract.ResultTypeOutcome || len(winners) == 0 {
		refunds := make([]AgentPredictionTransferView, 0, len(bets))
		for _, bet := range bets {
			refunds = append(refunds, AgentPredictionTransferView{
				Address:   bet.Address,
				AssetName: contract.BetAsset,
				Amount:    bet.Amount.String(),
				Reason:    "refund",
			})
		}
		return nil, refunds
	}
	deployerFee := decimalMulBPSQuery(total, agentcontract.PredictionDeployerFeeBPS)
	agentFee := decimalMulBPSQuery(total, agentcontract.PredictionAgentFeeBPS)
	bootstrapFee := decimalMulBPSQuery(total, agentcontract.PredictionBootstrapBPS)
	winnerPool := scommon.DecimalSub(total, deployerFee)
	winnerPool = scommon.DecimalSub(winnerPool, agentFee)
	winnerPool = scommon.DecimalSub(winnerPool, bootstrapFee)

	fees := []AgentPredictionTransferView{
		{Address: "deployer", AssetName: contract.BetAsset, Amount: deployerFee.String(), Reason: "deployer_fee"},
		{Address: "agent", AssetName: contract.BetAsset, Amount: agentFee.String(), Reason: "agent_fee"},
		{Address: "bootstrap", AssetName: contract.BetAsset, Amount: bootstrapFee.String(), Reason: "bootstrap_fee"},
	}
	payouts := make([]AgentPredictionTransferView, 0, len(winners))
	for _, winner := range winners {
		shareValue := new(big.Int).Mul(winnerPool.Value, winner.Amount.Value)
		shareValue.Div(shareValue, winnerTotal.Value)
		share := &scommon.Decimal{Precision: winnerPool.Precision, Value: shareValue}
		payouts = append(payouts, AgentPredictionTransferView{
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
	amount, err := scommon.NewDecimalFromString(value, agentcontract.MaxPredictionDecimalPrecision)
	if err != nil {
		return zeroQueryDecimal()
	}
	return amount
}

func decimalMulBPSQuery(value *scommon.Decimal, bps int) *scommon.Decimal {
	return value.MulBigInt(big.NewInt(int64(bps))).DivBigInt(big.NewInt(agentcontract.PredictionTotalBPS))
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
