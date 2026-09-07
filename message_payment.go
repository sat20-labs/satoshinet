package main

import (
	"fmt"
	"strings"
	"sync"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/database"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	MessageUsageItemSend  = "MESSAGE_SEND"
	TopicUsageItemService = "TOPIC_SERVICE"
)

var messageUsageBucketKey = []byte("message-usage")

// MessageSendAuthorizer supplies either per-message entitlement or continuous
// subscription authorization. If it mutates external payment state, it must be
// idempotent for the stable MessageID.
type MessageSendAuthorizer interface {
	AuthorizeMessageSend(senderAccount, messageID string) error
}

type persistentMessageUsageCharger struct {
	db         database.DB
	authorizer MessageSendAuthorizer
	mu         sync.Mutex
}

func NewPersistentMessageUsageCharger(db database.DB, authorizer MessageSendAuthorizer) MessageUsageCharger {
	return &persistentMessageUsageCharger{db: db, authorizer: authorizer}
}

func messageUsageBucket(tx database.Tx, create bool) (database.Bucket, error) {
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
	bucket := root.Bucket(messageUsageBucketKey)
	if bucket == nil && create {
		var err error
		bucket, err = root.CreateBucketIfNotExists(messageUsageBucketKey)
		if err != nil {
			return nil, err
		}
	}
	return bucket, nil
}

func (c *persistentMessageUsageCharger) ChargeMessageSend(senderAccount, messageID string) error {
	if c == nil || c.db == nil || !validateAccountID(senderAccount) || !wire.ValidMessageID(messageID) {
		return ErrMessageInvalidEnvelope
	}
	key := acceptedMessageDBKey(senderAccount, messageID)
	c.mu.Lock()
	defer c.mu.Unlock()
	alreadyCharged := false
	if err := c.db.View(func(tx database.Tx) error {
		bucket, err := messageUsageBucket(tx, false)
		if err != nil || bucket == nil {
			return err
		}
		alreadyCharged = string(bucket.Get(key)) == MessageUsageItemSend
		return nil
	}); err != nil {
		return err
	}
	if alreadyCharged {
		return nil
	}
	if c.authorizer == nil {
		return fmt.Errorf("%w: missing %s authorizer", ErrMessageTopicPermission, MessageUsageItemSend)
	}
	if err := c.authorizer.AuthorizeMessageSend(senderAccount, messageID); err != nil {
		return err
	}
	return c.db.Update(func(tx database.Tx) error {
		bucket, err := messageUsageBucket(tx, true)
		if err != nil {
			return err
		}
		if string(bucket.Get(key)) == MessageUsageItemSend {
			return nil
		}
		return bucket.Put(key, []byte(MessageUsageItemSend))
	})
}

type AutopaySubscriptionMessageAuthorizer struct {
	StateProvider      dkvs.AutopayStateProvider
	ContractForAccount func(accountID string) (string, error)
	ServiceName        string
	AddressParams      *chaincfg.Params
}

func (a AutopaySubscriptionMessageAuthorizer) AuthorizeMessageSend(senderAccount, _ string) error {
	if a.StateProvider == nil || a.ContractForAccount == nil || a.AddressParams == nil {
		return ErrMessageTopicPermission
	}
	contract, err := a.ContractForAccount(senderAccount)
	if err != nil || strings.TrimSpace(contract) == "" {
		return ErrMessageTopicPermission
	}
	return authorizeActiveAutopayDelegate(a.StateProvider, contract, senderAccount, a.ServiceName, a.AddressParams)
}

// HasActiveSubscription is used only to select mailbox retention for newly
// delivered Direct messages. A missing/expired delegate is a normal free
// result; provider failures remain retryable and must not silently grant paid
// retention or downgrade to free storage.
func (a AutopaySubscriptionMessageAuthorizer) HasActiveSubscription(accountID string) (bool, error) {
	if a.StateProvider == nil || a.ContractForAccount == nil || a.AddressParams == nil {
		return false, nil
	}
	contract, err := a.ContractForAccount(accountID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(contract) == "" {
		return false, nil
	}
	return activeAutopayDelegate(a.StateProvider, contract, accountID, a.ServiceName, a.AddressParams)
}

type AutopayTopicServiceAuthorizer struct {
	StateProvider    dkvs.AutopayStateProvider
	ContractForTopic func(topic TopicMeta) (string, error)
	ServiceName      string
	AddressParams    *chaincfg.Params
}

func (a AutopayTopicServiceAuthorizer) AuthorizeTopicService(topic TopicMeta) error {
	if a.StateProvider == nil || a.ContractForTopic == nil || a.AddressParams == nil {
		return ErrMessageTopicPermission
	}
	contract, err := a.ContractForTopic(topic)
	if err != nil || strings.TrimSpace(contract) == "" {
		return ErrMessageTopicPermission
	}
	return authorizeActiveAutopayDelegate(a.StateProvider, contract, topic.OwnerAccount, a.ServiceName, a.AddressParams)
}

func authorizeActiveAutopayDelegate(provider dkvs.AutopayStateProvider, contract, accountID, serviceName string, params *chaincfg.Params) error {
	active, err := activeAutopayDelegate(provider, contract, accountID, serviceName, params)
	if err != nil {
		return err
	}
	if !active {
		return ErrMessageTopicPermission
	}
	return nil
}

func activeAutopayDelegate(provider dkvs.AutopayStateProvider, contract, accountID, serviceName string, params *chaincfg.Params) (bool, error) {
	pubKey, err := dkvs.AccountPubKey(accountID)
	if err != nil {
		return false, nil
	}
	payer, err := dkvs.P2TRAddressFromPubKeyBytes(pubKey, params)
	if err != nil {
		return false, nil
	}
	state, err := provider.GetAutopayState(strings.TrimSpace(contract))
	if err != nil {
		return false, err
	}
	if state == nil || !strings.EqualFold(strings.TrimSpace(state.TemplateName), "autopay.tc") ||
		!strings.EqualFold(strings.TrimSpace(state.Status), "active") || state.Closed {
		return false, nil
	}
	if expected := strings.TrimSpace(serviceName); expected != "" && !strings.EqualFold(strings.TrimSpace(state.ServiceName), expected) {
		return false, nil
	}
	delegate, ok := state.Delegates[strings.TrimSpace(payer)]
	if !ok || !strings.EqualFold(strings.TrimSpace(delegate.Status), "active") {
		return false, nil
	}
	current := state.CurrentBlock
	if current <= 0 {
		return false, nil
	}
	if delegate.LastPayHeight < current {
		return false, nil
	}
	return true, nil
}
