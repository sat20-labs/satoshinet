package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"

	"github.com/sat20-labs/satoshinet/wire"
)

const (
	maxMessageRecipientsPerEnvelope = 4096
	maxTopicKeyPackageSize          = 4096
)

func writeMessageBytes(buf *bytes.Buffer, value []byte) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], uint64(len(value)))
	buf.Write(scratch[:n])
	buf.Write(value)
}

func writeMessageString(buf *bytes.Buffer, value string) { writeMessageBytes(buf, []byte(value)) }

func readMessageBytes(r *bytes.Reader, max int) ([]byte, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil || length > uint64(max) || length > uint64(r.Len()) {
		return nil, ErrMessageInvalidEnvelope
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(r, value); err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	return value, nil
}

func readMessageString(r *bytes.Reader, max int) (string, error) {
	value, err := readMessageBytes(r, max)
	return string(value), err
}

func writeMessageUint64(buf *bytes.Buffer, value uint64) {
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], value)
	buf.Write(scratch[:])
}

func readMessageUint64(r *bytes.Reader) (uint64, error) {
	var scratch [8]byte
	if _, err := io.ReadFull(r, scratch[:]); err != nil {
		return 0, ErrMessageInvalidEnvelope
	}
	return binary.BigEndian.Uint64(scratch[:]), nil
}

func writeMessageCount(buf *bytes.Buffer, count int) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], uint64(count))
	buf.Write(scratch[:n])
}

func validateAccountID(accountID string) bool {
	if len(accountID) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(accountID)
	return err == nil && len(decoded) == 32
}

func MarshalMessageEnvelope(envelope *MessageEnvelope) ([]byte, error) {
	if envelope == nil || envelope.MessageType == 0 || len(envelope.Payload) == 0 ||
		len(envelope.SourceCoreNode) > wire.MaxDKVSNotifyTargetSize || len(envelope.Payload) > wire.MaxDKVSNotifyDataSize {
		return nil, ErrMessageInvalidEnvelope
	}
	var buf bytes.Buffer
	buf.WriteByte(byte(envelope.MessageType))
	writeMessageString(&buf, envelope.SourceCoreNode)
	writeMessageBytes(&buf, envelope.Payload)
	if envelope.MessageType == MessageTypeTopicFanout || envelope.MessageType == MessageTypeTopicKeyFanout {
		if len(envelope.Signature) == 0 || len(envelope.Signature) > wire.MaxDKVSSignatureSize {
			return nil, ErrMessageInvalidSignature
		}
		writeMessageBytes(&buf, envelope.Signature)
	}
	if buf.Len() > wire.MaxDKVSNotifyDataSize {
		return nil, ErrMessageTooLarge
	}
	return buf.Bytes(), nil
}

func UnmarshalMessageEnvelope(encoded []byte) (*MessageEnvelope, error) {
	if len(encoded) == 0 || len(encoded) > wire.MaxDKVSNotifyDataSize {
		return nil, ErrMessageInvalidEnvelope
	}
	r := bytes.NewReader(encoded)
	typeByte, err := r.ReadByte()
	if err != nil || typeByte == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	source, err := readMessageString(r, wire.MaxDKVSNotifyTargetSize)
	if err != nil {
		return nil, err
	}
	payload, err := readMessageBytes(r, wire.MaxDKVSNotifyDataSize)
	if err != nil || len(payload) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	envelope := &MessageEnvelope{MessageType: MessageType(typeByte), SourceCoreNode: source, Payload: payload}
	if envelope.MessageType == MessageTypeTopicFanout || envelope.MessageType == MessageTypeTopicKeyFanout {
		envelope.Signature, err = readMessageBytes(r, wire.MaxDKVSSignatureSize)
		if err != nil || len(envelope.Signature) == 0 {
			return nil, ErrMessageInvalidSignature
		}
	}
	if r.Len() != 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	return envelope, nil
}

func MarshalDirectMessage(message *DirectMessage, includeSignature bool) ([]byte, error) {
	encoded, err := wire.SerializeDirectMessage(message, includeSignature)
	if err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	return encoded, nil
}

func UnmarshalDirectMessage(encoded []byte) (*DirectMessage, error) {
	message, err := wire.DeserializeDirectMessage(encoded)
	if err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	return message, nil
}

func serializeMessageAck(ack *MessageAck, includeSignature bool) ([]byte, error) {
	if ack == nil || ack.OriginalType == 0 || !validateAccountID(ack.SenderAccount) || ack.Status == 0 || len(ack.ErrorCode) > 128 || len(ack.DeliveryID) > 64 {
		return nil, ErrMessageInvalidEnvelope
	}
	if includeSignature && (len(ack.Signature) == 0 || len(ack.Signature) > wire.MaxDKVSSignatureSize) {
		return nil, ErrMessageInvalidSignature
	}
	requiresMessageID := ack.OriginalType == MessageTypeDirect || ack.OriginalType == MessageTypeTopicPublish || ack.OriginalType == MessageTypeTopicFanout
	if (requiresMessageID && !wire.ValidMessageID(ack.MessageID)) || (!requiresMessageID && ack.MessageID != "" && !wire.ValidMessageID(ack.MessageID)) {
		return nil, ErrMessageInvalidEnvelope
	}
	if ack.DeliveryID != "" && len(ack.DeliveryID) != 64 {
		return nil, ErrMessageInvalidEnvelope
	}
	var buf bytes.Buffer
	buf.WriteByte(byte(ack.OriginalType))
	writeMessageString(&buf, ack.SenderAccount)
	writeMessageUint64(&buf, ack.SenderMsgID)
	writeMessageString(&buf, ack.MessageID)
	buf.WriteByte(byte(ack.Status))
	writeMessageString(&buf, ack.ErrorCode)
	writeMessageString(&buf, ack.DeliveryID)
	writeMessageUint64(&buf, ack.RetryAfterMS)
	if includeSignature {
		writeMessageBytes(&buf, ack.Signature)
	}
	return buf.Bytes(), nil
}

func MarshalMessageAck(ack *MessageAck) ([]byte, error) {
	return serializeMessageAck(ack, true)
}

func UnmarshalMessageAck(encoded []byte) (*MessageAck, error) {
	r := bytes.NewReader(encoded)
	original, err := r.ReadByte()
	if err != nil || original == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	sender, err := readMessageString(r, 64)
	if err != nil || !validateAccountID(sender) {
		return nil, ErrMessageInvalidEnvelope
	}
	msgID, err := readMessageUint64(r)
	if err != nil {
		return nil, err
	}
	messageID, err := readMessageString(r, wire.MessageIDHexSize)
	if err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	status, err := r.ReadByte()
	if err != nil || status == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	errorCode, err := readMessageString(r, 128)
	if err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	deliveryID, err := readMessageString(r, 64)
	if err != nil || (deliveryID != "" && len(deliveryID) != 64) {
		return nil, ErrMessageInvalidEnvelope
	}
	retryAfterMS, err := readMessageUint64(r)
	if err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	signature, err := readMessageBytes(r, wire.MaxDKVSSignatureSize)
	if err != nil || len(signature) == 0 || r.Len() != 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	originalType := MessageType(original)
	requiresMessageID := originalType == MessageTypeDirect || originalType == MessageTypeTopicPublish || originalType == MessageTypeTopicFanout
	if (requiresMessageID && !wire.ValidMessageID(messageID)) || (!requiresMessageID && messageID != "" && !wire.ValidMessageID(messageID)) {
		return nil, ErrMessageInvalidEnvelope
	}
	return &MessageAck{OriginalType: originalType, SenderAccount: sender, SenderMsgID: msgID, MessageID: messageID, Status: MessageAckStatus(status), ErrorCode: errorCode, DeliveryID: deliveryID, RetryAfterMS: retryAfterMS, Signature: signature}, nil
}

func MarshalTopicPublish(message *TopicPublishMessage, includeSignature bool) ([]byte, error) {
	encoded, err := wire.SerializeTopicPublishMessage(message, includeSignature)
	if err != nil {
		if len(message.Ciphertext) > wire.MaxDKVSValueSize {
			return nil, ErrMessageTooLarge
		}
		return nil, ErrMessageInvalidEnvelope
	}
	return encoded, nil
}

func UnmarshalTopicPublish(encoded []byte) (*TopicPublishMessage, error) {
	message, err := wire.DeserializeTopicPublishMessage(encoded)
	if err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	return message, nil
}

func MarshalTopicFanout(message *TopicFanoutMessage) ([]byte, error) {
	if message == nil || message.TopicName == "" || len(message.TopicName) > 64 || !validateAccountID(message.SenderAccount) ||
		!wire.ValidMessageID(message.MessageID) ||
		len(message.Ciphertext) == 0 || len(message.Ciphertext) > wire.MaxDKVSValueSize || len(message.SenderSignature) == 0 ||
		len(message.SenderSignature) > wire.MaxDKVSSignatureSize || len(message.Recipients) == 0 || len(message.Recipients) > maxMessageRecipientsPerEnvelope {
		return nil, ErrMessageInvalidEnvelope
	}
	var buf bytes.Buffer
	writeMessageString(&buf, message.TopicName)
	writeMessageString(&buf, message.SenderAccount)
	writeMessageUint64(&buf, message.SenderMsgID)
	writeMessageString(&buf, message.MessageID)
	writeMessageUint64(&buf, message.KeySeq)
	writeMessageBytes(&buf, message.Ciphertext)
	writeMessageBytes(&buf, message.SenderSignature)
	writeMessageCount(&buf, len(message.Recipients))
	for _, recipient := range message.Recipients {
		if !validateAccountID(recipient) {
			return nil, ErrMessageInvalidEnvelope
		}
		writeMessageString(&buf, recipient)
	}
	if buf.Len() > wire.MaxDKVSNotifyDataSize {
		return nil, ErrMessageTooLarge
	}
	return buf.Bytes(), nil
}

func UnmarshalTopicFanout(encoded []byte) (*TopicFanoutMessage, error) {
	r := bytes.NewReader(encoded)
	topic, err := readMessageString(r, 64)
	if err != nil || topic == "" {
		return nil, ErrMessageInvalidEnvelope
	}
	sender, err := readMessageString(r, 64)
	if err != nil || !validateAccountID(sender) {
		return nil, ErrMessageInvalidEnvelope
	}
	msgID, err := readMessageUint64(r)
	if err != nil {
		return nil, err
	}
	messageID, err := readMessageString(r, wire.MessageIDHexSize)
	if err != nil || !wire.ValidMessageID(messageID) {
		return nil, ErrMessageInvalidEnvelope
	}
	keySeq, err := readMessageUint64(r)
	if err != nil {
		return nil, err
	}
	ciphertext, err := readMessageBytes(r, wire.MaxDKVSValueSize)
	if err != nil || len(ciphertext) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	signature, err := readMessageBytes(r, wire.MaxDKVSSignatureSize)
	if err != nil || len(signature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	count, err := binary.ReadUvarint(r)
	if err != nil || count == 0 || count > maxMessageRecipientsPerEnvelope {
		return nil, ErrMessageInvalidEnvelope
	}
	recipients := make([]string, 0, count)
	for n := uint64(0); n < count; n++ {
		recipient, err := readMessageString(r, 64)
		if err != nil || !validateAccountID(recipient) {
			return nil, ErrMessageInvalidEnvelope
		}
		recipients = append(recipients, recipient)
	}
	if r.Len() != 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	return &TopicFanoutMessage{TopicName: topic, SenderAccount: sender, SenderMsgID: msgID, MessageID: messageID, KeySeq: keySeq, Ciphertext: ciphertext, SenderSignature: signature, Recipients: recipients}, nil
}

func MarshalTopicKeyFanout(message *TopicKeyFanoutMessage) ([]byte, error) {
	encoded, err := wire.SerializeTopicKeyFanoutMessage(message)
	if err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	return encoded, nil
}

func UnmarshalTopicKeyFanout(encoded []byte) (*TopicKeyFanoutMessage, error) {
	message, err := wire.DeserializeTopicKeyFanoutMessage(encoded)
	if err != nil {
		return nil, ErrMessageInvalidEnvelope
	}
	return message, nil
}

func MarshalTopicCreateRequest(request *TopicCreateRequest) ([]byte, error) {
	if request == nil || len(request.OwnerSignature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	encoded, err := wire.EncodeTopicJSON(request)
	if err != nil || len(encoded) > wire.MaxDKVSNotifyDataSize {
		return nil, ErrMessageInvalidEnvelope
	}
	return encoded, nil
}
func UnmarshalTopicCreateRequest(encoded []byte) (*TopicCreateRequest, error) {
	var request TopicCreateRequest
	if wire.DecodeTopicJSON(encoded, &request) != nil || len(request.OwnerSignature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	return &request, nil
}
func MarshalTopicMembershipRequest(request *TopicMembershipRequest) ([]byte, error) {
	if request == nil || len(request.Signature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	encoded, err := wire.EncodeTopicJSON(request)
	if err != nil || len(encoded) > wire.MaxDKVSNotifyDataSize {
		return nil, ErrMessageInvalidEnvelope
	}
	return encoded, nil
}
func UnmarshalTopicMembershipRequest(encoded []byte) (*TopicMembershipRequest, error) {
	var request TopicMembershipRequest
	if wire.DecodeTopicJSON(encoded, &request) != nil || len(request.Signature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	return &request, nil
}
func MarshalTopicMembershipCommit(commit *TopicMembershipCommit) ([]byte, error) {
	if commit == nil || len(commit.IssuerSignature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	encoded, err := wire.EncodeTopicJSON(commit)
	if err != nil || len(encoded) > wire.MaxDKVSNotifyDataSize {
		return nil, ErrMessageInvalidEnvelope
	}
	return encoded, nil
}
func UnmarshalTopicMembershipCommit(encoded []byte) (*TopicMembershipCommit, error) {
	var commit TopicMembershipCommit
	if wire.DecodeTopicJSON(encoded, &commit) != nil || len(commit.IssuerSignature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	return &commit, nil
}

func MarshalTopicJoinRejection(rejection *TopicJoinRejection) ([]byte, error) {
	if rejection == nil || len(rejection.OwnerSignature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	encoded, err := wire.EncodeTopicJSON(rejection)
	if err != nil || len(encoded) > wire.MaxDKVSNotifyDataSize {
		return nil, ErrMessageInvalidEnvelope
	}
	return encoded, nil
}

func UnmarshalTopicJoinRejection(encoded []byte) (*TopicJoinRejection, error) {
	var rejection TopicJoinRejection
	if wire.DecodeTopicJSON(encoded, &rejection) != nil || len(rejection.OwnerSignature) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	return &rejection, nil
}
