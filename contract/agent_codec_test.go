package contract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentPredictionCheckAllowsSatoshiBetAsset(t *testing.T) {
	contract := AgentPredictionContract{
		Subtype:      SubtypePrediction,
		Title:        "2026 World Cup",
		Description:  "France vs Senegal",
		TimeBase:     TimeBaseUnix,
		EventTime:    1781636400,
		BetDeadline:  1781634600,
		ConfirmAfter: 1781647200,
		SourceURL:    "https://worldcup.cctv.com/2026/schedule/index.shtml",
		BetAsset:     SatoshiAssetName,
		MinBetUnit:   "1000",
		Outcomes: []AgentPredictionOutcome{
			{ID: "win", Text: "France wins"},
			{ID: "lost", Text: "France loses"},
			{ID: "draw", Text: "Draw"},
		},
	}

	require.NoError(t, contract.Check())
}

func TestAgentPredictionCheckRejectsInvalidBetAsset(t *testing.T) {
	contract := AgentPredictionContract{
		Subtype:      SubtypePrediction,
		Title:        "2026 World Cup",
		Description:  "France vs Senegal",
		TimeBase:     TimeBaseUnix,
		EventTime:    1781636400,
		BetDeadline:  1781634600,
		ConfirmAfter: 1781647200,
		SourceURL:    "https://worldcup.cctv.com/2026/schedule/index.shtml",
		BetAsset:     "invalid",
		MinBetUnit:   "1000",
		Outcomes: []AgentPredictionOutcome{
			{ID: "win", Text: "France wins"},
			{ID: "lost", Text: "France loses"},
			{ID: "draw", Text: "Draw"},
		},
	}

	require.ErrorContains(t, contract.Check(), "invalid asset name")
}
