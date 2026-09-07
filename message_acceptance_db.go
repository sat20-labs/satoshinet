package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/wire"
)

var (
	messageManagerBucketKey = []byte("message-manager")
	messageNextBucketKey    = []byte("sender-next")
	messageAcceptBucketKey  = []byte("accepted")
)

type databaseMessageAcceptanceStore struct {
	db database.DB
	mu sync.Mutex
}

func newDatabaseMessageAcceptanceStore(db database.DB) MessageAcceptanceStore {
	if db == nil {
		return newMemoryMessageAcceptanceStore()
	}
	return &databaseMessageAcceptanceStore{db: db}
}

func acceptedMessageDBKey(sender, messageID string) []byte {
	return []byte(acceptedMessageKey(sender, messageID))
}

func encodeAcceptedMessage(message acceptedMessage) ([]byte, error) {
	if !validateAccountID(message.SenderAccount) || !wire.ValidMessageID(message.MessageID) || message.Target == "" || len(message.Data) == 0 {
		return nil, ErrMessageInvalidEnvelope
	}
	var buf bytes.Buffer
	buf.Write(message.Digest[:])
	writeMessageUint64(&buf, message.SenderMsgID)
	writeMessageString(&buf, message.MessageID)
	writeMessageString(&buf, message.SenderAccount)
	writeMessageString(&buf, message.Target)
	writeMessageBytes(&buf, message.Data)
	var flags byte
	if message.Final {
		flags |= 1
	}
	buf.WriteByte(flags)
	buf.WriteByte(byte(message.Status))
	return buf.Bytes(), nil
}

func decodeAcceptedMessage(encoded []byte) (acceptedMessage, error) {
	var message acceptedMessage
	r := bytes.NewReader(encoded)
	if _, err := r.Read(message.Digest[:]); err != nil {
		return message, ErrMessageInvalidEnvelope
	}
	msgID, err := readMessageUint64(r)
	if err != nil {
		return message, err
	}
	messageID, err := readMessageString(r, wire.MessageIDHexSize)
	if err != nil || !wire.ValidMessageID(messageID) {
		return message, ErrMessageInvalidEnvelope
	}
	sender, err := readMessageString(r, 64)
	if err != nil || !validateAccountID(sender) {
		return message, ErrMessageInvalidEnvelope
	}
	target, err := readMessageString(r, 256)
	if err != nil || target == "" {
		return message, ErrMessageInvalidEnvelope
	}
	data, err := readMessageBytes(r, 4*1024*1024)
	if err != nil || len(data) == 0 {
		return message, ErrMessageInvalidEnvelope
	}
	flags, err := r.ReadByte()
	if err != nil || flags&^byte(1) != 0 {
		return message, ErrMessageInvalidEnvelope
	}
	status, err := r.ReadByte()
	if err != nil || r.Len() != 0 {
		return message, ErrMessageInvalidEnvelope
	}
	message.SenderAccount = sender
	message.SenderMsgID = msgID
	message.MessageID = messageID
	message.Target = target
	message.Data = data
	message.Final = flags&1 != 0
	message.Status = MessageAckStatus(status)
	return message, nil
}

func messageBuckets(tx database.Tx, create bool) (database.Bucket, database.Bucket, error) {
	root := tx.Metadata().Bucket(messageManagerBucketKey)
	if root == nil && create {
		var err error
		root, err = tx.Metadata().CreateBucketIfNotExists(messageManagerBucketKey)
		if err != nil {
			return nil, nil, err
		}
	}
	if root == nil {
		return nil, nil, nil
	}
	next := root.Bucket(messageNextBucketKey)
	accepted := root.Bucket(messageAcceptBucketKey)
	if create {
		var err error
		if next == nil {
			next, err = root.CreateBucketIfNotExists(messageNextBucketKey)
			if err != nil {
				return nil, nil, err
			}
		}
		if accepted == nil {
			accepted, err = root.CreateBucketIfNotExists(messageAcceptBucketKey)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return next, accepted, nil
}

func readNextSenderMsgID(bucket database.Bucket, sender string) uint64 {
	if bucket == nil {
		return 0
	}
	encoded := bucket.Get([]byte(sender))
	if len(encoded) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(encoded)
}

func (s *databaseMessageAcceptanceStore) NextSenderMsgID(senderAccount string) uint64 {
	if s == nil || s.db == nil || !validateAccountID(senderAccount) {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var next uint64
	_ = s.db.View(func(tx database.Tx) error {
		nextBucket, _, err := messageBuckets(tx, false)
		if err != nil {
			return err
		}
		next = readNextSenderMsgID(nextBucket, senderAccount)
		return nil
	})
	return next
}

func (s *databaseMessageAcceptanceStore) Accept(senderAccount string, senderMsgID uint64, messageID string, digest [32]byte, target string, data []byte, charge func() error) (acceptedMessage, bool, error) {
	if s == nil || s.db == nil || !validateAccountID(senderAccount) || !wire.ValidMessageID(messageID) || target == "" || len(data) == 0 {
		return acceptedMessage{}, false, ErrMessageInvalidEnvelope
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	idKey := acceptedMessageDBKey(senderAccount, messageID)
	var previous *acceptedMessage
	var expected uint64
	if err := s.db.View(func(tx database.Tx) error {
		nextBucket, acceptedBucket, err := messageBuckets(tx, false)
		if err != nil {
			return err
		}
		expected = readNextSenderMsgID(nextBucket, senderAccount)
		if acceptedBucket == nil {
			return nil
		}
		encoded := acceptedBucket.Get(idKey)
		if len(encoded) == 0 {
			return nil
		}
		decoded, err := decodeAcceptedMessage(encoded)
		if err != nil {
			return err
		}
		previous = &decoded
		return nil
	}); err != nil {
		return acceptedMessage{}, false, err
	}
	if previous != nil {
		if previous.Digest != digest || previous.SenderMsgID != senderMsgID {
			return acceptedMessage{}, false, ErrMessageInvalidSequence
		}
		if !previous.Final && previous.Target != target {
			previous.Target = target
			encoded, err := encodeAcceptedMessage(*previous)
			if err != nil {
				return acceptedMessage{}, false, err
			}
			if err := s.db.Update(func(tx database.Tx) error {
				_, acceptedBucket, err := messageBuckets(tx, false)
				if err != nil || acceptedBucket == nil {
					return err
				}
				return acceptedBucket.Put(idKey, encoded)
			}); err != nil {
				return acceptedMessage{}, false, err
			}
		}
		return *previous, true, nil
	}
	if senderMsgID != expected {
		return acceptedMessage{}, false, fmt.Errorf("%w: got=%d want=%d", ErrMessageInvalidSequence, senderMsgID, expected)
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
	encodedAccepted, err := encodeAcceptedMessage(accepted)
	if err != nil {
		return acceptedMessage{}, false, err
	}
	if expected == ^uint64(0) {
		return acceptedMessage{}, false, ErrMessageInvalidSequence
	}
	var nextEncoded [8]byte
	binary.BigEndian.PutUint64(nextEncoded[:], expected+1)
	err = s.db.Update(func(tx database.Tx) error {
		nextBucket, acceptedBucket, err := messageBuckets(tx, true)
		if err != nil {
			return err
		}
		if current := readNextSenderMsgID(nextBucket, senderAccount); current != expected {
			return ErrMessageInvalidSequence
		}
		if current := acceptedBucket.Get(idKey); len(current) != 0 {
			decoded, err := decodeAcceptedMessage(current)
			if err != nil {
				return err
			}
			if decoded.Digest != digest {
				return ErrMessageInvalidSequence
			}
			accepted = decoded
			return nil
		}
		if err := acceptedBucket.Put(idKey, encodedAccepted); err != nil {
			return err
		}
		return nextBucket.Put([]byte(senderAccount), nextEncoded[:])
	})
	if err != nil {
		return acceptedMessage{}, false, err
	}
	return accepted, false, nil
}

func (s *databaseMessageAcceptanceStore) MarkAck(senderAccount, messageID string, senderMsgID uint64, sourceCore string, status MessageAckStatus) error {
	if s == nil || s.db == nil || !validateAccountID(senderAccount) || !wire.ValidMessageID(messageID) || sourceCore == "" || status == 0 {
		return ErrMessageInvalidEnvelope
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := acceptedMessageDBKey(senderAccount, messageID)
	return s.db.Update(func(tx database.Tx) error {
		_, acceptedBucket, err := messageBuckets(tx, false)
		if err != nil {
			return err
		}
		if acceptedBucket == nil {
			return ErrMessageInvalidSequence
		}
		encoded := acceptedBucket.Get(key)
		if len(encoded) == 0 {
			return ErrMessageInvalidSequence
		}
		message, err := decodeAcceptedMessage(encoded)
		if err != nil {
			return err
		}
		if message.SenderMsgID != senderMsgID {
			return ErrMessageInvalidSequence
		}
		if message.Target != sourceCore {
			return ErrMessageInvalidEnvelope
		}
		message.Status = status
		message.Final = status == MessageAckOK || status == MessageAckRejected
		encoded, err = encodeAcceptedMessage(message)
		if err != nil {
			return err
		}
		return acceptedBucket.Put(key, encoded)
	})
}

func (s *databaseMessageAcceptanceStore) Pending() ([]acceptedMessage, error) {
	if s == nil || s.db == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := make([]acceptedMessage, 0)
	err := s.db.View(func(tx database.Tx) error {
		_, acceptedBucket, err := messageBuckets(tx, false)
		if err != nil || acceptedBucket == nil {
			return err
		}
		return acceptedBucket.ForEach(func(_, value []byte) error {
			message, err := decodeAcceptedMessage(value)
			if err != nil {
				return err
			}
			if !message.Final {
				pending = append(pending, message)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].SenderAccount != pending[j].SenderAccount {
			return pending[i].SenderAccount < pending[j].SenderAccount
		}
		return pending[i].SenderMsgID < pending[j].SenderMsgID
	})
	return pending, nil
}

func (s *databaseMessageAcceptanceStore) PruneExpiredDirect(now time.Time) error {
	if s == nil || s.db == nil {
		return ErrMessageInvalidEnvelope
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired [][]byte
	if err := s.db.View(func(tx database.Tx) error {
		_, acceptedBucket, err := messageBuckets(tx, false)
		if err != nil || acceptedBucket == nil {
			return err
		}
		return acceptedBucket.ForEach(func(key, value []byte) error {
			message, err := decodeAcceptedMessage(value)
			if err != nil {
				return err
			}
			if acceptedDirectExpired(message, now) {
				expired = append(expired, append([]byte(nil), key...))
			}
			return nil
		})
	}); err != nil || len(expired) == 0 {
		return err
	}
	return s.db.Update(func(tx database.Tx) error {
		_, acceptedBucket, err := messageBuckets(tx, false)
		if err != nil || acceptedBucket == nil {
			return err
		}
		for _, key := range expired {
			if err := acceptedBucket.Delete(key); err != nil {
				return err
			}
		}
		return nil
	})
}
