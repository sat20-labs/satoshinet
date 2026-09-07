package main

import (
	"bytes"
	"sort"
	"sync"

	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

var topicDeliveryBucketKey = []byte("topic-delivery")

type databaseTopicDeliveryStore struct {
	db database.DB
	mu sync.Mutex
}

func newDatabaseTopicDeliveryStore(db database.DB) TopicDeliveryStore {
	if db == nil {
		return newMemoryTopicDeliveryStore()
	}
	return &databaseTopicDeliveryStore{db: db}
}

func topicDeliveryBucket(tx database.Tx, create bool) (database.Bucket, error) {
	root := tx.Metadata().Bucket(messageManagerBucketKey)
	if root == nil && create {
		var err error
		root, err = tx.Metadata().CreateBucketIfNotExists(messageManagerBucketKey)
		if err != nil {
			return nil, err
		}
	}
	if root == nil {
		return nil, nil
	}
	bucket := root.Bucket(topicDeliveryBucketKey)
	if bucket == nil && create {
		var err error
		bucket, err = root.CreateBucketIfNotExists(topicDeliveryBucketKey)
		if err != nil {
			return nil, err
		}
	}
	return bucket, nil
}

func encodeTopicPendingDelivery(delivery TopicPendingDelivery) ([]byte, error) {
	if len(delivery.DeliveryID) != 64 || delivery.MessageType == 0 || delivery.TopicName == "" ||
		!validateAccountID(delivery.SenderAccount) || delivery.TargetCore == "" || len(delivery.Data) == 0 ||
		(delivery.MessageType == MessageTypeTopicFanout && !wire.ValidMessageID(delivery.MessageID)) {
		return nil, ErrMessageInvalidEnvelope
	}
	var buf bytes.Buffer
	buf.WriteByte(byte(delivery.MessageType))
	writeMessageString(&buf, delivery.DeliveryID)
	writeMessageString(&buf, delivery.TopicName)
	writeMessageString(&buf, delivery.SenderAccount)
	writeMessageUint64(&buf, delivery.SenderMsgID)
	writeMessageString(&buf, delivery.MessageID)
	writeMessageString(&buf, delivery.TargetCore)
	writeMessageBytes(&buf, delivery.Data)
	return buf.Bytes(), nil
}

func decodeTopicPendingDelivery(encoded []byte) (TopicPendingDelivery, error) {
	var delivery TopicPendingDelivery
	r := bytes.NewReader(encoded)
	messageType, err := r.ReadByte()
	if err != nil || messageType == 0 {
		return delivery, ErrMessageInvalidEnvelope
	}
	deliveryID, err := readMessageString(r, 64)
	if err != nil || len(deliveryID) != 64 {
		return delivery, ErrMessageInvalidEnvelope
	}
	topicName, err := readMessageString(r, 64)
	if err != nil || topicName == "" {
		return delivery, ErrMessageInvalidEnvelope
	}
	sender, err := readMessageString(r, 64)
	if err != nil || !validateAccountID(sender) {
		return delivery, ErrMessageInvalidEnvelope
	}
	msgID, err := readMessageUint64(r)
	if err != nil {
		return delivery, err
	}
	messageID, err := readMessageString(r, wire.MessageIDHexSize)
	if err != nil || (MessageType(messageType) == MessageTypeTopicFanout && !wire.ValidMessageID(messageID)) ||
		(MessageType(messageType) != MessageTypeTopicFanout && messageID != "") {
		return delivery, ErrMessageInvalidEnvelope
	}
	target, err := readMessageString(r, 256)
	if err != nil || target == "" {
		return delivery, ErrMessageInvalidEnvelope
	}
	data, err := readMessageBytes(r, 4*1024*1024)
	if err != nil || len(data) == 0 || r.Len() != 0 {
		return delivery, ErrMessageInvalidEnvelope
	}
	return TopicPendingDelivery{
		DeliveryID: deliveryID, MessageType: MessageType(messageType), TopicName: topicName,
		SenderAccount: sender, SenderMsgID: msgID, MessageID: messageID, TargetCore: target, Data: data,
	}, nil
}

func (s *databaseTopicDeliveryStore) SavePending(deliveries []TopicPendingDelivery) error {
	if s == nil || s.db == nil {
		return ErrMessageInvalidEnvelope
	}
	encoded := make(map[string][]byte, len(deliveries))
	for _, delivery := range deliveries {
		value, err := encodeTopicPendingDelivery(delivery)
		if err != nil {
			return err
		}
		encoded[delivery.DeliveryID] = value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx database.Tx) error {
		bucket, err := topicDeliveryBucket(tx, true)
		if err != nil {
			return err
		}
		for id, value := range encoded {
			if previous := bucket.Get([]byte(id)); len(previous) != 0 {
				if !bytes.Equal(previous, value) {
					return ErrMessageInvalidEnvelope
				}
				continue
			}
			if err := bucket.Put([]byte(id), value); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *databaseTopicDeliveryStore) Acknowledge(deliveryID, targetCore string, status MessageAckStatus) error {
	if s == nil || s.db == nil || len(deliveryID) != 64 || targetCore == "" || status == 0 {
		return ErrMessageInvalidEnvelope
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx database.Tx) error {
		bucket, err := topicDeliveryBucket(tx, false)
		if err != nil || bucket == nil {
			return err
		}
		encoded := bucket.Get([]byte(deliveryID))
		if len(encoded) == 0 {
			return nil
		}
		delivery, err := decodeTopicPendingDelivery(encoded)
		if err != nil {
			return err
		}
		if delivery.TargetCore != targetCore {
			return ErrMessageInvalidEnvelope
		}
		if status == MessageAckOK {
			return bucket.Delete([]byte(deliveryID))
		}
		// Retryable and rejected deliveries remain durable. A rejected mailbox
		// may become writable after the recipient changes policy or frees space.
		return nil
	})
}

func (s *databaseTopicDeliveryStore) Pending() ([]TopicPendingDelivery, error) {
	if s == nil || s.db == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := make([]TopicPendingDelivery, 0)
	err := s.db.View(func(tx database.Tx) error {
		bucket, err := topicDeliveryBucket(tx, false)
		if err != nil || bucket == nil {
			return err
		}
		return bucket.ForEach(func(_, value []byte) error {
			delivery, err := decodeTopicPendingDelivery(value)
			if err != nil {
				return err
			}
			pending = append(pending, delivery)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].DeliveryID < pending[j].DeliveryID })
	return pending, nil
}
