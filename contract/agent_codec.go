package contract

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/sat20-labs/satoshinet/txscript"
)

const (
	SubtypePrediction = "prediction"

	CurrentAgentVersion           uint32 = 1
	MaxPredictionDecimalPrecision        = 10

	AgentInvokeAPIReady   = "ready"
	AgentInvokeAPIBet     = "bet"
	AgentInvokeAPIConfirm = "confirm"
	AgentInvokeAPIReject  = "reject"
	AgentInvokeAPIClose   = ContractInvokeAPIClose

	TimeBaseUnix   = "unix"
	TimeBaseHeight = "height"

	ResultTypeOutcome      = "outcome"
	ResultTypeCancelled    = "cancelled"
	ResultTypeInvalid      = "invalid"
	ResultTypeUnverifiable = "unverifiable"
)

const (
	PredictionDeployerFeeBPS = 600
	PredictionAgentFeeBPS    = 300
	PredictionBootstrapBPS   = 100
	PredictionWinnerPoolBPS  = 9000
	PredictionTotalBPS       = 10000
)

const (
	AgentStatusPendingReady = "PendingReady"
	AgentStatusReady        = "Ready"
	AgentStatusRejected     = "Rejected"
	AgentStatusInvalid      = "Invalid"
	AgentStatusCompleted    = "Completed"
	AgentStatusFailed       = "Failed"
	AgentStatusDisputed     = "Disputed"
	AgentStatusExpired      = "Expired"
)

const (
	AgentPredictionStatusBetting       = "Betting"
	AgentPredictionStatusClosedForBet  = "ClosedForBet"
	AgentPredictionStatusPendingResult = "PendingResult"
	AgentPredictionStatusConfirmed     = "Confirmed"
	AgentPredictionStatusSettled       = "Settled"
	AgentPredictionStatusRefundable    = "Refundable"
	AgentPredictionStatusRejected      = "Rejected"
)

type AgentPredictionContract struct {
	Subtype      string                   `json:"subtype"`
	Title        string                   `json:"title"`
	Description  string                   `json:"description"`
	TimeBase     string                   `json:"time_base"`
	EventTime    int64                    `json:"event_time"`
	BetDeadline  int64                    `json:"bet_deadline"`
	ConfirmAfter int64                    `json:"confirm_after"`
	SourceURL    string                   `json:"source_url"`
	BetAsset     string                   `json:"bet_asset"`
	MinBetUnit   string                   `json:"min_bet_unit"`
	Outcomes     []AgentPredictionOutcome `json:"outcomes"`
}

type AgentPredictionOutcome struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type AgentPredictionBetParam struct {
	OutcomeID string `json:"outcome_id"`
}

type AgentPredictionConfirmParam struct {
	ResultType   string `json:"result_type"`
	OutcomeID    string `json:"outcome_id,omitempty"`
	Result       string `json:"result"`
	ResultURL    string `json:"result_url"`
	ObservedAt   int64  `json:"observed_at"`
	AgentVersion uint32 `json:"agent_version,omitempty"`
	ModelVersion string `json:"model_version,omitempty"`
}

type AgentPredictionRejectParam struct {
	Reason    string `json:"reason"`
	CheckedAt int64  `json:"checked_at"`
}

var agentOutcomeIDPattern = regexp.MustCompile(`^[a-z]+$`)

func (c AgentPredictionContract) Encode() ([]byte, error) {
	builder := txscript.NewScriptBuilder().
		AddData([]byte(c.Subtype)).
		AddData([]byte(c.Title)).
		AddData([]byte(c.Description)).
		AddData([]byte(c.TimeBase)).
		AddInt64(c.EventTime).
		AddInt64(c.BetDeadline).
		AddInt64(c.ConfirmAfter).
		AddData([]byte(c.SourceURL)).
		AddData([]byte(c.BetAsset)).
		AddData([]byte(c.MinBetUnit)).
		AddInt64(int64(len(c.Outcomes)))
	for _, outcome := range c.Outcomes {
		builder.AddData([]byte(outcome.ID)).
			AddData([]byte(outcome.Text))
	}
	return builder.Script()
}

func DecodeAgentPredictionContract(data []byte) (AgentPredictionContract, error) {
	var c AgentPredictionContract
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	var err error
	if c.Subtype, err = decodeAgentScriptString(&tokenizer, "prediction subtype"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.Title, err = decodeAgentScriptString(&tokenizer, "prediction title"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.Description, err = decodeAgentScriptString(&tokenizer, "prediction description"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.TimeBase, err = decodeAgentScriptString(&tokenizer, "prediction time base"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.EventTime, err = decodeAgentScriptInt64(&tokenizer, "prediction event time"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.BetDeadline, err = decodeAgentScriptInt64(&tokenizer, "prediction bet deadline"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.ConfirmAfter, err = decodeAgentScriptInt64(&tokenizer, "prediction confirm after"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.SourceURL, err = decodeAgentScriptString(&tokenizer, "prediction source url"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.BetAsset, err = decodeAgentScriptString(&tokenizer, "prediction bet asset"); err != nil {
		return AgentPredictionContract{}, err
	}
	if c.MinBetUnit, err = decodeAgentScriptString(&tokenizer, "prediction min bet unit"); err != nil {
		return AgentPredictionContract{}, err
	}
	outcomeCount, err := decodeAgentScriptInt64(&tokenizer, "prediction outcome count")
	if err != nil {
		return AgentPredictionContract{}, err
	}
	if outcomeCount < 0 || outcomeCount > int64(len(data)) {
		return AgentPredictionContract{}, fmt.Errorf("invalid prediction outcome count %d", outcomeCount)
	}
	c.Outcomes = make([]AgentPredictionOutcome, 0, int(outcomeCount))
	for i := int64(0); i < outcomeCount; i++ {
		id, err := decodeAgentScriptString(&tokenizer, fmt.Sprintf("prediction outcome %d id", i))
		if err != nil {
			return AgentPredictionContract{}, err
		}
		text, err := decodeAgentScriptString(&tokenizer, fmt.Sprintf("prediction outcome %d text", i))
		if err != nil {
			return AgentPredictionContract{}, err
		}
		c.Outcomes = append(c.Outcomes, AgentPredictionOutcome{ID: id, Text: text})
	}
	if err := finishAgentScriptDecode(&tokenizer, "prediction contract"); err != nil {
		return AgentPredictionContract{}, err
	}
	if err := requireCanonicalAgentScript(data, c.Encode, "prediction contract"); err != nil {
		return AgentPredictionContract{}, err
	}
	return c, nil
}

func (c AgentPredictionContract) Check() error {
	if c.Subtype != SubtypePrediction {
		return fmt.Errorf("invalid prediction subtype %s", c.Subtype)
	}
	if strings.TrimSpace(c.Title) == "" {
		return fmt.Errorf("prediction title is empty")
	}
	if strings.TrimSpace(c.Description) == "" {
		return fmt.Errorf("prediction description is empty")
	}
	if c.TimeBase != TimeBaseUnix && c.TimeBase != TimeBaseHeight {
		return fmt.Errorf("invalid prediction time base %s", c.TimeBase)
	}
	if c.EventTime <= 0 {
		return fmt.Errorf("invalid prediction event time %d", c.EventTime)
	}
	if c.BetDeadline <= 0 {
		return fmt.Errorf("invalid prediction bet deadline %d", c.BetDeadline)
	}
	if c.ConfirmAfter <= 0 {
		return fmt.Errorf("invalid prediction confirm after %d", c.ConfirmAfter)
	}
	if c.BetDeadline >= c.EventTime {
		return fmt.Errorf("prediction bet deadline must be before event time")
	}
	if c.ConfirmAfter <= c.EventTime {
		return fmt.Errorf("prediction confirm_after must be after event time")
	}
	if strings.TrimSpace(c.SourceURL) == "" {
		return fmt.Errorf("source url is empty")
	}
	if err := checkAgentPredictionBetAssetName(c.BetAsset); err != nil {
		return err
	}
	if _, err := parsePositiveDecimal("min bet unit", c.MinBetUnit); err != nil {
		return err
	}
	if len(c.Outcomes) < 2 {
		return fmt.Errorf("prediction outcomes must contain at least 2 items")
	}
	seen := make(map[string]struct{}, len(c.Outcomes))
	for _, outcome := range c.Outcomes {
		if err := checkAgentPredictionOutcome(outcome); err != nil {
			return err
		}
		if _, ok := seen[outcome.ID]; ok {
			return fmt.Errorf("duplicate prediction outcome id %s", outcome.ID)
		}
		seen[outcome.ID] = struct{}{}
	}
	return nil
}

func checkAgentPredictionBetAssetName(assetName string) error {
	if assetName == SatoshiAssetName {
		return nil
	}
	return checkTemplateAssetName(assetName)
}

func (p AgentPredictionBetParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.OutcomeID)).
		Script()
}

func DecodeAgentPredictionBetParam(data []byte) (AgentPredictionBetParam, error) {
	var p AgentPredictionBetParam
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	var err error
	if p.OutcomeID, err = decodeAgentScriptString(&tokenizer, "prediction outcome id"); err != nil {
		return AgentPredictionBetParam{}, err
	}
	if err := finishAgentScriptDecode(&tokenizer, "prediction bet param"); err != nil {
		return AgentPredictionBetParam{}, err
	}
	if err := requireCanonicalAgentScript(data, p.Encode, "prediction bet param"); err != nil {
		return AgentPredictionBetParam{}, err
	}
	return p, nil
}

func (p AgentPredictionConfirmParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.ResultType)).
		AddData([]byte(p.OutcomeID)).
		AddData([]byte(p.Result)).
		AddData([]byte(p.ResultURL)).
		AddInt64(p.ObservedAt).
		AddInt64(int64(p.AgentVersion)).
		AddData([]byte(p.ModelVersion)).
		Script()
}

func DecodeAgentPredictionConfirmParam(data []byte) (AgentPredictionConfirmParam, error) {
	var p AgentPredictionConfirmParam
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	var err error
	if p.ResultType, err = decodeAgentScriptString(&tokenizer, "prediction result type"); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	if p.OutcomeID, err = decodeAgentScriptString(&tokenizer, "prediction outcome id"); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	if p.Result, err = decodeAgentScriptString(&tokenizer, "prediction result"); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	if p.ResultURL, err = decodeAgentScriptString(&tokenizer, "prediction result url"); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	if p.ObservedAt, err = decodeAgentScriptInt64(&tokenizer, "prediction observed at"); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	agentVersion, err := decodeAgentScriptInt64(&tokenizer, "prediction agent version")
	if err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	if agentVersion < 0 || uint64(agentVersion) > uint64(^uint32(0)) {
		return AgentPredictionConfirmParam{}, fmt.Errorf("invalid prediction agent version %d", agentVersion)
	}
	p.AgentVersion = uint32(agentVersion)
	if p.ModelVersion, err = decodeAgentScriptString(&tokenizer, "prediction model version"); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	if err := finishAgentScriptDecode(&tokenizer, "prediction confirm param"); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	if err := requireCanonicalAgentScript(data, p.Encode, "prediction confirm param"); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	return p, nil
}

func (p AgentPredictionRejectParam) Encode() ([]byte, error) {
	return txscript.NewScriptBuilder().
		AddData([]byte(p.Reason)).
		AddInt64(p.CheckedAt).
		Script()
}

func DecodeAgentPredictionRejectParam(data []byte) (AgentPredictionRejectParam, error) {
	var p AgentPredictionRejectParam
	tokenizer := txscript.MakeScriptTokenizer(0, data)
	var err error
	if p.Reason, err = decodeAgentScriptString(&tokenizer, "prediction reject reason"); err != nil {
		return AgentPredictionRejectParam{}, err
	}
	if p.CheckedAt, err = decodeAgentScriptInt64(&tokenizer, "prediction checked at"); err != nil {
		return AgentPredictionRejectParam{}, err
	}
	if err := finishAgentScriptDecode(&tokenizer, "prediction reject param"); err != nil {
		return AgentPredictionRejectParam{}, err
	}
	if err := requireCanonicalAgentScript(data, p.Encode, "prediction reject param"); err != nil {
		return AgentPredictionRejectParam{}, err
	}
	return p, nil
}

func decodeAgentScriptString(tokenizer *txscript.ScriptTokenizer, field string) (string, error) {
	if !tokenizer.Next() {
		if err := tokenizer.Err(); err != nil {
			return "", fmt.Errorf("decode %s: %w", field, err)
		}
		return "", fmt.Errorf("missing %s", field)
	}
	if err := tokenizer.Err(); err != nil {
		return "", fmt.Errorf("decode %s: %w", field, err)
	}
	return string(tokenizer.Data()), nil
}

func decodeAgentScriptInt64(tokenizer *txscript.ScriptTokenizer, field string) (int64, error) {
	if !tokenizer.Next() {
		if err := tokenizer.Err(); err != nil {
			return 0, fmt.Errorf("decode %s: %w", field, err)
		}
		return 0, fmt.Errorf("missing %s", field)
	}
	if err := tokenizer.Err(); err != nil {
		return 0, fmt.Errorf("decode %s: %w", field, err)
	}
	opcode := tokenizer.Opcode()
	data := tokenizer.Data()
	if data == nil &&
		opcode != txscript.OP_0 &&
		opcode != txscript.OP_1NEGATE &&
		(opcode < txscript.OP_1 || opcode > txscript.OP_16) {
		return 0, fmt.Errorf("invalid %s integer opcode %d", field, opcode)
	}
	if len(data) > 8 {
		return 0, fmt.Errorf("invalid %s integer length %d", field, len(data))
	}
	return tokenizer.ExtractInt64(), nil
}

func finishAgentScriptDecode(tokenizer *txscript.ScriptTokenizer, name string) error {
	if tokenizer.Next() {
		return fmt.Errorf("unexpected %s fields", name)
	}
	if err := tokenizer.Err(); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	return nil
}

func requireCanonicalAgentScript(data []byte, encode func() ([]byte, error), name string) error {
	canonical, err := encode()
	if err != nil {
		return fmt.Errorf("encode canonical %s: %w", name, err)
	}
	if !bytes.Equal(data, canonical) {
		return fmt.Errorf("non-canonical %s encoding", name)
	}
	return nil
}

func checkAgentPredictionOutcome(outcome AgentPredictionOutcome) error {
	if !agentOutcomeIDPattern.MatchString(outcome.ID) {
		return fmt.Errorf("invalid prediction outcome id %s", outcome.ID)
	}
	if strings.TrimSpace(outcome.Text) == "" {
		return fmt.Errorf("prediction outcome text is empty")
	}
	return nil
}
