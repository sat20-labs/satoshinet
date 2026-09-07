package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/wire"
)

type MessageBindingResolver interface {
	CoreNodeForAccount(accountID string) (string, error)
	AccountBoundToCore(accountID, coreNodeID string) bool
}

type MessageMailboxStore interface {
	AppendDirectMessage(message *DirectMessage) (bool, error)
	AppendTopicMessage(recipient string, message *TopicFanoutMessage) (bool, error)
	AppendTopicKeyPackage(recipient string, message *TopicKeyFanoutMessage, packageRecipient TopicKeyPackageRecipient) (bool, error)
}

type MessageRouter interface {
	RouteMessageNotify(message *wire.MsgDKVSNotify) error
}

// MessageUsageCharger is keyed by the stable (senderAccount, messageID) and
// must be idempotent. SenderMsgID is only the sequence between an account and
// its currently bound CoreNode.
type MessageUsageCharger interface {
	ChargeMessageSend(senderAccount, messageID string) error
}

type acceptedMessage struct {
	SenderAccount string
	SenderMsgID   uint64
	MessageID     string
	Digest        [32]byte
	Target        string
	Data          []byte
	Final         bool
	Status        MessageAckStatus
}

type MessageAcceptanceStore interface {
	NextSenderMsgID(senderAccount string) uint64
	Accept(senderAccount string, senderMsgID uint64, messageID string, digest [32]byte, target string, data []byte, charge func() error) (acceptedMessage, bool, error)
	MarkAck(senderAccount, messageID string, senderMsgID uint64, sourceCore string, status MessageAckStatus) error
}

type pendingMessageAcceptanceStore interface {
	Pending() ([]acceptedMessage, error)
}

type memoryMessageAcceptanceStore struct {
	mu       sync.Mutex
	next     map[string]uint64
	accepted map[string]acceptedMessage
}

func newMemoryMessageAcceptanceStore() *memoryMessageAcceptanceStore {
	return &memoryMessageAcceptanceStore{next: make(map[string]uint64), accepted: make(map[string]acceptedMessage)}
}

func acceptedMessageKey(senderAccount, messageID string) string {
	return senderAccount + ":" + messageID
}

func (s *memoryMessageAcceptanceStore) NextSenderMsgID(senderAccount string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next[senderAccount]
}

func (s *memoryMessageAcceptanceStore) Accept(senderAccount string, senderMsgID uint64, messageID string, digest [32]byte, target string, data []byte, charge func() error) (acceptedMessage, bool, error) {
	if !wire.ValidMessageID(messageID) {
		return acceptedMessage{}, false, ErrMessageInvalidEnvelope
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := acceptedMessageKey(senderAccount, messageID)
	if previous, ok := s.accepted[id]; ok {
		if previous.Digest != digest || previous.SenderMsgID != senderMsgID {
			return acceptedMessage{}, false, ErrMessageInvalidSequence
		}
		if !previous.Final && target != "" && previous.Target != target {
			previous.Target = target
			s.accepted[id] = previous
		}
		return previous, true, nil
	}
	expected := s.next[senderAccount]
	if senderMsgID != expected {
		return acceptedMessage{}, false, fmt.Errorf("%w: got=%d want=%d", ErrMessageInvalidSequence, senderMsgID, expected)
	}
	if expected == ^uint64(0) {
		return acceptedMessage{}, false, ErrMessageInvalidSequence
	}
	if charge != nil {
		if err := charge(); err != nil {
			return acceptedMessage{}, false, err
		}
	}
	accepted := acceptedMessage{
		SenderAccount: senderAccount,
		SenderMsgID:   senderMsgID,
		MessageID:     messageID,
		Digest:        digest,
		Target:        target,
		Data:          append([]byte(nil), data...),
	}
	s.accepted[id] = accepted
	s.next[senderAccount] = expected + 1
	return accepted, false, nil
}

func (s *memoryMessageAcceptanceStore) MarkAck(senderAccount, messageID string, senderMsgID uint64, sourceCore string, status MessageAckStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := acceptedMessageKey(senderAccount, messageID)
	accepted, ok := s.accepted[id]
	if !ok || accepted.SenderMsgID != senderMsgID {
		return ErrMessageInvalidSequence
	}
	if accepted.Target != sourceCore {
		return ErrMessageInvalidEnvelope
	}
	accepted.Status = status
	accepted.Final = status == MessageAckOK || status == MessageAckRejected
	s.accepted[id] = accepted
	return nil
}

func (s *memoryMessageAcceptanceStore) Pending() ([]acceptedMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := make([]acceptedMessage, 0)
	for _, message := range s.accepted {
		if message.Final {
			continue
		}
		copyMessage := message
		copyMessage.Data = append([]byte(nil), message.Data...)
		pending = append(pending, copyMessage)
	}
	return pending, nil
}

func (s *memoryMessageAcceptanceStore) PruneExpiredDirect(now time.Time) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, message := range s.accepted {
		if acceptedDirectExpired(message, now) {
			delete(s.accepted, key)
		}
	}
	return nil
}

type MessageManager struct {
	localCore      string
	bindings       MessageBindingResolver
	mailbox        MessageMailboxStore
	router         MessageRouter
	usage          MessageUsageCharger
	accepted       MessageAcceptanceStore
	topics         *TopicManager
	directLimiter  *directAdmissionLimiter
	directReplay   *directReplayWindow
	coreSigner     func([]byte) ([]byte, error)
	coreVerifier   func(string, string, *MessageAck) error
	coreAuthority  func(string) bool
	fanoutVerifier func(string, *MessageEnvelope) error
	queryGuard     *messageServiceQueryGuard
	retryMu        sync.Mutex
	retryNotBefore map[string]time.Time
}

func (m *MessageManager) SetCoreSigner(signer func([]byte) ([]byte, error)) {
	if m != nil {
		m.coreSigner = signer
	}
}

func (m *MessageManager) setCoreVerifier(verifier func(string, string, *MessageAck) error) {
	if m != nil && verifier != nil {
		m.coreVerifier = verifier
	}
}

func NewMessageManager(localCore string, bindings MessageBindingResolver, mailbox MessageMailboxStore, router MessageRouter, usage MessageUsageCharger, accepted MessageAcceptanceStore) *MessageManager {
	if accepted == nil {
		accepted = newMemoryMessageAcceptanceStore()
	}
	manager := &MessageManager{
		localCore: localCore, bindings: bindings, mailbox: mailbox, router: router,
		usage: usage, accepted: accepted,
		directLimiter:  newDirectAdmissionLimiter(DefaultDirectAdmissionPolicy()),
		directReplay:   newDirectReplayWindow(maxDirectReplayEntries, defaultDirectAcceptanceTTL),
		coreVerifier:   verifyCoreMessageAck,
		fanoutVerifier: verifyCoreTopicDelivery,
		queryGuard:     newMessageServiceQueryGuard(),
		retryNotBefore: make(map[string]time.Time),
	}
	manager.topics = NewTopicManager(manager)
	return manager
}

func (m *MessageManager) GetNextSenderMsgID(senderAccount string) uint64 {
	if m == nil || m.accepted == nil {
		return 0
	}
	return m.accepted.NextSenderMsgID(senderAccount)
}

func (m *MessageManager) routeAccepted(accepted acceptedMessage, duplicate bool) error {
	if m == nil || m.router == nil {
		return ErrMessageInvalidEnvelope
	}
	if duplicate && accepted.Final {
		return nil
	}
	return m.router.RouteMessageNotify(&wire.MsgDKVSNotify{
		Target:    accepted.Target,
		EventType: wire.DKVSNotifyEventMessage,
		Data:      append([]byte(nil), accepted.Data...),
	})
}

func (m *MessageManager) RetryPending() error {
	if m == nil || m.router == nil || m.accepted == nil {
		return ErrMessageInvalidEnvelope
	}
	store, ok := m.accepted.(pendingMessageAcceptanceStore)
	if !ok {
		return nil
	}
	if err := m.pruneExpiredDirectAcceptances(); err != nil {
		return err
	}
	pending, err := store.Pending()
	if err != nil {
		return err
	}
	var firstErr error
	for _, accepted := range pending {
		if !m.retryReady(accepted) {
			continue
		}
		if err := m.routeAccepted(accepted, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *MessageManager) SendDirectMessage(message *DirectMessage) error {
	if m == nil || m.bindings == nil || m.router == nil || m.accepted == nil {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyDirectMessageSignature(message); err != nil {
		return err
	}
	if !m.bindings.AccountBoundToCore(message.SenderAccount, m.localCore) {
		return ErrMessageNotBoundHere
	}
	if m.directLimiter != nil {
		if err := m.directLimiter.Admit("out", message.SenderAccount, message.RecipientAccount, len(message.Ciphertext)); err != nil {
			return err
		}
	}
	target, err := m.bindings.CoreNodeForAccount(message.RecipientAccount)
	if err != nil || target == "" {
		return ErrMessageBindingNotFound
	}
	payload, err := MarshalDirectMessage(message, true)
	if err != nil {
		return err
	}
	envelopeData, err := MarshalMessageEnvelope(&MessageEnvelope{MessageType: MessageTypeDirect, SourceCoreNode: m.localCore, Payload: payload})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(envelopeData)
	// Direct is free. Topic service authorization remains independent; no
	// MESSAGE_SEND entitlement is consumed here.
	accepted, duplicate, err := m.accepted.Accept(message.SenderAccount, message.SenderMsgID, message.MessageID, digest, target, envelopeData, nil)
	if err != nil {
		return err
	}
	return m.routeAccepted(accepted, duplicate)
}

func (m *MessageManager) HandleMessageNotify(message *wire.MsgDKVSNotify) error {
	if m == nil || message == nil || message.EventType != wire.DKVSNotifyEventMessage || message.Target != m.localCore {
		return ErrMessageInvalidEnvelope
	}
	envelope, err := UnmarshalMessageEnvelope(message.Data)
	if err != nil {
		return err
	}
	switch envelope.MessageType {
	case MessageTypeDirect:
		return m.handleIncomingDirect(envelope)
	case MessageTypeAck:
		return m.handleIncomingAck(envelope)
	case MessageTypeTopicJoin:
		return m.handleTopicMembershipRequest(envelope, wire.TopicMembershipJoin)
	case MessageTypeTopicLeave:
		return m.handleTopicMembershipRequest(envelope, wire.TopicMembershipLeave)
	case MessageTypeTopicJoinResult:
		return m.handleTopicJoinRejection(envelope)
	case MessageTypeTopicMembershipCommit:
		return m.handleTopicMembershipCommit(envelope)
	case MessageTypeTopicPublish:
		return m.topics.handlePublish(envelope)
	case MessageTypeTopicFanout:
		return m.handleTopicFanout(envelope)
	case MessageTypeTopicKeyFanout:
		return m.handleTopicKeyFanout(envelope)
	default:
		return ErrMessageUnsupportedType
	}
}

func (m *MessageManager) handleIncomingDirect(envelope *MessageEnvelope) error {
	message, err := UnmarshalDirectMessage(envelope.Payload)
	if err != nil {
		return err
	}
	if err := verifyDirectMessageSignature(message); err != nil {
		return err
	}
	if err := m.validateAccountSource(message.SenderAccount, envelope.SourceCoreNode); err != nil {
		return err
	}
	if m.bindings == nil || !m.bindings.AccountBoundToCore(message.RecipientAccount, m.localCore) {
		return m.sendAck(envelope.SourceCoreNode, &MessageAck{OriginalType: MessageTypeDirect, SenderAccount: message.SenderAccount, SenderMsgID: message.SenderMsgID, MessageID: message.MessageID, Status: MessageAckRetryable, ErrorCode: "RECIPIENT_MOVED"})
	}
	replayKey := directReplayKey(message)
	if m.directReplay != nil {
		duplicate, pending := m.directReplay.begin(replayKey, time.Now())
		if duplicate {
			return m.sendAck(envelope.SourceCoreNode, &MessageAck{OriginalType: MessageTypeDirect, SenderAccount: message.SenderAccount, SenderMsgID: message.SenderMsgID, MessageID: message.MessageID, Status: MessageAckOK})
		}
		if pending {
			return m.sendAck(envelope.SourceCoreNode, &MessageAck{OriginalType: MessageTypeDirect, SenderAccount: message.SenderAccount, SenderMsgID: message.SenderMsgID, MessageID: message.MessageID, Status: MessageAckRetryable, ErrorCode: "DIRECT_DELIVERY_IN_PROGRESS", RetryAfterMS: 100})
		}
	}
	if m.directLimiter != nil {
		if err := m.directLimiter.Admit("in", message.SenderAccount, message.RecipientAccount, len(message.Ciphertext)); err != nil {
			if m.directReplay != nil {
				m.directReplay.finish(replayKey, false, time.Now())
			}
			if ackErr := m.sendAck(envelope.SourceCoreNode, &MessageAck{
				OriginalType: MessageTypeDirect, SenderAccount: message.SenderAccount,
				SenderMsgID: message.SenderMsgID, MessageID: message.MessageID,
				Status: MessageAckRetryable, ErrorCode: "DIRECT_RATE_LIMITED",
				RetryAfterMS: directRetryAfterMS(err),
			}); ackErr != nil {
				return ackErr
			}
			return err
		}
	}
	if m.mailbox == nil {
		if m.directReplay != nil {
			m.directReplay.finish(replayKey, false, time.Now())
		}
		return ErrMessageInvalidEnvelope
	}
	_, err = m.mailbox.AppendDirectMessage(message)
	if m.directReplay != nil {
		m.directReplay.finish(replayKey, err == nil, time.Now())
	}
	if err != nil {
		status := MessageAckRetryable
		code := "MAILBOX_WRITE_FAILED"
		retryAfterMS := uint64(0)
		if errors.Is(err, ErrMessageMailboxFull) {
			code = "MAILBOX_FULL"
			retryAfterMS = uint64(time.Minute / time.Millisecond)
		}
		if ackErr := m.sendAck(envelope.SourceCoreNode, &MessageAck{OriginalType: MessageTypeDirect, SenderAccount: message.SenderAccount, SenderMsgID: message.SenderMsgID, MessageID: message.MessageID, Status: status, ErrorCode: code, RetryAfterMS: retryAfterMS}); ackErr != nil {
			return ackErr
		}
		return err
	}
	return m.sendAck(envelope.SourceCoreNode, &MessageAck{OriginalType: MessageTypeDirect, SenderAccount: message.SenderAccount, SenderMsgID: message.SenderMsgID, MessageID: message.MessageID, Status: MessageAckOK})
}

func directRetryAfterMS(err error) uint64 {
	var limited *DirectRateLimitError
	if !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		return 0
	}
	millis := limited.RetryAfter.Milliseconds()
	if millis <= 0 {
		return 1
	}
	return uint64(millis)
}

func (m *MessageManager) validateAccountSource(accountID, sourceCore string) error {
	if m == nil || m.bindings == nil || sourceCore == "" {
		return ErrMessageInvalidEnvelope
	}
	boundCore, err := m.bindings.CoreNodeForAccount(accountID)
	if err != nil || boundCore == "" {
		return ErrMessageBindingNotFound
	}
	if boundCore != sourceCore {
		return ErrMessageNotBoundHere
	}
	return nil
}

func (m *MessageManager) sendAck(target string, ack *MessageAck) error {
	if target == "" || m.router == nil || m.coreSigner == nil {
		return ErrMessageBindingNotFound
	}
	payloadToSign, err := messageAckSigningPayload(m.localCore, target, ack)
	if err != nil {
		return err
	}
	ack.Signature, err = m.coreSigner(payloadToSign)
	if err != nil || len(ack.Signature) == 0 {
		return ErrMessageInvalidSignature
	}
	payload, err := MarshalMessageAck(ack)
	if err != nil {
		return err
	}
	data, err := MarshalMessageEnvelope(&MessageEnvelope{MessageType: MessageTypeAck, SourceCoreNode: m.localCore, Payload: payload})
	if err != nil {
		return err
	}
	return m.router.RouteMessageNotify(&wire.MsgDKVSNotify{Target: target, EventType: wire.DKVSNotifyEventMessage, Data: data})
}

func (m *MessageManager) handleIncomingAck(envelope *MessageEnvelope) error {
	ack, err := UnmarshalMessageAck(envelope.Payload)
	if err != nil {
		return err
	}
	if m.coreVerifier == nil {
		return ErrMessageInvalidSignature
	}
	if err := m.coreVerifier(envelope.SourceCoreNode, m.localCore, ack); err != nil {
		return err
	}
	switch ack.OriginalType {
	case MessageTypeTopicFanout, MessageTypeTopicKeyFanout:
		return m.topics.handleDeliveryAck(envelope.SourceCoreNode, ack)
	default:
		m.recordRetryAdvice(ack)
		return m.accepted.MarkAck(ack.SenderAccount, ack.MessageID, ack.SenderMsgID, envelope.SourceCoreNode, ack.Status)
	}
}

func (m *MessageManager) recordRetryAdvice(ack *MessageAck) {
	if m == nil || ack == nil || !wire.ValidMessageID(ack.MessageID) {
		return
	}
	key := acceptedMessageKey(ack.SenderAccount, ack.MessageID)
	m.retryMu.Lock()
	defer m.retryMu.Unlock()
	if ack.Status == MessageAckRetryable && ack.RetryAfterMS != 0 {
		m.retryNotBefore[key] = time.Now().Add(time.Duration(ack.RetryAfterMS) * time.Millisecond)
		return
	}
	delete(m.retryNotBefore, key)
}

func (m *MessageManager) retryReady(accepted acceptedMessage) bool {
	if m == nil {
		return false
	}
	key := acceptedMessageKey(accepted.SenderAccount, accepted.MessageID)
	m.retryMu.Lock()
	defer m.retryMu.Unlock()
	notBefore, ok := m.retryNotBefore[key]
	if !ok {
		return true
	}
	if time.Now().Before(notBefore) {
		return false
	}
	delete(m.retryNotBefore, key)
	return true
}

func (m *MessageManager) handleTopicFanout(envelope *MessageEnvelope) error {
	if err := m.authorizeTopicDelivery(envelope); err != nil {
		return err
	}
	fanout, err := UnmarshalTopicFanout(envelope.Payload)
	if err != nil {
		return err
	}
	if err := verifyTopicFanoutSignature(fanout); err != nil {
		return err
	}
	if m.mailbox == nil || m.bindings == nil {
		return ErrMessageInvalidEnvelope
	}
	var firstErr error
	for _, recipient := range fanout.Recipients {
		if !m.bindings.AccountBoundToCore(recipient, m.localCore) {
			if firstErr == nil {
				firstErr = ErrMessageNotBoundHere
			}
			continue
		}
		if _, err := m.mailbox.AppendTopicMessage(recipient, fanout); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	status := MessageAckOK
	code := ""
	if firstErr != nil {
		status = MessageAckRetryable
		code = "TOPIC_FANOUT_INCOMPLETE"
	}
	ackErr := m.sendAck(envelope.SourceCoreNode, &MessageAck{
		OriginalType:  MessageTypeTopicFanout,
		SenderAccount: fanout.SenderAccount,
		SenderMsgID:   fanout.SenderMsgID,
		MessageID:     fanout.MessageID,
		Status:        status,
		ErrorCode:     code,
		DeliveryID:    topicDeliveryID(envelope.Payload),
	})
	if ackErr != nil {
		return ackErr
	}
	return firstErr
}

func (m *MessageManager) handleTopicKeyFanout(envelope *MessageEnvelope) error {
	if err := m.authorizeTopicDelivery(envelope); err != nil {
		return err
	}
	fanout, err := UnmarshalTopicKeyFanout(envelope.Payload)
	if err != nil {
		return err
	}
	if m.mailbox == nil || m.bindings == nil {
		return ErrMessageInvalidEnvelope
	}
	var firstErr error
	for _, packageRecipient := range fanout.Recipients {
		if err := verifyTopicKeyPackageSignature(fanout, packageRecipient); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !m.bindings.AccountBoundToCore(packageRecipient.Recipient, m.localCore) {
			if firstErr == nil {
				firstErr = ErrMessageNotBoundHere
			}
			continue
		}
		if _, err := m.mailbox.AppendTopicKeyPackage(packageRecipient.Recipient, fanout, packageRecipient); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	status := MessageAckOK
	code := ""
	if firstErr != nil {
		status = MessageAckRetryable
		code = "TOPIC_KEY_FANOUT_INCOMPLETE"
	}
	ackErr := m.sendAck(envelope.SourceCoreNode, &MessageAck{
		OriginalType:  MessageTypeTopicKeyFanout,
		SenderAccount: fanout.IssuerAccount,
		SenderMsgID:   fanout.KeySeq,
		Status:        status,
		ErrorCode:     code,
		DeliveryID:    topicDeliveryID(envelope.Payload),
	})
	if ackErr != nil {
		return ackErr
	}
	return firstErr
}

func (m *MessageManager) routeTopicControl(target string, messageType MessageType, payload []byte) error {
	if m == nil || m.router == nil || target == "" || len(payload) == 0 {
		return ErrMessageInvalidEnvelope
	}
	data, err := MarshalMessageEnvelope(&MessageEnvelope{
		MessageType: messageType, SourceCoreNode: m.localCore, Payload: payload,
	})
	if err != nil {
		return err
	}
	return m.router.RouteMessageNotify(&wire.MsgDKVSNotify{
		Target: target, EventType: wire.DKVSNotifyEventMessage, Data: data,
	})
}

func (m *MessageManager) SubmitTopicMembershipRequest(request *TopicMembershipRequest) error {
	if m == nil || m.bindings == nil || request == nil {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicMembershipRequestSignature(request); err != nil {
		return err
	}
	if !m.bindings.AccountBoundToCore(request.AccountID, m.localCore) {
		return ErrMessageNotBoundHere
	}
	payload, err := MarshalTopicMembershipRequest(request)
	if err != nil {
		return err
	}
	messageType := MessageTypeTopicJoin
	if request.RequestType == wire.TopicMembershipLeave {
		messageType = MessageTypeTopicLeave
	}
	return m.routeTopicControl(request.ServiceCoreNode, messageType, payload)
}

func (m *MessageManager) SubmitTopicJoinRejection(rejection *TopicJoinRejection) error {
	if m == nil || m.bindings == nil || rejection == nil {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicJoinRejectionSignature(rejection); err != nil {
		return err
	}
	if !m.bindings.AccountBoundToCore(rejection.OwnerAccount, m.localCore) {
		return ErrMessageNotBoundHere
	}
	payload, err := MarshalTopicJoinRejection(rejection)
	if err != nil {
		return err
	}
	return m.routeTopicControl(rejection.ServiceCoreNode, MessageTypeTopicJoinResult, payload)
}

func (m *MessageManager) SubmitTopicMembershipCommit(commit *TopicMembershipCommit) error {
	if m == nil || m.bindings == nil || commit == nil {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicMembershipCommitSignature(commit); err != nil {
		return err
	}
	if !m.bindings.AccountBoundToCore(commit.IssuerAccount, m.localCore) {
		return ErrMessageNotBoundHere
	}
	payload, err := MarshalTopicMembershipCommit(commit)
	if err != nil {
		return err
	}
	return m.routeTopicControl(commit.ServiceCoreNode, MessageTypeTopicMembershipCommit, payload)
}

func (m *MessageManager) handleTopicMembershipRequest(envelope *MessageEnvelope, expected string) error {
	request, err := UnmarshalTopicMembershipRequest(envelope.Payload)
	if err != nil {
		return err
	}
	if request.RequestType != expected || request.ServiceCoreNode != m.localCore {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicMembershipRequestSignature(request); err != nil {
		return err
	}
	if err := m.validateAccountSource(request.AccountID, envelope.SourceCoreNode); err != nil {
		return err
	}
	if expected == wire.TopicMembershipJoin {
		_, err = m.topics.RequestJoin(request.TopicName, request.AccountID)
	} else {
		_, err = m.topics.RequestLeave(request.TopicName, request.AccountID)
	}
	return err
}

func (m *MessageManager) handleTopicJoinRejection(envelope *MessageEnvelope) error {
	rejection, err := UnmarshalTopicJoinRejection(envelope.Payload)
	if err != nil {
		return err
	}
	if rejection.ServiceCoreNode != m.localCore {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicJoinRejectionSignature(rejection); err != nil {
		return err
	}
	if err := m.validateAccountSource(rejection.OwnerAccount, envelope.SourceCoreNode); err != nil {
		return err
	}
	return m.topics.RejectJoin(rejection)
}

func (m *MessageManager) handleTopicMembershipCommit(envelope *MessageEnvelope) error {
	commit, err := UnmarshalTopicMembershipCommit(envelope.Payload)
	if err != nil {
		return err
	}
	if commit.ServiceCoreNode != m.localCore {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicMembershipCommitSignature(commit); err != nil {
		return err
	}
	if err := m.validateAccountSource(commit.IssuerAccount, envelope.SourceCoreNode); err != nil {
		return err
	}
	return m.topics.CommitMembershipChange(commit)
}
