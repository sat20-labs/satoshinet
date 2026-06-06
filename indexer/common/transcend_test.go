package common

import "testing"

func TestDescendPayloadV2RoundTrip(t *testing.T) {
	l1TxId := "820cf0b942b301b548fd1fd65c7cd7a8d57ddcbd31d9c1811b9da17fb8c38e58"
	payload, err := EncodeDescendPayloadV2(l1TxId, DESCEND_OP_SPLICING_OUT, []uint32{1, 3})
	if err != nil {
		t.Fatalf("EncodeDescendPayloadV2 failed: %v", err)
	}

	decoded, err := ParseDescendPayload(payload)
	if err != nil {
		t.Fatalf("ParseDescendPayload failed: %v", err)
	}
	if decoded.Version != 2 {
		t.Fatalf("version mismatch: got %d", decoded.Version)
	}
	if decoded.Operation != DESCEND_OP_SPLICING_OUT {
		t.Fatalf("operation mismatch: got %d", decoded.Operation)
	}
	if decoded.L1TxId != l1TxId {
		t.Fatalf("l1 txid mismatch: got %s want %s", decoded.L1TxId, l1TxId)
	}
	if len(decoded.ReturnedOutputVouts) != 2 ||
		decoded.ReturnedOutputVouts[0] != 1 ||
		decoded.ReturnedOutputVouts[1] != 3 {
		t.Fatalf("returned vouts mismatch: %v", decoded.ReturnedOutputVouts)
	}
}

func TestDescendPayloadV1Legacy(t *testing.T) {
	decoded, err := ParseDescendPayload([]byte("legacy-txid"))
	if err != nil {
		t.Fatalf("ParseDescendPayload failed: %v", err)
	}
	if decoded.Version != 1 {
		t.Fatalf("version mismatch: got %d", decoded.Version)
	}
	if decoded.Operation != DESCEND_OP_UNKNOWN {
		t.Fatalf("operation mismatch: got %d", decoded.Operation)
	}
	if decoded.L1TxId != "legacy-txid" {
		t.Fatalf("legacy l1 txid mismatch: %s", decoded.L1TxId)
	}
	if decoded.LegacyPayload != "legacy-txid" {
		t.Fatalf("legacy payload mismatch: %s", decoded.LegacyPayload)
	}
}
