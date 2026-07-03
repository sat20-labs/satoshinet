package wire

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

func TestDKVSMessagesWire(t *testing.T) {
	hash := chainhash.DoubleHashH([]byte("record"))
	keyHash := chainhash.DoubleHashH([]byte("/personal/a/b"))
	record := &DKVSRecord{
		Version:      1,
		Key:          "/personal/a/b",
		Value:        []byte("value"),
		Data:         []byte("data"),
		PubKey:       []byte{2, 1, 2, 3},
		Signature:    []byte{48, 1, 1},
		Seq:          10,
		IssueTime:    20,
		TTL:          30,
		ExpiryHeight: 40,
		FeeProof:     []byte("fee"),
		Flags:        1,
	}
	tests := []Message{
		&MsgDKVSNotify{EventType: 1, Key: record.Key, KeyHash: keyHash, RecordHash: hash, Seq: 10, ExpiryHeight: 40, Size: 100, SourceNode: "node", Flags: 1},
		&MsgDKVSInv{Items: []DKVSInvItem{{Key: record.Key, KeyHash: keyHash, RecordHash: hash, Seq: 10}}},
		&MsgDKVSGet{Keys: []string{record.Key}, RecordHashes: []chainhash.Hash{hash}},
		&MsgDKVSData{Records: []*DKVSRecord{record}, NotFound: []chainhash.Hash{hash}},
		&MsgDKVSSyncRequest{Cursor: []byte("cursor"), Limit: 10, Filters: []DKVSSyncFilter{{Type: "prefix", Target: "/personal/a"}}},
		&MsgDKVSSyncResponse{Records: []*DKVSRecord{record}, NextCursor: []byte("next"), Done: true, CheckpointRoot: hash},
	}
	for _, test := range tests {
		var buf bytes.Buffer
		if err := test.BtcEncode(&buf, ProtocolVersion, BaseEncoding); err != nil {
			t.Fatalf("%s encode: %v", test.Command(), err)
		}
		decoded, err := makeEmptyMessage(test.Command())
		if err != nil {
			t.Fatalf("%s make empty: %v", test.Command(), err)
		}
		if err := decoded.BtcDecode(&buf, ProtocolVersion, BaseEncoding); err != nil {
			t.Fatalf("%s decode: %v", test.Command(), err)
		}
		if !reflect.DeepEqual(decoded, test) {
			t.Fatalf("%s mismatch\n got %#v\nwant %#v", test.Command(), decoded, test)
		}
	}
}

func TestDKVSMessagesShortRead(t *testing.T) {
	for _, test := range sampleDKVSMessages() {
		var encoded bytes.Buffer
		if err := test.BtcEncode(&encoded, ProtocolVersion, BaseEncoding); err != nil {
			t.Fatalf("%s encode: %v", test.Command(), err)
		}
		payload := encoded.Bytes()
		if uint32(len(payload)) > test.MaxPayloadLength(ProtocolVersion) {
			t.Fatalf("%s payload len %d exceeds max %d", test.Command(), len(payload), test.MaxPayloadLength(ProtocolVersion))
		}
		for n := 0; n < len(payload); n++ {
			decoded, err := makeEmptyMessage(test.Command())
			if err != nil {
				t.Fatalf("%s make empty: %v", test.Command(), err)
			}
			if err := decoded.BtcDecode(bytes.NewReader(payload[:n]), ProtocolVersion, BaseEncoding); err == nil {
				t.Fatalf("%s decoded short payload length %d", test.Command(), n)
			}
		}
	}
}

func TestDKVSMessagesOversize(t *testing.T) {
	hash := chainhash.DoubleHashH([]byte("record"))
	longKey := string(bytes.Repeat([]byte("k"), MaxDKVSKeySize+1))
	longValue := bytes.Repeat([]byte("v"), MaxDKVSValueSize+1)
	longCursor := bytes.Repeat([]byte("c"), MaxDKVSCursorSize+1)
	tests := []Message{
		&MsgDKVSNotify{Key: longKey},
		&MsgDKVSInv{Items: []DKVSInvItem{{Key: longKey}}},
		&MsgDKVSGet{Keys: []string{longKey}},
		&MsgDKVSData{Records: []*DKVSRecord{{Version: 1, Key: "/personal/a/b", Value: longValue}}},
		&MsgDKVSSyncRequest{Cursor: longCursor},
		&MsgDKVSSyncRequest{Filters: []DKVSSyncFilter{{Type: string(bytes.Repeat([]byte("t"), MaxDKVSFilterTypeSize+1)), Target: "/tmp/a"}}},
		&MsgDKVSSyncRequest{Filters: []DKVSSyncFilter{{Type: "prefix", Target: longKey}}},
		&MsgDKVSSyncResponse{NextCursor: longCursor},
	}
	for _, test := range tests {
		if err := test.BtcEncode(&bytes.Buffer{}, ProtocolVersion, BaseEncoding); err == nil {
			t.Fatalf("%s accepted oversized payload", test.Command())
		}
	}

	countTests := []struct {
		name string
		msg  Message
	}{
		{"inv", &MsgDKVSInv{}},
		{"get", &MsgDKVSGet{}},
		{"data", &MsgDKVSData{}},
		{"syncres", &MsgDKVSSyncResponse{}},
	}
	for _, test := range countTests {
		var payload bytes.Buffer
		if err := WriteVarInt(&payload, ProtocolVersion, uint64(MaxDKVSItemsPerMsg)+1); err != nil {
			t.Fatal(err)
		}
		if test.name == "data" || test.name == "syncres" {
			payload.Reset()
			if err := WriteVarInt(&payload, ProtocolVersion, uint64(MaxDKVSRecordsPerMsg)+1); err != nil {
				t.Fatal(err)
			}
		}
		if err := test.msg.BtcDecode(bytes.NewReader(payload.Bytes()), ProtocolVersion, BaseEncoding); err == nil {
			t.Fatalf("%s accepted oversized count", test.name)
		}
	}

	var syncReqPayload bytes.Buffer
	if err := WriteVarBytes(&syncReqPayload, ProtocolVersion, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeElements(&syncReqPayload, uint32(1)); err != nil {
		t.Fatal(err)
	}
	if err := WriteVarInt(&syncReqPayload, ProtocolVersion, uint64(MaxDKVSSyncFilters)+1); err != nil {
		t.Fatal(err)
	}
	if err := (&MsgDKVSSyncRequest{}).BtcDecode(bytes.NewReader(syncReqPayload.Bytes()), ProtocolVersion, BaseEncoding); err == nil {
		t.Fatalf("syncreq accepted oversized filter count")
	}

	_ = hash
}

func TestDKVSSyncRequestLegacyPayload(t *testing.T) {
	var payload bytes.Buffer
	if err := WriteVarBytes(&payload, ProtocolVersion, []byte("cursor")); err != nil {
		t.Fatal(err)
	}
	if err := writeElements(&payload, uint32(10)); err != nil {
		t.Fatal(err)
	}
	var decoded MsgDKVSSyncRequest
	if err := decoded.BtcDecode(bytes.NewReader(payload.Bytes()), ProtocolVersion, BaseEncoding); err != nil {
		t.Fatal(err)
	}
	if string(decoded.Cursor) != "cursor" || decoded.Limit != 10 || len(decoded.Filters) != 0 {
		t.Fatalf("decoded legacy sync request=%#v", decoded)
	}
}

func TestDKVSCommandLength(t *testing.T) {
	commands := []string{
		CmdDKVSNotify,
		CmdDKVSInv,
		CmdDKVSGet,
		CmdDKVSData,
		CmdDKVSSyncRequest,
		CmdDKVSSyncResponse,
	}
	for _, command := range commands {
		if len(command) > CommandSize {
			t.Fatalf("%s exceeds command size", command)
		}
	}
}

func sampleDKVSMessages() []Message {
	hash := chainhash.DoubleHashH([]byte("record"))
	keyHash := chainhash.DoubleHashH([]byte("/personal/a/b"))
	record := &DKVSRecord{
		Version:      1,
		Key:          "/personal/a/b",
		Value:        []byte("value"),
		Data:         []byte("data"),
		PubKey:       []byte{2, 1, 2, 3},
		Signature:    []byte{48, 1, 1},
		Seq:          10,
		IssueTime:    20,
		TTL:          30,
		ExpiryHeight: 40,
		FeeProof:     []byte("fee"),
		Flags:        1,
	}
	return []Message{
		&MsgDKVSNotify{EventType: 1, Key: record.Key, KeyHash: keyHash, RecordHash: hash, Seq: 10, ExpiryHeight: 40, Size: 100, SourceNode: "node", Flags: 1},
		&MsgDKVSInv{Items: []DKVSInvItem{{Key: record.Key, KeyHash: keyHash, RecordHash: hash, Seq: 10}}},
		&MsgDKVSGet{Keys: []string{record.Key}, RecordHashes: []chainhash.Hash{hash}},
		&MsgDKVSData{Records: []*DKVSRecord{record}, NotFound: []chainhash.Hash{hash}},
		&MsgDKVSSyncRequest{Cursor: []byte("cursor"), Limit: 10},
		&MsgDKVSSyncResponse{Records: []*DKVSRecord{record}, NextCursor: []byte("next"), Done: true, CheckpointRoot: hash},
	}
}
