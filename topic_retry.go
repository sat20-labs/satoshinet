package main

import (
	"bytes"
	"sort"

	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

type topicDeliveryReplaceStore interface {
	ReplacePending(deliveryID, targetCore string, replacements []TopicPendingDelivery) (bool, error)
}

func cloneTopicPendingDelivery(delivery TopicPendingDelivery) TopicPendingDelivery {
	copyDelivery := delivery
	copyDelivery.Data = append([]byte(nil), delivery.Data...)
	return copyDelivery
}

func (s *memoryTopicDeliveryStore) ReplacePending(deliveryID, targetCore string, replacements []TopicPendingDelivery) (bool, error) {
	if s == nil || deliveryID == "" || targetCore == "" {
		return false, ErrMessageInvalidEnvelope
	}
	encoded := make(map[string]TopicPendingDelivery, len(replacements))
	for _, replacement := range replacements {
		if replacement.DeliveryID == "" || replacement.TargetCore == "" || len(replacement.Data) == 0 {
			return false, ErrMessageInvalidEnvelope
		}
		encoded[replacement.DeliveryID] = cloneTopicPendingDelivery(replacement)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.pending[deliveryID]
	if !ok {
		return false, nil
	}
	if current.TargetCore != targetCore {
		return false, ErrMessageInvalidEnvelope
	}
	for id, replacement := range encoded {
		if previous, exists := s.pending[id]; exists && id != deliveryID {
			if previous.TargetCore != replacement.TargetCore || !bytes.Equal(previous.Data, replacement.Data) {
				return false, ErrMessageInvalidEnvelope
			}
		}
	}
	if _, retained := encoded[deliveryID]; !retained {
		delete(s.pending, deliveryID)
	}
	for id, replacement := range encoded {
		s.pending[id] = replacement
	}
	return true, nil
}

func (s *databaseTopicDeliveryStore) ReplacePending(deliveryID, targetCore string, replacements []TopicPendingDelivery) (bool, error) {
	if s == nil || s.db == nil || deliveryID == "" || targetCore == "" {
		return false, ErrMessageInvalidEnvelope
	}
	encoded := make(map[string][]byte, len(replacements))
	for _, replacement := range replacements {
		value, err := encodeTopicPendingDelivery(replacement)
		if err != nil {
			return false, err
		}
		encoded[replacement.DeliveryID] = value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	replaced := false
	err := s.db.Update(func(tx database.Tx) error {
		bucket, err := topicDeliveryBucket(tx, false)
		if err != nil || bucket == nil {
			return err
		}
		currentEncoded := bucket.Get([]byte(deliveryID))
		if len(currentEncoded) == 0 {
			return nil
		}
		current, err := decodeTopicPendingDelivery(currentEncoded)
		if err != nil {
			return err
		}
		if current.TargetCore != targetCore {
			return ErrMessageInvalidEnvelope
		}
		for id, value := range encoded {
			if previous := bucket.Get([]byte(id)); len(previous) != 0 && id != deliveryID && !bytes.Equal(previous, value) {
				return ErrMessageInvalidEnvelope
			}
		}
		for id, value := range encoded {
			if err := bucket.Put([]byte(id), value); err != nil {
				return err
			}
		}
		if _, retained := encoded[deliveryID]; !retained {
			if err := bucket.Delete([]byte(deliveryID)); err != nil {
				return err
			}
		}
		replaced = true
		return nil
	})
	return replaced, err
}

func topicDeliveriesUnchanged(original TopicPendingDelivery, replacements []TopicPendingDelivery) bool {
	return len(replacements) == 1 &&
		replacements[0].DeliveryID == original.DeliveryID &&
		replacements[0].TargetCore == original.TargetCore &&
		bytes.Equal(replacements[0].Data, original.Data)
}

func (m *TopicManager) resolveTopicMessageFanout(fanout *TopicFanoutMessage) ([]TopicPendingDelivery, error) {
	if m == nil || m.manager == nil || m.manager.bindings == nil || fanout == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	groups := make(map[string][]string)
	for _, recipient := range fanout.Recipients {
		core, err := m.manager.bindings.CoreNodeForAccount(recipient)
		if err != nil || core == "" {
			return nil, ErrMessageBindingNotFound
		}
		groups[core] = append(groups[core], recipient)
	}
	publish := &TopicPublishMessage{
		TopicName: fanout.TopicName, SenderAccount: fanout.SenderAccount,
		SenderMsgID: fanout.SenderMsgID, MessageID: fanout.MessageID, KeySeq: fanout.KeySeq,
		Ciphertext: fanout.Ciphertext, SenderSignature: fanout.SenderSignature,
	}
	cores := make([]string, 0, len(groups))
	for core := range groups {
		cores = append(cores, core)
	}
	sort.Strings(cores)
	pending := make([]TopicPendingDelivery, 0, len(cores))
	for _, core := range cores {
		batches, err := m.buildFanoutBatches(core, publish, groups[core])
		if err != nil {
			return nil, err
		}
		pending = append(pending, batches...)
	}
	return pending, nil
}

func (m *TopicManager) buildKeyFanoutBatches(core string, message *TopicKeyFanoutMessage, recipients []TopicKeyPackageRecipient) ([]TopicPendingDelivery, error) {
	if m == nil || m.manager == nil || core == "" || message == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	pending := make([]TopicPendingDelivery, 0, 1)
	for len(recipients) != 0 {
		best := 0
		var bestData []byte
		for n := 1; n <= len(recipients); n++ {
			batch := &TopicKeyFanoutMessage{
				TopicName: message.TopicName, KeySeq: message.KeySeq,
				IssuerAccount: message.IssuerAccount, Recipients: recipients[:n],
			}
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
			return nil, ErrMessageTooLarge
		}
		envelope, err := UnmarshalMessageEnvelope(bestData)
		if err != nil {
			return nil, err
		}
		pending = append(pending, TopicPendingDelivery{
			DeliveryID:  topicDeliveryID(envelope.Payload),
			MessageType: MessageTypeTopicKeyFanout,
			TopicName:   message.TopicName, SenderAccount: message.IssuerAccount,
			SenderMsgID: message.KeySeq, TargetCore: core, Data: bestData,
		})
		recipients = recipients[best:]
	}
	return pending, nil
}

func (m *TopicManager) resolveTopicKeyFanout(fanout *TopicKeyFanoutMessage) ([]TopicPendingDelivery, error) {
	if m == nil || m.manager == nil || m.manager.bindings == nil || fanout == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	groups := make(map[string][]TopicKeyPackageRecipient)
	for _, recipient := range fanout.Recipients {
		core, err := m.manager.bindings.CoreNodeForAccount(recipient.Recipient)
		if err != nil || core == "" {
			return nil, ErrMessageBindingNotFound
		}
		groups[core] = append(groups[core], recipient)
	}
	cores := make([]string, 0, len(groups))
	for core := range groups {
		cores = append(cores, core)
	}
	sort.Strings(cores)
	pending := make([]TopicPendingDelivery, 0, len(cores))
	for _, core := range cores {
		batches, err := m.buildKeyFanoutBatches(core, fanout, groups[core])
		if err != nil {
			return nil, err
		}
		pending = append(pending, batches...)
	}
	return pending, nil
}

func (m *TopicManager) refreshPendingDelivery(delivery TopicPendingDelivery) ([]TopicPendingDelivery, error) {
	if m == nil || m.deliveries == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	replacer, ok := m.deliveries.(topicDeliveryReplaceStore)
	if !ok {
		return []TopicPendingDelivery{delivery}, nil
	}
	envelope, err := UnmarshalMessageEnvelope(delivery.Data)
	if err != nil {
		return nil, err
	}
	var replacements []TopicPendingDelivery
	switch envelope.MessageType {
	case MessageTypeTopicFanout:
		fanout, err := UnmarshalTopicFanout(envelope.Payload)
		if err != nil {
			return nil, err
		}
		replacements, err = m.resolveTopicMessageFanout(fanout)
		if err != nil {
			return nil, err
		}
	case MessageTypeTopicKeyFanout:
		fanout, err := UnmarshalTopicKeyFanout(envelope.Payload)
		if err != nil {
			return nil, err
		}
		m.mu.RLock()
		topic := m.topics[fanout.TopicName]
		currentKeySeq := uint64(0)
		if topic != nil {
			currentKeySeq = topic.state.KeySeq
		}
		m.mu.RUnlock()
		if currentKeySeq == 0 {
			return nil, ErrMessageTopicNotFound
		}
		if fanout.KeySeq > currentKeySeq {
			// Prepared membership commit: keep durable but do not expose a future
			// key before membership+KeySeq is committed.
			return nil, nil
		}
		replacements, err = m.resolveTopicKeyFanout(fanout)
		if err != nil {
			return nil, err
		}
	default:
		return []TopicPendingDelivery{delivery}, nil
	}
	if topicDeliveriesUnchanged(delivery, replacements) {
		return []TopicPendingDelivery{delivery}, nil
	}
	replaced, err := replacer.ReplacePending(delivery.DeliveryID, delivery.TargetCore, replacements)
	if err != nil {
		return nil, err
	}
	if !replaced {
		// A concurrent success ACK already removed the old job.
		return nil, nil
	}
	return replacements, nil
}

// RetryPendingResolved re-resolves every recipient placement before retrying a
// durable Topic batch. If accounts moved CoreNodes, the old pending job is
// atomically replaced by newly grouped batches before any packet is sent.
func (m *TopicManager) RetryPendingResolved() error {
	if m == nil || m.deliveries == nil || m.manager == nil || m.manager.router == nil {
		return ErrMessageInvalidEnvelope
	}
	pending, err := m.deliveries.Pending()
	if err != nil {
		return err
	}
	var firstErr error
	for _, delivery := range pending {
		resolved, err := m.refreshPendingDelivery(delivery)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, retry := range resolved {
			if err := m.manager.router.RouteMessageNotify(&wire.MsgDKVSNotify{
				Target: retry.TargetCore, EventType: wire.DKVSNotifyEventMessage,
				Data: append([]byte(nil), retry.Data...),
			}); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
