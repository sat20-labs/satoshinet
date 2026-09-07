package main

import "github.com/sat20-labs/satoshinet/database"

// NewPersistentMessageManager is the production constructor. Sender sequence,
// accepted outgoing jobs and Topic fan-out pending deliveries share the node's
// durable metadata database; the mailbox body itself remains in DKVS.
func NewPersistentMessageManager(localCore string, bindings MessageBindingResolver, mailbox MessageMailboxStore, router MessageRouter, usage MessageUsageCharger, topicService TopicServiceAuthorizer, db database.DB) *MessageManager {
	manager := NewMessageManager(localCore, bindings, mailbox, router, usage, newDatabaseMessageAcceptanceStore(db))
	manager.topics.SetDeliveryStore(newDatabaseTopicDeliveryStore(db))
	manager.topics.SetServiceAuthorizer(topicService)
	return manager
}
