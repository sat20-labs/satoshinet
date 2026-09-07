package main

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	dbpkg "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

type e2eMailboxWriter struct{ idx *dkvs.Indexer }

func (w e2eMailboxWriter) PutDKVSInternalMailbox(record *wire.DKVSRecord) (bool, error) {
	return w.idx.PutInternalMailbox(record)
}

type e2eTopicBackend struct{ idx *dkvs.Indexer }

func (b e2eTopicBackend) PutDKVSInternalTopicValues(values map[string][]byte) (int, error) {
	return b.idx.PutInternalTopicValues(values)
}
func (b e2eTopicBackend) ListDKVSInternalTopicRecords() ([]*wire.DKVSRecord, error) {
	return b.idx.ListInternalTopicRecords()
}

func newMessageE2EIndexer(t *testing.T) *dkvs.Indexer {
	t.Helper()
	database := dbpkg.NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	return dkvs.New(database, dkvs.Config{
		EndpointID:    "test-core-node",
		CurrentHeight: func() uint64 { return 100 },
		MailboxPolicy: dkvs.MailboxPolicy{
			MaxMessages: 1000, MaxMsgBytes: 8 << 20, MaxMessagesPerSender: 1000,
			MaxMsgBytesPerSender: 8 << 20, MaxMsgSize: wire.MaxDKVSValueSize,
			MaxMsgTTL: 1000, MaxShares: 1000, MaxShareBytes: 8 << 20,
			MaxShareSize: wire.MaxDKVSValueSize, MaxShareTTL: 2000,
		},
	})
}

func e2eMailbox(idx *dkvs.Indexer) *DKVSMessageMailboxStore {
	return NewDKVSMessageMailboxStore(
		e2eMailboxWriter{idx: idx},
		func() uint64 { return 100 },
		func(string) uint64 { return 1000 },
		func(string) uint64 { return 2000 },
	)
}

type messageE2ENetwork struct {
	mu          sync.Mutex
	bootstrap   string
	nodes       map[string]*messageE2ENode
	edges       map[string]map[string]bool
	dropNextAck bool
	beforeLocal func(target string, message *wire.MsgDKVSNotify)
}

type messageE2ENode struct {
	id      string
	network *messageE2ENetwork
	manager *MessageManager
}

type messageE2EPeer struct {
	network *messageE2ENetwork
	from    string
	to      string
}

func (p *messageE2EPeer) Connected() bool { return p != nil && p.network != nil }
func (p *messageE2EPeer) QueueMessage(message wire.Message, _ chan<- struct{}) {
	notify, ok := message.(*wire.MsgDKVSNotify)
	if !ok {
		return
	}
	p.network.deliver(p.from, p.to, notify)
}

type messageE2ETransport struct{ node *messageE2ENode }

func (t messageE2ETransport) LocalCoreID() string { return t.node.id }
func (t messageE2ETransport) BootstrapCoreID() string {
	return t.node.network.bootstrap
}
func (t messageE2ETransport) DirectPeer(target string) messageNotifyPeer {
	if t.node.network.isConnected(t.node.id, target) {
		return &messageE2EPeer{network: t.node.network, from: t.node.id, to: target}
	}
	return nil
}
func (t messageE2ETransport) DeliverLocal(message *wire.MsgDKVSNotify) error {
	if t.node.manager == nil {
		return ErrMessageBindingNotFound
	}
	n := t.node.network
	n.mu.Lock()
	hook := n.beforeLocal
	n.mu.Unlock()
	if hook != nil {
		hook(t.node.id, message)
	}
	return t.node.manager.HandleMessageNotify(message)
}

type messageE2ERouter struct{ node *messageE2ENode }

func (r messageE2ERouter) RouteMessageNotify(message *wire.MsgDKVSNotify) error {
	return routeDirectedMessageNotify(messageE2ETransport{node: r.node}, message, false)
}

func newMessageE2ENetwork(bootstrap string, ids ...string) *messageE2ENetwork {
	n := &messageE2ENetwork{bootstrap: bootstrap, nodes: make(map[string]*messageE2ENode), edges: make(map[string]map[string]bool)}
	for _, id := range ids {
		n.nodes[id] = &messageE2ENode{id: id, network: n}
	}
	return n
}

func (n *messageE2ENetwork) connect(a, b string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.edges[a] == nil {
		n.edges[a] = make(map[string]bool)
	}
	if n.edges[b] == nil {
		n.edges[b] = make(map[string]bool)
	}
	n.edges[a][b] = true
	n.edges[b][a] = true
}

func (n *messageE2ENetwork) isConnected(a, b string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.edges[a] != nil && n.edges[a][b]
}

func wireRoundTripNotify(message *wire.MsgDKVSNotify) (*wire.MsgDKVSNotify, error) {
	var encoded bytes.Buffer
	if err := message.BtcEncode(&encoded, wire.ProtocolVersion, wire.BaseEncoding); err != nil {
		return nil, err
	}
	var decoded wire.MsgDKVSNotify
	if err := decoded.BtcDecode(bytes.NewReader(encoded.Bytes()), wire.ProtocolVersion, wire.BaseEncoding); err != nil {
		return nil, err
	}
	return &decoded, nil
}

func (n *messageE2ENetwork) deliver(from, to string, message *wire.MsgDKVSNotify) {
	decoded, err := wireRoundTripNotify(message)
	if err != nil {
		panic(err)
	}
	n.mu.Lock()
	if n.dropNextAck {
		if envelope, err := UnmarshalMessageEnvelope(decoded.Data); err == nil && envelope.MessageType == MessageTypeAck {
			n.dropNextAck = false
			n.mu.Unlock()
			return
		}
	}
	node := n.nodes[to]
	n.mu.Unlock()
	if node == nil {
		return
	}
	_ = from
	_ = routeDirectedMessageNotify(messageE2ETransport{node: node}, decoded, true)
}

func attachMessageE2EManager(node *messageE2ENode, bindings MessageBindingResolver, mailbox MessageMailboxStore, usage MessageUsageCharger) *MessageManager {
	manager := newTestMessageManager(node.id, bindings, mailbox, messageE2ERouter{node: node}, usage, nil)
	node.manager = manager
	return manager
}

func TestDKVSMessageE2EDirectViaBootstrapAckLossAndAccountBoundMailbox(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	network := newMessageE2ENetwork("bootstrap", "core-a", "bootstrap", "core-b")
	network.connect("core-a", "bootstrap")
	network.connect("bootstrap", "core-b")
	idxA := newMessageE2EIndexer(t)
	idxB := newMessageE2EIndexer(t)
	charger := &testMessageCharger{}
	managerA := attachMessageE2EManager(network.nodes["core-a"], bindings, e2eMailbox(idxA), charger)
	attachMessageE2EManager(network.nodes["core-b"], bindings, e2eMailbox(idxB), nil)

	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "encrypted-direct")
	network.dropNextAck = true
	if err := managerA.SendDirectMessage(message); err != nil {
		t.Fatal(err)
	}
	key, err := dkvs.MailMsgKey(recipient, sender, message.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := idxB.Get(key)
	if err != nil {
		t.Fatalf("recipient mailbox missing after bootstrap relay: %v", err)
	}
	decoded, err := UnmarshalDirectMessage(stored.Value)
	if err != nil || decoded.SenderAccount != sender || decoded.RecipientAccount != recipient || string(decoded.Ciphertext) != "encrypted-direct" {
		t.Fatalf("stored message=%#v err=%v", decoded, err)
	}
	pending, _ := managerA.accepted.(pendingMessageAcceptanceStore).Pending()
	if len(pending) != 1 || charger.unique() != 0 || managerA.GetNextSenderMsgID(sender) != 1 {
		t.Fatalf("pending=%d charges=%d next=%d", len(pending), charger.unique(), managerA.GetNextSenderMsgID(sender))
	}

	// Same signed request retries through Bootstrap; B returns an ACK after its
	// idempotent mailbox lookup, and A clears the durable outgoing job.
	if err := managerA.SendDirectMessage(message); err != nil {
		t.Fatal(err)
	}
	pending, _ = managerA.accepted.(pendingMessageAcceptanceStore).Pending()
	if len(pending) != 0 || charger.unique() != 0 || charger.callCount() != 0 {
		t.Fatalf("retry pending=%d unique charges=%d calls=%d", len(pending), charger.unique(), charger.callCount())
	}
	records, total, err := idxB.ListPrefix("/mail/"+recipient, 0, 100)
	if err != nil || total != 1 || len(records) != 1 {
		t.Fatalf("mailbox records=%d total=%d err=%v", len(records), total, err)
	}
	if _, err := idxB.GetForRelay(key); !errors.Is(err, dkvs.ErrRecordNotFound) {
		t.Fatalf("AccountBound mailbox was relayable: %v", err)
	}
	checkpoint, err := idxB.Checkpoint()
	if err != nil || checkpoint.ActiveRecordCount != 0 {
		t.Fatalf("mailbox leaked into checkpoint=%#v err=%v", checkpoint, err)
	}
	third := newMessageE2EIndexer(t)
	if _, err := third.AcceptRemoteRecord(stored, "core-b"); err == nil {
		t.Fatal("ordinary DKVS remote apply accepted AccountBound mailbox")
	}
}

func TestDKVSMessageE2ERetargetsSameMessageIDAfterRecipientMoves(t *testing.T) {
	senderPriv, sender := testAccount(t)
	_, recipient := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{sender: "core-a", recipient: "core-b"}}
	network := newMessageE2ENetwork("bootstrap", "core-a", "bootstrap", "core-b", "core-c")
	network.connect("core-a", "bootstrap")
	network.connect("bootstrap", "core-b")
	network.connect("bootstrap", "core-c")
	idxA := newMessageE2EIndexer(t)
	idxB := newMessageE2EIndexer(t)
	idxC := newMessageE2EIndexer(t)
	charger := &testMessageCharger{}
	managerA := attachMessageE2EManager(network.nodes["core-a"], bindings, e2eMailbox(idxA), charger)
	attachMessageE2EManager(network.nodes["core-b"], bindings, e2eMailbox(idxB), nil)
	attachMessageE2EManager(network.nodes["core-c"], bindings, e2eMailbox(idxC), nil)

	message := signedTestDirect(t, senderPriv, sender, recipient, 0, "move-safe")
	moved := false
	network.beforeLocal = func(target string, notify *wire.MsgDKVSNotify) {
		if target != "core-b" || moved {
			return
		}
		envelope, err := UnmarshalMessageEnvelope(notify.Data)
		if err == nil && envelope.MessageType == MessageTypeDirect {
			moved = true
			bindings.set(recipient, "core-c")
		}
	}
	if err := managerA.SendDirectMessage(message); err != nil {
		t.Fatal(err)
	}
	key, _ := dkvs.MailMsgKey(recipient, sender, message.MessageID)
	if _, err := idxB.Get(key); !errors.Is(err, dkvs.ErrRecordNotFound) {
		t.Fatalf("old CoreNode wrote moved recipient mailbox: %v", err)
	}
	pending, _ := managerA.accepted.(pendingMessageAcceptanceStore).Pending()
	if len(pending) != 1 || pending[0].Target != "core-b" {
		t.Fatalf("initial pending=%#v", pending)
	}
	network.beforeLocal = nil
	if err := managerA.SendDirectMessage(message); err != nil {
		t.Fatal(err)
	}
	if _, err := idxC.Get(key); err != nil {
		t.Fatalf("new CoreNode mailbox missing: %v", err)
	}
	pending, _ = managerA.accepted.(pendingMessageAcceptanceStore).Pending()
	if len(pending) != 0 || charger.unique() != 0 || managerA.GetNextSenderMsgID(sender) != 1 {
		t.Fatalf("pending=%d charges=%d next=%d", len(pending), charger.unique(), managerA.GetNextSenderMsgID(sender))
	}
}

func TestDKVSMessageE2ETopicFanoutMailboxAndCatalogRestart(t *testing.T) {
	ownerPriv, owner := testAccount(t)
	_, memberB := testAccount(t)
	_, memberC := testAccount(t)
	bindings := &testMessageBindings{bindings: map[string]string{owner: "host", memberB: "core-b", memberC: "core-c"}}
	network := newMessageE2ENetwork("bootstrap", "host", "bootstrap", "core-b", "core-c")
	network.connect("host", "bootstrap")
	network.connect("bootstrap", "core-b")
	network.connect("bootstrap", "core-c")
	hostIdx := newMessageE2EIndexer(t)
	idxB := newMessageE2EIndexer(t)
	idxC := newMessageE2EIndexer(t)
	charger := &testMessageCharger{}
	host := attachMessageE2EManager(network.nodes["host"], bindings, e2eMailbox(hostIdx), charger)
	attachMessageE2EManager(network.nodes["core-b"], bindings, e2eMailbox(idxB), nil)
	attachMessageE2EManager(network.nodes["core-c"], bindings, e2eMailbox(idxC), nil)
	catalog := NewDKVSTopicCatalogStore(e2eTopicBackend{idx: hostIdx})
	if err := host.topics.SetCatalogStore(catalog); err != nil {
		t.Fatal(err)
	}
	if err := host.topics.CreateTopic(TopicMeta{TopicName: "developers", DisplayName: "Developers", OwnerAccount: owner, ServiceCoreNode: "host"}); err != nil {
		t.Fatal(err)
	}
	forceTopicActiveMembersForTest(t, host.topics, "developers", 3, memberB, memberC)
	keySeq := uint64(3)
	publish := signedTestTopic(t, ownerPriv, owner, "developers", 0, keySeq, "one-topic-ciphertext")
	if err := host.topics.SendTopicMessage(publish); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		idx       *dkvs.Indexer
		recipient string
	}{{idxB, memberB}, {idxC, memberC}} {
		key, err := dkvs.MailTopicMessageKey(item.recipient, "developers", owner, publish.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		record, err := item.idx.Get(key)
		if err != nil {
			t.Fatalf("topic mailbox %s missing: %v", item.recipient, err)
		}
		stored, err := UnmarshalTopicPublish(record.Value)
		if err != nil || !bytes.Equal(stored.Ciphertext, publish.Ciphertext) || !bytes.Equal(stored.SenderSignature, publish.SenderSignature) {
			t.Fatalf("topic stored=%#v err=%v", stored, err)
		}
	}
	pending, err := host.topics.deliveries.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("topic pending=%d err=%v", len(pending), err)
	}
	if charger.unique() != 1 || host.GetNextSenderMsgID(owner) != 1 {
		t.Fatalf("topic charges=%d next=%d", charger.unique(), host.GetNextSenderMsgID(owner))
	}

	// Recreate the TopicManager over the same DKVS Host catalog and verify
	// CurrentKeySeq/membership survive a Host process restart.
	restarted := newTestMessageManager("host", bindings, e2eMailbox(hostIdx), messageE2ERouter{node: network.nodes["host"]}, charger, nil)
	if err := restarted.topics.SetCatalogStore(catalog); err != nil {
		t.Fatal(err)
	}
	state, members, err := restarted.topics.TopicState("developers")
	if err != nil || state.KeySeq != keySeq || state.MemberCount != 3 || len(members) != 3 {
		t.Fatalf("restarted state=%+v members=%d err=%v", state, len(members), err)
	}
	topicRecords, err := hostIdx.ListInternalTopicRecords()
	if err != nil || len(topicRecords) != 5 {
		t.Fatalf("topic catalog records=%d err=%v", len(topicRecords), err)
	}
	checkpoint, err := hostIdx.Checkpoint()
	if err != nil || checkpoint.ActiveRecordCount != 0 {
		t.Fatalf("host-local topic state leaked into checkpoint=%#v err=%v", checkpoint, err)
	}
}
