package agent

import (
	"bytes"
	"testing"
)

func TestDeployPayloadRoundTrip(t *testing.T) {
	payload := DeployPayload{
		GasLimit:        1000,
		SubType:         SubtypePrediction,
		Version:         CurrentAgentVersion,
		DeployNonce:     3,
		ContractContent: []byte(`{"subtype":"prediction"}`),
	}
	encoded, err := EncodeDeployPayload(payload)
	if err != nil {
		t.Fatalf("EncodeDeployPayload failed: %v", err)
	}
	decoded, err := DecodeDeployPayload(encoded)
	if err != nil {
		t.Fatalf("DecodeDeployPayload failed: %v", err)
	}
	if decoded.GasLimit != payload.GasLimit ||
		decoded.SubType != payload.SubType ||
		decoded.Version != payload.Version ||
		decoded.DeployNonce != payload.DeployNonce ||
		string(decoded.ContractContent) != string(payload.ContractContent) {
		t.Fatalf("decoded payload mismatch: %#v", decoded)
	}
}

func TestInvokePayloadRoundTrip(t *testing.T) {
	payload := InvokePayload{
		GasLimit:  1000,
		CallNonce: 7,
		Action:    InvokeAPIConfirm,
		Param:     []byte(`{"result_type":"outcome"}`),
	}
	encoded, err := EncodeInvokePayload(payload)
	if err != nil {
		t.Fatalf("EncodeInvokePayload failed: %v", err)
	}
	decoded, err := DecodeInvokePayload(encoded)
	if err != nil {
		t.Fatalf("DecodeInvokePayload failed: %v", err)
	}
	if decoded.GasLimit != payload.GasLimit ||
		decoded.CallNonce != payload.CallNonce ||
		decoded.Action != payload.Action ||
		!bytes.Equal(decoded.Param, payload.Param) {
		t.Fatalf("decoded payload mismatch: %#v", decoded)
	}
}
