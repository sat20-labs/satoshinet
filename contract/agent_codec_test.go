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

func TestAgentInvokePayloadRoundTrip(t *testing.T) {
	want := AgentInvokePayload{
		GasLimit:  12345,
		CallNonce: 67890,
		Action:    AgentInvokeAPIConfirm,
		Param:     []byte("confirm-param"),
	}
	encoded, err := EncodeAgentInvokePayload(want)
	require.NoError(t, err)

	got, err := DecodeAgentInvokePayload(encoded)
	require.NoError(t, err)
	require.Equal(t, want.GasLimit, got.GasLimit)
	require.Equal(t, want.CallNonce, got.CallNonce)
	require.Equal(t, want.Action, got.Action)
	require.Equal(t, want.Param, got.Param)
}

func TestAgentDeployPayloadRoundTrip(t *testing.T) {
	want := AgentDeployPayload{
		GasLimit:        43210,
		Subtype:         SubtypePrediction,
		AgentVersion:    CurrentAgentVersion,
		Deployer:        "tb1pdeployer",
		Random:          []byte("random"),
		ContractContent: []byte("contract-content"),
	}
	encoded, err := EncodeAgentDeployPayload(want)
	require.NoError(t, err)

	got, err := DecodeAgentDeployPayload(encoded)
	require.NoError(t, err)
	require.Equal(t, want.GasLimit, got.GasLimit)
	require.Equal(t, want.Subtype, got.Subtype)
	require.Equal(t, want.AgentVersion, got.AgentVersion)
	require.Equal(t, want.Deployer, got.Deployer)
	require.Equal(t, want.Random, got.Random)
	require.Equal(t, want.ContractContent, got.ContractContent)
}
