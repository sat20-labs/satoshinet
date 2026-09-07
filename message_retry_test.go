package main

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/wire"
)

func lastTestRoutedMessage(t *testing.T, router *testMessageRouter) *wire.MsgDKVSNotify {
	t.Helper()
	router.mu.Lock()
	defer router.mu.Unlock()
	if len(router.messages) == 0 {
		t.Fatal("no routed message")
	}
	message := *router.messages[len(router.messages)-1]
	message.Data = append([]byte(nil), message.Data...)
	return &message
}

func TestMessageRetryReResolvesDirectRecipientWithoutRebilling(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{
		sender:    "core-a",
		recipient: "core-b",
	}}
	router := &testMessageRouter{}
	charger := &testMessageCharger{}
	manager := newTestMessageManager("core-a", bindings, &testMessageMailbox{}, router, charger, nil)
	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "ciphertext")

	if err := manager.SendDirectMessage(message); err != nil {
		t.Fatal(err)
	}
	if got := lastTestRoutedMessage(t, router).Target; got != "core-b" {
		t.Fatalf("initial target=%s want=core-b", got)
	}

	bindings.set(recipient, "core-c")
	if err := manager.RetryPendingResolved(); err != nil {
		t.Fatal(err)
	}
	if got := lastTestRoutedMessage(t, router).Target; got != "core-c" {
		t.Fatalf("retry target=%s want=core-c", got)
	}
	if charger.callCount() != 0 || charger.unique() != 0 {
		t.Fatalf("retry rebilled calls=%d unique=%d", charger.callCount(), charger.unique())
	}
	if got := manager.GetNextSenderMsgID(sender); got != 1 {
		t.Fatalf("retry advanced SenderMsgID next=%d", got)
	}

	pending, err := manager.accepted.(pendingMessageAcceptanceStore).Pending()
	if err != nil || len(pending) != 1 || pending[0].Target != "core-c" {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
}

func TestTopicRetryReGroupsMovedRecipientsBeforeRouting(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{
		owner:     "topic-host",
		recipient: "core-b",
	}}
	router := &testMessageRouter{}
	manager := newTestMessageManager("topic-host", bindings, &testMessageMailbox{}, router, nil, nil)
	if err := manager.topics.CreateTopic(TopicMeta{
		TopicName: "developers", OwnerAccount: owner, ServiceCoreNode: "topic-host",
		MaxMembers: 10,
	}); err != nil {
		t.Fatal(err)
	}
	forceTopicActiveMembersForTest(t, manager.topics, "developers", 2, recipient)
	keySeq := uint64(2)

	publish := signedTestTopic(t, ownerPriv, owner, "developers", 0, keySeq, "ciphertext")
	payload, err := MarshalTopicPublish(publish, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalMessageEnvelope(&MessageEnvelope{
		MessageType: MessageTypeTopicPublish, SourceCoreNode: "topic-host", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.HandleMessageNotify(&wire.MsgDKVSNotify{
		Target: "topic-host", EventType: wire.DKVSNotifyEventMessage, Data: data,
	}); err != nil {
		t.Fatal(err)
	}

	pending, err := manager.topics.deliveries.Pending()
	if err != nil || len(pending) != 1 || pending[0].TargetCore != "core-b" {
		t.Fatalf("initial pending=%#v err=%v", pending, err)
	}

	bindings.set(recipient, "core-c")
	if err := manager.topics.RetryPendingResolved(); err != nil {
		t.Fatal(err)
	}
	pending, err = manager.topics.deliveries.Pending()
	if err != nil || len(pending) != 1 || pending[0].TargetCore != "core-c" {
		t.Fatalf("migrated pending=%#v err=%v", pending, err)
	}
	last := lastTestRoutedMessage(t, router)
	if last.Target != "core-c" {
		t.Fatalf("retry target=%s want=core-c", last.Target)
	}
	envelope, err := UnmarshalMessageEnvelope(last.Data)
	if err != nil || envelope.MessageType != MessageTypeTopicFanout {
		t.Fatalf("retry envelope=%#v err=%v", envelope, err)
	}
}

func TestTopicKeyRetryReGroupsMovedRecipient(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{
		owner:     "topic-host",
		recipient: "core-b",
	}}
	router := &testMessageRouter{}
	manager := newTestMessageManager("topic-host", bindings, &testMessageMailbox{}, router, nil, nil)
	if err := manager.topics.CreateTopic(TopicMeta{
		TopicName: "developers", OwnerAccount: owner, ServiceCoreNode: "topic-host",
		MaxMembers: 10,
	}); err != nil {
		t.Fatal(err)
	}
	forceTopicActiveMembersForTest(t, manager.topics, "developers", 2, recipient)
	keySeq := uint64(2)
	item := TopicKeyPackageRecipient{Recipient: recipient, EncryptedTopicKey: []byte("encrypted-key")}
	hash, err := topicKeyPackageSigningHash("developers", keySeq, owner, item)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(ownerPriv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	item.IssuerSignature = sig.Serialize()
	fanout := &TopicKeyFanoutMessage{
		TopicName: "developers", KeySeq: keySeq, IssuerAccount: owner,
		Recipients: []TopicKeyPackageRecipient{item},
	}
	if err := manager.topics.FanoutTopicKeyPackages(fanout); err != nil {
		t.Fatal(err)
	}
	pending, err := manager.topics.deliveries.Pending()
	if err != nil || len(pending) != 1 || pending[0].TargetCore != "core-b" {
		t.Fatalf("initial key pending=%#v err=%v", pending, err)
	}

	bindings.set(recipient, "core-c")
	if err := manager.topics.RetryPendingResolved(); err != nil {
		t.Fatal(err)
	}
	pending, err = manager.topics.deliveries.Pending()
	if err != nil || len(pending) != 1 || pending[0].TargetCore != "core-c" {
		t.Fatalf("migrated key pending=%#v err=%v", pending, err)
	}
	last := lastTestRoutedMessage(t, router)
	if last.Target != "core-c" {
		t.Fatalf("key retry target=%s want=core-c", last.Target)
	}
	envelope, err := UnmarshalMessageEnvelope(last.Data)
	if err != nil || envelope.MessageType != MessageTypeTopicKeyFanout {
		t.Fatalf("key retry envelope=%#v err=%v", envelope, err)
	}
}
