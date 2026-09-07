package main

import (
	"errors"
	"strconv"

	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type MessageMailboxRecordWriter interface {
	PutDKVSInternalMailbox(record *wire.DKVSRecord) (bool, error)
}

type MessageRetentionResolver interface {
	HasActiveSubscription(accountID string) (bool, error)
}

type DKVSMessageMailboxStore struct {
	writer           MessageMailboxRecordWriter
	currentHeight    func() uint64
	msgTTLForAccount func(accountID string) uint64
	keyTTLForAccount func(accountID string) uint64
	retention        MessageRetentionResolver
}

func NewDKVSMessageMailboxStoreWithRetention(writer MessageMailboxRecordWriter, currentHeight func() uint64,
	msgTTLForAccount, keyTTLForAccount func(string) uint64, retention MessageRetentionResolver) *DKVSMessageMailboxStore {
	store := NewDKVSMessageMailboxStore(writer, currentHeight, msgTTLForAccount, keyTTLForAccount)
	store.retention = retention
	return store
}

func NewDKVSMessageMailboxStore(writer MessageMailboxRecordWriter, currentHeight func() uint64,
	msgTTLForAccount, keyTTLForAccount func(string) uint64) *DKVSMessageMailboxStore {
	if keyTTLForAccount == nil {
		keyTTLForAccount = msgTTLForAccount
	}
	return &DKVSMessageMailboxStore{
		writer: writer, currentHeight: currentHeight,
		msgTTLForAccount: msgTTLForAccount, keyTTLForAccount: keyTTLForAccount,
	}
}

func (s *DKVSMessageMailboxStore) heightAndTTL(accountID string, keyPackage, direct bool) (uint64, uint64, error) {
	if s == nil || s.writer == nil || s.currentHeight == nil || s.msgTTLForAccount == nil || s.keyTTLForAccount == nil {
		return 0, 0, ErrMessageInvalidEnvelope
	}
	height := s.currentHeight()
	if direct && s.retention != nil {
		paid, err := s.retention.HasActiveSubscription(accountID)
		if err != nil {
			return 0, 0, err
		}
		if paid {
			return height, 0, nil
		}
	}
	ttl := s.msgTTLForAccount(accountID)
	if keyPackage {
		ttl = s.keyTTLForAccount(accountID)
	}
	if ttl == 0 {
		return 0, 0, ErrMessageMailboxFull
	}
	return height, ttl, nil
}

func mapMailboxWriteError(err error) error {
	if errors.Is(err, dkvs.ErrMailboxFull) || errors.Is(err, dkvs.ErrFreeLocalQuotaExceeded) || errors.Is(err, dkvs.ErrRecordTooLarge) {
		return ErrMessageMailboxFull
	}
	return err
}

func (s *DKVSMessageMailboxStore) put(accountID, key string, value []byte, keyPackage, direct bool) (bool, error) {
	height, ttl, err := s.heightAndTTL(accountID, keyPackage, direct)
	if err != nil {
		return false, err
	}
	record := &wire.DKVSRecord{
		Version:     dkvs.Version,
		Key:         key,
		Value:       append([]byte(nil), value...),
		Seq:         1,
		IssueHeight: height,
		TTL:         ttl,
	}
	if direct && ttl != 0 {
		proof, proofErr := dkvs.NewFreeLocalFeeProof(key, "mail", uint32(dkvs.RecordSize(record)), height+ttl)
		if proofErr != nil {
			return false, proofErr
		}
		record.FeeProof, proofErr = dkvs.EncodeFeeProof(proof)
		if proofErr != nil {
			return false, proofErr
		}
	}
	updated, err := s.writer.PutDKVSInternalMailbox(record)
	return updated, mapMailboxWriteError(err)
}

func (s *DKVSMessageMailboxStore) AppendDirectMessage(message *DirectMessage) (bool, error) {
	if message == nil {
		return false, ErrMessageInvalidEnvelope
	}
	key, err := dkvs.MailMsgKey(message.RecipientAccount, message.SenderAccount, message.MessageID)
	if err != nil {
		return false, err
	}
	value, err := MarshalDirectMessage(message, true)
	if err != nil {
		return false, err
	}
	return s.put(message.RecipientAccount, key, value, false, true)
}

func (s *DKVSMessageMailboxStore) AppendTopicMessage(recipient string, message *TopicFanoutMessage) (bool, error) {
	if message == nil || !validateAccountID(recipient) {
		return false, ErrMessageInvalidEnvelope
	}
	key, err := dkvs.MailTopicMessageKey(recipient, message.TopicName, message.SenderAccount, message.MessageID)
	if err != nil {
		return false, err
	}
	value, err := MarshalTopicPublish(&TopicPublishMessage{
		TopicName:       message.TopicName,
		SenderAccount:   message.SenderAccount,
		SenderMsgID:     message.SenderMsgID,
		MessageID:       message.MessageID,
		KeySeq:          message.KeySeq,
		Ciphertext:      message.Ciphertext,
		SenderSignature: message.SenderSignature,
	}, true)
	if err != nil {
		return false, err
	}
	return s.put(recipient, key, value, false, false)
}

func (s *DKVSMessageMailboxStore) AppendTopicKeyPackage(recipient string, message *TopicKeyFanoutMessage, packageRecipient TopicKeyPackageRecipient) (bool, error) {
	if message == nil || packageRecipient.Recipient != recipient || !validateAccountID(recipient) {
		return false, ErrMessageInvalidEnvelope
	}
	key, err := dkvs.MailTopicKeyKey(recipient, message.TopicName, strconv.FormatUint(message.KeySeq, 10))
	if err != nil {
		return false, err
	}
	value, err := MarshalTopicKeyFanout(&TopicKeyFanoutMessage{
		TopicName:     message.TopicName,
		KeySeq:        message.KeySeq,
		IssuerAccount: message.IssuerAccount,
		Recipients:    []TopicKeyPackageRecipient{packageRecipient},
	})
	if err != nil {
		return false, err
	}
	return s.put(recipient, key, value, true, false)
}
