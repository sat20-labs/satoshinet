package agent

import contractframework "github.com/sat20-labs/satoshinet/contract/framework"

var _ contractframework.StateViewProvider = (*Runtime)(nil)

type RuntimeStateView struct {
	Subtype       string              `json:"subtype"`
	Version       uint32              `json:"version"`
	Status        string              `json:"status"`
	Deployer      string              `json:"deployer,omitempty"`
	Contract      PredictionContract  `json:"contract"`
	Prediction    PredictionStateView `json:"prediction"`
	ManagedAssets []string            `json:"assets,omitempty"`
}

type PredictionStateView struct {
	Status          string                   `json:"status"`
	BetAsset        string                   `json:"betAsset,omitempty"`
	GasBalance      string                   `json:"gasBalance,omitempty"`
	BetCount        int                      `json:"betCount"`
	TotalBetAmount  string                   `json:"totalBetAmount,omitempty"`
	Outcomes        []PredictionOutcomeView  `json:"outcomes,omitempty"`
	LatestConfirm   *PredictionConfirmRecord `json:"latestConfirm,omitempty"`
	LatestRejection *PredictionRejectRecord  `json:"latestRejection,omitempty"`
}

type PredictionOutcomeView struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	BetCount int    `json:"betCount"`
	Amount   string `json:"amount,omitempty"`
	Winner   bool   `json:"winner,omitempty"`
}

func (r *Runtime) StateView(ctx contractframework.StateViewContext) (interface{}, error) {
	outcomes := make([]PredictionOutcomeView, 0, len(r.contract.Outcomes))
	byID := make(map[string]int, len(r.contract.Outcomes))
	for _, outcome := range r.contract.Outcomes {
		byID[outcome.ID] = len(outcomes)
		outcomes = append(outcomes, PredictionOutcomeView{
			ID:   outcome.ID,
			Text: outcome.Text,
		})
	}

	total := ""
	for _, bet := range r.state.Prediction.Bets {
		total = decimalStringAdd(total, bet.Amount)
		if idx, ok := byID[bet.OutcomeID]; ok {
			outcomes[idx].BetCount++
			outcomes[idx].Amount = decimalStringAdd(outcomes[idx].Amount, bet.Amount)
		}
	}

	var latestConfirm *PredictionConfirmRecord
	if n := len(r.state.Prediction.Confirmations); n > 0 {
		record := r.state.Prediction.Confirmations[n-1]
		latestConfirm = &record
		for i := range outcomes {
			outcomes[i].Winner = outcomes[i].ID == record.OutcomeID
		}
	}
	var latestRejection *PredictionRejectRecord
	if n := len(r.state.Prediction.Rejections); n > 0 {
		record := r.state.Prediction.Rejections[n-1]
		latestRejection = &record
	}

	return RuntimeStateView{
		Subtype:  r.deploy.SubType,
		Version:  r.deploy.Version,
		Status:   r.state.Status,
		Deployer: r.deployer,
		Contract: r.contract,
		Prediction: PredictionStateView{
			Status:          r.state.Prediction.Status,
			BetAsset:        r.contract.BetAsset,
			GasBalance:      r.state.Prediction.GasBalance,
			BetCount:        len(r.state.Prediction.Bets),
			TotalBetAmount:  total,
			Outcomes:        outcomes,
			LatestConfirm:   latestConfirm,
			LatestRejection: latestRejection,
		},
		ManagedAssets: []string{r.contract.BetAsset},
	}, nil
}
