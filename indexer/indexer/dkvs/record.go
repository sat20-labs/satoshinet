package dkvs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
)

var signatureDomain = []byte("satoshinet-dkvs-record-v1")

func RecordSize(record *wire.DKVSRecord) int {
	if record == nil {
		return 0
	}
	return len(record.Key) + len(record.Value) + len(record.PubKey) +
		len(record.Signature) + len(record.FeeProof) + 64
}

func RecordHash(record *wire.DKVSRecord) chainhash.Hash {
	b := canonicalRecordBytes(record, true)
	return chainhash.DoubleHashH(b)
}

func KeyHash(key string) chainhash.Hash {
	return chainhash.DoubleHashH([]byte(key))
}

func SigningHash(record *wire.DKVSRecord) [32]byte {
	return sha256.Sum256(canonicalRecordBytes(record, false))
}

func SigningMessage(record *wire.DKVSRecord) []byte {
	return canonicalRecordBytes(record, false)
}

func VerifySignature(record *wire.DKVSRecord) error {
	if record == nil || len(record.PubKey) == 0 || len(record.Signature) == 0 {
		return ErrInvalidSignature
	}
	pubKey, err := btcec.ParsePubKey(record.PubKey)
	if err != nil {
		return err
	}
	sig, err := ecdsa.ParseSignature(record.Signature)
	if err != nil {
		return err
	}
	hash := SigningHash(record)
	if !sig.Verify(hash[:], pubKey) {
		return ErrInvalidSignature
	}
	return nil
}

func CompareRecords(a, b *wire.DKVSRecord) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	if a.Seq > b.Seq {
		return 1
	}
	if a.Seq < b.Seq {
		return -1
	}
	if a.ExpiryHeight > b.ExpiryHeight {
		return 1
	}
	if a.ExpiryHeight < b.ExpiryHeight {
		return -1
	}
	ah := RecordHash(a)
	bh := RecordHash(b)
	return bytes.Compare(ah[:], bh[:])
}

func IsExpired(record *wire.DKVSRecord, height uint64, now uint64) bool {
	if record == nil {
		return true
	}
	if record.ExpiryHeight != 0 && height != 0 && record.ExpiryHeight <= height {
		return true
	}
	if record.TTL != 0 && record.IssueTime != 0 && now > record.IssueTime+record.TTL {
		return true
	}
	return false
}

func MarshalRecord(record *wire.DKVSRecord) ([]byte, error) {
	return json.Marshal(record)
}

func UnmarshalRecord(data []byte) (*wire.DKVSRecord, error) {
	var record wire.DKVSRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func NewNotifyEvent(eventType uint32, record *wire.DKVSRecord, sourceNode string) (*NotifyEvent, error) {
	if record == nil {
		return nil, ErrInvalidRecord
	}
	return &NotifyEvent{
		EventType:    eventType,
		Key:          record.Key,
		KeyHash:      KeyHash(record.Key),
		RecordHash:   RecordHash(record),
		Seq:          record.Seq,
		ExpiryHeight: record.ExpiryHeight,
		Size:         uint32(RecordSize(record)),
		SourceNode:   sourceNode,
		Flags:        record.Flags,
	}, nil
}

func MarshalNotifyEvent(event *NotifyEvent) ([]byte, error) {
	if event == nil {
		return nil, ErrInvalidRecord
	}
	return json.Marshal(event)
}

func UnmarshalNotifyEvent(data []byte) (*NotifyEvent, error) {
	var event NotifyEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	if event.Key == "" {
		return nil, ErrInvalidKey
	}
	return &event, nil
}

func currentUnixMilli() uint64 {
	return uint64(time.Now().UnixMilli())
}

func canonicalRecordBytes(record *wire.DKVSRecord, includeSignature bool) []byte {
	var buf bytes.Buffer
	writeBytes(&buf, signatureDomain)
	if record == nil {
		return buf.Bytes()
	}
	writeUint32(&buf, record.Version)
	writeString(&buf, record.Key)
	writeHash(&buf, sha256.Sum256(record.Value))
	writeBytes(&buf, record.PubKey)
	writeUint64(&buf, record.Seq)
	writeUint64(&buf, record.IssueTime)
	writeUint64(&buf, record.TTL)
	writeUint64(&buf, record.ExpiryHeight)
	writeHash(&buf, sha256.Sum256(record.FeeProof))
	writeUint32(&buf, record.Flags)
	if includeSignature {
		writeBytes(&buf, record.Signature)
	}
	return buf.Bytes()
}

func writeString(buf *bytes.Buffer, s string) {
	writeBytes(buf, []byte(s))
}

func writeBytes(buf *bytes.Buffer, b []byte) {
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], uint64(len(b)))
	buf.Write(scratch[:])
	buf.Write(b)
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var scratch [4]byte
	binary.LittleEndian.PutUint32(scratch[:], v)
	buf.Write(scratch[:])
}

func writeUint64(buf *bytes.Buffer, v uint64) {
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], v)
	buf.Write(scratch[:])
}

func writeHash(buf *bytes.Buffer, h [32]byte) {
	buf.Write(h[:])
}

func HexRecordHash(record *wire.DKVSRecord) string {
	h := RecordHash(record)
	return hex.EncodeToString(h[:])
}
