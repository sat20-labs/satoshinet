package agent

import (
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"

	scommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

const MaxPredictionDecimalPrecision = 10
const MaxPredictionConfirmResultLen = 128

var outcomeIDPattern = regexp.MustCompile(`^[a-z]+$`)

type PredictionContract struct {
	Subtype      string              `json:"subtype"`
	Title        string              `json:"title"`
	Description  string              `json:"description"`
	TimeBase     string              `json:"time_base"`
	EventTime    int64               `json:"event_time"`
	BetDeadline  int64               `json:"bet_deadline"`
	ConfirmAfter int64               `json:"confirm_after"`
	SourceURL    string              `json:"source_url"`
	BetAsset     string              `json:"bet_asset"`
	MinBetUnit   string              `json:"min_bet_unit"`
	Outcomes     []PredictionOutcome `json:"outcomes"`
}

type PredictionOutcome struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type PredictionBetParam struct {
	OutcomeID string `json:"outcome_id"`
}

type PredictionConfirmParam struct {
	ResultType   string `json:"result_type"`
	OutcomeID    string `json:"outcome_id,omitempty"`
	Result       string `json:"result"`
	ResultURL    string `json:"result_url"`
	ObservedAt   int64  `json:"observed_at"`
	AgentVersion uint32 `json:"agent_version,omitempty"`
	ModelVersion string `json:"model_version,omitempty"`
}

type PredictionRejectParam struct {
	Reason    string `json:"reason"`
	CheckedAt int64  `json:"checked_at"`
}

func (c PredictionContract) Encode() ([]byte, error) {
	return c.commonContract().Encode()
}

func (c PredictionContract) NetworkExclusive() bool {
	return false
}

func DecodePredictionContract(data []byte) (PredictionContract, error) {
	c, err := contractcommon.DecodeAgentPredictionContract(data)
	if err != nil {
		return PredictionContract{}, err
	}
	return predictionContractFromCommon(c), nil
}

func (c PredictionContract) Check() error {
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
	if err := checkSourceURL(c.SourceURL); err != nil {
		return err
	}
	if err := checkAssetName(c.BetAsset); err != nil {
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
		if err := checkOutcome(outcome); err != nil {
			return err
		}
		if _, ok := seen[outcome.ID]; ok {
			return fmt.Errorf("duplicate prediction outcome id %s", outcome.ID)
		}
		seen[outcome.ID] = struct{}{}
	}
	return nil
}

func (c PredictionContract) HasOutcome(id string) bool {
	for _, outcome := range c.Outcomes {
		if outcome.ID == id {
			return true
		}
	}
	return false
}

func (p PredictionBetParam) Encode() ([]byte, error) {
	return contractcommon.AgentPredictionBetParam(p).Encode()
}

func DecodePredictionBetParam(data []byte) (PredictionBetParam, error) {
	p, err := contractcommon.DecodeAgentPredictionBetParam(data)
	if err != nil {
		return PredictionBetParam{}, err
	}
	return PredictionBetParam(p), nil
}

func (p PredictionBetParam) Check(contract PredictionContract) error {
	if !contract.HasOutcome(p.OutcomeID) {
		return fmt.Errorf("unknown prediction outcome id %s", p.OutcomeID)
	}
	return nil
}

func (p PredictionConfirmParam) Encode() ([]byte, error) {
	return contractcommon.AgentPredictionConfirmParam(p).Encode()
}

func DecodePredictionConfirmParam(data []byte) (PredictionConfirmParam, error) {
	p, err := contractcommon.DecodeAgentPredictionConfirmParam(data)
	if err != nil {
		return PredictionConfirmParam{}, err
	}
	return PredictionConfirmParam(p), nil
}

func (p PredictionConfirmParam) Check(contract PredictionContract) error {
	switch p.ResultType {
	case ResultTypeOutcome:
		if !contract.HasOutcome(p.OutcomeID) {
			return fmt.Errorf("unknown prediction outcome id %s", p.OutcomeID)
		}
	case ResultTypeCancelled, ResultTypeInvalid, ResultTypeUnverifiable:
		if p.OutcomeID != "" {
			return fmt.Errorf("outcome id must be empty for result type %s", p.ResultType)
		}
	default:
		return fmt.Errorf("invalid prediction result type %s", p.ResultType)
	}
	if !ResultURLAllowed(contract.SourceURL, p.ResultURL) {
		return fmt.Errorf("result url is outside source site")
	}
	result := strings.TrimSpace(p.Result)
	if result == "" {
		return fmt.Errorf("prediction result is empty")
	}
	if len(result) > MaxPredictionConfirmResultLen {
		return fmt.Errorf("prediction result is too long: %d > %d", len(result), MaxPredictionConfirmResultLen)
	}
	if p.ObservedAt <= 0 {
		return fmt.Errorf("invalid observed_at %d", p.ObservedAt)
	}
	return nil
}

func (p PredictionRejectParam) Encode() ([]byte, error) {
	return contractcommon.AgentPredictionRejectParam(p).Encode()
}

func DecodePredictionRejectParam(data []byte) (PredictionRejectParam, error) {
	p, err := contractcommon.DecodeAgentPredictionRejectParam(data)
	if err != nil {
		return PredictionRejectParam{}, err
	}
	return PredictionRejectParam(p), nil
}

func (p PredictionRejectParam) Check() error {
	if strings.TrimSpace(p.Reason) == "" {
		return fmt.Errorf("prediction reject reason is empty")
	}
	if p.CheckedAt <= 0 {
		return fmt.Errorf("invalid checked_at %d", p.CheckedAt)
	}
	return nil
}

func CheckBetAmount(amount, minUnit string) error {
	amt, err := parsePositiveDecimal("bet amount", amount)
	if err != nil {
		return err
	}
	unit, err := parsePositiveDecimal("min bet unit", minUnit)
	if err != nil {
		return err
	}
	if amt.Cmp(unit) < 0 {
		return fmt.Errorf("bet amount is below min bet unit")
	}
	precision := amt.Precision
	if unit.Precision > precision {
		precision = unit.Precision
	}
	alignedAmt := amt.NewPrecision(precision)
	alignedUnit := unit.NewPrecision(precision)
	if alignedUnit.Value.Sign() == 0 {
		return fmt.Errorf("min bet unit is zero")
	}
	if new(big.Int).Mod(alignedAmt.Value, alignedUnit.Value).Sign() != 0 {
		return fmt.Errorf("bet amount is not a multiple of min bet unit")
	}
	return nil
}

func ResultURLAllowed(sourceURL, resultURL string) bool {
	source, err := parseHTTPURL(sourceURL)
	if err != nil {
		return false
	}
	result, err := parseHTTPURL(resultURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(source.Scheme, result.Scheme) &&
		hostInScope(source.Hostname(), result.Hostname())
}

func hostInScope(sourceHost, resultHost string) bool {
	sourceHost = strings.TrimSuffix(strings.ToLower(sourceHost), ".")
	resultHost = strings.TrimSuffix(strings.ToLower(resultHost), ".")
	if resultHost == sourceHost || strings.HasSuffix(resultHost, "."+sourceHost) {
		return true
	}
	sourceDomain, err := siteSearchDomain(sourceHost)
	if err != nil {
		return false
	}
	resultDomain, err := siteSearchDomain(resultHost)
	if err != nil {
		return false
	}
	return sourceDomain == resultDomain
}

func checkOutcome(outcome PredictionOutcome) error {
	if !outcomeIDPattern.MatchString(outcome.ID) {
		return fmt.Errorf("invalid prediction outcome id %s", outcome.ID)
	}
	if strings.TrimSpace(outcome.Text) == "" {
		return fmt.Errorf("prediction outcome %s text is empty", outcome.ID)
	}
	return nil
}

func (c PredictionContract) commonContract() contractcommon.AgentPredictionContract {
	outcomes := make([]contractcommon.AgentPredictionOutcome, len(c.Outcomes))
	for i, outcome := range c.Outcomes {
		outcomes[i] = contractcommon.AgentPredictionOutcome(outcome)
	}
	return contractcommon.AgentPredictionContract{
		Subtype:      c.Subtype,
		Title:        c.Title,
		Description:  c.Description,
		TimeBase:     c.TimeBase,
		EventTime:    c.EventTime,
		BetDeadline:  c.BetDeadline,
		ConfirmAfter: c.ConfirmAfter,
		SourceURL:    c.SourceURL,
		BetAsset:     c.BetAsset,
		MinBetUnit:   c.MinBetUnit,
		Outcomes:     outcomes,
	}
}

func predictionContractFromCommon(c contractcommon.AgentPredictionContract) PredictionContract {
	outcomes := make([]PredictionOutcome, len(c.Outcomes))
	for i, outcome := range c.Outcomes {
		outcomes[i] = PredictionOutcome(outcome)
	}
	return PredictionContract{
		Subtype:      c.Subtype,
		Title:        c.Title,
		Description:  c.Description,
		TimeBase:     c.TimeBase,
		EventTime:    c.EventTime,
		BetDeadline:  c.BetDeadline,
		ConfirmAfter: c.ConfirmAfter,
		SourceURL:    c.SourceURL,
		BetAsset:     c.BetAsset,
		MinBetUnit:   c.MinBetUnit,
		Outcomes:     outcomes,
	}
}

func checkAssetName(assetName string) error {
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return fmt.Errorf("invalid asset name %s", assetName)
	}
	return nil
}

func checkSourceURL(raw string) error {
	_, err := parseHTTPURL(raw)
	if err != nil {
		return fmt.Errorf("invalid source url %s", raw)
	}
	return nil
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid url scheme")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("missing url host")
	}
	return u, nil
}

func parsePositiveDecimal(field, value string) (*scommon.Decimal, error) {
	if value == "" || value == "0" {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	decimal, err := scommon.NewDecimalFromString(value, MaxPredictionDecimalPrecision)
	if err != nil {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	if decimal.Cmp(scommon.NewDefaultDecimal(0)) <= 0 {
		return nil, fmt.Errorf("invalid %s %s", field, value)
	}
	return decimal, nil
}
