package wire

import (
	"bytes"
	"testing"
)

func TestDKVSSingleRecordBlobWireLimits(t *testing.T) {
	blob := &DKVSRecord{
		Version:   1,
		Key:       "/blob/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/state",
		Value:     bytes.Repeat([]byte{0x5a}, MaxDKVSBlobValueSize),
		IssueHeight: 1,
	}
	encoded, err := SerializeDKVSRecord(blob)
	if err != nil {
		t.Fatalf("serialize max blob: %v", err)
	}
	decoded, err := DeserializeDKVSRecord(encoded)
	if err != nil {
		t.Fatalf("deserialize max blob: %v", err)
	}
	if decoded.Key != blob.Key || !bytes.Equal(decoded.Value, blob.Value) {
		t.Fatal("blob record changed during wire round trip")
	}

	tooLarge := *blob
	tooLarge.Value = bytes.Repeat([]byte{1}, MaxDKVSBlobValueSize+1)
	if _, err := SerializeDKVSRecord(&tooLarge); err == nil {
		t.Fatal("blob larger than 1 MiB was accepted")
	}

	ordinary := *blob
	ordinary.Key = "/personal/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/state"
	ordinary.Value = bytes.Repeat([]byte{1}, MaxDKVSValueSize+1)
	if _, err := SerializeDKVSRecord(&ordinary); err == nil {
		t.Fatal("oversized ordinary value was accepted")
	}
}

func TestDKVSRecordMessagesEnforceAggregatePayloadBudget(t *testing.T) {
	records := make([]*DKVSRecord, 0, 4)
	for index := 0; index < 4; index++ {
		records = append(records, &DKVSRecord{
			Version:   1,
			Key:       "/blob/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/blob" + string(rune('a'+index)),
			Value:     bytes.Repeat([]byte{byte(index)}, MaxDKVSBlobValueSize),
			IssueHeight: 1,
		})
	}
	var encoded bytes.Buffer
	msg := &MsgDKVSData{Records: records}
	if err := msg.BtcEncode(&encoded, ProtocolVersion, BaseEncoding); err == nil {
		t.Fatal("aggregate DKVS message above protocol budget was accepted")
	}

	encoded.Reset()
	msg.Records = records[:3]
	if err := msg.BtcEncode(&encoded, ProtocolVersion, BaseEncoding); err != nil {
		t.Fatalf("three max-sized blob records should fit: %v", err)
	}
}
