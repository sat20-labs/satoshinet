package main

import (
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
)

type testMessageNotifyPeer struct {
	connected bool
	messages  []*wire.MsgDKVSNotify
}

func (p *testMessageNotifyPeer) Connected() bool { return p != nil && p.connected }
func (p *testMessageNotifyPeer) QueueMessage(message wire.Message, _ chan<- struct{}) {
	if notify, ok := message.(*wire.MsgDKVSNotify); ok {
		copyMessage := *notify
		copyMessage.Data = append([]byte(nil), notify.Data...)
		p.messages = append(p.messages, &copyMessage)
	}
}

type testCoreMessageTransport struct {
	local     string
	bootstrap string
	peers     map[string]*testMessageNotifyPeer
	localMsgs []*wire.MsgDKVSNotify
	localErr  error
}

func (t *testCoreMessageTransport) LocalCoreID() string     { return t.local }
func (t *testCoreMessageTransport) BootstrapCoreID() string { return t.bootstrap }
func (t *testCoreMessageTransport) DirectPeer(id string) messageNotifyPeer {
	if t == nil {
		return nil
	}
	return t.peers[id]
}
func (t *testCoreMessageTransport) DeliverLocal(message *wire.MsgDKVSNotify) error {
	copyMessage := *message
	copyMessage.Data = append([]byte(nil), message.Data...)
	t.localMsgs = append(t.localMsgs, &copyMessage)
	return t.localErr
}

func directedTestNotify(target string) *wire.MsgDKVSNotify {
	return &wire.MsgDKVSNotify{Target: target, EventType: wire.DKVSNotifyEventMessage, Data: []byte("opaque")}
}

func TestDKVSMessageRouteLocalDirectBootstrapAndNoBroadcast(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		transport := &testCoreMessageTransport{local: "core-a", bootstrap: "bootstrap", peers: map[string]*testMessageNotifyPeer{}}
		if err := routeDirectedMessageNotify(transport, directedTestNotify("core-a"), false); err != nil {
			t.Fatal(err)
		}
		if len(transport.localMsgs) != 1 {
			t.Fatalf("local deliveries=%d", len(transport.localMsgs))
		}
	})

	t.Run("direct-peer", func(t *testing.T) {
		target := &testMessageNotifyPeer{connected: true}
		bootstrap := &testMessageNotifyPeer{connected: true}
		transport := &testCoreMessageTransport{local: "core-a", bootstrap: "bootstrap", peers: map[string]*testMessageNotifyPeer{
			"core-b": target, "bootstrap": bootstrap,
		}}
		if err := routeDirectedMessageNotify(transport, directedTestNotify("core-b"), false); err != nil {
			t.Fatal(err)
		}
		if len(target.messages) != 1 || len(bootstrap.messages) != 0 {
			t.Fatalf("target=%d bootstrap=%d", len(target.messages), len(bootstrap.messages))
		}
	})

	t.Run("bootstrap-fallback", func(t *testing.T) {
		bootstrap := &testMessageNotifyPeer{connected: true}
		transport := &testCoreMessageTransport{local: "core-a", bootstrap: "bootstrap", peers: map[string]*testMessageNotifyPeer{"bootstrap": bootstrap}}
		if err := routeDirectedMessageNotify(transport, directedTestNotify("core-b"), false); err != nil {
			t.Fatal(err)
		}
		if len(bootstrap.messages) != 1 || bootstrap.messages[0].Target != "core-b" {
			t.Fatalf("bootstrap messages=%#v", bootstrap.messages)
		}
	})

	t.Run("normal-core-does-not-relay-inbound", func(t *testing.T) {
		bootstrap := &testMessageNotifyPeer{connected: true}
		target := &testMessageNotifyPeer{connected: true}
		transport := &testCoreMessageTransport{local: "core-a", bootstrap: "bootstrap", peers: map[string]*testMessageNotifyPeer{
			"bootstrap": bootstrap, "core-b": target,
		}}
		err := routeDirectedMessageNotify(transport, directedTestNotify("core-b"), true)
		if !errors.Is(err, errMessageTargetUnavailable) {
			t.Fatalf("err=%v", err)
		}
		if len(target.messages) != 0 || len(bootstrap.messages) != 0 {
			t.Fatalf("inbound message was relayed target=%d bootstrap=%d", len(target.messages), len(bootstrap.messages))
		}
	})

	t.Run("bootstrap-relays-only-to-target", func(t *testing.T) {
		target := &testMessageNotifyPeer{connected: true}
		other := &testMessageNotifyPeer{connected: true}
		transport := &testCoreMessageTransport{local: "bootstrap", bootstrap: "bootstrap", peers: map[string]*testMessageNotifyPeer{
			"core-b": target, "core-c": other,
		}}
		if err := routeDirectedMessageNotify(transport, directedTestNotify("core-b"), true); err != nil {
			t.Fatal(err)
		}
		if len(target.messages) != 1 || len(other.messages) != 0 {
			t.Fatalf("target=%d other=%d", len(target.messages), len(other.messages))
		}
	})

	t.Run("missing-target-is-not-broadcast", func(t *testing.T) {
		otherA := &testMessageNotifyPeer{connected: true}
		otherB := &testMessageNotifyPeer{connected: true}
		transport := &testCoreMessageTransport{local: "bootstrap", bootstrap: "bootstrap", peers: map[string]*testMessageNotifyPeer{
			"core-a": otherA, "core-b": otherB,
		}}
		err := routeDirectedMessageNotify(transport, directedTestNotify("missing"), true)
		if !errors.Is(err, errMessageTargetUnavailable) {
			t.Fatalf("err=%v", err)
		}
		if len(otherA.messages) != 0 || len(otherB.messages) != 0 {
			t.Fatalf("missing target was broadcast: a=%d b=%d", len(otherA.messages), len(otherB.messages))
		}
	})
}

func TestDKVSMessageRouteRejectsInvalidDirectedNotify(t *testing.T) {
	transport := &testCoreMessageTransport{local: "core-a", bootstrap: "bootstrap", peers: map[string]*testMessageNotifyPeer{}}
	for _, message := range []*wire.MsgDKVSNotify{
		{EventType: wire.DKVSNotifyEventMessage, Data: []byte("x")},
		{Target: "core-a", EventType: 1, Data: []byte("x")},
		{Target: "core-a", EventType: wire.DKVSNotifyEventMessage},
	} {
		if err := routeDirectedMessageNotify(transport, message, false); !errors.Is(err, ErrMessageInvalidEnvelope) {
			t.Fatalf("message=%#v err=%v", message, err)
		}
	}
}
