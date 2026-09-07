package main

import (
	"errors"

	"github.com/sat20-labs/satoshinet/wire"
)

type MessageType uint8

const (
	MessageTypeDirect MessageType = iota + 1
	MessageTypeTopicJoin
	MessageTypeTopicJoinResult
	MessageTypeTopicLeave
	MessageTypeTopicPublish
	MessageTypeTopicFanout
	MessageTypeTopicKeyFanout
	MessageTypeAck
	MessageTypeTopicMembershipCommit
)

type MessageAckStatus uint8

const (
	MessageAckOK MessageAckStatus = iota + 1
	MessageAckRetryable
	MessageAckRejected
)

var (
	ErrMessageInvalidEnvelope  = errors.New("invalid message envelope")
	ErrMessageInvalidSignature = errors.New("invalid message signature")
	ErrMessageInvalidSequence  = errors.New("invalid message sequence")
	ErrMessageNotBoundHere     = errors.New("message account is not bound to this core node")
	ErrMessageBindingNotFound  = errors.New("message account binding not found")
	ErrMessageMailboxFull      = errors.New("message mailbox full")
	ErrMessageTopicNotFound    = errors.New("message topic not found")
	ErrMessageTopicPermission  = errors.New("message topic permission denied")
	ErrMessageTopicKeyMismatch = errors.New("message topic key sequence mismatch")
	ErrMessageTooLarge         = errors.New("message payload too large")
	ErrMessageRateLimited      = errors.New("direct message rate limited")
	ErrMessageUnsupportedType  = errors.New("unsupported message type")
)

type MessageEnvelope struct {
	MessageType    MessageType
	SourceCoreNode string
	Payload        []byte
	// Topic fan-outs carry the service CoreNode's authorization over the whole
	// delivery, including its recipients and destination CoreNode.
	Signature []byte
}

type DirectMessage = wire.DirectMessage

type MessageAck struct {
	OriginalType  MessageType
	SenderAccount string
	SenderMsgID   uint64
	MessageID     string
	Status        MessageAckStatus
	ErrorCode     string
	RetryAfterMS  uint64
	// DeliveryID is only needed for Topic fan-out/key-package batches. It is
	// the hex SHA-256 of the canonical fan-out payload and is empty for normal
	// Direct/TopicPublish ACKs.
	DeliveryID string
	// Signature is produced by the CoreNode that generated this ACK. It covers
	// both CoreNode identities and every ACK field above.
	Signature []byte
}

type TopicPublishMessage = wire.TopicPublishMessage

type TopicKeyPackageRecipient = wire.TopicKeyPackageRecipient

type TopicKeyFanoutMessage = wire.TopicKeyFanoutMessage

type TopicMeta = wire.TopicMeta

type TopicState = wire.TopicState

type TopicMember = wire.TopicMember

type TopicCreateRequest = wire.TopicCreateRequest

type TopicMembershipRequest = wire.TopicMembershipRequest

type TopicMembershipCommit = wire.TopicMembershipCommit

type TopicJoinRejection = wire.TopicJoinRejection

type TopicServiceSnapshot = wire.TopicServiceSnapshot

type TopicFanoutMessage struct {
	TopicName       string
	SenderAccount   string
	SenderMsgID     uint64
	MessageID       string
	KeySeq          uint64
	Ciphertext      []byte
	SenderSignature []byte
	Recipients      []string
}
