package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

func newTestMessageManager(localCore string, bindings MessageBindingResolver, mailbox MessageMailboxStore,
	router MessageRouter, usage MessageUsageCharger, accepted MessageAcceptanceStore) *MessageManager {
	manager := NewMessageManager(localCore, bindings, mailbox, router, usage, accepted)
	manager.SetCoreSigner(func(payload []byte) ([]byte, error) {
		digest := sha256.Sum256(payload)
		return digest[:], nil
	})
	manager.coreAuthority = func(string) bool { return true }
	manager.fanoutVerifier = func(target string, envelope *MessageEnvelope) error {
		payload, err := topicDeliverySigningPayload(target, envelope)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		if !bytes.Equal(envelope.Signature, digest[:]) {
			return ErrMessageInvalidSignature
		}
		return nil
	}
	manager.setCoreVerifier(func(source, target string, ack *MessageAck) error {
		payload, err := messageAckSigningPayload(source, target, ack)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		if !bytes.Equal(ack.Signature, digest[:]) {
			return ErrMessageInvalidSignature
		}
		return nil
	})
	return manager
}

func marshalTestMessageAck(t *testing.T, source, target string, ack *MessageAck) []byte {
	t.Helper()
	payload, err := messageAckSigningPayload(source, target, ack)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	ack.Signature = digest[:]
	encoded, err := MarshalMessageAck(ack)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestDKVSMessageAckIsSignedBySourceCoreNode(t *testing.T) {
	coreKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	targetKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	_, account := testAccount(t)
	source := hex.EncodeToString(coreKey.PubKey().SerializeCompressed())
	target := hex.EncodeToString(targetKey.PubKey().SerializeCompressed())
	ack := &MessageAck{
		OriginalType: MessageTypeDirect, SenderAccount: account, SenderMsgID: 3,
		MessageID: testStableMessageID("signed-ack"), Status: MessageAckOK,
	}
	payload, err := messageAckSigningPayload(source, target, ack)
	if err != nil {
		t.Fatal(err)
	}
	ack.Signature = ecdsa.Sign(coreKey, chainhash.HashB(payload)).Serialize()
	if err := verifyCoreMessageAck(source, target, ack); err != nil {
		t.Fatalf("valid CoreNode ACK rejected: %v", err)
	}
	ack.Status = MessageAckRejected
	if err := verifyCoreMessageAck(source, target, ack); !errors.Is(err, ErrMessageInvalidSignature) {
		t.Fatalf("tampered ACK err=%v", err)
	}
}

type testMessageBindings struct {
	mu       sync.RWMutex
	bindings map[string]string
}

func (b *testMessageBindings) CoreNodeForAccount(accountID string) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	core := b.bindings[accountID]
	if core == "" {
		return "", ErrMessageBindingNotFound
	}
	return core, nil
}
func (b *testMessageBindings) AccountBoundToCore(accountID, coreNodeID string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.bindings[accountID] == coreNodeID
}
func (b *testMessageBindings) set(accountID, coreNodeID string) {
	b.mu.Lock()
	b.bindings[accountID] = coreNodeID
	b.mu.Unlock()
}

type testMessageRouter struct {
	mu       sync.Mutex
	messages []*wire.MsgDKVSNotify
	hook     func(*wire.MsgDKVSNotify) error
}

func (r *testMessageRouter) RouteMessageNotify(message *wire.MsgDKVSNotify) error {
	r.mu.Lock()
	copyMessage := *message
	copyMessage.Data = append([]byte(nil), message.Data...)
	r.messages = append(r.messages, &copyMessage)
	hook := r.hook
	r.mu.Unlock()
	if hook != nil {
		return hook(message)
	}
	return nil
}
func (r *testMessageRouter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

type testMessageCharger struct {
	mu      sync.Mutex
	calls   int
	charged map[string]struct{}
}

func (c *testMessageCharger) ChargeMessageSend(account, messageID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.charged == nil {
		c.charged = make(map[string]struct{})
	}
	c.charged[acceptedMessageKey(account, messageID)] = struct{}{}
	return nil
}
func (c *testMessageCharger) unique() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.charged)
}
func (c *testMessageCharger) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

type testMessageMailbox struct {
	mu        sync.Mutex
	direct    map[string][]byte
	topic     map[string][]byte
	keys      map[string][]byte
	appendErr error
	onAppend  func()
}

func (m *testMessageMailbox) AppendDirectMessage(message *DirectMessage) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appendErr != nil {
		return false, m.appendErr
	}
	if m.direct == nil {
		m.direct = make(map[string][]byte)
	}
	key := acceptedMessageKey(message.SenderAccount, message.MessageID) + ":" + message.RecipientAccount
	value, _ := MarshalDirectMessage(message, true)
	if previous, ok := m.direct[key]; ok {
		if string(previous) != string(value) {
			return false, ErrMessageInvalidEnvelope
		}
		return false, nil
	}
	m.direct[key] = value
	if m.onAppend != nil {
		m.onAppend()
	}
	return true, nil
}
func (m *testMessageMailbox) AppendTopicMessage(recipient string, message *TopicFanoutMessage) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appendErr != nil {
		return false, m.appendErr
	}
	if m.topic == nil {
		m.topic = make(map[string][]byte)
	}
	key := recipient + ":" + message.TopicName + ":" + acceptedMessageKey(message.SenderAccount, message.MessageID)
	value, _ := MarshalTopicFanout(message)
	if previous, ok := m.topic[key]; ok {
		if string(previous) != string(value) {
			return false, ErrMessageInvalidEnvelope
		}
		return false, nil
	}
	m.topic[key] = value
	return true, nil
}
func (m *testMessageMailbox) AppendTopicKeyPackage(recipient string, message *TopicKeyFanoutMessage, item TopicKeyPackageRecipient) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appendErr != nil {
		return false, m.appendErr
	}
	if m.keys == nil {
		m.keys = make(map[string][]byte)
	}
	key := recipient + ":" + message.TopicName
	value, _ := MarshalTopicKeyFanout(&TopicKeyFanoutMessage{TopicName: message.TopicName, KeySeq: message.KeySeq, IssuerAccount: message.IssuerAccount, Recipients: []TopicKeyPackageRecipient{item}})
	if previous, ok := m.keys[key]; ok {
		if string(previous) != string(value) {
			return false, ErrMessageInvalidEnvelope
		}
		return false, nil
	}
	m.keys[key] = value
	return true, nil
}

func testAccount(t *testing.T) (*btcec.PrivateKey, string) {
	t.Helper()
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return priv, dkvs.AccountID(priv.PubKey().SerializeCompressed())
}

func testStableMessageID(parts ...interface{}) string {
	digest := sha256.Sum256([]byte(fmt.Sprint(parts...)))
	return hex.EncodeToString(digest[:16])
}

func signedTestDirect(t *testing.T, priv *btcec.PrivateKey, sender, recipient string, msgID uint64, body string) *DirectMessage {
	t.Helper()
	message := &DirectMessage{SenderAccount: sender, RecipientAccount: recipient, SenderMsgID: msgID, MessageID: testStableMessageID("direct", sender, recipient, msgID, body), Ciphertext: []byte(body)}
	hash, err := directMessageSigningHash(message)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	message.SenderSignature = sig.Serialize()
	return message
}

func signedTestTopic(t *testing.T, priv *btcec.PrivateKey, sender, topic string, msgID, keySeq uint64, body string) *TopicPublishMessage {
	t.Helper()
	message := &TopicPublishMessage{TopicName: topic, SenderAccount: sender, SenderMsgID: msgID, MessageID: testStableMessageID("topic", topic, sender, msgID, keySeq, body), KeySeq: keySeq, Ciphertext: []byte(body)}
	hash, err := topicMessageSigningHash(message)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := schnorr.Sign(priv, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	message.SenderSignature = sig.Serialize()
	return message
}

func TestDKVSMessageSenderSequenceFreeDirectRetryAndFinalAck(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	router := &testMessageRouter{}
	charger := &testMessageCharger{}
	store := newMemoryMessageAcceptanceStore()
	manager := newTestMessageManager("core-a", bindings, &testMessageMailbox{}, router, charger, store)

	for id := uint64(0); id < 3; id++ {
		if err := manager.SendDirectMessage(signedTestDirect(t, senderPriv, sender, recipient, id, string(rune('a'+id)))); err != nil {
			t.Fatalf("send %d: %v", id, err)
		}
	}
	if got := manager.GetNextSenderMsgID(sender); got != 3 {
		t.Fatalf("next id=%d want=3", got)
	}
	if charger.unique() != 0 || charger.callCount() != 0 {
		t.Fatalf("charges unique=%d calls=%d", charger.unique(), charger.callCount())
	}
	if router.count() != 3 {
		t.Fatalf("routes=%d", router.count())
	}

	// Network retry with the same signed bytes is routed again but neither
	// consumes a sequence nor introduces a Direct charge.
	retry := signedTestDirect(t, senderPriv, sender, recipient, 2, "c")
	if err := manager.SendDirectMessage(retry); err != nil {
		t.Fatal(err)
	}
	if charger.callCount() != 0 || manager.GetNextSenderMsgID(sender) != 3 || router.count() != 4 {
		t.Fatalf("retry charges=%d next=%d routes=%d", charger.callCount(), manager.GetNextSenderMsgID(sender), router.count())
	}

	if err := manager.SendDirectMessage(signedTestDirect(t, senderPriv, sender, recipient, 4, "gap")); !errors.Is(err, ErrMessageInvalidSequence) {
		t.Fatalf("gap err=%v", err)
	}
	if err := manager.SendDirectMessage(signedTestDirect(t, senderPriv, sender, recipient, 2, "different")); !errors.Is(err, ErrMessageInvalidSequence) {
		t.Fatalf("same id different content err=%v", err)
	}

	ackPayload := marshalTestMessageAck(t, "core-b", "core-a", &MessageAck{OriginalType: MessageTypeDirect, SenderAccount: sender, SenderMsgID: 2, MessageID: retry.MessageID, Status: MessageAckOK})
	ackData, err := MarshalMessageEnvelope(&MessageEnvelope{MessageType: MessageTypeAck, SourceCoreNode: "core-b", Payload: ackPayload})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.HandleMessageNotify(&wire.MsgDKVSNotify{Target: "core-a", EventType: wire.DKVSNotifyEventMessage, Data: ackData}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SendDirectMessage(retry); err != nil {
		t.Fatal(err)
	}
	if router.count() != 4 || charger.callCount() != 0 {
		t.Fatalf("final duplicate routes=%d charges=%d", router.count(), charger.callCount())
	}
}

func TestDKVSMessageRejectsAuthorFromNonBoundSourceCore(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	mailbox := &testMessageMailbox{}
	manager := newTestMessageManager("core-b", bindings, mailbox, &testMessageRouter{}, nil, nil)
	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "ciphertext")
	payload, err := MarshalDirectMessage(message, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := MarshalMessageEnvelope(&MessageEnvelope{
		MessageType: MessageTypeDirect, SourceCoreNode: "forged-core", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.HandleMessageNotify(&wire.MsgDKVSNotify{
		Target: "core-b", EventType: wire.DKVSNotifyEventMessage, Data: data,
	})
	if !errors.Is(err, ErrMessageNotBoundHere) {
		t.Fatalf("forged source err=%v", err)
	}
	if len(mailbox.direct) != 0 {
		t.Fatalf("forged source wrote mailbox: %#v", mailbox.direct)
	}
}

func TestDKVSMessageRejectsAckFromUnexpectedCore(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	manager := newTestMessageManager("core-a", bindings, &testMessageMailbox{}, &testMessageRouter{}, nil, nil)
	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "ciphertext")
	if err := manager.SendDirectMessage(message); err != nil {
		t.Fatal(err)
	}
	ackPayload := marshalTestMessageAck(t, "core-c", "core-a", &MessageAck{
		OriginalType: MessageTypeDirect, SenderAccount: sender, SenderMsgID: 0,
		MessageID: message.MessageID, Status: MessageAckOK,
	})
	ackData, err := MarshalMessageEnvelope(&MessageEnvelope{
		MessageType: MessageTypeAck, SourceCoreNode: "core-c", Payload: ackPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.HandleMessageNotify(&wire.MsgDKVSNotify{
		Target: "core-a", EventType: wire.DKVSNotifyEventMessage, Data: ackData,
	})
	if !errors.Is(err, ErrMessageInvalidEnvelope) {
		t.Fatalf("unexpected ACK source err=%v", err)
	}
	pending, err := manager.accepted.(pendingMessageAcceptanceStore).Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%#v err=%v", pending, err)
	}
}

func TestDKVSMessageDirectAckOccursAfterMailboxCommitAndRetryIsIdempotent(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	mailbox := &testMessageMailbox{}
	committed := false
	mailbox.onAppend = func() { committed = true }
	ackRouter := &testMessageRouter{}
	ackRouter.hook = func(message *wire.MsgDKVSNotify) error {
		if !committed {
			t.Fatal("ACK routed before mailbox commit")
		}
		envelope, err := UnmarshalMessageEnvelope(message.Data)
		if err != nil {
			return err
		}
		ack, err := UnmarshalMessageAck(envelope.Payload)
		if err != nil {
			return err
		}
		if ack.Status != MessageAckOK {
			t.Fatalf("ack status=%d", ack.Status)
		}
		return nil
	}
	receiver := newTestMessageManager("core-b", bindings, mailbox, ackRouter, nil, nil)
	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "ciphertext")
	payload, _ := MarshalDirectMessage(message, true)
	data, _ := MarshalMessageEnvelope(&MessageEnvelope{MessageType: MessageTypeDirect, SourceCoreNode: "core-a", Payload: payload})
	notify := &wire.MsgDKVSNotify{Target: "core-b", EventType: wire.DKVSNotifyEventMessage, Data: data}
	if err := receiver.HandleMessageNotify(notify); err != nil {
		t.Fatal(err)
	}
	// Simulate the recipient persisting the message locally and deleting the
	// endpoint cache entry before the source receives its ACK. A network retry
	// must be ACKed without recreating the deleted mailbox KV.
	mailbox.mu.Lock()
	for key := range mailbox.direct {
		delete(mailbox.direct, key)
	}
	mailbox.mu.Unlock()
	if err := receiver.HandleMessageNotify(notify); err != nil {
		t.Fatal(err)
	}
	mailbox.mu.Lock()
	stored := len(mailbox.direct)
	mailbox.mu.Unlock()
	if stored != 0 || ackRouter.count() != 2 {
		t.Fatalf("stored=%d acks=%d", stored, ackRouter.count())
	}
}

func TestDKVSMessageDirectRejectsTampering(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	manager := newTestMessageManager("core-a", bindings, &testMessageMailbox{}, &testMessageRouter{}, &testMessageCharger{}, nil)
	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "ciphertext")
	message.Ciphertext[0] ^= 1
	if err := manager.SendDirectMessage(message); !errors.Is(err, ErrMessageInvalidSignature) {
		t.Fatalf("tampered err=%v", err)
	}
}

func TestDKVSMessageDirectAndTopicShareSenderSequence(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	router := &testMessageRouter{}
	charger := &testMessageCharger{}
	manager := newTestMessageManager("core-a", bindings, &testMessageMailbox{}, router, charger, nil)
	if err := manager.SendDirectMessage(signedTestDirect(t, senderPriv, sender, recipient, 0, "direct")); err != nil {
		t.Fatal(err)
	}
	if err := manager.topics.SendTopicMessageToHost(signedTestTopic(t, senderPriv, sender, "developers", 1, 1, "topic"), "topic-host"); err != nil {
		t.Fatal(err)
	}
	if manager.GetNextSenderMsgID(sender) != 2 || charger.unique() != 1 {
		t.Fatalf("next=%d charges=%d", manager.GetNextSenderMsgID(sender), charger.unique())
	}
}

func TestDKVSMessageConcurrentSameIDOnlyOneContentAccepted(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	shared := newMemoryMessageAcceptanceStore()
	charger := &testMessageCharger{}
	managerA := newTestMessageManager("core-a", bindings, &testMessageMailbox{}, &testMessageRouter{}, charger, shared)
	managerB := newTestMessageManager("core-a", bindings, &testMessageMailbox{}, &testMessageRouter{}, charger, shared)
	messages := []*DirectMessage{
		signedTestDirect(t, senderPriv, sender, recipient, 0, "phone"),
		signedTestDirect(t, senderPriv, sender, recipient, 0, "desktop"),
	}
	errs := make(chan error, 2)
	go func() { errs <- managerA.SendDirectMessage(messages[0]) }()
	go func() { errs <- managerB.SendDirectMessage(messages[1]) }()
	first, second := <-errs, <-errs
	accepted := 0
	rejected := 0
	for _, err := range []error{first, second} {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrMessageInvalidSequence) {
			rejected++
		} else {
			t.Fatalf("unexpected err=%v", err)
		}
	}
	if accepted != 1 || rejected != 1 || shared.NextSenderMsgID(sender) != 1 || charger.unique() != 0 {
		t.Fatalf("accepted=%d rejected=%d next=%d charges=%d", accepted, rejected, shared.NextSenderMsgID(sender), charger.unique())
	}
}
