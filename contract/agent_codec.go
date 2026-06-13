package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	SubtypePrediction = "prediction"

	CurrentAgentVersion uint32 = 1

	AgentInvokeAPIReady   = "ready"
	AgentInvokeAPIBet     = "bet"
	AgentInvokeAPIConfirm = "confirm"
	AgentInvokeAPIReject  = "reject"

	TimeBaseUnix   = "unix"
	TimeBaseHeight = "height"

	ResultTypeOutcome      = "outcome"
	ResultTypeCancelled    = "cancelled"
	ResultTypeInvalid      = "invalid"
	ResultTypeUnverifiable = "unverifiable"
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
	ResultType        string `json:"result_type"`
	OutcomeID         string `json:"outcome_id,omitempty"`
	SourceURL         string `json:"source_url"`
	ResultURL         string `json:"result_url"`
	ResultHash        string `json:"result_hash"`
	ObservedAt        int64  `json:"observed_at"`
	AgentVersion      string `json:"agent_version,omitempty"`
	ModelVersion      string `json:"model_version,omitempty"`
	CoreNodePubKey    string `json:"core_node_pubkey,omitempty"`
	CoreNodeSignature string `json:"core_node_signature,omitempty"`
}

type AgentPredictionRejectParam struct {
	Reason    string `json:"reason"`
	CheckedAt int64  `json:"checked_at"`
}

var agentOutcomeIDPattern = regexp.MustCompile(`^[a-z]+$`)

func (c AgentPredictionContract) Encode() ([]byte, error) {
	return json.Marshal(c)
}

func DecodeAgentPredictionContract(data []byte) (AgentPredictionContract, error) {
	var c AgentPredictionContract
	if err := json.Unmarshal(data, &c); err != nil {
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
	if err := checkTemplateAssetName(c.BetAsset); err != nil {
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

func (p AgentPredictionBetParam) Encode() ([]byte, error) {
	return json.Marshal(p)
}

func DecodeAgentPredictionBetParam(data []byte) (AgentPredictionBetParam, error) {
	var p AgentPredictionBetParam
	if err := json.Unmarshal(data, &p); err != nil {
		return AgentPredictionBetParam{}, err
	}
	return p, nil
}

func (p AgentPredictionConfirmParam) Encode() ([]byte, error) {
	return json.Marshal(p)
}

func DecodeAgentPredictionConfirmParam(data []byte) (AgentPredictionConfirmParam, error) {
	var p AgentPredictionConfirmParam
	if err := json.Unmarshal(data, &p); err != nil {
		return AgentPredictionConfirmParam{}, err
	}
	return p, nil
}

func (p AgentPredictionRejectParam) Encode() ([]byte, error) {
	return json.Marshal(p)
}

func DecodeAgentPredictionRejectParam(data []byte) (AgentPredictionRejectParam, error) {
	var p AgentPredictionRejectParam
	if err := json.Unmarshal(data, &p); err != nil {
		return AgentPredictionRejectParam{}, err
	}
	return p, nil
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
