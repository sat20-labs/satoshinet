package main

import (
	"encoding/hex"
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

func TestTopicDeliveryRequiresRegisteredCoreAndBoundSignature(t *testing.T) {
	key, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	source := hex.EncodeToString(key.PubKey().SerializeCompressed())
	sender := NewMessageManager(source, nil, nil, nil, nil, nil)
	sender.SetCoreSigner(func(body []byte) ([]byte, error) {
		return ecdsa.Sign(key, chainhash.HashB(body)).Serialize(), nil
	})
	receiver := NewMessageManager("destination", nil, nil, nil, nil, nil)
	for _, kind := range []MessageType{MessageTypeTopicFanout, MessageTypeTopicKeyFanout} {
		encoded, err := sender.marshalTopicDelivery(kind, receiver.localCore, []byte("topic,issuer,keyseq,recipients,ciphertext"))
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := UnmarshalMessageEnvelope(encoded)
		if err != nil {
			t.Fatal(err)
		}
		receiver.coreAuthority = nil
		if !errors.Is(receiver.authorizeTopicDelivery(envelope), ErrMessageTopicPermission) {
			t.Fatal("unregistered self-signed delivery accepted")
		}
		receiver.coreAuthority = func(core string) bool { return core == source }
		if err := receiver.authorizeTopicDelivery(envelope); err != nil {
			t.Fatal(err)
		}
		if err := verifyCoreTopicDelivery("other-destination", envelope); err == nil {
			t.Fatal("signature allowed retargeting")
		}
		altered := *envelope
		altered.Payload = []byte("attacker-chosen recipients or keyseq")
		if err := receiver.authorizeTopicDelivery(&altered); err == nil {
			t.Fatal("signature allowed payload substitution")
		}
		altered = *envelope
		altered.Signature = nil
		if err := receiver.authorizeTopicDelivery(&altered); err == nil {
			t.Fatal("unsigned delivery accepted")
		}
		if _, err := MarshalMessageEnvelope(&altered); err == nil {
			t.Fatal("unsigned fanout encoded")
		}
		if _, err := UnmarshalMessageEnvelope(encoded[:len(encoded)-1]); err == nil {
			t.Fatal("truncated signature accepted")
		}
	}
}

func TestUnsignedTopicDeliveryRejectedBeforeMailboxWrite(t *testing.T) {
	manager := NewMessageManager("victim-core", nil, nil, nil, nil, nil)
	for _, kind := range []MessageType{MessageTypeTopicFanout, MessageTypeTopicKeyFanout} {
		envelope := &MessageEnvelope{MessageType: kind, SourceCoreNode: "attacker", Payload: []byte("self-signed package")}
		var err error
		if kind == MessageTypeTopicFanout {
			err = manager.handleTopicFanout(envelope)
		} else {
			err = manager.handleTopicKeyFanout(envelope)
		}
		if !errors.Is(err, ErrMessageTopicPermission) {
			t.Fatalf("unauthorized mailbox path returned %v", err)
		}
	}
}
