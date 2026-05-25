package agent

import "strings"

type RuntimeState struct {
	Status     string                 `json:"status"`
	Prediction PredictionRuntimeState `json:"prediction"`
}

type PredictionRuntimeState struct {
	Status        string                         `json:"status"`
	Bets          map[string]PredictionBetRecord `json:"bets,omitempty"`
	Confirmations []PredictionConfirmRecord      `json:"confirmations,omitempty"`
}

type PredictionBetRecord struct {
	Address   string `json:"address"`
	OutcomeID string `json:"outcome_id"`
	Amount    string `json:"amount"`
}

type PredictionConfirmRecord struct {
	Agent      string `json:"agent"`
	ResultType string `json:"result_type"`
	OutcomeID  string `json:"outcome_id,omitempty"`
	SourceURL  string `json:"source_url"`
	ResultURL  string `json:"result_url"`
	ResultHash string `json:"result_hash"`
	ObservedAt int64  `json:"observed_at"`
}

func (s RuntimeState) Clone() RuntimeState {
	out := s
	out.Prediction = s.Prediction.Clone()
	return out
}

func (s PredictionRuntimeState) Clone() PredictionRuntimeState {
	out := s
	if s.Bets != nil {
		out.Bets = make(map[string]PredictionBetRecord, len(s.Bets))
		for k, v := range s.Bets {
			out.Bets[k] = v
		}
	}
	out.Confirmations = append([]PredictionConfirmRecord(nil), s.Confirmations...)
	return out
}

func predictionBetKey(address, outcomeID string) string {
	return strings.Join([]string{address, outcomeID}, "\x00")
}
