package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/sat20-labs/satoshinet/wire"
)

type managedTopic struct {
	meta    TopicMeta
	state   TopicState
	members map[string]TopicMember
}

type TopicServiceAuthorizer interface {
	AuthorizeTopicService(topic TopicMeta) error
}

type TopicPendingDelivery struct {
	DeliveryID    string
	MessageType   MessageType
	TopicName     string
	SenderAccount string
	SenderMsgID   uint64
	MessageID     string
	TargetCore    string
	Data          []byte
}

type TopicDeliveryStore interface {
	SavePending(deliveries []TopicPendingDelivery) error
	Acknowledge(deliveryID, targetCore string, status MessageAckStatus) error
	Pending() ([]TopicPendingDelivery, error)
}

type memoryTopicDeliveryStore struct {
	mu      sync.Mutex
	pending map[string]TopicPendingDelivery
}

func newMemoryTopicDeliveryStore() *memoryTopicDeliveryStore {
	return &memoryTopicDeliveryStore{pending: make(map[string]TopicPendingDelivery)}
}

func (s *memoryTopicDeliveryStore) SavePending(deliveries []TopicPendingDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, delivery := range deliveries {
		if delivery.DeliveryID == "" || delivery.TargetCore == "" || len(delivery.Data) == 0 {
			return ErrMessageInvalidEnvelope
		}
		if previous, ok := s.pending[delivery.DeliveryID]; ok {
			if previous.TargetCore != delivery.TargetCore || string(previous.Data) != string(delivery.Data) {
				return ErrMessageInvalidEnvelope
			}
			continue
		}
		copyDelivery := delivery
		copyDelivery.Data = append([]byte(nil), delivery.Data...)
		s.pending[delivery.DeliveryID] = copyDelivery
	}
	return nil
}

func (s *memoryTopicDeliveryStore) Acknowledge(deliveryID, targetCore string, status MessageAckStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delivery, ok := s.pending[deliveryID]
	if !ok {
		return nil
	}
	if delivery.TargetCore != targetCore {
		return ErrMessageInvalidEnvelope
	}
	// Only a successful mailbox commit retires Topic delivery responsibility.
	// REJECTED (for example MAILBOX_FULL) remains durable so a later policy or
	// capacity change can retry it without losing the recipient.
	if status == MessageAckOK {
		delete(s.pending, deliveryID)
	}
	return nil
}

func (s *memoryTopicDeliveryStore) Pending() ([]TopicPendingDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TopicPendingDelivery, 0, len(s.pending))
	for _, delivery := range s.pending {
		copyDelivery := delivery
		copyDelivery.Data = append([]byte(nil), delivery.Data...)
		out = append(out, copyDelivery)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeliveryID < out[j].DeliveryID })
	return out, nil
}

type TopicManager struct {
	manager    *MessageManager
	mu         sync.RWMutex
	topics     map[string]*managedTopic
	deliveries TopicDeliveryStore
	authorizer TopicServiceAuthorizer
	catalog    TopicCatalogStore
}

func NewTopicManager(manager *MessageManager) *TopicManager {
	return &TopicManager{
		manager:    manager,
		topics:     make(map[string]*managedTopic),
		deliveries: newMemoryTopicDeliveryStore(),
	}
}

func cloneManagedTopic(topic *managedTopic) *managedTopic {
	if topic == nil {
		return nil
	}
	copyTopic := &managedTopic{meta: topic.meta, state: topic.state, members: make(map[string]TopicMember, len(topic.members))}
	copyTopic.meta.ServicePubKey = append([]byte(nil), topic.meta.ServicePubKey...)
	for account, member := range topic.members {
		copyTopic.members[account] = member
	}
	return copyTopic
}

func topicMemberSlice(topic *managedTopic) []TopicMember {
	if topic == nil {
		return nil
	}
	members := make([]TopicMember, 0, len(topic.members))
	for _, member := range topic.members {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].AccountID < members[j].AccountID })
	return members
}

func topicMemberHoldsCurrentKey(member TopicMember) bool {
	return member.Status == "ACTIVE" || member.Status == "PENDING_LEAVE"
}

func topicMemberCanPublish(member TopicMember) bool {
	return member.Status == "ACTIVE"
}

func topicKeyHolderAccounts(topic *managedTopic) []string {
	if topic == nil {
		return nil
	}
	accounts := make([]string, 0, topic.state.MemberCount)
	for accountID, member := range topic.members {
		if topicMemberHoldsCurrentKey(member) {
			accounts = append(accounts, accountID)
		}
	}
	sort.Strings(accounts)
	return accounts
}

func validateTopicSnapshot(snapshot TopicCatalogSnapshot, localCore string) (*managedTopic, error) {
	if snapshot.Meta.TopicName == "" || snapshot.Meta.ServiceCoreNode != localCore || !validateAccountID(snapshot.Meta.OwnerAccount) ||
		snapshot.State.KeySeq == 0 || snapshot.State.Status == "" {
		return nil, ErrMessageInvalidEnvelope
	}
	topic := &managedTopic{meta: snapshot.Meta, state: snapshot.State, members: make(map[string]TopicMember, len(snapshot.Members))}
	keyHolders := uint32(0)
	ownerActive := false
	for _, member := range snapshot.Members {
		if !validateAccountID(member.AccountID) || member.Status == "" {
			return nil, ErrMessageInvalidEnvelope
		}
		switch member.Status {
		case "ACTIVE", "PENDING_LEAVE", "PENDING_JOIN", "LEFT", "BANNED":
		default:
			return nil, ErrMessageInvalidEnvelope
		}
		topic.members[member.AccountID] = member
		if topicMemberHoldsCurrentKey(member) {
			keyHolders++
		}
		if member.AccountID == snapshot.Meta.OwnerAccount && member.Role == "OWNER" && member.Status == "ACTIVE" {
			ownerActive = true
		}
	}
	if keyHolders != snapshot.State.MemberCount || !ownerActive {
		return nil, ErrMessageInvalidEnvelope
	}
	return topic, nil
}

func (m *TopicManager) SetCatalogStore(store TopicCatalogStore) error {
	if m == nil || store == nil || m.manager == nil {
		return ErrMessageInvalidEnvelope
	}
	snapshots, err := store.LoadTopics()
	if err != nil {
		return err
	}
	loaded := make(map[string]*managedTopic, len(snapshots))
	for _, snapshot := range snapshots {
		topic, err := validateTopicSnapshot(snapshot, m.manager.localCore)
		if err != nil {
			return err
		}
		if _, exists := loaded[topic.meta.TopicName]; exists {
			return ErrMessageInvalidEnvelope
		}
		loaded[topic.meta.TopicName] = topic
	}
	m.mu.Lock()
	m.catalog = store
	m.topics = loaded
	m.mu.Unlock()
	return nil
}

func (m *TopicManager) persistTopicLocked(topic *managedTopic) error {
	if m.catalog == nil {
		return nil
	}
	return m.catalog.SaveTopic(topic.meta, topic.state, topicMemberSlice(topic))
}

func (m *TopicManager) SetDeliveryStore(store TopicDeliveryStore) {
	if m == nil || store == nil {
		return
	}
	m.mu.Lock()
	m.deliveries = store
	m.mu.Unlock()
}

func (m *TopicManager) SetServiceAuthorizer(authorizer TopicServiceAuthorizer) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.authorizer = authorizer
	m.mu.Unlock()
}

func (m *TopicManager) authorizeTopic(topic *managedTopic) error {
	if topic == nil || topic.state.Status != "ACTIVE" {
		return ErrMessageTopicNotFound
	}
	if m.authorizer != nil {
		return m.authorizer.AuthorizeTopicService(topic.meta)
	}
	return nil
}

func sameTopicMeta(left, right TopicMeta) bool {
	return left.TopicName == right.TopicName && left.DisplayName == right.DisplayName &&
		left.OwnerAccount == right.OwnerAccount && left.ServiceCoreNode == right.ServiceCoreNode &&
		string(left.ServicePubKey) == string(right.ServicePubKey) && left.JoinPolicy == right.JoinPolicy &&
		left.MessagePolicy == right.MessagePolicy && left.MaxMembers == right.MaxMembers &&
		left.CreatedAtHeight == right.CreatedAtHeight
}

func (m *TopicManager) CreateTopic(meta TopicMeta) error {
	if m == nil || m.manager == nil || meta.TopicName == "" || len(meta.TopicName) > 64 || !validateAccountID(meta.OwnerAccount) || meta.ServiceCoreNode == "" {
		return ErrMessageInvalidEnvelope
	}
	if meta.ServiceCoreNode != m.manager.localCore || m.manager.bindings == nil || !m.manager.bindings.AccountBoundToCore(meta.OwnerAccount, m.manager.localCore) {
		return ErrMessageTopicPermission
	}
	if meta.MaxMembers == 0 {
		meta.MaxMembers = 1024
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, exists := m.topics[meta.TopicName]; exists {
		if sameTopicMeta(existing.meta, meta) {
			return nil
		}
		return ErrMessageTopicPermission
	}
	topic := &managedTopic{
		meta:    meta,
		state:   TopicState{Status: "ACTIVE", KeySeq: 1, MemberCount: 1},
		members: map[string]TopicMember{meta.OwnerAccount: {AccountID: meta.OwnerAccount, Status: "ACTIVE", Role: "OWNER", JoinedSeq: 1}},
	}
	if m.authorizer != nil {
		if err := m.authorizer.AuthorizeTopicService(meta); err != nil {
			return err
		}
	}
	if err := m.persistTopicLocked(topic); err != nil {
		return err
	}
	m.topics[meta.TopicName] = topic
	return nil
}

func (m *TopicManager) JoinTopic(topicName, accountID string) (uint64, error) {
	return m.RequestJoin(topicName, accountID)
}

func (m *TopicManager) RequestJoin(topicName, accountID string) (uint64, error) {
	if !validateAccountID(accountID) {
		return 0, ErrMessageInvalidEnvelope
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	topic := m.topics[topicName]
	if err := m.authorizeTopic(topic); err != nil {
		return 0, err
	}
	if member, ok := topic.members[accountID]; ok {
		switch member.Status {
		case "ACTIVE", "PENDING_LEAVE", "PENDING_JOIN":
			return topic.state.KeySeq, nil
		case "BANNED":
			return 0, ErrMessageTopicPermission
		}
	}
	if topic.meta.MaxMembers != 0 && topic.state.MemberCount >= topic.meta.MaxMembers {
		return 0, ErrMessageTopicPermission
	}
	candidate := cloneManagedTopic(topic)
	candidate.members[accountID] = TopicMember{AccountID: accountID, Status: "PENDING_JOIN", Role: "MEMBER"}
	if err := m.persistTopicLocked(candidate); err != nil {
		return 0, err
	}
	m.topics[topicName] = candidate
	return candidate.state.KeySeq, nil
}

func (m *TopicManager) RejectJoin(rejection *TopicJoinRejection) error {
	if m == nil || rejection == nil {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicJoinRejectionSignature(rejection); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	topic := m.topics[rejection.TopicName]
	if topic == nil || topic.meta.ServiceCoreNode != m.manager.localCore || rejection.ServiceCoreNode != m.manager.localCore ||
		topic.meta.OwnerAccount != rejection.OwnerAccount {
		return ErrMessageTopicPermission
	}
	member, ok := topic.members[rejection.TargetAccount]
	if ok && member.Status == "LEFT" && member.Role == "MEMBER" && member.JoinedSeq == 0 && member.LeftSeq == 0 {
		return nil
	}
	if err := m.authorizeTopic(topic); err != nil {
		return err
	}
	if !ok || member.Status != "PENDING_JOIN" {
		return ErrMessageTopicPermission
	}
	candidate := cloneManagedTopic(topic)
	member.Status = "LEFT"
	candidate.members[rejection.TargetAccount] = member
	if err := m.persistTopicLocked(candidate); err != nil {
		return err
	}
	m.topics[rejection.TopicName] = candidate
	return nil
}

func (m *TopicManager) LeaveTopic(topicName, accountID string) (uint64, error) {
	return m.RequestLeave(topicName, accountID)
}

func (m *TopicManager) RequestLeave(topicName, accountID string) (uint64, error) {
	if !validateAccountID(accountID) {
		return 0, ErrMessageInvalidEnvelope
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	topic := m.topics[topicName]
	if err := m.authorizeTopic(topic); err != nil {
		return 0, err
	}
	member, ok := topic.members[accountID]
	if !ok || member.Status == "LEFT" {
		return topic.state.KeySeq, nil
	}
	if member.Role == "OWNER" {
		return 0, ErrMessageTopicPermission
	}
	candidate := cloneManagedTopic(topic)
	switch member.Status {
	case "PENDING_JOIN":
		member.Status = "LEFT"
		candidate.members[accountID] = member
	case "ACTIVE":
		member.Status = "PENDING_LEAVE"
		candidate.members[accountID] = member
	case "PENDING_LEAVE":
		return topic.state.KeySeq, nil
	case "BANNED":
		return topic.state.KeySeq, nil
	default:
		return 0, ErrMessageTopicPermission
	}
	if err := m.persistTopicLocked(candidate); err != nil {
		return 0, err
	}
	m.topics[topicName] = candidate
	return candidate.state.KeySeq, nil
}

func (m *TopicManager) TopicSnapshot(topicName string) (TopicServiceSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	topic := m.topics[topicName]
	if topic == nil {
		return TopicServiceSnapshot{}, ErrMessageTopicNotFound
	}
	return TopicServiceSnapshot{Meta: topic.meta, State: topic.state, Members: topicMemberSlice(topic)}, nil
}

func (m *TopicManager) TopicState(topicName string) (TopicState, []TopicMember, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	topic := m.topics[topicName]
	if topic == nil {
		return TopicState{}, nil, ErrMessageTopicNotFound
	}
	return topic.state, topicMemberSlice(topic), nil
}

func (m *TopicManager) SendTopicMessage(message *TopicPublishMessage) error {
	if m == nil || m.manager == nil || m.manager.bindings == nil || m.manager.router == nil {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicMessageSignature(message); err != nil {
		return err
	}
	if !m.manager.bindings.AccountBoundToCore(message.SenderAccount, m.manager.localCore) {
		return ErrMessageNotBoundHere
	}
	m.mu.RLock()
	topic := m.topics[message.TopicName]
	if topic == nil {
		m.mu.RUnlock()
		return ErrMessageTopicNotFound
	}
	host := topic.meta.ServiceCoreNode
	m.mu.RUnlock()
	return m.sendTopicMessageToHost(message, host)
}

// SendTopicMessageToHost is used by a sender CoreNode that learned the Topic
// Host from the topic directory/state but does not host the Topic itself.
func (m *TopicManager) SendTopicMessageToHost(message *TopicPublishMessage, host string) error {
	if m == nil || m.manager == nil || m.manager.bindings == nil || m.manager.router == nil || host == "" {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicMessageSignature(message); err != nil {
		return err
	}
	if !m.manager.bindings.AccountBoundToCore(message.SenderAccount, m.manager.localCore) {
		return ErrMessageNotBoundHere
	}
	return m.sendTopicMessageToHost(message, host)
}

func (m *TopicManager) sendTopicMessageToHost(message *TopicPublishMessage, host string) error {
	payload, err := MarshalTopicPublish(message, true)
	if err != nil {
		return err
	}
	data, err := MarshalMessageEnvelope(&MessageEnvelope{MessageType: MessageTypeTopicPublish, SourceCoreNode: m.manager.localCore, Payload: payload})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	accepted, duplicate, err := m.manager.accepted.Accept(message.SenderAccount, message.SenderMsgID, message.MessageID, digest, host, data, func() error {
		if m.manager.usage == nil {
			return nil
		}
		return m.manager.usage.ChargeMessageSend(message.SenderAccount, message.MessageID)
	})
	if err != nil {
		return err
	}
	return m.manager.routeAccepted(accepted, duplicate)
}

func topicDeliveryID(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (m *TopicManager) handlePublish(envelope *MessageEnvelope) error {
	publish, err := UnmarshalTopicPublish(envelope.Payload)
	if err != nil {
		return err
	}
	if err := verifyTopicMessageSignature(publish); err != nil {
		return err
	}
	if err := m.manager.validateAccountSource(publish.SenderAccount, envelope.SourceCoreNode); err != nil {
		return err
	}
	reject := func(cause error, status MessageAckStatus, code string) error {
		ackErr := m.manager.sendAck(envelope.SourceCoreNode, &MessageAck{
			OriginalType: MessageTypeTopicPublish, SenderAccount: publish.SenderAccount,
			SenderMsgID: publish.SenderMsgID, MessageID: publish.MessageID, Status: status, ErrorCode: code,
		})
		if ackErr != nil {
			return ackErr
		}
		return cause
	}

	m.mu.RLock()
	topic := m.topics[publish.TopicName]
	if topic == nil || topic.meta.ServiceCoreNode != m.manager.localCore {
		m.mu.RUnlock()
		return reject(ErrMessageTopicNotFound, MessageAckRejected, "TOPIC_NOT_FOUND")
	}
	if err := m.authorizeTopic(topic); err != nil {
		m.mu.RUnlock()
		return reject(err, MessageAckRetryable, "TOPIC_SERVICE_UNAVAILABLE")
	}
	member, ok := topic.members[publish.SenderAccount]
	if !ok || !topicMemberCanPublish(member) {
		m.mu.RUnlock()
		return reject(ErrMessageTopicPermission, MessageAckRejected, "TOPIC_PERMISSION")
	}
	if topic.state.KeySeq != publish.KeySeq {
		m.mu.RUnlock()
		return reject(ErrMessageTopicKeyMismatch, MessageAckRejected, "TOPIC_KEY_MISMATCH")
	}
	recipients := make([]string, 0, topic.state.MemberCount)
	for accountID, member := range topic.members {
		if member.Status == "ACTIVE" && accountID != publish.SenderAccount {
			recipients = append(recipients, accountID)
		}
	}
	m.mu.RUnlock()

	groups := make(map[string][]string)
	for _, recipient := range recipients {
		coreNode, err := m.manager.bindings.CoreNodeForAccount(recipient)
		if err != nil || coreNode == "" {
			return reject(ErrMessageBindingNotFound, MessageAckRetryable, "TOPIC_RECIPIENT_BINDING")
		}
		groups[coreNode] = append(groups[coreNode], recipient)
	}
	cores := make([]string, 0, len(groups))
	for coreNode := range groups {
		cores = append(cores, coreNode)
	}
	sort.Strings(cores)
	pending := make([]TopicPendingDelivery, 0, len(cores))
	for _, coreNode := range cores {
		batches, err := m.buildFanoutBatches(coreNode, publish, groups[coreNode])
		if err != nil {
			return reject(err, MessageAckRetryable, "TOPIC_FANOUT_BUILD")
		}
		pending = append(pending, batches...)
	}
	if m.deliveries == nil {
		return reject(ErrMessageInvalidEnvelope, MessageAckRetryable, "TOPIC_DELIVERY_STORE")
	}
	if err := m.deliveries.SavePending(pending); err != nil {
		return reject(err, MessageAckRetryable, "TOPIC_PENDING_STORE")
	}
	// Sender success is bounded by the durable Service CoreNode pending commit,
	// not by downstream member availability.
	if err := m.manager.sendAck(envelope.SourceCoreNode, &MessageAck{
		OriginalType: MessageTypeTopicPublish, SenderAccount: publish.SenderAccount,
		SenderMsgID: publish.SenderMsgID, MessageID: publish.MessageID, Status: MessageAckOK,
	}); err != nil {
		return err
	}
	return m.retryDeliveries(pending)
}

func (m *TopicManager) buildFanoutBatches(coreNode string, publish *TopicPublishMessage, recipients []string) ([]TopicPendingDelivery, error) {
	pending := make([]TopicPendingDelivery, 0, 1)
	for len(recipients) != 0 {
		lo, hi := 1, len(recipients)
		var bestData []byte
		best := 0
		for lo <= hi {
			mid := lo + (hi-lo)/2
			fanout := &TopicFanoutMessage{
				TopicName: publish.TopicName, SenderAccount: publish.SenderAccount,
				SenderMsgID: publish.SenderMsgID, MessageID: publish.MessageID, KeySeq: publish.KeySeq,
				Ciphertext: publish.Ciphertext, SenderSignature: publish.SenderSignature,
				Recipients: recipients[:mid],
			}
			payload, err := MarshalTopicFanout(fanout)
			if err != nil {
				hi = mid - 1
				continue
			}
			data, err := m.manager.marshalTopicDelivery(MessageTypeTopicFanout, coreNode, payload)
			if err == nil && len(data) <= wire.MaxDKVSNotifyDataSize {
				best, bestData = mid, data
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if best == 0 {
			return nil, fmt.Errorf("%w: fanout envelope", ErrMessageTooLarge)
		}
		envelope, err := UnmarshalMessageEnvelope(bestData)
		if err != nil {
			return nil, err
		}
		pending = append(pending, TopicPendingDelivery{
			DeliveryID: topicDeliveryID(envelope.Payload), MessageType: MessageTypeTopicFanout,
			TopicName: publish.TopicName, SenderAccount: publish.SenderAccount,
			SenderMsgID: publish.SenderMsgID, MessageID: publish.MessageID, TargetCore: coreNode, Data: bestData,
		})
		recipients = recipients[best:]
	}
	return pending, nil
}

func (m *TopicManager) FanoutTopicKeyPackages(message *TopicKeyFanoutMessage) error {
	if m == nil || m.manager == nil || m.manager.bindings == nil || m.deliveries == nil || message == nil {
		return ErrMessageInvalidEnvelope
	}
	m.mu.RLock()
	topic := m.topics[message.TopicName]
	if topic == nil || topic.meta.ServiceCoreNode != m.manager.localCore || topic.state.KeySeq != message.KeySeq {
		m.mu.RUnlock()
		return ErrMessageTopicKeyMismatch
	}
	issuer, ok := topic.members[message.IssuerAccount]
	if !ok || issuer.Status != "ACTIVE" || issuer.Role != "OWNER" {
		m.mu.RUnlock()
		return ErrMessageTopicPermission
	}
	activeMembers := make(map[string]struct{}, topic.state.MemberCount)
	for accountID, member := range topic.members {
		if topicMemberHoldsCurrentKey(member) {
			activeMembers[accountID] = struct{}{}
		}
	}
	m.mu.RUnlock()
	groups := make(map[string][]TopicKeyPackageRecipient)
	for _, item := range message.Recipients {
		if _, ok := activeMembers[item.Recipient]; !ok {
			return ErrMessageTopicPermission
		}
		if err := verifyTopicKeyPackageSignature(message, item); err != nil {
			return err
		}
		core, err := m.manager.bindings.CoreNodeForAccount(item.Recipient)
		if err != nil || core == "" {
			return ErrMessageBindingNotFound
		}
		groups[core] = append(groups[core], item)
	}
	pending := make([]TopicPendingDelivery, 0, len(groups))
	for core, recipients := range groups {
		for len(recipients) != 0 {
			best := 0
			var bestData []byte
			for n := 1; n <= len(recipients); n++ {
				batch := &TopicKeyFanoutMessage{TopicName: message.TopicName, KeySeq: message.KeySeq, IssuerAccount: message.IssuerAccount, Recipients: recipients[:n]}
				payload, err := MarshalTopicKeyFanout(batch)
				if err != nil {
					break
				}
				data, err := m.manager.marshalTopicDelivery(MessageTypeTopicKeyFanout, core, payload)
				if err != nil || len(data) > wire.MaxDKVSNotifyDataSize {
					break
				}
				best, bestData = n, data
			}
			if best == 0 {
				return ErrMessageTooLarge
			}
			envelope, err := UnmarshalMessageEnvelope(bestData)
			if err != nil {
				return err
			}
			pending = append(pending, TopicPendingDelivery{
				DeliveryID: topicDeliveryID(envelope.Payload), MessageType: MessageTypeTopicKeyFanout,
				TopicName: message.TopicName, SenderAccount: message.IssuerAccount,
				SenderMsgID: message.KeySeq, TargetCore: core, Data: bestData,
			})
			recipients = recipients[best:]
		}
	}
	if err := m.deliveries.SavePending(pending); err != nil {
		return err
	}
	return m.retryDeliveries(pending)
}

func (m *TopicManager) retryDeliveries(deliveries []TopicPendingDelivery) error {
	var firstErr error
	for _, delivery := range deliveries {
		if err := m.manager.router.RouteMessageNotify(&wire.MsgDKVSNotify{
			Target: delivery.TargetCore, EventType: wire.DKVSNotifyEventMessage,
			Data: append([]byte(nil), delivery.Data...),
		}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *TopicManager) RetryPending() error {
	if m == nil || m.deliveries == nil {
		return nil
	}
	pending, err := m.deliveries.Pending()
	if err != nil {
		return err
	}
	return m.retryDeliveries(pending)
}

func (m *TopicManager) handleDeliveryAck(sourceCore string, ack *MessageAck) error {
	if m == nil || m.deliveries == nil || ack == nil || ack.DeliveryID == "" {
		return ErrMessageInvalidEnvelope
	}
	pending, err := m.deliveries.Pending()
	if err != nil {
		return err
	}
	for _, delivery := range pending {
		if delivery.DeliveryID != ack.DeliveryID {
			continue
		}
		if delivery.MessageType != ack.OriginalType || delivery.SenderAccount != ack.SenderAccount ||
			delivery.SenderMsgID != ack.SenderMsgID || delivery.MessageID != ack.MessageID {
			return ErrMessageInvalidEnvelope
		}
		break
	}
	return m.deliveries.Acknowledge(ack.DeliveryID, sourceCore, ack.Status)
}

func (m *TopicManager) rollbackPreparedTopicDeliveries(deliveries []TopicPendingDelivery) {
	if m == nil || m.deliveries == nil {
		return
	}
	for _, delivery := range deliveries {
		_ = m.deliveries.Acknowledge(delivery.DeliveryID, delivery.TargetCore, MessageAckOK)
	}
}

func topicPackageRecipientsExact(packages []TopicKeyPackageRecipient, expected []string) bool {
	if len(packages) != len(expected) {
		return false
	}
	actual := make([]string, 0, len(packages))
	seen := make(map[string]struct{}, len(packages))
	for _, item := range packages {
		if _, ok := seen[item.Recipient]; ok {
			return false
		}
		seen[item.Recipient] = struct{}{}
		actual = append(actual, item.Recipient)
	}
	sort.Strings(actual)
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

// CommitMembershipChange is the only operation that changes the effective
// Topic key-holder set or KeySeq. The Service CoreNode never creates TopicKey;
// it only validates an owner-signed commit and the complete post-change
// KeyPackage set, stages durable deliveries, commits membership+KeySeq, then
// releases those staged packages for fan-out.
func (m *TopicManager) CommitMembershipChange(commit *TopicMembershipCommit) error {
	if m == nil || m.manager == nil || m.manager.bindings == nil || m.deliveries == nil || commit == nil {
		return ErrMessageInvalidEnvelope
	}
	if err := verifyTopicMembershipCommitSignature(commit); err != nil {
		return err
	}
	commitHash, err := wire.TopicMembershipCommitSigningHash(commit)
	if err != nil {
		return ErrMessageInvalidEnvelope
	}
	commitHashHex := hex.EncodeToString(commitHash[:])

	m.mu.Lock()
	topic := m.topics[commit.TopicName]
	if topic != nil && commit.NewKeySeq == topic.state.KeySeq && topic.state.LastCommitHash == commitHashHex {
		m.mu.Unlock()
		return nil
	}
	if err := m.authorizeTopic(topic); err != nil {
		m.mu.Unlock()
		return err
	}
	if topic.meta.ServiceCoreNode != m.manager.localCore || commit.ServiceCoreNode != m.manager.localCore ||
		commit.BaseKeySeq != topic.state.KeySeq || commit.NewKeySeq != topic.state.KeySeq+1 {
		m.mu.Unlock()
		return ErrMessageTopicKeyMismatch
	}
	issuer, issuerOK := topic.members[commit.IssuerAccount]
	if !issuerOK || issuer.Status != "ACTIVE" {
		m.mu.Unlock()
		return ErrMessageTopicPermission
	}
	candidate := cloneManagedTopic(topic)
	switch commit.ChangeType {
	case wire.TopicMembershipAdd:
		if commit.IssuerAccount != topic.meta.OwnerAccount {
			m.mu.Unlock()
			return ErrMessageTopicPermission
		}
		member, ok := candidate.members[commit.TargetAccount]
		if !ok || member.Status != "PENDING_JOIN" ||
			(candidate.meta.MaxMembers != 0 && candidate.state.MemberCount >= candidate.meta.MaxMembers) {
			m.mu.Unlock()
			return ErrMessageTopicPermission
		}
		member.Status = "ACTIVE"
		member.Role = "MEMBER"
		member.JoinedSeq = commit.NewKeySeq
		member.LeftSeq = 0
		candidate.members[commit.TargetAccount] = member
		candidate.state.MemberCount++
	case wire.TopicMembershipRemove:
		member, ok := candidate.members[commit.TargetAccount]
		if !ok || member.Role == "OWNER" || member.Status != "PENDING_LEAVE" || commit.IssuerAccount == commit.TargetAccount {
			m.mu.Unlock()
			return ErrMessageTopicPermission
		}
		member.Status = "LEFT"
		member.LeftSeq = commit.NewKeySeq
		candidate.members[commit.TargetAccount] = member
		if candidate.state.MemberCount <= 1 {
			m.mu.Unlock()
			return ErrMessageInvalidEnvelope
		}
		candidate.state.MemberCount--
	case wire.TopicMembershipKick, wire.TopicMembershipBan:
		if commit.IssuerAccount != topic.meta.OwnerAccount {
			m.mu.Unlock()
			return ErrMessageTopicPermission
		}
		member, ok := candidate.members[commit.TargetAccount]
		if !ok || member.Role == "OWNER" || !topicMemberHoldsCurrentKey(member) {
			m.mu.Unlock()
			return ErrMessageTopicPermission
		}
		if commit.ChangeType == wire.TopicMembershipBan {
			member.Status = "BANNED"
		} else {
			member.Status = "LEFT"
		}
		member.LeftSeq = commit.NewKeySeq
		candidate.members[commit.TargetAccount] = member
		if candidate.state.MemberCount <= 1 {
			m.mu.Unlock()
			return ErrMessageInvalidEnvelope
		}
		candidate.state.MemberCount--
	case wire.TopicMembershipRotate:
		if commit.IssuerAccount != topic.meta.OwnerAccount {
			m.mu.Unlock()
			return ErrMessageTopicPermission
		}
	default:
		m.mu.Unlock()
		return ErrMessageInvalidEnvelope
	}
	candidate.state.KeySeq = commit.NewKeySeq
	candidate.state.LastCommitHash = commitHashHex
	expected := topicKeyHolderAccounts(candidate)
	if uint32(len(expected)) != candidate.state.MemberCount || !topicPackageRecipientsExact(commit.KeyPackages, expected) {
		m.mu.Unlock()
		return ErrMessageTopicPermission
	}
	fanout := &TopicKeyFanoutMessage{
		TopicName: commit.TopicName, KeySeq: commit.NewKeySeq,
		IssuerAccount: commit.IssuerAccount, Recipients: append([]TopicKeyPackageRecipient(nil), commit.KeyPackages...),
	}
	for _, item := range fanout.Recipients {
		if err := verifyTopicKeyPackageSignature(fanout, item); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	deliveries, err := m.resolveTopicKeyFanout(fanout)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if err := m.deliveries.SavePending(deliveries); err != nil {
		m.mu.Unlock()
		return err
	}
	if err := m.persistTopicLocked(candidate); err != nil {
		m.rollbackPreparedTopicDeliveries(deliveries)
		m.mu.Unlock()
		return err
	}
	m.topics[commit.TopicName] = candidate
	m.mu.Unlock()
	return m.retryDeliveries(deliveries)
}
