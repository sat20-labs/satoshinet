package wire

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

const (
	// MessageServicePath is exposed by the CoreNode STP HTTP service. The HTTP
	// layer is only a transport bridge; authorization remains in SatoshiNet.
	MessageServicePath = "/message/service"

	MessageServiceActionBindAccount     = "BIND_ACCOUNT"
	MessageServiceActionNextMessage     = "NEXT_MESSAGE_ID"
	MessageServiceActionSendDirect      = "SEND_DIRECT"
	MessageServiceActionDeleteMailbox   = "DELETE_MAILBOX"
	MessageServiceActionCreateTopic     = "CREATE_TOPIC"
	MessageServiceActionTopicState      = "TOPIC_STATE"
	MessageServiceActionTopicJoin       = "TOPIC_JOIN"
	MessageServiceActionTopicLeave      = "TOPIC_LEAVE"
	MessageServiceActionTopicRejectJoin = "TOPIC_REJECT_JOIN"
	MessageServiceActionTopicCommit     = "TOPIC_MEMBERSHIP_COMMIT"
	MessageServiceActionTopicPublish    = "TOPIC_PUBLISH"

	// A Direct message may carry a large application payload such as an RGB11
	// consignment. Keep explicit headroom for DirectMessage + MessageEnvelope
	// framing below MaxDKVSNotifyDataSize.
	MaxDirectMessageCiphertextSize = MaxDKVSBlobValueSize - 4*1024
	MessageIDHexSize               = 32
)

type MessageServiceRequest struct {
	Action         string              `json:"action"`
	Record         *DKVSRecord         `json:"record,omitempty"`
	AccountID      string              `json:"account_id,omitempty"`
	Direct         *DirectMessage      `json:"direct,omitempty"`
	TargetCoreNode string              `json:"target_core_node,omitempty"`
	TopicName      string              `json:"topic_name,omitempty"`
	Payload        []byte              `json:"payload,omitempty"`
	Auth           *MessageServiceAuth `json:"auth,omitempty"`
}

// MessageServiceAuth authorizes account-scoped read operations. The nonce is
// single-use at the CoreNode until ExpiresAtMS and prevents captured requests
// from being replayed.
type MessageServiceAuth struct {
	Nonce       string `json:"nonce"`
	ExpiresAtMS uint64 `json:"expires_at_ms"`
	Signature   []byte `json:"signature"`
}

type MessageServiceResponse struct {
	Code            int    `json:"code"`
	Msg             string `json:"msg,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	RetryAfterMS    uint64 `json:"retry_after_ms,omitempty"`
	NextSenderMsgID uint64 `json:"next_sender_msg_id,omitempty"`
	Payload         []byte `json:"payload,omitempty"`
}

type DirectMessage struct {
	SenderAccount    string `json:"sender_account"`
	SenderMsgID      uint64 `json:"sender_msg_id"`
	MessageID        string `json:"message_id"`
	RecipientAccount string `json:"recipient_account"`
	Ciphertext       []byte `json:"ciphertext"`
	SenderSignature  []byte `json:"sender_signature,omitempty"`
}

// ValidMessageID accepts the canonical time-sortable message identity: eight
// bytes of Unix microseconds followed by eight bytes of cryptographic entropy.
func ValidMessageID(messageID string) bool {
	if len(messageID) != MessageIDHexSize || messageID != strings.ToLower(messageID) {
		return false
	}
	decoded, err := hex.DecodeString(messageID)
	return err == nil && len(decoded) == MessageIDHexSize/2
}

var directMessageSignatureDomain = []byte("satoshinet-message-direct")

var messageServiceQuerySignatureDomain = []byte("satoshinet-message-service-query-v1")

// MessageServiceQuerySigningHash is shared by wallets and CoreNodes. Only the
// two account-scoped read actions use it; adding request fields later cannot
// silently weaken the authorization boundary.
func MessageServiceQuerySigningHash(request *MessageServiceRequest) ([32]byte, error) {
	var zero [32]byte
	if request == nil || request.Auth == nil || !validMessageAccountID(request.AccountID) ||
		request.Auth.Nonce == "" || request.Auth.ExpiresAtMS == 0 {
		return zero, errors.New("invalid message service authorization")
	}
	action := strings.ToUpper(strings.TrimSpace(request.Action))
	if action != MessageServiceActionNextMessage && action != MessageServiceActionTopicState {
		return zero, errors.New("message service action does not support query authorization")
	}
	if action == MessageServiceActionTopicState && strings.TrimSpace(request.TopicName) == "" {
		return zero, errors.New("invalid topic query")
	}
	var buf bytes.Buffer
	writeDirectBytes(&buf, messageServiceQuerySignatureDomain)
	writeDirectBytes(&buf, []byte(action))
	writeDirectBytes(&buf, []byte(request.AccountID))
	writeDirectBytes(&buf, []byte(strings.TrimSpace(request.TopicName)))
	writeDirectBytes(&buf, []byte(request.Auth.Nonce))
	var expiry [8]byte
	binary.BigEndian.PutUint64(expiry[:], request.Auth.ExpiresAtMS)
	buf.Write(expiry[:])
	return sha256.Sum256(buf.Bytes()), nil
}

func validMessageAccountID(accountID string) bool {
	if len(accountID) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(accountID)
	return err == nil && len(decoded) == 32
}

func writeDirectBytes(buf *bytes.Buffer, value []byte) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], uint64(len(value)))
	buf.Write(scratch[:n])
	buf.Write(value)
}

func readDirectBytes(r *bytes.Reader, max int) ([]byte, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil || length > uint64(max) || length > uint64(r.Len()) {
		return nil, errors.New("invalid direct message field")
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(r, value); err != nil {
		return nil, errors.New("truncated direct message field")
	}
	return value, nil
}

// SerializeDirectMessage is the canonical Direct-message encoding used by both
// wallet signatures and the CoreNode MessageManager.
func SerializeDirectMessage(message *DirectMessage, includeSignature bool) ([]byte, error) {
	if message == nil || !validMessageAccountID(message.SenderAccount) || !ValidMessageID(message.MessageID) ||
		!validMessageAccountID(message.RecipientAccount) || len(message.Ciphertext) == 0 {
		return nil, errors.New("invalid direct message")
	}
	if len(message.Ciphertext) > MaxDirectMessageCiphertextSize {
		return nil, errors.New("direct message ciphertext too large")
	}
	if includeSignature && (len(message.SenderSignature) == 0 || len(message.SenderSignature) > MaxDKVSSignatureSize) {
		return nil, errors.New("invalid direct message signature")
	}
	var buf bytes.Buffer
	writeDirectBytes(&buf, []byte(message.SenderAccount))
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], message.SenderMsgID)
	buf.Write(id[:])
	writeDirectBytes(&buf, []byte(message.MessageID))
	writeDirectBytes(&buf, []byte(message.RecipientAccount))
	writeDirectBytes(&buf, message.Ciphertext)
	if includeSignature {
		writeDirectBytes(&buf, message.SenderSignature)
	}
	return buf.Bytes(), nil
}

func DeserializeDirectMessage(encoded []byte) (*DirectMessage, error) {
	r := bytes.NewReader(encoded)
	sender, err := readDirectBytes(r, 64)
	if err != nil || !validMessageAccountID(string(sender)) {
		return nil, errors.New("invalid direct message sender")
	}
	var id [8]byte
	if _, err := io.ReadFull(r, id[:]); err != nil {
		return nil, errors.New("missing direct message id")
	}
	messageID, err := readDirectBytes(r, MessageIDHexSize)
	if err != nil || !ValidMessageID(string(messageID)) {
		return nil, errors.New("invalid direct message identity")
	}
	recipient, err := readDirectBytes(r, 64)
	if err != nil || !validMessageAccountID(string(recipient)) {
		return nil, errors.New("invalid direct message recipient")
	}
	ciphertext, err := readDirectBytes(r, MaxDirectMessageCiphertextSize)
	if err != nil || len(ciphertext) == 0 {
		return nil, errors.New("invalid direct message ciphertext")
	}
	signature, err := readDirectBytes(r, MaxDKVSSignatureSize)
	if err != nil || len(signature) == 0 || r.Len() != 0 {
		return nil, errors.New("invalid direct message signature")
	}
	return &DirectMessage{
		SenderAccount: string(sender), SenderMsgID: binary.BigEndian.Uint64(id[:]),
		MessageID: string(messageID), RecipientAccount: string(recipient), Ciphertext: ciphertext, SenderSignature: signature,
	}, nil
}

func DirectMessageSigningHash(message *DirectMessage) ([32]byte, error) {
	var zero [32]byte
	encoded, err := SerializeDirectMessage(message, false)
	if err != nil {
		return zero, err
	}
	payload := make([]byte, 0, len(directMessageSignatureDomain)+len(encoded))
	payload = append(payload, directMessageSignatureDomain...)
	payload = append(payload, encoded...)
	return sha256.Sum256(payload), nil
}
