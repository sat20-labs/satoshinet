package agent

import (
	"encoding/json"
	"testing"

	contractframework "github.com/sat20-labs/satoshinet/contract/framework"
	"github.com/stretchr/testify/require"
)

func TestPredictionStateViewAggregatesBets(t *testing.T) {
	runtime := newTestRuntime(t)
	require.NoError(t, runtime.ApplyReady(ApplyReadyRequest{Invoker: "core"}))
	require.NoError(t, runtime.ApplyBet(ApplyBetRequest{
		Invoker:   "alice",
		Param:     PredictionBetParam{OutcomeID: "a"},
		AssetName: SatoshiAssetName,
		Amount:    "10000",
		GasAmount: "5",
		TimeValue: validPredictionContract().BetDeadline - 1,
	}))
	require.NoError(t, runtime.ApplyBet(ApplyBetRequest{
		Invoker:   "bob",
		Param:     PredictionBetParam{OutcomeID: "a"},
		AssetName: SatoshiAssetName,
		Amount:    "20000",
		TimeValue: validPredictionContract().BetDeadline - 1,
	}))
	require.NoError(t, runtime.ApplyBet(ApplyBetRequest{
		Invoker:   "carol",
		Param:     PredictionBetParam{OutcomeID: "b"},
		AssetName: SatoshiAssetName,
		Amount:    "30000",
		TimeValue: validPredictionContract().BetDeadline - 1,
	}))

	got, err := runtime.StateView(contractframework.StateViewContext{})
	require.NoError(t, err)
	view, ok := got.(RuntimeStateView)
	require.True(t, ok)
	require.Equal(t, StatusReady, view.Status)
	require.Equal(t, PredictionStatusBetting, view.Prediction.Status)
	require.Equal(t, 3, view.Prediction.BetCount)
	require.Equal(t, "60000", view.Prediction.TotalBetAmount)
	require.Equal(t, "5", view.Prediction.GasBalance)
	require.Len(t, view.Prediction.Outcomes, 2)
	require.Equal(t, "30000", view.Prediction.Outcomes[0].Amount)
	require.Equal(t, 2, view.Prediction.Outcomes[0].BetCount)
	require.Equal(t, "30000", view.Prediction.Outcomes[1].Amount)

	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "alice")
	require.NotContains(t, string(encoded), "bob")
	require.NotContains(t, string(encoded), "carol")
}
