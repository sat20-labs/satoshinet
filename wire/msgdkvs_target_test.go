package wire

import (
	"bytes"
	"strings"
	"testing"
)

func TestDKVSNotifyTargetRoundTrip(t *testing.T) {
	want := &MsgDKVSNotify{
		Target:    strings.Repeat("a", 66),
		EventType: DKVSNotifyEventMessage,
		Data:      []byte("opaque-message-envelope"),
	}
	var encoded bytes.Buffer
	if err := want.BtcEncode(&encoded, ProtocolVersion, BaseEncoding); err != nil {
		t.Fatal(err)
	}
	var got MsgDKVSNotify
	if err := got.BtcDecode(bytes.NewReader(encoded.Bytes()), ProtocolVersion, BaseEncoding); err != nil {
		t.Fatal(err)
	}
	if got.Target != want.Target || got.EventType != want.EventType || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("decoded notify=%#v want=%#v", got, want)
	}
}

func TestDKVSNotifyEmptyTargetKeepsBroadcastSemantics(t *testing.T) {
	msg := &MsgDKVSNotify{EventType: 1, Data: []byte{1}}
	var encoded bytes.Buffer
	if err := msg.BtcEncode(&encoded, ProtocolVersion, BaseEncoding); err != nil {
		t.Fatal(err)
	}
	var decoded MsgDKVSNotify
	if err := decoded.BtcDecode(bytes.NewReader(encoded.Bytes()), ProtocolVersion, BaseEncoding); err != nil {
		t.Fatal(err)
	}
	if decoded.Target != "" {
		t.Fatalf("target=%q want empty", decoded.Target)
	}
}

func TestDKVSNotifyRejectsOversizeTarget(t *testing.T) {
	msg := &MsgDKVSNotify{
		Target:    strings.Repeat("a", MaxDKVSNotifyTargetSize+1),
		EventType: DKVSNotifyEventMessage,
		Data:      []byte{1},
	}
	if err := msg.BtcEncode(&bytes.Buffer{}, ProtocolVersion, BaseEncoding); err == nil {
		t.Fatal("oversize target accepted")
	}
}
