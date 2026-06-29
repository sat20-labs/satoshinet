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

func TestUnifiedAgentInvokeEnvelopeRoundTrip(t *testing.T) {
	want := InvokePayload{
		GasLimit:  12345,
		CallNonce: 67890,
		Action:    AgentInvokeAPIConfirm,
		Param:     []byte("confirm-param"),
	}
	encoded := EncodeInvokePayload(want)

	got, err := DecodeInvokePayload(encoded)
	require.NoError(t, err)
	require.Equal(t, want.GasLimit, got.GasLimit)
	require.Equal(t, want.CallNonce, got.CallNonce)
	require.Equal(t, want.Action, got.Action)
	require.Equal(t, want.Param, got.Param)
}

func TestUnifiedAgentDeployEnvelopeRoundTrip(t *testing.T) {
	want := DeployPayload{
		Type:            ContractTypeAgent,
		GasLimit:        43210,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     7,
		ContractContent: []byte("contract-content"),
	}
	encoded := EncodeDeployPayload(want)

	got, err := DecodeDeployPayload(encoded)
	require.NoError(t, err)
	require.Equal(t, want.Type, got.Type)
	require.Equal(t, want.GasLimit, got.GasLimit)
	require.Equal(t, want.SubType, got.SubType)
	require.Equal(t, want.Version, got.Version)
	require.Equal(t, want.DeployNonce, got.DeployNonce)
	require.Equal(t, want.ContractContent, got.ContractContent)
}
