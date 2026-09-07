package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/wire"
)

type orderingTopicDeliveryStore struct {
	saved bool
	items []TopicPendingDelivery
}

func (s *orderingTopicDeliveryStore) SavePending(deliveries []TopicPendingDelivery) error {
	s.saved = true
	s.items = append(s.items, deliveries...)
	return nil
}
func (s *orderingTopicDeliveryStore) Acknowledge(string, string, MessageAckStatus) error { return nil }
func (s *orderingTopicDeliveryStore) Pending() ([]TopicPendingDelivery, error) {
	return append([]TopicPendingDelivery(nil), s.items...), nil
}

func syntheticMessageAccount(n int) string { return fmt.Sprintf("%064x", n+1) }

func signedTopicKeyPackage(t *testing.T, issuerPriv *btcec.PrivateKey, issuer, topic string, keySeq uint64, recipient string, key []byte) TopicKeyPackageRecipient {
	t.Helper()
	item := TopicKeyPackageRecipient{Recipient: recipient, EncryptedTopicKey: append([]byte(nil), key...)}
	hash, err := topicKeyPackageSigningHash(topic, keySeq, issuer, item)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(issuerPriv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	item.IssuerSignature = sig.Serialize()
	return item
}

func TestDKVSMessageTopicFanout1000MembersAcross20CoreNodes(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	bindings := &testMessageBindings{bindings: make(map[string]string)}
	bindings.bindings[owner] = "host"
	router := &testMessageRouter{}
	manager := newTestMessageManager("host", bindings, &testMessageMailbox{}, router, nil, nil)
	if err := manager.topics.CreateTopic(TopicMeta{TopicName: "developers", OwnerAccount: owner, ServiceCoreNode: "host", MaxMembers: 1000}); err != nil {
		t.Fatal(err)
	}

	stableMembers := make([]string, 0, 999)
	for n := 0; n < 999; n++ {
		account := syntheticMessageAccount(n + 10000)
		bindings.bindings[account] = fmt.Sprintf("core-%02d", n%20)
		stableMembers = append(stableMembers, account)
	}
	forceTopicActiveMembersForTest(t, manager.topics, "developers", 2, stableMembers...)
	state, members, err := manager.topics.TopicState("developers")
	if err != nil {
		t.Fatal(err)
	}
	if state.MemberCount != 1000 || len(members) != 1000 || state.KeySeq != 2 {
		t.Fatalf("state=%+v members=%d", state, len(members))
	}

	publish := signedTestTopic(t, ownerPriv, owner, "developers", 0, state.KeySeq, "one-shared-ciphertext")
	payload, err := MarshalTopicPublish(publish, true)
	if err != nil {
		t.Fatal(err)
	}
	envelope := &MessageEnvelope{MessageType: MessageTypeTopicPublish, SourceCoreNode: "host", Payload: payload}
	if err := manager.topics.handlePublish(envelope); err != nil {
		t.Fatal(err)
	}

	router.mu.Lock()
	routed := append([]*wire.MsgDKVSNotify(nil), router.messages...)
	router.mu.Unlock()
	fanouts := 0
	recipients := 0
	cores := make(map[string]struct{})
	for _, notify := range routed {
		envelope, err := UnmarshalMessageEnvelope(notify.Data)
		if err != nil {
			t.Fatal(err)
		}
		if envelope.MessageType == MessageTypeAck {
			continue
		}
		if envelope.MessageType != MessageTypeTopicFanout {
			t.Fatalf("unexpected type=%d", envelope.MessageType)
		}
		fanout, err := UnmarshalTopicFanout(envelope.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fanout.Ciphertext, publish.Ciphertext) || !bytes.Equal(fanout.SenderSignature, publish.SenderSignature) {
			t.Fatal("fanout did not reuse the original signed ciphertext")
		}
		fanouts++
		recipients += len(fanout.Recipients)
		cores[notify.Target] = struct{}{}
	}
	if fanouts != 20 || len(cores) != 20 || recipients != 999 {
		t.Fatalf("fanouts=%d cores=%d recipients=%d", fanouts, len(cores), recipients)
	}
	pending, err := manager.topics.deliveries.Pending()
	if err != nil || len(pending) != 20 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
}

func TestDKVSMessageTopicPublishAckAfterDurablePending(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	_, member := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{owner: "host", member: "core-b"}}
	deliveryStore := &orderingTopicDeliveryStore{}
	router := &testMessageRouter{}
	manager := newTestMessageManager("host", bindings, &testMessageMailbox{}, router, nil, nil)
	manager.topics.SetDeliveryStore(deliveryStore)
	if err := manager.topics.CreateTopic(TopicMeta{TopicName: "topic", OwnerAccount: owner, ServiceCoreNode: "host"}); err != nil {
		t.Fatal(err)
	}
	forceTopicActiveMembersForTest(t, manager.topics, "topic", 2, member)
	keySeq := uint64(2)
	router.hook = func(message *wire.MsgDKVSNotify) error {
		envelope, err := UnmarshalMessageEnvelope(message.Data)
		if err != nil {
			return err
		}
		if envelope.MessageType == MessageTypeAck && !deliveryStore.saved {
			t.Fatal("Topic Publish ACK was emitted before durable pending commit")
		}
		return nil
	}
	publish := signedTestTopic(t, ownerPriv, owner, "topic", 0, keySeq, "ciphertext")
	payload, _ := MarshalTopicPublish(publish, true)
	if err := manager.topics.handlePublish(&MessageEnvelope{MessageType: MessageTypeTopicPublish, SourceCoreNode: "host", Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if !deliveryStore.saved || len(deliveryStore.items) != 1 {
		t.Fatalf("saved=%v items=%d", deliveryStore.saved, len(deliveryStore.items))
	}
}

func TestDKVSMessageTopicMembershipRotatesKeySeq(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	_, member := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{owner: "host", member: "core-b"}}
	manager := newTestMessageManager("host", bindings, &testMessageMailbox{}, &testMessageRouter{}, nil, nil)
	if err := manager.topics.CreateTopic(TopicMeta{TopicName: "topic", OwnerAccount: owner, ServiceCoreNode: "host"}); err != nil {
		t.Fatal(err)
	}
	state, _, _ := manager.topics.TopicState("topic")
	if state.KeySeq != 1 || state.MemberCount != 1 {
		t.Fatalf("initial state=%+v", state)
	}

	base, err := manager.topics.RequestJoin("topic", member)
	if err != nil || base != 1 {
		t.Fatalf("join request keyseq=%d err=%v", base, err)
	}
	assertTopicMemberStatusForTest(t, manager.topics, "topic", member, "PENDING_JOIN")
	state, _, _ = manager.topics.TopicState("topic")
	if state.KeySeq != 1 || state.MemberCount != 1 {
		t.Fatalf("join request changed effective state=%+v", state)
	}
	joined := commitTopicJoinForTest(t, manager.topics, ownerPriv, owner, "topic", member)
	if joined != 2 {
		t.Fatalf("join commit keyseq=%d", joined)
	}
	assertTopicMemberStatusForTest(t, manager.topics, "topic", member, "ACTIVE")
	state, _, _ = manager.topics.TopicState("topic")
	if state.KeySeq != 2 || state.MemberCount != 2 {
		t.Fatalf("post-join state=%+v", state)
	}
	if again, err := manager.topics.RequestJoin("topic", member); err != nil || again != 2 {
		t.Fatalf("idempotent active join request keyseq=%d err=%v", again, err)
	}

	leaveBase, err := manager.topics.RequestLeave("topic", member)
	if err != nil || leaveBase != 2 {
		t.Fatalf("leave request keyseq=%d err=%v", leaveBase, err)
	}
	assertTopicMemberStatusForTest(t, manager.topics, "topic", member, "PENDING_LEAVE")
	state, _, _ = manager.topics.TopicState("topic")
	if state.KeySeq != 2 || state.MemberCount != 2 {
		t.Fatalf("leave request changed key-holder state=%+v", state)
	}
	left := commitTopicLeaveForTest(t, manager.topics, ownerPriv, owner, "topic", member)
	if left != 3 {
		t.Fatalf("leave commit keyseq=%d", left)
	}
	assertTopicMemberStatusForTest(t, manager.topics, "topic", member, "LEFT")
	state, _, _ = manager.topics.TopicState("topic")
	if state.KeySeq != 3 || state.MemberCount != 1 {
		t.Fatalf("post-leave state=%+v", state)
	}
	if again, err := manager.topics.RequestLeave("topic", member); err != nil || again != 3 {
		t.Fatalf("idempotent left request keyseq=%d err=%v", again, err)
	}

	_, rejected := testAccount(t)
	bindings.bindings[rejected] = "core-c"
	if _, err := manager.topics.RequestJoin("topic", rejected); err != nil {
		t.Fatal(err)
	}
	rejection := signedTopicJoinRejectionForTest(t, ownerPriv, owner, "topic", "host", rejected)
	if err := manager.topics.RejectJoin(rejection); err != nil {
		t.Fatal(err)
	}
	if err := manager.topics.RejectJoin(rejection); err != nil {
		t.Fatalf("exact join rejection retry: %v", err)
	}
}

func TestDKVSMessageTopicKeyPackageOnlyForCurrentMembers(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	_, active := testAccount(t)
	_, removed := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{owner: "host", active: "core-a", removed: "core-b"}}
	manager := newTestMessageManager("host", bindings, &testMessageMailbox{}, &testMessageRouter{}, nil, nil)
	if err := manager.topics.CreateTopic(TopicMeta{TopicName: "topic", OwnerAccount: owner, ServiceCoreNode: "host"}); err != nil {
		t.Fatal(err)
	}
	forceTopicActiveMembersForTest(t, manager.topics, "topic", 3, active, removed)
	if _, err := manager.topics.RequestLeave("topic", removed); err != nil {
		t.Fatal(err)
	}
	commit := signedTopicMembershipCommitForTest(t, ownerPriv, owner, "topic", "host", 3,
		wire.TopicMembershipRemove, removed, []string{owner, active})
	if err := manager.topics.CommitMembershipChange(commit); err != nil {
		t.Fatal(err)
	}
	keySeq := commit.NewKeySeq
	activePackage := signedTopicKeyPackage(t, ownerPriv, owner, "topic", keySeq, active, []byte("active-key"))
	if err := manager.topics.FanoutTopicKeyPackages(&TopicKeyFanoutMessage{TopicName: "topic", KeySeq: keySeq, IssuerAccount: owner, Recipients: []TopicKeyPackageRecipient{activePackage}}); err != nil {
		t.Fatalf("active package: %v", err)
	}
	removedPackage := signedTopicKeyPackage(t, ownerPriv, owner, "topic", keySeq, removed, []byte("removed-key"))
	err := manager.topics.FanoutTopicKeyPackages(&TopicKeyFanoutMessage{TopicName: "topic", KeySeq: keySeq, IssuerAccount: owner, Recipients: []TopicKeyPackageRecipient{removedPackage}})
	if !errors.Is(err, ErrMessageTopicPermission) {
		t.Fatalf("removed member package err=%v", err)
	}
}

func TestDKVSMessageTopicFanoutTamperingFailsSignature(t *testing.T) {
	priv, sender := testAccount(t)
	_, recipient := testAccount(t)
	publish := signedTestTopic(t, priv, sender, "topic", 7, 3, "ciphertext")
	fanout := &TopicFanoutMessage{
		TopicName: publish.TopicName, SenderAccount: sender, SenderMsgID: publish.SenderMsgID,
		MessageID: publish.MessageID, KeySeq: publish.KeySeq, Ciphertext: append([]byte(nil), publish.Ciphertext...),
		SenderSignature: append([]byte(nil), publish.SenderSignature...), Recipients: []string{recipient},
	}
	if err := verifyTopicFanoutSignature(fanout); err != nil {
		t.Fatal(err)
	}
	fanout.Ciphertext[0] ^= 1
	if err := verifyTopicFanoutSignature(fanout); !errors.Is(err, ErrMessageInvalidSignature) {
		t.Fatalf("tamper err=%v", err)
	}
}

func TestDKVSMessageTopicPendingAckUsesDeliveryID(t *testing.T) {
	store := newMemoryTopicDeliveryStore()
	deliveries := []TopicPendingDelivery{
		{DeliveryID: fmt.Sprintf("%064x", 1), MessageType: MessageTypeTopicFanout, TopicName: "topic", SenderAccount: syntheticMessageAccount(1), SenderMsgID: 1, TargetCore: "core-a", Data: []byte("a")},
		{DeliveryID: fmt.Sprintf("%064x", 2), MessageType: MessageTypeTopicFanout, TopicName: "topic", SenderAccount: syntheticMessageAccount(1), SenderMsgID: 1, TargetCore: "core-a", Data: []byte("b")},
	}
	if err := store.SavePending(deliveries); err != nil {
		t.Fatal(err)
	}
	if err := store.Acknowledge(deliveries[0].DeliveryID, "core-a", MessageAckOK); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Pending()
	if err != nil || len(pending) != 1 || pending[0].DeliveryID != deliveries[1].DeliveryID {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
}

func TestDKVSMessageTopicMembershipCommitExactReplay(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	_, member := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{owner: "host", member: "core-b"}}
	manager := newTestMessageManager("host", bindings, &testMessageMailbox{}, &testMessageRouter{}, nil, nil)
	if err := manager.topics.CreateTopic(TopicMeta{TopicName: "topic", OwnerAccount: owner, ServiceCoreNode: "host"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.topics.RequestJoin("topic", member); err != nil {
		t.Fatal(err)
	}
	commit := signedTopicMembershipCommitForTest(t, ownerPriv, owner, "topic", "host", 1,
		wire.TopicMembershipAdd, member, []string{owner, member})
	if err := manager.topics.CommitMembershipChange(commit); err != nil {
		t.Fatal(err)
	}
	state, _, err := manager.topics.TopicState("topic")
	if err != nil || state.KeySeq != 2 || state.LastCommitHash == "" {
		t.Fatalf("committed state=%+v err=%v", state, err)
	}
	if err := manager.topics.CommitMembershipChange(commit); err != nil {
		t.Fatalf("exact commit replay failed: %v", err)
	}
	stateAfterReplay, _, _ := manager.topics.TopicState("topic")
	if stateAfterReplay != state {
		t.Fatalf("exact replay changed state before=%+v after=%+v", state, stateAfterReplay)
	}

	changed := *commit
	changed.KeyPackages = append([]TopicKeyPackageRecipient(nil), commit.KeyPackages...)
	changed.KeyPackages[0] = commit.KeyPackages[0]
	changed.KeyPackages[0].EncryptedTopicKey = []byte("different-key-package")
	packageHash, err := topicKeyPackageSigningHash(changed.TopicName, changed.NewKeySeq, changed.IssuerAccount, changed.KeyPackages[0])
	if err != nil {
		t.Fatal(err)
	}
	packageSig, err := schnorr.Sign(ownerPriv, packageHash[:])
	if err != nil {
		t.Fatal(err)
	}
	changed.KeyPackages[0].IssuerSignature = packageSig.Serialize()
	commitHash, err := wire.TopicMembershipCommitSigningHash(&changed)
	if err != nil {
		t.Fatal(err)
	}
	commitSig, err := schnorr.Sign(ownerPriv, commitHash[:])
	if err != nil {
		t.Fatal(err)
	}
	changed.IssuerSignature = commitSig.Serialize()
	if err := manager.topics.CommitMembershipChange(&changed); !errors.Is(err, ErrMessageTopicKeyMismatch) {
		t.Fatalf("different same-seq commit err=%v", err)
	}
}
