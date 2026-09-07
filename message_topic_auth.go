package main

import (
	"bytes"
	"encoding/hex"

	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
)

func topicDeliverySigningPayload(target string, envelope *MessageEnvelope) ([]byte, error) {
	if target == "" || envelope == nil || envelope.SourceCoreNode == "" || len(envelope.Payload) == 0 ||
		(envelope.MessageType != MessageTypeTopicFanout && envelope.MessageType != MessageTypeTopicKeyFanout) {
		return nil, ErrMessageInvalidEnvelope
	}
	var payload bytes.Buffer
	writeMessageString(&payload, "satoshinet-topic-delivery-v1")
	writeMessageString(&payload, envelope.SourceCoreNode)
	writeMessageString(&payload, target)
	payload.WriteByte(byte(envelope.MessageType))
	writeMessageBytes(&payload, envelope.Payload)
	return payload.Bytes(), nil
}

// The service CoreNode attests that its TopicManager authorized this delivery.
// The signature survives Bootstrap relay and binds the full recipient list;
// a peer's self-reported ValidatorId or an account signature is not authority.
func (m *MessageManager) marshalTopicDelivery(kind MessageType, target string, body []byte) ([]byte, error) {
	if m == nil || m.coreSigner == nil {
		return nil, ErrMessageInvalidSignature
	}
	envelope := &MessageEnvelope{MessageType: kind, SourceCoreNode: m.localCore, Payload: body}
	payload, err := topicDeliverySigningPayload(target, envelope)
	if err != nil {
		return nil, err
	}
	envelope.Signature, err = m.coreSigner(payload)
	if err != nil {
		return nil, err
	}
	return MarshalMessageEnvelope(envelope)
}

func verifyCoreTopicDelivery(target string, envelope *MessageEnvelope) error {
	payload, err := topicDeliverySigningPayload(target, envelope)
	if err != nil {
		return err
	}
	encoded, err := hex.DecodeString(envelope.SourceCoreNode)
	if err != nil {
		return ErrMessageInvalidSignature
	}
	pubKey, err := btcec.ParsePubKey(encoded)
	if err != nil {
		return ErrMessageInvalidSignature
	}
	sig, err := ecdsa.ParseDERSignature(envelope.Signature)
	if err != nil || !anchortx.VerifyMessage(pubKey, payload, sig) {
		return ErrMessageInvalidSignature
	}
	return nil
}

func (m *MessageManager) authorizeTopicDelivery(envelope *MessageEnvelope) error {
	if m == nil || envelope == nil || m.coreAuthority == nil || !m.coreAuthority(envelope.SourceCoreNode) {
		return ErrMessageTopicPermission
	}
	if m.fanoutVerifier == nil {
		return ErrMessageInvalidSignature
	}
	return m.fanoutVerifier(m.localCore, envelope)
}
