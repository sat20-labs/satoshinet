package dkvs

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/wire"
)

func internalMailboxTestRecord(key string, value []byte, ttl uint64) *wire.DKVSRecord {
	record := &wire.DKVSRecord{
		Version: Version, Key: key, Value: append([]byte(nil), value...),
		Seq: 1, IssueHeight: 1, TTL: ttl,
	}
	parsed, _ := ParseKey(key)
	if isInternalDirectMailbox(parsed) && ttl != 0 {
		record.FeeProof, _ = EncodeFeeProof(&FeeProof{Mode: FeeModeFreeLocal})
	}
	return record
}

func signedMailboxOwnerTombstoneForTest(t *testing.T, owner *btcec.PrivateKey, key string, seq uint64) *wire.DKVSRecord {
	t.Helper()
	record, err := NewAccountRecord(key, nil, RecordOptions{Seq: seq, IssueHeight: 1, TTL: 100, Flags: FlagTombstone})
	if err != nil {
		t.Fatal(err)
	}
	hash := SigningHash(record)
	sig, err := schnorr.Sign(owner, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	record.Signature = sig.Serialize()
	return record
}
