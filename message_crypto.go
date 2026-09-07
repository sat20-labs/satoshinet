package main

import (
	"bytes"
	"encoding/hex"

	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

var messageAckSignatureDomain = []byte("satoshinet-message-ack-v1")

func messageAckSigningPayload(sourceCore, targetCore string, ack *MessageAck) ([]byte, error) {
	if sourceCore == "" || targetCore == "" || ack == nil {
		return nil, ErrMessageInvalidEnvelope
	}
	encoded, err := serializeMessageAck(ack, false)
	if err != nil {
		return nil, err
	}
	var payload bytes.Buffer
	writeMessageBytes(&payload, messageAckSignatureDomain)
	writeMessageString(&payload, sourceCore)
	writeMessageString(&payload, targetCore)
	writeMessageBytes(&payload, encoded)
	return payload.Bytes(), nil
}

func verifyCoreMessageAck(sourceCore, targetCore string, ack *MessageAck) error {
	if ack == nil || len(ack.Signature) == 0 {
		return ErrMessageInvalidSignature
	}
	encoded, err := hex.DecodeString(sourceCore)
	if err != nil {
		return ErrMessageInvalidSignature
	}
	pubKey, err := btcec.ParsePubKey(encoded)
	if err != nil {
		return ErrMessageInvalidSignature
	}
	signature, err := ecdsa.ParseDERSignature(ack.Signature)
	if err != nil {
		return ErrMessageInvalidSignature
	}
	payload, err := messageAckSigningPayload(sourceCore, targetCore, ack)
	if err != nil || !anchortx.VerifyMessage(pubKey, payload, signature) {
		return ErrMessageInvalidSignature
	}
	return nil
}

func directMessageSigningHash(message *DirectMessage) ([32]byte, error) {
	hash, err := wire.DirectMessageSigningHash(message)
	if err != nil {
		return [32]byte{}, ErrMessageInvalidEnvelope
	}
	return hash, nil
}

func topicMessageSigningHash(message *TopicPublishMessage) ([32]byte, error) {
	hash, err := wire.TopicPublishSigningHash(message)
	if err != nil {
		return [32]byte{}, ErrMessageInvalidEnvelope
	}
	return hash, nil
}

func topicKeyPackageSigningHash(topicName string, keySeq uint64, issuerAccount string, packageRecipient TopicKeyPackageRecipient) ([32]byte, error) {
	hash, err := wire.TopicKeyPackageSigningHash(topicName, keySeq, issuerAccount, packageRecipient)
	if err != nil {
		return [32]byte{}, ErrMessageInvalidEnvelope
	}
	return hash, nil
}

func verifyAccountSchnorr(accountID string, signature []byte, hash [32]byte) error {
	pubKeyBytes, err := dkvs.AccountPubKey(accountID)
	if err != nil {
		return ErrMessageInvalidSignature
	}
	pubKey, err := btcec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return ErrMessageInvalidSignature
	}
	sig, err := schnorr.ParseSignature(signature)
	if err != nil || !sig.Verify(hash[:], pubKey) {
		return ErrMessageInvalidSignature
	}
	return nil
}

func verifyDirectMessageSignature(message *DirectMessage) error {
	if message == nil {
		return ErrMessageInvalidSignature
	}
	hash, err := directMessageSigningHash(message)
	if err != nil {
		return err
	}
	return verifyAccountSchnorr(message.SenderAccount, message.SenderSignature, hash)
}

func verifyTopicMessageSignature(message *TopicPublishMessage) error {
	if message == nil {
		return ErrMessageInvalidSignature
	}
	hash, err := topicMessageSigningHash(message)
	if err != nil {
		return err
	}
	return verifyAccountSchnorr(message.SenderAccount, message.SenderSignature, hash)
}

func verifyTopicFanoutSignature(message *TopicFanoutMessage) error {
	if message == nil {
		return ErrMessageInvalidSignature
	}
	return verifyTopicMessageSignature(&TopicPublishMessage{
		TopicName:       message.TopicName,
		SenderAccount:   message.SenderAccount,
		SenderMsgID:     message.SenderMsgID,
		MessageID:       message.MessageID,
		KeySeq:          message.KeySeq,
		Ciphertext:      message.Ciphertext,
		SenderSignature: message.SenderSignature,
	})
}

func verifyTopicKeyPackageSignature(message *TopicKeyFanoutMessage, packageRecipient TopicKeyPackageRecipient) error {
	if message == nil {
		return ErrMessageInvalidSignature
	}
	hash, err := topicKeyPackageSigningHash(message.TopicName, message.KeySeq, message.IssuerAccount, packageRecipient)
	if err != nil {
		return err
	}
	return verifyAccountSchnorr(message.IssuerAccount, packageRecipient.IssuerSignature, hash)
}

func verifyTopicMembershipRequestSignature(request *TopicMembershipRequest) error {
	if request == nil {
		return ErrMessageInvalidSignature
	}
	hash, err := wire.TopicMembershipRequestSigningHash(request)
	if err != nil {
		return ErrMessageInvalidEnvelope
	}
	return verifyAccountSchnorr(request.AccountID, request.Signature, hash)
}

func verifyTopicMembershipCommitSignature(commit *TopicMembershipCommit) error {
	if commit == nil {
		return ErrMessageInvalidSignature
	}
	hash, err := wire.TopicMembershipCommitSigningHash(commit)
	if err != nil {
		return ErrMessageInvalidEnvelope
	}
	return verifyAccountSchnorr(commit.IssuerAccount, commit.IssuerSignature, hash)
}

func verifyTopicCreateRequestSignature(request *TopicCreateRequest) error {
	if request == nil {
		return ErrMessageInvalidSignature
	}
	hash, err := wire.TopicCreateRequestSigningHash(request)
	if err != nil {
		return ErrMessageInvalidEnvelope
	}
	return verifyAccountSchnorr(request.Meta.OwnerAccount, request.OwnerSignature, hash)
}

func verifyTopicJoinRejectionSignature(rejection *TopicJoinRejection) error {
	if rejection == nil {
		return ErrMessageInvalidSignature
	}
	hash, err := wire.TopicJoinRejectionSigningHash(rejection)
	if err != nil {
		return ErrMessageInvalidEnvelope
	}
	return verifyAccountSchnorr(rejection.OwnerAccount, rejection.OwnerSignature, hash)
}
