package wire

import (
	"bytes"
	"strings"
	"testing"
)

func TestDKVSContractWireLimits(t *testing.T) {
	for _, key := range []string{
		"/contract/rgb11/" + strings.Repeat("a", 64),
		"/contract/evm/source/" + strings.Repeat("b", 40),
	} {
		t.Run(key, func(t *testing.T) {
			record := &DKVSRecord{Version: 1, Key: key, Value: bytes.Repeat([]byte{1}, MaxDKVSBlobValueSize), IssueHeight: 1}
			raw, err := SerializeDKVSRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DeserializeDKVSRecord(raw)
			if err != nil || decoded.Key != key || !bytes.Equal(decoded.Value, record.Value) {
				t.Fatalf("contract wire round trip: %v", err)
			}
			record.Value = append(record.Value, 1)
			if _, err := SerializeDKVSRecord(record); err == nil {
				t.Fatal("oversized contract accepted")
			}
		})
	}
	if DKVSValueSizeLimit("/rgb11/alice/usd/1") != MaxDKVSValueSize {
		t.Fatal("old RGB namespace retained contract limit")
	}
	if DKVSValueSizeLimit("/contract/example/extra/id") != MaxDKVSBlobValueSize {
		t.Fatal("invalid contract shape retained contract limit")
	}
}
