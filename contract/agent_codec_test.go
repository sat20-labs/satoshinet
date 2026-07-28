package contract

import (
	"encoding/json"
	"testing"

	"github.com/sat20-labs/satoshinet/txscript"
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
	param, err := (AgentPredictionConfirmParam{
		ResultType:   ResultTypeOutcome,
		OutcomeID:    "win",
		Result:       "France wins 2-1",
		ResultURL:    "https://worldcup.cctv.com/2026/result.shtml",
		ObservedAt:   1781636500,
		AgentVersion: CurrentAgentVersion,
		ModelVersion: "model-v1",
	}).Encode()
	require.NoError(t, err)
	want := InvokePayload{
		GasLimit:  12345,
		CallNonce: 67890,
		Action:    AgentInvokeAPIConfirm,
		Param:     param,
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
	content, err := validAgentPredictionCodecContract().Encode()
	require.NoError(t, err)
	want := DeployPayload{
		Type:            ContractTypeAgent,
		GasLimit:        43210,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     7,
		ContractContent: content,
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

func TestAgentPredictionScriptCodecRoundTrip(t *testing.T) {
	contract := validAgentPredictionCodecContract()
	content, err := contract.Encode()
	require.NoError(t, err)
	require.False(t, json.Valid(content))

	decodedContract, err := DecodeAgentPredictionContract(content)
	require.NoError(t, err)
	require.Equal(t, contract, decodedContract)

	bet := AgentPredictionBetParam{OutcomeID: "win"}
	betData, err := bet.Encode()
	require.NoError(t, err)
	decodedBet, err := DecodeAgentPredictionBetParam(betData)
	require.NoError(t, err)
	require.Equal(t, bet, decodedBet)

	confirm := AgentPredictionConfirmParam{
		ResultType:   ResultTypeOutcome,
		OutcomeID:    "win",
		Result:       "France wins 2-1",
		ResultURL:    "https://worldcup.cctv.com/2026/result.shtml",
		ObservedAt:   1781636500,
		AgentVersion: CurrentAgentVersion,
		ModelVersion: "model-v1",
	}
	confirmData, err := confirm.Encode()
	require.NoError(t, err)
	decodedConfirm, err := DecodeAgentPredictionConfirmParam(confirmData)
	require.NoError(t, err)
	require.Equal(t, confirm, decodedConfirm)

	reject := AgentPredictionRejectParam{Reason: "source is unavailable", CheckedAt: 1781636500}
	rejectData, err := reject.Encode()
	require.NoError(t, err)
	decodedReject, err := DecodeAgentPredictionRejectParam(rejectData)
	require.NoError(t, err)
	require.Equal(t, reject, decodedReject)
}

func TestAgentPredictionScriptCodecRejectsLegacyJSON(t *testing.T) {
	_, err := DecodeAgentPredictionContract([]byte(`{"subtype":"prediction"}`))
	require.Error(t, err)
	_, err = DecodeAgentPredictionBetParam([]byte(`{"outcome_id":"win"}`))
	require.Error(t, err)
	_, err = DecodeAgentPredictionConfirmParam([]byte(`{"result_type":"outcome"}`))
	require.Error(t, err)
	_, err = DecodeAgentPredictionRejectParam([]byte(`{"reason":"invalid"}`))
	require.Error(t, err)
}

func TestAgentPredictionScriptCodecRejectsTrailingFields(t *testing.T) {
	contractData, err := validAgentPredictionCodecContract().Encode()
	require.NoError(t, err)
	betData, err := (AgentPredictionBetParam{OutcomeID: "win"}).Encode()
	require.NoError(t, err)
	confirmData, err := (AgentPredictionConfirmParam{
		ResultType: ResultTypeOutcome,
		OutcomeID:  "win",
		Result:     "France wins 2-1",
		ResultURL:  "https://worldcup.cctv.com/2026/result.shtml",
		ObservedAt: 1781636500,
	}).Encode()
	require.NoError(t, err)
	rejectData, err := (AgentPredictionRejectParam{
		Reason:    "source is unavailable",
		CheckedAt: 1781636500,
	}).Encode()
	require.NoError(t, err)

	tests := []struct {
		name   string
		data   []byte
		decode func([]byte) error
	}{
		{
			name: "contract",
			data: contractData,
			decode: func(data []byte) error {
				_, err := DecodeAgentPredictionContract(data)
				return err
			},
		},
		{
			name: "bet",
			data: betData,
			decode: func(data []byte) error {
				_, err := DecodeAgentPredictionBetParam(data)
				return err
			},
		},
		{
			name: "confirm",
			data: confirmData,
			decode: func(data []byte) error {
				_, err := DecodeAgentPredictionConfirmParam(data)
				return err
			},
		},
		{
			name: "reject",
			data: rejectData,
			decode: func(data []byte) error {
				_, err := DecodeAgentPredictionRejectParam(data)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := append(append([]byte(nil), test.data...), txscript.OP_1)
			require.Error(t, test.decode(data))
		})
	}
}

func TestAgentPredictionScriptCodecRejectsNonCanonicalPush(t *testing.T) {
	data := []byte{txscript.OP_PUSHDATA1, 1, 'a'}
	_, err := DecodeAgentPredictionBetParam(data)
	require.ErrorContains(t, err, "non-canonical")
}

func validAgentPredictionCodecContract() AgentPredictionContract {
	return AgentPredictionContract{
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
}
