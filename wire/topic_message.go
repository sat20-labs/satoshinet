package wire

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const (
	TopicMembershipJoin   = "JOIN"
	TopicMembershipLeave  = "LEAVE"
	TopicMembershipAdd    = "ADD"
	TopicMembershipRemove = "REMOVE"
	TopicMembershipKick   = "KICK"
	TopicMembershipBan    = "BAN"
	TopicMembershipRotate = "ROTATE"
)

var (
	topicPublishSignatureDomain    = []byte("satoshinet-message-topic-publish")
	topicKeyPackageSignatureDomain = []byte("satoshinet-topic-key-package")
	topicMembershipRequestDomain   = []byte("satoshinet-topic-membership-request")
	topicMembershipCommitDomain    = []byte("satoshinet-topic-membership-commit")
	topicCreateRequestDomain       = []byte("satoshinet-topic-create-request")
)

type TopicPublishMessage struct {
	TopicName       string `json:"topic_name"`
	SenderAccount   string `json:"sender_account"`
	SenderMsgID     uint64 `json:"sender_msg_id"`
	MessageID       string `json:"message_id"`
	KeySeq          uint64 `json:"key_seq"`
	Ciphertext      []byte `json:"ciphertext"`
	SenderSignature []byte `json:"sender_signature"`
}

type TopicKeyPackageRecipient struct {
	Recipient         string `json:"recipient"`
	EncryptedTopicKey []byte `json:"encrypted_topic_key"`
	IssuerSignature   []byte `json:"issuer_signature"`
}

type TopicKeyFanoutMessage struct {
	TopicName     string                     `json:"topic_name"`
	KeySeq        uint64                     `json:"key_seq"`
	IssuerAccount string                     `json:"issuer_account"`
	Recipients    []TopicKeyPackageRecipient `json:"recipients"`
}

type TopicMeta struct {
	TopicName       string `json:"topic_name"`
	DisplayName     string `json:"display_name"`
	OwnerAccount    string `json:"owner_account"`
	ServiceCoreNode string `json:"service_core_node"`
	ServicePubKey   []byte `json:"service_pub_key,omitempty"`
	JoinPolicy      string `json:"join_policy,omitempty"`
	MessagePolicy   string `json:"message_policy,omitempty"`
	MaxMembers      uint32 `json:"max_members,omitempty"`
	CreatedAtHeight uint64 `json:"created_at_height,omitempty"`
}

type TopicState struct {
	Status         string `json:"status"`
	KeySeq         uint64 `json:"key_seq"`
	MemberCount    uint32 `json:"member_count"`
	LastCommitHash string `json:"last_commit_hash,omitempty"`
}

type TopicMember struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"`
	Role      string `json:"role"`
	JoinedSeq uint64 `json:"joined_seq,omitempty"`
	LeftSeq   uint64 `json:"left_seq,omitempty"`
}

type TopicCreateRequest struct {
	Meta           TopicMeta `json:"meta"`
	OwnerSignature []byte    `json:"owner_signature"`
}

type TopicMembershipRequest struct {
	TopicName       string `json:"topic_name"`
	AccountID       string `json:"account_id"`
	ServiceCoreNode string `json:"service_core_node"`
	RequestType     string `json:"request_type"`
	Signature       []byte `json:"signature"`
}

type TopicMembershipCommit struct {
	TopicName       string                     `json:"topic_name"`
	ServiceCoreNode string                     `json:"service_core_node"`
	IssuerAccount   string                     `json:"issuer_account"`
	BaseKeySeq      uint64                     `json:"base_key_seq"`
	NewKeySeq       uint64                     `json:"new_key_seq"`
	ChangeType      string                     `json:"change_type"`
	TargetAccount   string                     `json:"target_account,omitempty"`
	KeyPackages     []TopicKeyPackageRecipient `json:"key_packages"`
	IssuerSignature []byte                     `json:"issuer_signature"`
}

type TopicJoinRejection struct {
	TopicName       string `json:"topic_name"`
	ServiceCoreNode string `json:"service_core_node"`
	OwnerAccount    string `json:"owner_account"`
	TargetAccount   string `json:"target_account"`
	OwnerSignature  []byte `json:"owner_signature"`
}

type TopicServiceSnapshot struct {
	Meta    TopicMeta     `json:"meta"`
	State   TopicState    `json:"state"`
	Members []TopicMember `json:"members"`
}

func validTopicAccountID(accountID string) bool {
	if len(accountID) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(accountID)
	return err == nil && len(decoded) == 32
}

func topicWriteBytes(buf *bytes.Buffer, value []byte) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], uint64(len(value)))
	buf.Write(scratch[:n])
	buf.Write(value)
}
func topicWriteString(buf *bytes.Buffer, value string) { topicWriteBytes(buf, []byte(value)) }
func topicWriteU64(buf *bytes.Buffer, value uint64) {
	var scratch [8]byte
	binary.BigEndian.PutUint64(scratch[:], value)
	buf.Write(scratch[:])
}
func topicReadBytes(r *bytes.Reader, max int) ([]byte, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil || length > uint64(max) || length > uint64(r.Len()) {
		return nil, fmt.Errorf("invalid topic message")
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(r, value); err != nil {
		return nil, fmt.Errorf("invalid topic message")
	}
	return value, nil
}
func topicReadString(r *bytes.Reader, max int) (string, error) {
	value, err := topicReadBytes(r, max)
	return string(value), err
}
func topicReadU64(r *bytes.Reader) (uint64, error) {
	var scratch [8]byte
	if _, err := io.ReadFull(r, scratch[:]); err != nil {
		return 0, fmt.Errorf("invalid topic message")
	}
	return binary.BigEndian.Uint64(scratch[:]), nil
}

func SerializeTopicPublishMessage(message *TopicPublishMessage, includeSignature bool) ([]byte, error) {
	if message == nil || message.TopicName == "" || len(message.TopicName) > 64 ||
		!validTopicAccountID(message.SenderAccount) || !ValidMessageID(message.MessageID) || len(message.Ciphertext) == 0 ||
		len(message.Ciphertext) > MaxDKVSValueSize {
		return nil, fmt.Errorf("invalid topic publish")
	}
	if includeSignature && (len(message.SenderSignature) == 0 || len(message.SenderSignature) > MaxDKVSSignatureSize) {
		return nil, fmt.Errorf("invalid topic publish signature")
	}
	var buf bytes.Buffer
	topicWriteString(&buf, message.TopicName)
	topicWriteString(&buf, message.SenderAccount)
	topicWriteU64(&buf, message.SenderMsgID)
	topicWriteString(&buf, message.MessageID)
	topicWriteU64(&buf, message.KeySeq)
	topicWriteBytes(&buf, message.Ciphertext)
	if includeSignature {
		topicWriteBytes(&buf, message.SenderSignature)
	}
	return buf.Bytes(), nil
}

func DeserializeTopicPublishMessage(encoded []byte) (*TopicPublishMessage, error) {
	r := bytes.NewReader(encoded)
	topic, err := topicReadString(r, 64)
	if err != nil || topic == "" {
		return nil, fmt.Errorf("invalid topic publish")
	}
	sender, err := topicReadString(r, 64)
	if err != nil || !validTopicAccountID(sender) {
		return nil, fmt.Errorf("invalid topic publish")
	}
	msgID, err := topicReadU64(r)
	if err != nil {
		return nil, err
	}
	messageID, err := topicReadString(r, MessageIDHexSize)
	if err != nil || !ValidMessageID(messageID) {
		return nil, fmt.Errorf("invalid topic message identity")
	}
	keySeq, err := topicReadU64(r)
	if err != nil {
		return nil, err
	}
	ciphertext, err := topicReadBytes(r, MaxDKVSValueSize)
	if err != nil || len(ciphertext) == 0 {
		return nil, fmt.Errorf("invalid topic publish")
	}
	sig, err := topicReadBytes(r, MaxDKVSSignatureSize)
	if err != nil || len(sig) == 0 || r.Len() != 0 {
		return nil, fmt.Errorf("invalid topic publish")
	}
	return &TopicPublishMessage{TopicName: topic, SenderAccount: sender, SenderMsgID: msgID, MessageID: messageID, KeySeq: keySeq, Ciphertext: ciphertext, SenderSignature: sig}, nil
}

func TopicPublishSigningHash(message *TopicPublishMessage) ([32]byte, error) {
	var zero [32]byte
	encoded, err := SerializeTopicPublishMessage(message, false)
	if err != nil {
		return zero, err
	}
	payload := append(append([]byte(nil), topicPublishSignatureDomain...), encoded...)
	return sha256.Sum256(payload), nil
}

func SerializeTopicKeyFanoutMessage(message *TopicKeyFanoutMessage) ([]byte, error) {
	if message == nil || message.TopicName == "" || len(message.TopicName) > 64 || !validTopicAccountID(message.IssuerAccount) ||
		len(message.Recipients) == 0 || len(message.Recipients) > 4096 {
		return nil, fmt.Errorf("invalid topic key fanout")
	}
	var buf bytes.Buffer
	topicWriteString(&buf, message.TopicName)
	topicWriteU64(&buf, message.KeySeq)
	topicWriteString(&buf, message.IssuerAccount)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(message.Recipients)))
	buf.Write(count[:])
	for _, item := range message.Recipients {
		if !validTopicAccountID(item.Recipient) || len(item.EncryptedTopicKey) == 0 || len(item.EncryptedTopicKey) > 4096 ||
			len(item.IssuerSignature) == 0 || len(item.IssuerSignature) > MaxDKVSSignatureSize {
			return nil, fmt.Errorf("invalid topic key package")
		}
		topicWriteString(&buf, item.Recipient)
		topicWriteBytes(&buf, item.EncryptedTopicKey)
		topicWriteBytes(&buf, item.IssuerSignature)
	}
	if buf.Len() > MaxDKVSNotifyDataSize {
		return nil, fmt.Errorf("topic key fanout too large")
	}
	return buf.Bytes(), nil
}

func DeserializeTopicKeyFanoutMessage(encoded []byte) (*TopicKeyFanoutMessage, error) {
	if len(encoded) == 0 || len(encoded) > MaxDKVSNotifyDataSize {
		return nil, fmt.Errorf("invalid topic key fanout")
	}
	r := bytes.NewReader(encoded)
	topic, err := topicReadString(r, 64)
	if err != nil || topic == "" {
		return nil, fmt.Errorf("invalid topic key fanout")
	}
	keySeq, err := topicReadU64(r)
	if err != nil || keySeq == 0 {
		return nil, fmt.Errorf("invalid topic key fanout")
	}
	issuer, err := topicReadString(r, 64)
	if err != nil || !validTopicAccountID(issuer) {
		return nil, fmt.Errorf("invalid topic key fanout")
	}
	var countBytes [8]byte
	if _, err := io.ReadFull(r, countBytes[:]); err != nil {
		return nil, fmt.Errorf("invalid topic key fanout")
	}
	count := binary.BigEndian.Uint64(countBytes[:])
	if count == 0 || count > 4096 {
		return nil, fmt.Errorf("invalid topic key fanout")
	}
	recipients := make([]TopicKeyPackageRecipient, 0, count)
	for n := uint64(0); n < count; n++ {
		recipient, err := topicReadString(r, 64)
		if err != nil || !validTopicAccountID(recipient) {
			return nil, fmt.Errorf("invalid topic key fanout")
		}
		encryptedKey, err := topicReadBytes(r, 4096)
		if err != nil || len(encryptedKey) == 0 {
			return nil, fmt.Errorf("invalid topic key fanout")
		}
		sig, err := topicReadBytes(r, MaxDKVSSignatureSize)
		if err != nil || len(sig) == 0 {
			return nil, fmt.Errorf("invalid topic key fanout")
		}
		recipients = append(recipients, TopicKeyPackageRecipient{Recipient: recipient, EncryptedTopicKey: encryptedKey, IssuerSignature: sig})
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("invalid topic key fanout")
	}
	return &TopicKeyFanoutMessage{TopicName: topic, KeySeq: keySeq, IssuerAccount: issuer, Recipients: recipients}, nil
}

func TopicKeyPackageSigningHash(topicName string, keySeq uint64, issuerAccount string,
	packageRecipient TopicKeyPackageRecipient) ([32]byte, error) {
	var zero [32]byte
	if topicName == "" || len(topicName) > 64 || !validTopicAccountID(issuerAccount) ||
		!validTopicAccountID(packageRecipient.Recipient) || len(packageRecipient.EncryptedTopicKey) == 0 ||
		len(packageRecipient.EncryptedTopicKey) > 4096 {
		return zero, fmt.Errorf("invalid topic key package")
	}
	var buf bytes.Buffer
	topicWriteString(&buf, topicName)
	topicWriteU64(&buf, keySeq)
	topicWriteString(&buf, packageRecipient.Recipient)
	topicWriteString(&buf, issuerAccount)
	topicWriteBytes(&buf, packageRecipient.EncryptedTopicKey)
	payload := append(append([]byte(nil), topicKeyPackageSignatureDomain...), buf.Bytes()...)
	return sha256.Sum256(payload), nil
}

func TopicCreateRequestSigningHash(request *TopicCreateRequest) ([32]byte, error) {
	var zero [32]byte
	if request == nil || request.Meta.TopicName == "" || len(request.Meta.TopicName) > 64 ||
		!validTopicAccountID(request.Meta.OwnerAccount) || request.Meta.ServiceCoreNode == "" {
		return zero, fmt.Errorf("invalid topic create request")
	}
	var buf bytes.Buffer
	topicWriteString(&buf, request.Meta.TopicName)
	topicWriteString(&buf, request.Meta.DisplayName)
	topicWriteString(&buf, request.Meta.OwnerAccount)
	topicWriteString(&buf, request.Meta.ServiceCoreNode)
	topicWriteBytes(&buf, request.Meta.ServicePubKey)
	topicWriteString(&buf, request.Meta.JoinPolicy)
	topicWriteString(&buf, request.Meta.MessagePolicy)
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], request.Meta.MaxMembers)
	buf.Write(count[:])
	topicWriteU64(&buf, request.Meta.CreatedAtHeight)
	payload := append(append([]byte(nil), topicCreateRequestDomain...), buf.Bytes()...)
	return sha256.Sum256(payload), nil
}

func TopicMembershipRequestSigningHash(request *TopicMembershipRequest) ([32]byte, error) {
	var zero [32]byte
	if request == nil || request.TopicName == "" || len(request.TopicName) > 64 ||
		!validTopicAccountID(request.AccountID) || request.ServiceCoreNode == "" ||
		(request.RequestType != TopicMembershipJoin && request.RequestType != TopicMembershipLeave) {
		return zero, fmt.Errorf("invalid topic membership request")
	}
	var buf bytes.Buffer
	topicWriteString(&buf, request.TopicName)
	topicWriteString(&buf, request.AccountID)
	topicWriteString(&buf, request.ServiceCoreNode)
	topicWriteString(&buf, request.RequestType)
	payload := append(append([]byte(nil), topicMembershipRequestDomain...), buf.Bytes()...)
	return sha256.Sum256(payload), nil
}

func TopicMembershipCommitSigningHash(commit *TopicMembershipCommit) ([32]byte, error) {
	var zero [32]byte
	if commit == nil || commit.TopicName == "" || len(commit.TopicName) > 64 || commit.ServiceCoreNode == "" ||
		!validTopicAccountID(commit.IssuerAccount) || commit.NewKeySeq != commit.BaseKeySeq+1 || commit.NewKeySeq == 0 {
		return zero, fmt.Errorf("invalid topic membership commit")
	}
	switch commit.ChangeType {
	case TopicMembershipAdd, TopicMembershipRemove, TopicMembershipKick, TopicMembershipBan:
		if !validTopicAccountID(commit.TargetAccount) {
			return zero, fmt.Errorf("invalid topic membership target")
		}
	case TopicMembershipRotate:
		if commit.TargetAccount != "" {
			return zero, fmt.Errorf("invalid topic rotation target")
		}
	default:
		return zero, fmt.Errorf("invalid topic membership change")
	}
	var buf bytes.Buffer
	topicWriteString(&buf, commit.TopicName)
	topicWriteString(&buf, commit.ServiceCoreNode)
	topicWriteString(&buf, commit.IssuerAccount)
	topicWriteU64(&buf, commit.BaseKeySeq)
	topicWriteU64(&buf, commit.NewKeySeq)
	topicWriteString(&buf, commit.ChangeType)
	topicWriteString(&buf, commit.TargetAccount)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(commit.KeyPackages)))
	buf.Write(count[:])
	for _, item := range commit.KeyPackages {
		if !validTopicAccountID(item.Recipient) || len(item.EncryptedTopicKey) == 0 || len(item.IssuerSignature) == 0 {
			return zero, fmt.Errorf("invalid topic key package")
		}
		topicWriteString(&buf, item.Recipient)
		topicWriteBytes(&buf, item.EncryptedTopicKey)
		topicWriteBytes(&buf, item.IssuerSignature)
	}
	payload := append(append([]byte(nil), topicMembershipCommitDomain...), buf.Bytes()...)
	return sha256.Sum256(payload), nil
}

func TopicJoinRejectionSigningHash(rejection *TopicJoinRejection) ([32]byte, error) {
	var zero [32]byte
	if rejection == nil || rejection.TopicName == "" || len(rejection.TopicName) > 64 || rejection.ServiceCoreNode == "" ||
		!validTopicAccountID(rejection.OwnerAccount) || !validTopicAccountID(rejection.TargetAccount) {
		return zero, fmt.Errorf("invalid topic join rejection")
	}
	var buf bytes.Buffer
	topicWriteString(&buf, rejection.TopicName)
	topicWriteString(&buf, rejection.ServiceCoreNode)
	topicWriteString(&buf, rejection.OwnerAccount)
	topicWriteString(&buf, rejection.TargetAccount)
	payload := append([]byte("satoshinet-topic-join-rejection"), buf.Bytes()...)
	return sha256.Sum256(payload), nil
}

func EncodeTopicJSON(value interface{}) ([]byte, error) { return json.Marshal(value) }
func DecodeTopicJSON(data []byte, value interface{}) error {
	if len(data) == 0 {
		return fmt.Errorf("empty topic payload")
	}
	return json.Unmarshal(data, value)
}
