package dkvs

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/wire"
)

func TestReviewLargeInternalDirectRemainsReadable(t *testing.T) {
	idx := testIndexer(t)
	recipient, sender := testMessageAccount(t), testMessageAccount(t)
	record := internalDirectRecord(t, recipient, sender, 0, 1, bytes.Repeat([]byte{0x42}, 32*1024))
	// Paid Direct retention is persistent and authenticated by MessageManager.
	record.TTL, record.FeeProof = 0, nil
	if updated, err := idx.PutInternalMailbox(record); err != nil || !updated {
		t.Fatalf("internal append: updated=%t err=%v", updated, err)
	}
	t.Run("Get", func(t *testing.T) {
		got, err := idx.Get(record.Key)
		if err != nil || got == nil || !bytes.Equal(got.Value, record.Value) {
			t.Fatalf("committed large Direct is unreadable: err=%v", err)
		}
	})
	t.Run("ReadPrefix", func(t *testing.T) {
		read, err := idx.ReadPrefix("/mail/" + recipient + "/msg")
		if err != nil || read == nil || len(read.Records) != 1 || !bytes.Equal(read.Records[0].Value, record.Value) {
			t.Fatalf("prefix omitted committed large Direct: err=%v records=%d", err, func() int {
				if read == nil {
					return -1
				}
				return len(read.Records)
			}())
		}
	})
	t.Run("GenericWriteRemainsForbidden", func(t *testing.T) {
		if _, err := idx.PutLocal(record); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("generic API must reject trusted internal Direct: err=%v", err)
		}
	})
	t.Run("DirectUpperBoundRemains", func(t *testing.T) {
		oversized := cloneRecord(record)
		oversized.Value = bytes.Repeat([]byte{0x42}, wire.MaxDKVSBlobValueSize+1)
		if _, err := idx.PutInternalMailbox(oversized); !errors.Is(err, ErrRecordTooLarge) {
			t.Fatalf("internal Direct exceeded the existing 1 MiB bound: err=%v", err)
		}
	})
	t.Run("OrdinarySizeLimitRemains", func(t *testing.T) {
		ordinary := &wire.DKVSRecord{Key: "/personal/" + recipient + "/large", Value: bytes.Repeat([]byte{0x42}, wire.MaxDKVSValueSize+1)}
		parsed, err := ParseKey(ordinary.Key)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRecordSizeForParsed(ordinary, parsed); !errors.Is(err, ErrRecordTooLarge) {
			t.Fatalf("ordinary KV size limit: err=%v", err)
		}
	})
}
